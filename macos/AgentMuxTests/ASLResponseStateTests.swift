import XCTest
@testable import AgentMux

/// Rows shaped like the Go response tests' fixtures (`tui/asl_response_test.go`).
enum ASLFixtures {
    static func replyRow() -> ASLRow {
        var row = ASLRow()
        row.rowID = "row-reply"
        row.planDir = "/tmp/plans/api"
        row.label = "api migration"
        row.runID = "run-reply"
        row.actionToken = "token-reply"
        row.actions = [ASLMethod.questionReply, ASLMethod.questionResolve]
        row.question = ASLRowQuestion(contextKind: "question", questionID: "q-api-1", unansweredCount: 2,
                                      questionIDs: ["q-api-1", "q-api-2"], askedAt: "2026-10-03T10:00:00Z")
        return row
    }

    static func pausedRow() -> ASLRow {
        var row = ASLRow()
        row.rowID = "row-paused"
        row.planDir = "/tmp/plans/web"
        row.runID = "run-paused"
        row.actionToken = "token-paused"
        row.actions = [ASLMethod.runResume]
        row.question = ASLRowQuestion(contextKind: "pause", questionID: "q-web-1", unansweredCount: 1, questionIDs: ["q-web-1"])
        return row
    }

    static let liveStatus = ASLStatus(available: true, mutationsEnabled: true)

    static func body(_ row: ASLRow, _ questionID: String, _ text: String, token: String = "") -> ASLQuestionBody {
        ASLQuestionBody(planDir: row.planDir, rowID: row.rowID, runID: row.runID, questionID: questionID,
                        contextKind: row.question?.contextKind ?? "", body: SecretText(text),
                        bodyBytes: text.utf8.count, actionToken: token)
    }
}

final class ASLResponseStateTests: XCTestCase {
    private var minted = 0
    private func mint() -> String {
        minted += 1
        return "op-\(minted)"
    }

    /// Opens a form on `row` and loads its first body.
    private func opened(_ row: ASLRow = ASLFixtures.replyRow(), body: String = "Should the migration run first?") -> ASLResponseState {
        guard case let .opened(state, effect) = ASLResponseState.open(row: row, view: .live, status: ASLFixtures.liveStatus) else {
            XCTFail("the form did not open")
            return ASLResponseState()
        }
        var loaded = state
        guard case let .fetch(seq, _, questionID) = effect else {
            XCTFail("opening did not fetch a body: \(effect)")
            return state
        }
        XCTAssertNil(loaded.questionLoaded(seq: seq, .success(ASLFixtures.body(row, questionID, body))))
        return loaded
    }

    private func params(_ effect: ASLPanelEffect?) -> ASLRequestParams? {
        guard case let .act(_, params) = effect else { return nil }
        return params
    }

    // MARK: Ported predicates

    func testButtonSets() {
        XCTAssertEqual(ASLResponse.buttons(.live).map(\.label), ["Reply", "Continue", "Pause"])
        XCTAssertEqual(ASLResponse.buttons(.live).map(\.method),
                       [ASLMethod.questionReply, ASLMethod.questionResolve, ASLMethod.questionResolve])
        XCTAssertEqual(ASLResponse.buttons(.live).map(\.resolution), ["", "continue", "pause"])
        XCTAssertEqual(ASLResponse.buttons(.live).map(\.needsText), [true, false, false])
        XCTAssertEqual(ASLResponse.buttons(.paused),
                       [ASLResponseButton(label: "Reply and resume", method: ASLMethod.runResume, needsText: true)])
    }

