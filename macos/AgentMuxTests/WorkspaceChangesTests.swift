import XCTest
@testable import AgentMux

private final class ChangesRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func release(_ index: Int = 0) { pending.remove(at: index)() }
}

@MainActor
final class WorkspaceChangesTests: XCTestCase {
    private let changes = #"{"root":"/checkout","base":"abc123","files":[{"path":"a","added":2,"deleted":1,"binary":false,"untracked":false},{"path":"b","added":1,"deleted":0,"binary":false,"untracked":true}],"truncated":false}"#

    private func command(_ response: @escaping ([String]) -> String) -> AMXCommand {
        var amx = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        amx.runner = { _, args, _, _ in
            .finished(ProcessOutput(status: 0, stdout: Data("{\"schema_version\":1,\"data\":\(response(args))}".utf8), stderr: Data()))
        }
        return amx
    }

    func testFileReadsUseResolvedBaseAndLatePatchDoesNotReplaceSelection() {
        let runner = ChangesRunner()
        var calls: [[String]] = []
        let amx = command { [changes] args in
            calls.append(args)
            if args[0] == "worktree" { return #"{"worktrees":[]}"# }
            let text = args.last == "b" ? "patch b" : "patch a"
            return String(changes.dropLast()) + ",\"patch\":\"\(text)\"}"
        }
        let model = WorkspaceChangesModel(command: { amx }, runner: runner)
        model.open(runtimeSessionID: "rs-example")
        runner.release()
        model.base = "a different base not yet applied"
        model.select("b")
        runner.release(2)
        XCTAssertEqual(model.patch.reveal(), "patch b")
        XCTAssertEqual(calls.last, ["session", "changes", "--json", "--directory", "/checkout", "--base", "abc123", "--file", "b"])
        runner.release(0)
        XCTAssertEqual(model.patch.reveal(), "patch b")
        model.close()
        runner.release()
        XCTAssertNil(model.worktree)
    }

    func testLandingUsesManagedIdentityOnceAndNeverRemovesWorktree() {
        let runner = ChangesRunner()
        var calls: [[String]] = []
        let amx = command { [changes] args in
            calls.append(args)
            if args[0] == "session" { return changes }
            if args[1] == "list" { return #"{"worktrees":[{"id":"wt-one","path":"/checkout"}]}"# }
            if args[1] == "diff" {
                return #"{"review":{"id":"wt-one","path":"/checkout","branch":"feature","source_branch":"main","merge_base":"parent","ahead":1,"behind":0,"dirty":false,"lands":"fast_forward"}}"#
            }
            return #"{"changed":true,"message":"Landed"}"#
        }
        let model = WorkspaceChangesModel(command: { amx }, runner: runner)
        model.open(directory: "/checkout")
        runner.release()
        runner.release() // first file
        runner.release() // inventory and review
        XCTAssertTrue(model.canLand)
        model.land()
        model.land()
        XCTAssertFalse(model.canLand)
        XCTAssertEqual(runner.pending.count, 1)
        runner.release()
        XCTAssertEqual(calls.last, ["worktree", "land", "--json", "--yes", "--", "wt-one"])
        XCTAssertEqual(model.landNotice, "Landed")
        XCTAssertFalse(model.landing)
    }

    func testConfirmationCannotLandADifferentReview() throws {
        let runner = ChangesRunner()
        let amx = command { _ in "{}" }
        let model = WorkspaceChangesModel(command: { amx }, runner: runner)
        let target = try JSONDecoder().decode(WorkspaceWorktreeReview.self, from: Data(#"{"review":{"id":"wt-old","path":"/old","source_branch":"main","merge_base":"parent","ahead":1,"behind":0,"dirty":false,"lands":"fast_forward"}}"#.utf8)).review
        model.land(target: target)
        XCTAssertTrue(runner.pending.isEmpty)
        XCTAssertFalse(model.landing)
        XCTAssertTrue(model.landNotice?.contains("changed") == true)
    }

    func testClosingDiscardsAReadAndCannotOpenAnUnlistedFile() {
        let runner = ChangesRunner()
        let amx = command { [changes] _ in changes }
        let model = WorkspaceChangesModel(command: { amx }, runner: runner)
        model.open(directory: "/checkout")
        model.close()
        runner.release()
        XCTAssertNil(model.changes)
        var opened = false
        model.onOpenFile = { _ in opened = true }
        model.openFile()
        XCTAssertFalse(opened)
    }
    func testFeedbackRetainsOriginalCheckoutAndRecoveryNeverLands() throws {
        let runner = ChangesRunner()
        var calls: [[String]] = []
        let amx = command { [changes] args in
            calls.append(args)
            if args[0] == "worktree" { return #"{"worktrees":[]}"# }
            return changes
        }
        let model = WorkspaceChangesModel(command: { amx }, runner: runner)
        let target = try JSONDecoder().decode(WorkspaceWorktreeReview.self, from: Data(#"{"review":{"id":"wt-old","path":"/old","source_branch":"main","merge_base":"parent","ahead":1,"behind":0,"dirty":false,"lands":"fast_forward"}}"#.utf8)).review
        model.land(target: target)
        let notice = try XCTUnwrap(model.feedbackHistory.entries.first)
        model.open(directory: "/new")
        XCTAssertNil(model.landFeedback)
        XCTAssertEqual(model.feedbackHistory.entries.count, 1)
        notice.recover()
        while !runner.pending.isEmpty { runner.release() }
        XCTAssertTrue(calls.contains { $0.contains("--directory") && $0.last == "/old" })
        XCTAssertFalse(calls.contains { $0.contains("land") })
    }

    func testComparisonAndDiffFreshnessRetainFailedReadsAndResetOnTargetChange() throws {
        let runner = ChangesRunner()
        var available = true
        let amx = command { [changes] args in args[0] == "worktree" ? #"{"worktrees":[]}"# : changes }
        let model = WorkspaceChangesModel(command: { available ? amx : nil }, runner: runner)
        model.open(directory: "/checkout")
        while !runner.pending.isEmpty { runner.release() }
        let comparison = try XCTUnwrap(model.comparisonRefreshedAt)
        let diff = try XCTUnwrap(model.diffRefreshedAt)
        available = false; model.refresh()
        XCTAssertEqual(model.comparisonRefreshedAt, comparison)
        XCTAssertEqual(model.diffRefreshedAt, diff)
        model.open(directory: "/other")
        XCTAssertNil(model.comparisonRefreshedAt)
        XCTAssertNil(model.diffRefreshedAt)
    }

}
