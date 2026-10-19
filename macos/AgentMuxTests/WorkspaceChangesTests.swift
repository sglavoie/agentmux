import Darwin
import XCTest
@testable import AgentMux

private final class ChangesRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func release(_ index: Int = 0) { pending.remove(at: index)() }
}

@MainActor
final class WorkspaceChangesTests: XCTestCase {
    func testSelectingAnotherFileStopsAnAlreadyRunningRead() async throws {
        let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: root) }
        let binary = root.appendingPathComponent("amx-fixture")
        let started = root.appendingPathComponent("started")
        let script = """
        #!/bin/sh
        if [ "$1" = worktree ]; then
          printf '%s' '{"schema_version":1,"data":{"worktrees":[]}}'
          exit 0
        fi
        previous=''
        for argument in "$@"; do
          if [ "$previous" = --file ] && [ "$argument" = a ]; then
            printf '%s' "$$" > "$AMX_TEST_STARTED"
            exec /bin/sleep 30
          fi
          previous="$argument"
        done
        printf '%s' '{"schema_version":1,"data":{"root":"/checkout","base":"HEAD","files":[{"path":"a","added":1,"deleted":0,"binary":false,"untracked":false},{"path":"b","added":1,"deleted":0,"binary":false,"untracked":false}],"patch":"patch b","truncated":false}}'
        """
        try Data(script.utf8).write(to: binary)
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: binary.path)
        var command = AMXCommand(binary: binary, launchEnvironment: LaunchEnvironment(home: root.path, path: "/bin"))
        command.inheritedEnvironment = ["AMX_TEST_STARTED": started.path]
        let model = WorkspaceChangesModel(command: { command })
        defer { model.close() }
        model.open(directory: "/checkout")
        var startedPID: Int32?
        for _ in 0..<200 {
            if let value = try? String(contentsOf: started, encoding: .utf8), let pid = Int32(value) {
                startedPID = pid
                break
            }
            try await Task.sleep(for: .milliseconds(25))
        }
        let pid = try XCTUnwrap(startedPID)
        XCTAssertEqual(kill(pid, 0), 0, "the obsolete read must actually be running")
        model.select("b")
        for _ in 0..<200 {
            if !model.reading && kill(pid, 0) != 0 { break }
            try await Task.sleep(for: .milliseconds(25))
        }
        XCTAssertNotEqual(kill(pid, 0), 0, "selection must stop the process, not merely discard its response")
        XCTAssertEqual(model.patch.reveal(), "patch b")
        XCTAssertNil(model.patchError)
    }
    func testRapidSelectionAndRefreshCancelQueuedReadsBeforeTheySpawn() {
        let runner = ChangesRunner()
        var calls: [[String]] = []
        let amx = command { [changes] args in
            calls.append(args)
            return args[0] == "worktree" ? #"{"worktrees":[]}"# : changes
        }
        let model = WorkspaceChangesModel(command: { amx }, runner: runner)
        model.open(directory: "/old")
        model.open(directory: "/checkout")
        runner.release() // obsolete comparison
        XCTAssertTrue(calls.isEmpty)
        runner.release() // current comparison queues file a and managed review
        model.select("b")
        runner.release() // obsolete file a
        XCTAssertEqual(calls.count, 1)
        runner.release() // managed review
        runner.release() // current file b
        XCTAssertEqual(calls.last?.last, "b")
        XCTAssertEqual(model.selectedFile, "b")
        model.refresh()
        model.close()
        let before = calls.count
        runner.release()
        XCTAssertEqual(calls.count, before, "closing cancels work, not just its delivery")
    }

    func testLargePatchesAndRapidFileSwitchingKeepLatestSelection() async throws {
        let patch = "@@ -1,20000 +1,20000 @@\n" + String(repeating: " context " + String(repeating: "x", count: 80) + "\n", count: 20_000)
        let large = try JSONSerialization.data(withJSONObject: ["schema_version": 1, "data": [
            "root": "/checkout", "base": "HEAD", "files": [["path": "a", "added": 1, "deleted": 0, "binary": false, "untracked": false],
                                                            ["path": "b", "added": 1, "deleted": 0, "binary": false, "untracked": false]],
            "patch": patch, "truncated": false]])
        var amx = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        amx.runner = { _, args, _, _ in
            XCTAssertFalse(Thread.isMainThread, "read and parse work must leave the UI thread")
            let data = args.first == "worktree" ? Data(#"{"schema_version":1,"data":{"worktrees":[]}}"#.utf8) : large
            return .finished(.init(status: 0, stdout: data, stderr: Data()))
        }
        let model = WorkspaceChangesModel(command: { amx })
        model.open(directory: "/checkout")
        for _ in 0..<200 {
            if model.changes != nil { break }
            try await Task.sleep(for: .milliseconds(25))
        }
        XCTAssertNotNil(model.changes)
        for _ in 0..<15 { model.select("a"); model.select("b") }
        for _ in 0..<400 {
            if !model.reading { break }
            try await Task.sleep(for: .milliseconds(25))
        }
        XCTAssertFalse(model.reading)
        XCTAssertEqual(model.selectedFile, "b")
        XCTAssertEqual(model.patchIdentity, model.diffIdentity)
        XCTAssertEqual(model.diff.lines.count, 20_002)
        XCTAssertNil(model.patchError)
        model.close()
    }
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