    func testKindForAndGateMethod() {
        XCTAssertEqual(ASLResponse.kind(for: ASLFixtures.replyRow()), .live)
        XCTAssertEqual(ASLResponse.kind(for: ASLFixtures.pausedRow()), .paused)
        var neither = ASLFixtures.pausedRow()
        neither.actions = []
        XCTAssertEqual(ASLResponse.kind(for: neither), .paused, "the context kind breaks the tie")
        neither.question?.contextKind = "question"
        XCTAssertEqual(ASLResponse.kind(for: neither), .live)
        var both = ASLFixtures.replyRow()
        both.actions = [ASLMethod.runResume, ASLMethod.questionReply]
        XCTAssertEqual(ASLResponse.kind(for: both), .live, "reply outranks resume")
        XCTAssertEqual(ASLResponse.gateMethod(.live), ASLMethod.questionReply)
        XCTAssertEqual(ASLResponse.gateMethod(.paused), ASLMethod.runResume)
    }

    func testQuestionIDs() {
        XCTAssertEqual(ASLResponse.questionIDs(ASLFixtures.replyRow()), ["q-api-1", "q-api-2"])
        var single = ASLFixtures.replyRow()
        single.question?.questionIDs = []
        XCTAssertEqual(ASLResponse.questionIDs(single), ["q-api-1"])
        single.question = nil
        XCTAssertEqual(ASLResponse.questionIDs(single), [])
    }

    func testOpenRefusesWithTheFixedSentences() {
        let row = ASLFixtures.replyRow()
        func refusal(_ row: ASLRow, view: ASLView = .live, status: ASLStatus = ASLFixtures.liveStatus) -> String? {
            if case let .refused(reason) = ASLResponseState.open(row: row, view: view, status: status) { return reason }
            return nil
        }
        XCTAssertEqual(refusal(row, view: .stale), "actions are unavailable: this snapshot is read-only")
        XCTAssertEqual(refusal(row, view: .unavailable), "actions are unavailable: agent-session-loop is not reachable")
        XCTAssertEqual(refusal(row, status: ASLStatus(available: true)),
                       "actions are unavailable: the daemon reports agent-session-loop mutations as disabled")
        var tokenless = row
        tokenless.actionToken = "  "
        XCTAssertEqual(refusal(tokenless), "actions are unavailable: this run carries no action token")
        var disabled = row
        disabled.actions = []
        disabled.disabledReasons = [ASLMethod.questionReply: "the question was withdrawn"]
        XCTAssertEqual(refusal(disabled), "reply is unavailable: the question was withdrawn")
        disabled.disabledReasons = [:]
        XCTAssertEqual(refusal(disabled), "reply is unavailable for this run")
        var paused = ASLFixtures.pausedRow()
        paused.actions = []
        XCTAssertEqual(refusal(paused), "resume is unavailable for this run")
        XCTAssertNil(refusal(row))
    }

    func testViewFromAFreshSnapshot() {
        XCTAssertEqual(ASLView.of(status: ASLFixtures.liveStatus, meta: ASLSnapshotMeta(), hasRows: true), .live)
        XCTAssertEqual(ASLView.of(status: ASLFixtures.liveStatus, meta: ASLSnapshotMeta(ageSeconds: 4, stale: true), hasRows: true), .stale)
        XCTAssertEqual(ASLView.of(status: ASLStatus(), meta: ASLSnapshotMeta(ageSeconds: 11, stale: true), hasRows: true), .unavailable)
        XCTAssertEqual(ASLView.of(status: ASLStatus(), meta: nil, hasRows: false), .unavailable)
    }

    func testOpenBindsTheRowsSelectedQuestion() {
        var row = ASLFixtures.replyRow()
        row.question?.questionID = "q-api-2"
        guard case let .opened(state, effect) = ASLResponseState.open(row: row, view: .live, status: ASLFixtures.liveStatus) else {
            return XCTFail("the form did not open")
        }
        XCTAssertEqual(state.target, ASLActionTarget(rowID: "row-reply", planDir: "/tmp/plans/api", runID: "run-reply",
                                                     questionID: "q-api-2", actionToken: "token-reply", label: "api migration"))
        XCTAssertEqual(state.questionIndex, 1)
        XCTAssertTrue(state.fetching)
        XCTAssertEqual(effect, .fetch(seq: 1, planDir: "/tmp/plans/api", questionID: "q-api-2"))
    }

