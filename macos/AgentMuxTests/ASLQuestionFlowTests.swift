import XCTest
@testable import AgentMux

/// Runs the work and its continuation at once, on the calling thread.
private struct ImmediateRunner: ASLWorkRunner {
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) { then(work()) }
}

/// Holds work until the test releases it, to stand in for a slow daemon.
private final class HeldRunner: ASLWorkRunner {
    private var held: [() -> Void] = []
    func run<T>(_ work: @escaping () -> T, then: @escaping (T) -> Void) {
        held.append { then(work()) }
    }
    func release() {
        let pending = held
        held = []
        pending.forEach { $0() }
    }
}

private final class FakeASLService: ASLService {
    var snapshotResult: Result<ASLResult, ASLRequestError> = .success(ASLResult())
    var bodies: [String: Result<ASLQuestionBody, ASLRequestError>] = [:]
    var actResults: [Result<ASLOperation, ASLRequestError>] = []
    private(set) var calls: [String] = []
    private(set) var actions: [ASLRequestParams] = []

    func snapshot() -> Result<ASLResult, ASLRequestError> {
        calls.append("asl_snapshot")
        return snapshotResult
    }

    func question(planDir: String, questionID: String) -> Result<ASLQuestionBody, ASLRequestError> {
        calls.append("asl_question \(questionID)")
        return bodies[questionID] ?? .failure(.refused("no-open-question: gone"))
    }

    func act(_ params: ASLRequestParams) -> Result<ASLOperation, ASLRequestError> {
        calls.append("asl_action \(params.method)")
        actions.append(params)
        return actResults.isEmpty ? .success(ASLOperation(operationID: params.operationID, status: "accepted")) : actResults.removeFirst()
    }
}

@MainActor
final class ASLQuestionFlowTests: XCTestCase {
    private var service: FakeASLService!
    private var closes: [String?] = []

    override func setUp() {
        super.setUp()
        service = FakeASLService()
        closes = []
    }

    private func flow(runner: ASLWorkRunner = ImmediateRunner()) -> ASLQuestionFlow {
        let flow = ASLQuestionFlow(service: service, runner: runner)
        flow.onClose = { [weak self] notice in self?.closes.append(notice) }
        return flow
    }

    private func serve(_ rows: [ASLRow], status: ASLStatus = ASLFixtures.liveStatus) {
        service.snapshotResult = .success(ASLResult(status: status, snapshot: ASLSnapshot(rows: rows), meta: ASLSnapshotMeta()))
    }

    func testReplyIsAcceptedAndThePanelCloses() {
        var row = ASLFixtures.replyRow()
        row.question?.questionIDs = ["q-api-1"]
        serve([ASLFixtures.pausedRow(), row])
        service.bodies["q-api-1"] = .success(ASLFixtures.body(row, "q-api-1", "Run the backfill now?"))
        let flow = flow()

        flow.open(ASLAnswerTarget(runID: "run-reply", windowID: 7))
        XCTAssertEqual(flow.phase, .open)
        XCTAssertEqual(flow.state.body.reveal(), "Run the backfill now?")
        XCTAssertEqual(flow.state.buttons.map(\.label), ["Reply", "Continue", "Pause"])

        flow.setDraft("yes, after the cutover")
        flow.submit(flow.state.buttons[0])

        XCTAssertEqual(service.calls, ["asl_snapshot", "asl_question q-api-1", "asl_action question.reply"])
        XCTAssertEqual(service.actions.first?.actionToken, "token-reply")
        XCTAssertEqual(service.actions.first?.text.reveal(), "yes, after the cutover")
        XCTAssertEqual(closes, [nil], "the panel closed itself without a notice")
        XCTAssertEqual(flow.phase, .idle)
        XCTAssertEqual(flow.state, ASLResponseState(), "the body and draft are cleared")
    }

    func testPausedRunOffersReplyAndResume() {
        let row = ASLFixtures.pausedRow()
        serve([row])
        service.bodies["q-web-1"] = .success(ASLFixtures.body(row, "q-web-1", "Paused: proceed?"))
        let flow = flow()
        flow.open(ASLAnswerTarget(runID: "run-paused"))
        XCTAssertEqual(flow.state.buttons.map(\.label), ["Reply and resume"])
        flow.setDraft("proceed")
        flow.submit(flow.state.buttons[0])
        XCTAssertEqual(service.actions.map(\.method), [ASLMethod.runResume])
        XCTAssertEqual(closes, [nil])
    }

