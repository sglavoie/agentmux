import XCTest
@testable import AgentMux

private final class PlanWorkRunner: ASLWorkRunner {
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { pending.append { then(work()) } }
    func release() { pending.removeFirst()() }
}

@MainActor
final class WorkspaceRunPlanTests: XCTestCase {
    private func command(_ run: @escaping ([String]) -> ProcessRun) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in run(args) }
        return command
    }
    private func reply(started: Bool = false, state: String = "ready") -> ProcessRun {
        .finished(ProcessOutput(status: 0, stdout: Data("""
        {"schema_version":1,"data":{"started":\(started),"readiness":{"state":"\(state)","complete_sessions":2,"pending_sessions":3,"findings":[]},"command":"asl run /plan","project":"API","profile":"work","directory":"/repo","runtime_session_id":"\(WorkspaceFixture.runtimeID(1))"}}
        """.utf8), stderr: Data()))
    }

    func testPlanAndOptionsUseArgumentsAndReadinessMustMatch() {
        let runner = PlanWorkRunner()
        var calls: [[String]] = []
        let command = command { args in calls.append(args); return self.reply() }
        let model = WorkspaceRunPlanModel(command: { command }, runner: runner)
        model.plan = "--not-a-flag"
        model.project = "API with spaces"
        model.harness = "codex"
        model.reviewHarness = "claude"
        model.commitMode = "at-end"
        model.start()
        XCTAssertTrue(calls.isEmpty)
        model.check(); runner.release()
        XCTAssertEqual(Array(calls[0].suffix(2)), ["--", "--not-a-flag"])
        XCTAssertTrue(calls[0].contains("--check"))
        XCTAssertTrue(calls[0].contains("API with spaces"))
        XCTAssertTrue(model.canStart)
        model.profile = "different"
        XCTAssertFalse(model.canStart)
        XCTAssertTrue(model.optionsChanged)
    }

    func testLateReadinessIsDiscardedAfterOptionsChange() {
        let runner = PlanWorkRunner()
        let command = command { _ in self.reply() }
        let model = WorkspaceRunPlanModel(command: { command }, runner: runner)
        model.plan = "/one"; model.check(); model.plan = "/two"; runner.release()
        XCTAssertNil(model.preview)
        XCTAssertFalse(model.canStart)
    }

    func testLaunchUsesExistingManagedPathAndPreventsDuplicateClicks() {
        let runner = PlanWorkRunner()
        var calls: [[String]] = []
        let command = command { args in calls.append(args); return self.reply(started: !args.contains("--check")) }
        let model = WorkspaceRunPlanModel(command: { command }, runner: runner)
        var opened: [String] = []
        model.onStarted = { opened.append($0) }
        model.plan = "/plan"; model.check(); runner.release()
        model.start(); model.start()
        XCTAssertTrue(model.launching)
        XCTAssertEqual(runner.pending.count, 1)
        runner.release()
        XCTAssertEqual(calls.count, 2)
        XCTAssertFalse(calls[1].contains("--check"))
        XCTAssertEqual(opened, [WorkspaceFixture.runtimeID(1)])
        XCTAssertFalse(model.canStart)
    }

    func testUnknownLaunchOutcomeStaysLockedAfterCheckingAgain() {
        let runner = PlanWorkRunner()
        let command = command { args in args.contains("--check") ? self.reply() : .timedOut }
        let model = WorkspaceRunPlanModel(command: { command }, runner: runner)
        model.plan = "/plan"; model.check(); runner.release(); model.start(); runner.release()
        XCTAssertTrue(model.notice?.contains("outcome is unknown") == true)
        model.check(); runner.release()
        XCTAssertFalse(model.canStart)
        XCTAssertTrue(model.submitted.contains("/plan"))
    }

    func testNotReadyDoesNotLaunch() {
        let runner = PlanWorkRunner()
        let command = command { _ in self.reply(state: "not_ready") }
        let model = WorkspaceRunPlanModel(command: { command }, runner: runner)
        model.plan = "/plan"; model.check(); runner.release(); model.start()
        XCTAssertFalse(model.canStart)
        XCTAssertFalse(model.launching)
        XCTAssertTrue(runner.pending.isEmpty)
    }

    func testPartialReadyCheckDoesNotAuthorizeLaunch() {
        let runner = PlanWorkRunner()
        let command = command { _ in
            guard case var .finished(output) = self.reply() else { fatalError() }
            output = ProcessOutput(status: 1, stdout: output.stdout, stderr: Data())
            return .finished(output)
        }
        let model = WorkspaceRunPlanModel(command: { command }, runner: runner)
        model.plan = "/plan"; model.check(); runner.release()
        XCTAssertNotNil(model.preview)
        XCTAssertFalse(model.canStart)
    }
}