    // MARK: Submitting (ported from tui/asl_response_test.go)

    func testLiveReplySubmitsCapturedTarget() {
        var state = opened()
        XCTAssertEqual(state.kind, .live)
        XCTAssertEqual(state.body.reveal(), "Should the migration run first?")
        state.setDraft("after the cutover")
        let sent = params(state.submit(state.buttons[0], mint: mint))
        XCTAssertEqual(sent?.method, ASLMethod.questionReply)
        XCTAssertEqual(sent?.questionID, "q-api-1")
        XCTAssertEqual(sent?.rowID, "row-reply")
        XCTAssertEqual(sent?.runID, "run-reply")
        XCTAssertEqual(sent?.actionToken, "token-reply")
        XCTAssertEqual(sent?.operationID, "op-1")
        XCTAssertEqual(sent?.text.reveal(), "after the cutover")
        XCTAssertTrue(state.inFlight)
        XCTAssertEqual(state.status, "submitting")
    }

    func testContinueAndPauseNeedNoAnswer() {
        for (index, resolution) in [(1, "continue"), (2, "pause")] {
            var state = opened()
            let sent = params(state.submit(state.buttons[index], mint: mint))
            XCTAssertEqual(sent?.method, ASLMethod.questionResolve)
            XCTAssertEqual(sent?.resolution, resolution)
            XCTAssertEqual(sent?.text, SecretText(), "an answerless closure carries no text")
        }
    }

    func testBlankAnswerIsRefused() {
        var state = opened()
        state.setDraft("  \n ")
        XCTAssertFalse(state.canSubmit(state.buttons[0]))
        XCTAssertNil(state.submit(state.buttons[0], mint: mint))
        XCTAssertEqual(state.notice, "the answer is empty: type a reply before submitting")
        XCTAssertEqual(state.noticeKind, .info, "a reply still to be typed is no error")
        XCTAssertFalse(state.inFlight)
        XCTAssertTrue(state.canSubmit(state.buttons[1]), "Continue needs no answer")
    }

    func testPausedRunOffersOnlyReplyAndResume() {
        var state = opened(ASLFixtures.pausedRow(), body: "The run paused: proceed?")
        XCTAssertEqual(state.kind, .paused)
        XCTAssertEqual(state.buttons.map(\.label), ["Reply and resume"])
        state.setDraft("proceed with the migration")
        let effect = state.submit(state.buttons[0], mint: mint)
        guard case let .act(seq, sent) = effect else { return XCTFail("no submission: \(String(describing: effect))") }
        XCTAssertEqual(sent.method, ASLMethod.runResume)
        XCTAssertEqual(sent.text.reveal(), "proceed with the migration")
        let after = state.actionFinished(seq: seq, .success(ASLOperation(operationID: sent.operationID, status: "accepted")))
        XCTAssertEqual(after, .close(notice: nil), "a resume closes the form")
        XCTAssertFalse(state.active)
        XCTAssertTrue(state.drafts.isEmpty)
    }

    func testNavigatesQuestionsAndFetchesEachBodySeparately() {
        var state = opened()
        state.setDraft("draft for one")
        let effect = state.move(by: 1)
        XCTAssertEqual(effect, .fetch(seq: 2, planDir: "/tmp/plans/api", questionID: "q-api-2"))
        XCTAssertEqual(state.target.questionID, "q-api-2")
        XCTAssertEqual(state.body, SecretText(), "the first body is not reused")
        XCTAssertEqual(state.target.actionToken, "token-reply", "navigation keeps the captured token")
        XCTAssertEqual(state.drafts["q-api-1"]?.reveal(), "draft for one")
        XCTAssertEqual(state.draft, SecretText())
        XCTAssertNil(state.move(by: 1), "the last question has no next")
        XCTAssertEqual(state.move(by: -1), .fetch(seq: 3, planDir: "/tmp/plans/api", questionID: "q-api-1"))
        XCTAssertEqual(state.draft.reveal(), "draft for one")
    }