    func testRemainingQuestionsAreAnsweredInTurn() {
        let row = ASLFixtures.replyRow()
        serve([row])
        service.bodies["q-api-1"] = .success(ASLFixtures.body(row, "q-api-1", "First?"))
        service.bodies["q-api-2"] = .success(ASLFixtures.body(row, "q-api-2", "Second?", token: "token-reply-2"))
        let flow = flow()
        flow.open(ASLAnswerTarget(runID: "run-reply"))
        flow.setDraft("one")
        flow.submit(flow.state.buttons[0])
        XCTAssertEqual(flow.phase, .open)
        XCTAssertEqual(flow.state.body.reveal(), "Second?")
        XCTAssertEqual(flow.state.target.actionToken, "token-reply-2")
        XCTAssertTrue(closes.isEmpty)
        flow.submit(flow.state.buttons[1])
        XCTAssertEqual(service.actions.map(\.questionID), ["q-api-1", "q-api-2"])
        XCTAssertEqual(service.actions.map(\.actionToken), ["token-reply", "token-reply-2"])
        XCTAssertEqual(service.actions.last?.resolution, "continue")
        XCTAssertEqual(closes, [nil])
    }

    func testARunThatMovedClosesThePanelWithANotice() {
        let row = ASLFixtures.replyRow()
        serve([row])
        service.bodies["q-api-1"] = .success(ASLFixtures.body(row, "q-api-1", "First?"))
        service.actResults = [.failure(.refused("stale-action-token: expired"))]
        let flow = flow()
        flow.open(ASLAnswerTarget(runID: "run-reply"))
        flow.setDraft("one")
        flow.submit(flow.state.buttons[0])
        XCTAssertEqual(closes, [ASLRefusal.targetChanged])
        XCTAssertEqual(flow.state, ASLResponseState())
    }

    func testRefusalsShowTheFixedSentence() {
        let flow = flow()
        serve([ASLFixtures.replyRow()])
        flow.open(ASLAnswerTarget(runID: "run-gone"))
        XCTAssertEqual(flow.phase, .refused("this run is no longer waiting on an answer"))

        service.snapshotResult = .failure(.refused("asl bridge disabled: \"secret\""))
        flow.open(ASLAnswerTarget(runID: "run-reply"))
        XCTAssertEqual(flow.phase, .refused("actions are unavailable: agent-session-loop is not reachable"))

        service.snapshotResult = .failure(.notRunning)
        flow.open(ASLAnswerTarget(runID: "run-other"))
        XCTAssertEqual(flow.phase, .refused("the daemon is not running: nothing was sent"))

        serve([ASLFixtures.replyRow()], status: ASLStatus(available: true))
        flow.open(ASLAnswerTarget(runID: "run-reply"))
        XCTAssertEqual(flow.phase, .refused("actions are unavailable: the daemon reports agent-session-loop mutations as disabled"))
        XCTAssertTrue(service.actions.isEmpty)
        XCTAssertTrue(closes.isEmpty)
    }

    func testCancelDropsTheFormAndLateReplies() {
        let row = ASLFixtures.replyRow()
        serve([row])
        service.bodies["q-api-1"] = .success(ASLFixtures.body(row, "q-api-1", "First?"))
        let runner = HeldRunner()
        let flow = flow(runner: runner)
        flow.open(ASLAnswerTarget(runID: "run-reply"))
        XCTAssertEqual(flow.phase, .loading)
        runner.release() // snapshot
        runner.release() // body
        flow.setDraft("secret answer text")
        flow.submit(flow.state.buttons[0])
        flow.cancel()
        XCTAssertEqual(flow.state, ASLResponseState())
        XCTAssertNil(flow.answering)
        runner.release() // the action's reply arrives after the close
        XCTAssertEqual(flow.phase, .idle)
        XCTAssertTrue(closes.isEmpty, "a reply for a cancelled form does nothing")
    }

    func testReopeningTheSameRunKeepsTheDraft() {
        let row = ASLFixtures.replyRow()
        serve([row])
        service.bodies["q-api-1"] = .success(ASLFixtures.body(row, "q-api-1", "First?"))
        let flow = flow()
        flow.open(ASLAnswerTarget(runID: "run-reply"))
        flow.setDraft("half written")
        flow.open(ASLAnswerTarget(runID: "run-reply"))
        XCTAssertEqual(flow.state.draft.reveal(), "half written")
        XCTAssertEqual(service.calls.filter { $0 == "asl_snapshot" }.count, 1)
    }