extension WorkspaceRunPlanTests {
    func testChoicesIncludeUnassignedProfilesAndKeepExactProjectIdentity() {
        let runner = PlanWorkRunner()
        let command = command { args in
            let data = args.first == "project"
                ? #"[{"title":"API","profile":"work","directory":"/repo"},{"title":"API","profile":"personal","directory":"/other"}]"#
                : #"[{"name":"work","harness":"claude"},{"name":"review-only","harness":"codex"}]"#
            return .finished(ProcessOutput(status: 0, stdout: Data("{\"schema_version\":1,\"data\":\(data)}".utf8), stderr: Data()))
        }
        let model = WorkspaceRunPlanModel(command: { command }, runner: runner)
        model.loadChoices(); runner.release()
        XCTAssertEqual(model.profiles.map(\.name), ["work", "review-only"])
        model.project = "personal/API"
        XCTAssertEqual(model.projectLabel, "API · personal")
        XCTAssertEqual(model.profileLabel, "personal (project default)")
        model.profile = "review-only"
        XCTAssertNil(model.profileProblem)
        XCTAssertTrue(model.arguments.contains("personal/API"))
        XCTAssertTrue(model.destinationSummary.contains("review-only"))
        model.profile = "missing"
        XCTAssertNotNil(model.profileProblem)
        model.project = "API"
        XCTAssertNotNil(model.projectProblem)
    }

    func testReadyPlanWithoutResolvedProjectCannotStart() {
        let runner = PlanWorkRunner()
        let command = command { _ in
            .finished(ProcessOutput(status: 0, stdout: Data(#"{"schema_version":1,"data":{"started":false,"readiness":{"state":"ready","complete_sessions":0,"pending_sessions":2,"findings":[]},"command":"asl run /plan","project":"","profile":"","directory":"/repo"}}"#.utf8), stderr: Data()))
        }
        let model = WorkspaceRunPlanModel(command: { command }, runner: runner)
        model.plan = "/plan"; model.check(); runner.release()
        XCTAssertFalse(model.canStart)
        XCTAssertEqual(model.projectLabel, "Infer from plan workspace")
    }

    func testChoiceFailureKeepsPreviousCatalogAndExplainsMissingDirectory() {
        let runner = PlanWorkRunner()
        var fail = false
        let command = command { args in
            if fail { return .timedOut }
            let data = args.first == "project"
                ? #"[{"title":"Missing","profile":"work","directory":"/absent","directory_problem":"does not exist"}]"#
                : #"[{"name":"work","harness":"claude"}]"#
            return .finished(ProcessOutput(status: 0, stdout: Data("{\"schema_version\":1,\"data\":\(data)}".utf8), stderr: Data()))
        }
        let model = WorkspaceRunPlanModel(command: { command }, runner: runner)
        model.loadChoices(); runner.release()
        model.project = "work/Missing"
        XCTAssertTrue(model.projectProblem?.contains("does not exist") == true)
        fail = true; model.loadChoices(); runner.release()
        XCTAssertEqual(model.projects.count, 1)
        XCTAssertNotNil(model.choicesError)
    }
}