    func testMovingDropsThePendingOperation() {
        var state = opened()
        state.setDraft("x")
        guard case let .act(seq, _) = state.submit(state.buttons[0], mint: mint) else { return XCTFail("no submission") }
        XCTAssertNil(state.actionFinished(seq: seq, .failure(.transport("timed out"))))
        XCTAssertNil(state.move(by: 0))
        _ = state.move(by: 1)
        XCTAssertEqual(state.pending, ASLPendingSubmission())
    }

    func testSubmitsTwoQuestionsEndToEnd() {
        let row = ASLFixtures.replyRow()
        var state = opened(row)
        state.setDraft("answer for one")
        guard case let .act(seq1, first) = state.submit(state.buttons[0], mint: mint) else { return XCTFail("no first submission") }

        let advance = state.actionFinished(seq: seq1, .success(ASLOperation(operationID: first.operationID, status: "accepted")))
        guard case let .fetch(fetchSeq, _, questionID) = advance else { return XCTFail("did not advance: \(String(describing: advance))") }
        XCTAssertEqual(questionID, "q-api-2")
        XCTAssertTrue(state.active, "a captured question is still open")
        XCTAssertNil(state.drafts["q-api-1"], "the answered draft is dropped")
        XCTAssertEqual(state.questionIDs, ["q-api-2"])
        XCTAssertTrue(state.reauthorize)

        state.setDraft("answer for two")
        XCTAssertNil(state.submit(state.buttons[0], mint: mint), "nothing is sent before the token is re-minted")
        XCTAssertEqual(state.notice, "the previous answer was accepted: this question is still being re-authorized")
        XCTAssertEqual(state.noticeKind, .info)

        XCTAssertNil(state.questionLoaded(seq: fetchSeq, .success(ASLFixtures.body(row, "q-api-2", "Second?", token: "token-reply-2"))))
        XCTAssertFalse(state.reauthorize)
        XCTAssertEqual(state.target.actionToken, "token-reply-2")

        guard case let .act(seq2, second) = state.submit(state.buttons[0], mint: mint) else { return XCTFail("no second submission") }
        XCTAssertEqual(second.questionID, "q-api-2")
        XCTAssertEqual(second.actionToken, "token-reply-2")
        XCTAssertEqual(second.text.reveal(), "answer for two")
        XCTAssertNotEqual(first.operationID, second.operationID)

        XCTAssertEqual(state.actionFinished(seq: seq2, .success(ASLOperation(status: "accepted"))), .close(notice: nil))
        XCTAssertEqual(state, ASLResponseState(), "the form is cleared after its last question")
    }

    func testReauthorizingFetchWithoutATokenClosesTheForm() {
        let row = ASLFixtures.replyRow()
        var state = opened(row)
        state.setDraft("one")
        guard case let .act(seq, _) = state.submit(state.buttons[0], mint: mint),
              case let .fetch(fetchSeq, _, _) = state.actionFinished(seq: seq, .success(ASLOperation(status: "accepted"))) else {
            return XCTFail("did not advance")
        }
        XCTAssertEqual(state.questionLoaded(seq: fetchSeq, .success(ASLFixtures.body(row, "q-api-2", "Second?"))),
                       .close(notice: ASLRefusal.targetChanged))
        XCTAssertFalse(state.active)
    }

    func testDoubleSubmitIsRefusedWhileInFlight() {
        var state = opened()
        state.setDraft("an answer")
        XCTAssertNotNil(state.submit(state.buttons[0], mint: mint))
        let seq = state.actionSeq
        XCTAssertNil(state.submit(state.buttons[0], mint: mint))
        XCTAssertEqual(state.actionSeq, seq, "the refused submission did not advance the sequence")
        XCTAssertFalse(state.canSubmit(state.buttons[0]))
    }