    func testClosingThePanelKeepsTheRunsDrafts() {
        let row = ASLFixtures.replyRow()
        serve([row, ASLFixtures.pausedRow()])
        service.bodies["q-api-1"] = .success(ASLFixtures.body(row, "q-api-1", "First?"))
        service.bodies["q-web-1"] = .success(ASLFixtures.body(ASLFixtures.pausedRow(), "q-web-1", "Paused?"))
        let flow = flow()
        flow.open(ASLAnswerTarget(runID: "run-reply"))
        flow.setDraft("half written")
        flow.cancel()
        XCTAssertEqual(flow.state, ASLResponseState(), "the form and its body go")
        XCTAssertEqual(flow.keptDrafts(run: "run-reply").mapValues { $0.reveal() }, ["q-api-1": "half written"])

        flow.open(ASLAnswerTarget(runID: "run-reply"))
        XCTAssertEqual(flow.state.draft.reveal(), "half written", "closing and reopening keeps the draft")

        // Opening another run keeps this one's draft too.
        flow.open(ASLAnswerTarget(runID: "run-paused"))
        XCTAssertEqual(flow.state.draft.reveal(), "")
        flow.open(ASLAnswerTarget(runID: "run-reply"))
        XCTAssertEqual(flow.state.draft.reveal(), "half written")
    }

    func testKeptDraftsGoWhenTheRunsQuestionsMove() {
        let row = ASLFixtures.replyRow()
        serve([row])
        service.bodies["q-api-1"] = .success(ASLFixtures.body(row, "q-api-1", "First?"))
        let flow = flow()
        flow.open(ASLAnswerTarget(runID: "run-reply"))
        flow.setDraft("half written")
        flow.cancel()

        serve([])
        flow.open(ASLAnswerTarget(runID: "run-reply"))
        XCTAssertEqual(flow.phase, .refused("this run is no longer waiting on an answer"))
        XCTAssertTrue(flow.keptDrafts(run: "run-reply").isEmpty)

        serve([row])
        flow.open(ASLAnswerTarget(runID: "run-reply"))
        flow.setDraft("another")
        service.actResults = [.failure(.refused("stale-action-token: expired"))]
        flow.submit(flow.state.buttons[0])
        XCTAssertEqual(closes, [ASLRefusal.targetChanged])
        flow.open(ASLAnswerTarget(runID: "run-reply"))
        XCTAssertEqual(flow.state.draft.reveal(), "", "a run that moved does not bring its draft back")
    }

    func testAnAnswerSentBeforeThePanelClosedIsReported() {
        let row = ASLFixtures.replyRow()
        serve([row])
        service.bodies["q-api-1"] = .success(ASLFixtures.body(row, "q-api-1", "First?"))
        let runner = HeldRunner()
        let flow = flow(runner: runner)
        var late: [String] = []
        flow.onLateResult = { late.append($0) }
        flow.open(ASLAnswerTarget(runID: "run-reply"))
        runner.release() // snapshot
        runner.release() // body
        flow.setDraft("sent anyway")
        flow.submit(flow.state.buttons[0])
        XCTAssertFalse(ASLQuestionPanelController.mayClose(flow), "the panel stays while the answer is on its way")
        flow.cancel()
        XCTAssertTrue(ASLQuestionPanelController.mayClose(flow))
        runner.release() // the action's reply
        XCTAssertEqual(late, ["the answer to api migration was accepted"])
        XCTAssertTrue(closes.isEmpty, "a late result never closes a panel opened since")
        XCTAssertTrue(flow.keptDrafts(run: "run-reply").isEmpty, "the answered draft is not kept")
    }

    func testLateNotices() {
        XCTAssertEqual(ASLQuestionFlow.lateNotice(label: "", .failure(.refused("no-open-question: x"))),
                       "the answer to the run was not applied: the run changed since it was selected")
        XCTAssertEqual(ASLQuestionFlow.lateNotice(label: "api", .failure(.transport("t"))),
                       "the answer to api did not complete: the action did not complete; its outcome is unknown and it was not retried")
    }

    func testABodyThatFailedToLoadLoadsAgain() {
        let row = ASLFixtures.replyRow()
        serve([row])
        service.bodies["q-api-1"] = .failure(.transport("timed out"))
        let flow = flow()
        flow.open(ASLAnswerTarget(runID: "run-reply"))
        XCTAssertEqual(flow.phase, .open)
        XCTAssertTrue(flow.state.loadFailed)
        service.bodies["q-api-1"] = .success(ASLFixtures.body(row, "q-api-1", "First?"))
        flow.reload()
        XCTAssertEqual(flow.state.body.reveal(), "First?")
        XCTAssertEqual(service.calls.filter { $0 == "asl_question q-api-1" }.count, 2)
    }
}

