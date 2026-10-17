import XCTest
@testable import AgentMux

private final class RunsWorkRunner: ASLWorkRunner {
    var hold = false
    var pending: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) {
        let value = work()
        if hold { pending.append { then(value) } } else { then(value) }
    }
    func release(_ index: Int = 0) { pending.remove(at: index)() }
}

private final class RunsService: ASLService {
    var result = ASLResult()
    var snapshotError: ASLRequestError?
    var actionError: ASLRequestError?
    var requests: [ASLRequestParams] = []
    func snapshot() -> Result<ASLResult, ASLRequestError> { snapshotError.map(Result.failure) ?? .success(result) }
    func question(planDir: String, questionID: String) -> Result<ASLQuestionBody, ASLRequestError> { .failure(.unsupported) }
    func act(_ params: ASLRequestParams) -> Result<ASLOperation, ASLRequestError> {
        requests.append(params)
        return actionError.map(Result.failure) ?? .success(ASLOperation(operationID: params.operationID, method: params.method, status: "accepted"))
    }
}

@MainActor
final class WorkspaceRunsTests: XCTestCase {
    private func row(_ id: String = "one") -> ASLRow {
        var row = ASLRow()
        row.rowID = id; row.runID = "run-\(id)"; row.planDir = "/\(id)"
        row.primaryState = "paused"; row.pauseReason = "verification-failed"
        row.actions = [ASLMethod.runResume, ASLMethod.runAbort, ASLMethod.runStopAfterCurrent]
        row.actionToken = "token-\(id)"
        return row
    }
    private func service(_ rows: [ASLRow]) -> RunsService {
        let service = RunsService()
        service.result = ASLResult(status: ASLStatus(available: true, mutationsEnabled: true), snapshot: ASLSnapshot(rows: rows), meta: ASLSnapshotMeta())
        return service
    }
    private func command(_ data: @escaping ([String]) -> String) -> AMXCommand {
        var command = AMXCommand(binary: URL(fileURLWithPath: "/amx"), launchEnvironment: LaunchEnvironment(home: "/h", path: "/bin"))
        command.runner = { _, args, _, _ in .finished(ProcessOutput(status: 0, stdout: Data("{\"schema_version\":1,\"data\":\(data(args))}".utf8), stderr: Data())) }
        return command
    }
    private func detail(plan: String, run: String) -> String {
        """
        {"plan_dir":"\(plan)","plan_title":"Plan","counts":{"complete":2,"pending":1},"sessions":[{"id":"2.1","status":"pending","path":"/one/session.md"}],"runner_state":{"run_id":"\(run)","paused":true,"reason":"verification-failed","run_started_at":"start","run_finished_at":""},"verification":[{"event":"verification_finished","at":"now","session_id":"2.1","passed":false,"check_count":3,"passed_count":2}],"verification_truncated":false}
        """
    }

    func testPausedRunWithoutQuestionIsListedAndCanResume() {
        let service = service([row()])
        let model = WorkspaceRunsModel(service: service, command: { nil }, runner: RunsWorkRunner())
        model.refresh()
        XCTAssertEqual(model.rows.count, 1)
        XCTAssertFalse(model.hasQuestion)
        XCTAssertNil(model.disabledReason(ASLMethod.runResume))
        model.perform(ASLMethod.runResume)
        XCTAssertEqual(service.requests.count, 1)
        XCTAssertEqual(service.requests[0].runID, "run-one")
        XCTAssertNil(model.pending)
        XCTAssertTrue(model.actionNotice?.contains("accepted") == true)
    }

    func testStaleAndReadOnlySnapshotsDisableControlsButKeepRows() {
        let service = service([row()])
        let model = WorkspaceRunsModel(service: service, command: { nil }, runner: RunsWorkRunner())
        model.refresh()
        service.result.status.snapshotStale = true
        model.refresh()
        XCTAssertEqual(model.rows.count, 1)
        XCTAssertNotNil(model.disabledReason(ASLMethod.runAbort))
        model.perform(ASLMethod.runAbort)
        XCTAssertTrue(service.requests.isEmpty)
        service.result.status.snapshotStale = false
        service.result.status.mutationsEnabled = false
        model.refresh()
        XCTAssertNotNil(model.disabledReason(ASLMethod.runResume))
        service.snapshotError = .notRunning
        model.refresh()
        XCTAssertTrue(model.stale)
        XCTAssertEqual(model.rows.count, 1)
    }

    func testFreshAuthorizationRefusesAReplacedRunOrNewQuestion() {
        let service = service([row()])
        let model = WorkspaceRunsModel(service: service, command: { nil }, runner: RunsWorkRunner())
        model.refresh()
        service.result.snapshot?.rows[0].runID = "replacement"
        model.perform(ASLMethod.runAbort)
        XCTAssertTrue(service.requests.isEmpty)
        model.refresh()
        service.result.snapshot?.rows[0].question = ASLRowQuestion(questionID: "new-question")
        model.perform(ASLMethod.runResume)
        XCTAssertTrue(service.requests.isEmpty)
    }