    func testRetryReusesTheOperationIDForTheSameCall() {
        var state = opened()
        state.setDraft("an answer")
        guard case let .act(seq, first) = state.submit(state.buttons[0], mint: mint) else { return XCTFail("no submission") }

        // A timeout leaves the outcome unknown: the form stays, says so, and
        // does not retry on its own.
        XCTAssertNil(state.actionFinished(seq: seq, .failure(.transport("read timed out"))))
        XCTAssertTrue(state.active)
        XCTAssertEqual(state.status, "failed")
        XCTAssertEqual(state.notice, "the action did not complete; its outcome is unknown and it was not retried")
        XCTAssertEqual(state.noticeKind, .error)
        XCTAssertEqual(state.draft.reveal(), "an answer", "the draft survives an ambiguous outcome")

        let retry = params(state.submit(state.buttons[0], mint: mint))
        XCTAssertEqual(retry?.operationID, first.operationID, "a manual retry of the same call reuses the ID")

        state.inFlight = false
        let other = params(state.submit(state.buttons[1], mint: mint))
        XCTAssertNotEqual(other?.operationID, first.operationID, "another closure is another call")
        XCTAssertEqual(state.pending.method, ASLMethod.questionResolve)
        XCTAssertEqual(state.pending.resolution, "continue")

        state.inFlight = false
        let pause = params(state.submit(state.buttons[2], mint: mint))
        XCTAssertNotEqual(pause?.operationID, other?.operationID, "another resolution is another call")
    }

    func testStaleRefusalsCloseTheForm() {
        var state = opened()
        state.setDraft("x")
        guard case let .act(seq, _) = state.submit(state.buttons[0], mint: mint) else { return XCTFail("no submission") }
        XCTAssertEqual(state.actionFinished(seq: seq, .failure(.refused("stale-action-token: token expired"))),
                       .close(notice: ASLRefusal.targetChanged))
        XCTAssertEqual(state, ASLResponseState())

        var fetching = opened()
        let effect = fetching.move(by: 1)
        guard case let .fetch(fetchSeq, _, _) = effect else { return XCTFail("no fetch") }
        XCTAssertEqual(fetching.questionLoaded(seq: fetchSeq, .failure(.refused("no-open-question: gone"))),
                       .close(notice: ASLRefusal.targetChanged))
    }

    func testFaultRefusalKeepsTheFormWithItsSentence() {
        var state = opened()
        state.setDraft("x")
        guard case let .act(seq, _) = state.submit(state.buttons[0], mint: mint) else { return XCTFail("no submission") }
        XCTAssertNil(state.actionFinished(seq: seq, .failure(.refused("mutation-failed: disk full while writing \"x\""))))
        XCTAssertTrue(state.active)
        XCTAssertEqual(state.notice, "agent-session-loop could not record the action; nothing was applied")
    }

    func testABodyForAnotherQuestionClosesTheForm() {
        let row = ASLFixtures.replyRow()
        guard case let .opened(start, .fetch(seq, _, _)) = ASLResponseState.open(row: row, view: .live, status: ASLFixtures.liveStatus) else {
            return XCTFail("the form did not open")
        }
        var state = start
        XCTAssertEqual(state.questionLoaded(seq: seq, .success(ASLFixtures.body(row, "q-other", "?"))),
                       .close(notice: ASLRefusal.targetChanged))

        var otherPlan = ASLFixtures.body(row, "q-api-1", "?")
        otherPlan.planDir = "/tmp/plans/elsewhere"
        XCTAssertFalse(ASLResponse.bodyMatches(otherPlan, target: start.target))
        XCTAssertTrue(ASLResponse.bodyMatches(ASLQuestionBody(), target: start.target), "an empty side is not a mismatch")
    }

