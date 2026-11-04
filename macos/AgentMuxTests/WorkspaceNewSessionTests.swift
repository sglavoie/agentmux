import XCTest
@testable import AgentMux

private final class NewSessionRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func release() { pending.removeFirst()() }
}

@MainActor
final class WorkspaceNewSessionTests: XCTestCase {
    private typealias F = WorkspaceFixture
    private func source() -> WorkspaceRow {
        WorkspaceSidebar.row(F.tmux(1, project: "API", profile: "work"), now: Date(), glyphs: .fallback)
    }
    private func reply(_ data: String, status: Int32 = 0) -> ProcessRun {
        .finished(ProcessOutput(status: status, stdout: Data(data.utf8), stderr: Data()))
    }
    private func command(_ calls: @escaping ([String]) -> ProcessRun) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in calls(args) }
        return command
    }
    private func choices(_ args: [String]) -> ProcessRun {
        if args.starts(with: ["project", "list"]) {
            return reply(#"{"schema_version":1,"data":[{"profile":"work","title":"API","harness":"claude","directory":"/configured"}]}"#)
        }
        if args.starts(with: ["config", "profiles"]) {
            return reply(#"{"schema_version":1,"data":[{"name":"work","harness":"claude"},{"name":"other","harness":"codex"},{"name":"review","harness":"codex"}]}"#)
        }
        return reply(#"{"schema_version":1,"data":{"runtime_session_id":"\#(F.runtimeID(1))","state":"live","directory":"/actual/worktree"}}"#)
    }

    func testLoadsEveryProfileAndUsesLiveDirectoryWithOptionalOverride() {
        let runner = NewSessionRunner()
        var calls: [[String]] = []
        let command = command { args in calls.append(args); return self.choices(args) }
        let model = WorkspaceNewSessionModel(source: source(), command: { command }, runner: runner)
        XCTAssertFalse(model.canLaunch)
        model.load(); model.load()
        XCTAssertEqual(runner.pending.count, 1)
        runner.release()
        XCTAssertTrue(model.canLaunch)
        XCTAssertEqual(model.profiles.map(\.name), ["work", "other", "review"], "profiles without projects are available too")
        XCTAssertEqual(model.profile, "work")
        XCTAssertEqual(model.launchDirectory, "/actual/worktree")
        XCTAssertEqual(calls.count, 3)
        model.profile = "review"
        model.directory = "../custom path; $(ignored)"
        XCTAssertEqual(model.launchDirectory, "/actual/custom path; $(ignored)")
        XCTAssertEqual(model.arguments, ["project", "launch", "--json", "--backend", "tmux", "--profile", "work",
                                          "--launch-profile", "review", "--directory", "/actual/custom path; $(ignored)", "--", "API"])
        model.directory = "~/repo"
        XCTAssertEqual(model.launchDirectory, NSHomeDirectory() + "/repo")
        model.directory = " \n "
        XCTAssertEqual(model.launchDirectory, "/actual/worktree")
        model.profile = "unknown"
        XCTAssertFalse(model.canLaunch)
    }

    func testLaunchOpensReportedSessionAndPreventsDuplicateClicks() {
        let runner = NewSessionRunner()
        var launches = 0
        let command = command { args in
            if args.starts(with: ["project", "launch"]) {
                launches += 1
                return self.reply(#"{"schema_version":1,"data":{"changed":true,"runtime_session_id":"\#(F.runtimeID(2))"}}"#)
            }
            return self.choices(args)
        }
        let model = WorkspaceNewSessionModel(source: source(), command: { command }, runner: runner)
        var opened: [String] = []
        model.onLaunched = { opened.append($0) }
        model.load(); runner.release()
        model.launch(); model.launch()
        XCTAssertTrue(model.launching)
        XCTAssertEqual(runner.pending.count, 1)
        runner.release()
        XCTAssertEqual(launches, 1)
        XCTAssertEqual(opened, [F.runtimeID(2)])
        XCTAssertFalse(model.canLaunch)
    }

    func testRejectedDirectoryCanBeCorrectedButUnknownOutcomeCannotBeRetried() {
        for timedOut in [false, true] {
            let runner = NewSessionRunner()
            let command = command { args in
                if args.starts(with: ["project", "launch"]) {
                    if timedOut { return .timedOut }
                    return self.reply(#"{"schema_version":1,"error":{"code":"directory_unusable","message":"Directory does not exist"}}"#, status: 1)
                }
                return self.choices(args)
            }
            let model = WorkspaceNewSessionModel(source: source(), command: { command }, runner: runner)
            model.load(); runner.release(); model.launch(); runner.release()
            XCTAssertNotNil(model.error)
            XCTAssertEqual(model.canLaunch, !timedOut)
            if timedOut { XCTAssertTrue(model.error?.contains("outcome is unknown") == true) }
        }
    }

    func testCanLaunchAgainFromAProfileWithoutItsOwnProjectTemplate() {
        let runner = NewSessionRunner()
        let command = command { args in self.choices(args) }
        let row = WorkspaceSidebar.row(F.tmux(1, project: "API", profile: "other", harness: "codex"), now: Date(), glyphs: .fallback)
        let model = WorkspaceNewSessionModel(source: row, command: { command }, runner: runner)
        model.load(); runner.release()
        XCTAssertTrue(model.canLaunch)
        XCTAssertEqual(model.profile, "other")
        XCTAssertEqual(model.project?.profile, "work", "the source template and launch profile are independent")
    }

    func testLoadFailureDoesNotEnableLaunch() {
        let runner = NewSessionRunner()
        let command = command { _ in self.reply(#"{"schema_version":1,"error":{"code":"config_invalid","message":"Invalid config"}}"#, status: 1) }
        let model = WorkspaceNewSessionModel(source: source(), command: { command }, runner: runner)
        model.load(); runner.release()
        XCTAssertFalse(model.canLaunch)
        XCTAssertEqual(model.error, "Invalid config")
    }
}