    func testFreshAuthorizationRefusesRevokedPermission() {
        let service = service([row()])
        let model = WorkspaceRunsModel(service: service, command: { nil }, runner: RunsWorkRunner())
        model.refresh()
        service.result.status.mutationsEnabled = false
        model.perform(ASLMethod.runAbort)
        XCTAssertTrue(service.requests.isEmpty)
        XCTAssertFalse(model.acting)
    }

    func testConfirmationCannotActOnAnotherSelectedRun() {
        let first = row()
        let service = service([first, row("two")])
        let model = WorkspaceRunsModel(service: service, command: { nil }, runner: RunsWorkRunner())
        model.refresh()
        model.select("two")
        model.perform(ASLMethod.runAbort, target: first)
        XCTAssertTrue(service.requests.isEmpty)
        XCTAssertTrue(model.actionNotice?.contains("selected run changed") == true)
    }

    func testUnknownOutcomeRetriesExactOperationEvenAfterSelectionChanges() {
        let service = service([row(), row("two")])
        let model = WorkspaceRunsModel(service: service, command: { nil }, runner: RunsWorkRunner())
        model.refresh()
        service.actionError = .transport("lost reply")
        model.perform(ASLMethod.runAbort)
        XCTAssertEqual(model.actionFeedback?.kind, .warning)
        XCTAssertEqual(model.actionFeedback?.recovery, .receipt)
        XCTAssertTrue(model.actionFeedback?.target.contains("run-one") == true)
        let original = model.pending
        XCTAssertNotNil(original)
        model.select("two")
        XCTAssertTrue(model.actionFeedback?.target.contains("run-one") == true)
        model.perform(ASLMethod.runAbort)
        XCTAssertEqual(service.requests.count, 1)
        model.refresh()
        XCTAssertEqual(service.requests.count, 1, "polling must never retry a mutation")
        service.actionError = nil
        model.retryReceipt()
        XCTAssertEqual(service.requests, [original!, original!])
        XCTAssertNil(model.pending)
    }

    func testDuplicateClickWhileReauthorizingDoesNotSubmitTwice() {
        let service = service([row()])
        let runner = RunsWorkRunner()
        let model = WorkspaceRunsModel(service: service, command: { nil }, runner: runner)
        model.refresh()
        runner.hold = true
        model.perform(ASLMethod.runAbort)
        model.perform(ASLMethod.runAbort)
        XCTAssertEqual(runner.pending.count, 1)
        runner.release()
        XCTAssertEqual(service.requests.count, 1)
        model.perform(ASLMethod.runAbort)
        XCTAssertEqual(service.requests.count, 1)
        runner.release()
        XCTAssertFalse(model.acting)
    }

    func testQuestionUsesExistingAnswerFlowInsteadOfAnswerlessResume() {
        var row = row()
        row.question = ASLRowQuestion(questionID: "q", unansweredCount: 1)
        let service = service([row])
        let model = WorkspaceRunsModel(service: service, command: { nil }, runner: RunsWorkRunner())
        var answered: ASLAnswerTarget?
        model.onAnswer = { answered = $0 }
        model.refresh(); model.answer(); model.perform(ASLMethod.runResume)
        XCTAssertEqual(answered?.runID, "run-one")
        XCTAssertTrue(service.requests.isEmpty)
    }

    func testDetailMatchesRunIdentityAndShowsActualCountsAndFailures() {
        let service = service([row()])
        var returnedRun = "run-one"
        let command = command { _ in self.detail(plan: "/one", run: returnedRun) }
        let model = WorkspaceRunsModel(service: service, command: { command }, runner: RunsWorkRunner())
        model.refresh()
        XCTAssertEqual(model.detail?.counts["complete"], 2)
        XCTAssertEqual(model.detail?.sessions[0].id, "2.1")
        XCTAssertTrue(model.detail?.verification[0].summary.contains("failed") == true)
        returnedRun = "other-run"
        model.readDetail()
        XCTAssertNil(model.detail)
        XCTAssertTrue(model.detailError?.contains("different run") == true)
    }

    func testLateDetailCannotReplaceAnotherSelectionOrReopenClosedView() {
        let service = service([row(), row("two")])
        let runner = RunsWorkRunner()
        let command = command { args in self.detail(plan: args.last!, run: "run-\((args.last! as NSString).lastPathComponent)") }
        let model = WorkspaceRunsModel(service: service, command: { command }, runner: runner)
        model.refresh()
        runner.hold = true
        model.readDetail()
        model.select("two")
        runner.release(1)
        XCTAssertEqual(model.detail?.runner.runID, "run-two")
        runner.release()
        XCTAssertEqual(model.detail?.runner.runID, "run-two")
        model.readDetail(); model.close(); runner.release()
        XCTAssertNil(model.detail)
    }
}