    func testLateRepliesAreIgnored() {
        let row = ASLFixtures.replyRow()
        guard case let .opened(start, .fetch(seq, _, _)) = ASLResponseState.open(row: row, view: .live, status: ASLFixtures.liveStatus) else {
            return XCTFail("the form did not open")
        }
        var state = start
        _ = state.move(by: 1)
        XCTAssertNil(state.questionLoaded(seq: seq, .success(ASLFixtures.body(row, "q-api-1", "First?"))))
        XCTAssertTrue(state.fetching, "the earlier fetch's reply did not land")
        XCTAssertEqual(state.body, SecretText())

        var closed = ASLResponseState()
        XCTAssertNil(closed.actionFinished(seq: 0, .success(ASLOperation())))
        XCTAssertNil(closed.questionLoaded(seq: 0, .success(ASLQuestionBody())))
    }

    // MARK: Privacy

    func testRedactsBodiesAndDraftsInEveryFormatting() {
        var state = opened(body: "Should the migration run first?")
        state.setDraft("confidential-answer")
        var dumped = ""
        dump(state, to: &dumped)
        var dumpedDrafts = ""
        dump(state.drafts, to: &dumpedDrafts)
        let renderings = [
            String(describing: state), String(reflecting: state), "\(state)", dumped, dumpedDrafts,
            String(describing: state.drafts), String(describing: state.body), "\(state.draft)",
            String(reflecting: state.body), String(describing: ASLFixtures.body(ASLFixtures.replyRow(), "q", "Should the migration run first?")),
        ]
        for rendering in renderings {
            XCTAssertFalse(rendering.contains("confidential-answer"), rendering)
            XCTAssertFalse(rendering.contains("Should the migration"), rendering)
        }
        XCTAssertTrue(String(describing: state).contains("drafts:1"), "the body-free draft count survives")
        XCTAssertEqual(String(describing: state.body), "[redacted]")

        guard let sent = params(state.submit(state.buttons[0], mint: mint)) else { return XCTFail("no submission") }
        XCTAssertFalse(String(describing: sent).contains("confidential-answer"))
        XCTAssertFalse(String(describing: sent).contains("token-reply"), "the token is not described either")
        XCTAssertFalse(String(describing: ASLRequestError.refused("mutation-failed: confidential-answer")).contains("confidential-answer"))
    }

    func testClosingClearsTheBodyAndEveryDraft() {
        var state = opened()
        state.setDraft("secret answer text")
        _ = state.move(by: 1)
        state.setDraft("another")
        state = ASLResponseState()
        XCTAssertFalse(state.active)
        XCTAssertTrue(state.drafts.isEmpty)
        XCTAssertEqual(state.body, SecretText())
    }

    // MARK: Refusals (ported from tui/asl/refusal_test.go)

    /// `aslbridge.ErrorCodes`, in its declared order.
    private let declaredCodes = [
        "malformed-json", "invalid-request", "unsupported-protocol", "unknown-method", "frame-too-large",
        "internal-error", "mutation-failed", "workbench-unavailable", "invalid-plan", "no-active-run",
        "run-mismatch", "not-paused", "no-open-question", "question-mismatch", "stale-action-token",
        "action-not-permitted", "invalid-action", "empty-text", "question-body-too-large",
        "missing-question-body", "contract-set-mismatch", "stale-snapshot",
    ]

    func testRefusalTextCoversEveryDeclaredErrorCode() {
        let generic = ASLRefusal.text(.refused("some-code-that-does-not-exist: detail"))
        XCTAssertEqual(generic, ASLRefusal.text(.transport("timed out")))
        for code in declaredCodes {
            let stale = ASLRefusal.staleCodes.contains(code)
            XCTAssertNotEqual(stale, ASLRefusal.errorCodes.contains(code), "\(code): exactly one of stale or error")
            let error = ASLRequestError.refused(code + ": drop the v1 column before the backfill lands")
            XCTAssertEqual(ASLRefusal.code(error), code)
            let text = ASLRefusal.text(error)
            XCTAssertFalse(text.contains("backfill"), "\(code) repeats the bridge message: \(text)")
            if stale {
                XCTAssertTrue(ASLRefusal.isStale(error), code)
            } else {
                XCTAssertNotEqual(text, generic, "\(code) falls through to the generic sentence")
            }
        }
        XCTAssertEqual(ASLRefusal.staleCodes.union(ASLRefusal.errorCodes), Set(declaredCodes),
                       "every mapped code is declared")
        XCTAssertEqual(ASLRefusal.code(.refused("stale-snapshot")), "stale-snapshot", "a bare code is recovered")
        XCTAssertEqual(ASLRefusal.code(.transport("stale-snapshot: x")), "", "only a refusal carries a code")
    }

    func testEveryRowActionHasAnOperatorLabel() {
        let methods = [ASLMethod.questionReply, ASLMethod.questionResolve, ASLMethod.runResume,
                       ASLMethod.runStopAfterCurrent, ASLMethod.runAbort, ASLMethod.workbenchEnsure, ASLMethod.runRedirect]
        for method in methods {
            XCTAssertNotEqual(ASLRefusal.methodLabel(method), method)
        }
    }

    func testOperationStates() {
        XCTAssertEqual(ASLResponse.operationState("accepted"), "accepted")
        XCTAssertEqual(ASLResponse.operationState("consumed"), "consumed")
        XCTAssertEqual(ASLResponse.operationState("expired"), "expired")
        XCTAssertEqual(ASLResponse.operationState("rejected"), "failed")
        XCTAssertEqual(ASLResponse.operationState("superseded"), "failed")
        XCTAssertEqual(ASLResponse.operationState(""), "submitting")
    }

    func testOperationIDsAreUnique() {
        let a = ASLResponse.newOperationID()
        let b = ASLResponse.newOperationID()
        XCTAssertTrue(a.hasPrefix("amx-"))
        XCTAssertEqual(a.count, 4 + 32)
        XCTAssertNotEqual(a, b)
    }

    func testAnswerTargetFindsItsRow() {
        let rows = [ASLFixtures.replyRow(), ASLFixtures.pausedRow()]
        XCTAssertEqual(ASLAnswerTarget(runID: "run-paused").row(in: rows)?.rowID, "row-paused")
        XCTAssertNil(ASLAnswerTarget(runID: "run-gone", planDir: "/tmp/plans/api").row(in: rows), "the run ID decides")
        XCTAssertEqual(ASLAnswerTarget(planDir: "/tmp/plans/../plans/api").row(in: rows)?.rowID, "row-reply")
        XCTAssertNil(ASLAnswerTarget().row(in: rows))
    }

    // MARK: Body loads and disabled buttons

    func testAFailedBodyLoadSaysSoBlocksSubmitAndLoadsAgain() {
        let row = ASLFixtures.replyRow()
        guard case let .opened(opened, .fetch(seq, _, _)) = ASLResponseState.open(row: row, view: .live, status: ASLFixtures.liveStatus) else {
            return XCTFail("the form did not open")
        }
        var state = opened
        XCTAssertNil(state.questionLoaded(seq: seq, .failure(.transport("read timed out"))))
        XCTAssertTrue(state.loadFailed)
        XCTAssertEqual(state.notice, "could not load the question: agent-session-loop did not answer")
        XCTAssertEqual(state.noticeKind, .error)
        state.setDraft("an answer")
        XCTAssertFalse(state.canSubmit(state.buttons[0]))
        XCTAssertFalse(state.canSubmit(state.buttons[1]), "nothing is closed unseen")
        XCTAssertEqual(state.disabledReason(state.buttons[1]), "Load the question again first")
        XCTAssertNil(state.submit(state.buttons[1], mint: mint))
        XCTAssertEqual(state.notice, "the question could not be loaded: load it again before answering")
        XCTAssertEqual(state.noticeKind, .error)
        XCTAssertFalse(state.inFlight)

        XCTAssertEqual(state.reload(), .fetch(seq: seq + 1, planDir: row.planDir, questionID: "q-api-1"))
        XCTAssertFalse(state.loadFailed)
        XCTAssertEqual(state.notice, "")
        XCTAssertNil(state.reload(), "one fetch at a time")
        XCTAssertNil(state.questionLoaded(seq: seq + 1, .success(ASLFixtures.body(row, "q-api-1", "First?"))))
        XCTAssertEqual(state.body.reveal(), "First?")
        XCTAssertTrue(state.canSubmit(state.buttons[0]))
    }

    func testAFailedReauthorizingLoadCanBeLoadedAgain() {
        let row = ASLFixtures.replyRow()
        var state = opened(row)
        state.setDraft("one")
        guard case let .act(seq, _) = state.submit(state.buttons[0], mint: mint),
              case let .fetch(fetchSeq, _, _) = state.actionFinished(seq: seq, .success(ASLOperation(status: "accepted"))) else {
            return XCTFail("did not advance")
        }
        XCTAssertNil(state.questionLoaded(seq: fetchSeq, .failure(.notRunning)))
        XCTAssertEqual(state.notice, "could not load the question: the daemon is not running")
        XCTAssertTrue(state.reauthorize)
        guard case let .fetch(again, _, _) = state.reload() else { return XCTFail("Load Again did not fetch") }
        XCTAssertEqual(state.disabledReason(state.buttons[1]), "Re-authorizing…")
        XCTAssertNil(state.questionLoaded(seq: again, .success(ASLFixtures.body(row, "q-api-2", "Second?", token: "token-reply-2"))))
        XCTAssertFalse(state.reauthorize)
        XCTAssertEqual(state.target.actionToken, "token-reply-2")
        XCTAssertTrue(state.canSubmit(state.buttons[1]), "the buttons come back once the token is re-minted")
    }

    func testReadTextNeverUsesActionWording() {
        XCTAssertEqual(ASLRefusal.readText(.refused("internal-error: x")),
                       "could not load the question: agent-session-loop reported an internal error")
        XCTAssertEqual(ASLRefusal.readText(.refused("something-new: \"secret\"")), "could not load the question")
        XCTAssertEqual(ASLRefusal.readText(.refused("question-body-too-large: x")),
                       ASLRefusal.text(.refused("question-body-too-large: x")))
        XCTAssertEqual(ASLRefusal.readText(.unsupported), ASLRefusal.text(.unsupported))
        for error: ASLRequestError in [.notRunning, .transport("t"), .refused("mutation-failed: x"), .refused("x")] {
            XCTAssertFalse(ASLRefusal.readText(error).contains("outcome"), "\(error)")
            XCTAssertFalse(ASLRefusal.readText(error).contains("nothing was"), "\(error)")
        }
    }

    func testDisabledButtonsSayWhy() {
        XCTAssertEqual(ASLResponseState().disabledReason(ASLResponse.buttons(.live)[0]), "Loading…")
        guard case .opened(var state, _) = ASLResponseState.open(row: ASLFixtures.replyRow(), view: .live, status: ASLFixtures.liveStatus) else {
            return XCTFail("the form did not open")
        }
        XCTAssertEqual(state.disabledReason(state.buttons[0]), "Loading…")
        state = opened()
        XCTAssertEqual(state.disabledReason(state.buttons[0]), "Type a reply")
        XCTAssertNil(state.disabledReason(state.buttons[1]))
        state.setDraft("an answer")
        XCTAssertNil(state.disabledReason(state.buttons[0]))
        _ = state.submit(state.buttons[0], mint: mint)
        XCTAssertEqual(state.disabledReason(state.buttons[0]), "Submitting…")
    }

    func testKeptDraftsAreRestoredOnlyForQuestionsStillOpen() {
        var state = opened()
        state.setDraft("kept")
        _ = state.move(by: 1)
        state.setDraft("   ")
        XCTAssertEqual(state.keptDrafts.mapValues { $0.reveal() }, ["q-api-1": "kept"], "a blank draft is not kept")

        var reopened = opened()
        reopened.restore(drafts: ["q-api-1": SecretText("kept"), "q-gone": SecretText("answered elsewhere")])
        XCTAssertEqual(reopened.draft.reveal(), "kept")
        XCTAssertNil(reopened.drafts["q-gone"])
    }
}
