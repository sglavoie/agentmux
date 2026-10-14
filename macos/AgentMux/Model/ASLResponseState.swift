import Foundation

// The answer panel's data and rules, ported from `tui/asl/response_state.go`,
// `refusal.go`, and the response flow in `tui/model_asl.go`: the identity a
// form is bound to, the buttons it offers, the drafts it holds, how a
// submission's operation ID is minted and reused, and how an accepted closure
// advances through the questions still open. Nothing here talks to the
// daemon: each step returns the request to make, and `ASLQuestionFlow` makes
// it.

/// Which run the operator asked to answer, from a menu row or a banner. It
/// names a run, never a question body or a token: the panel reads both from
/// a fresh `asl_snapshot`.
struct ASLAnswerTarget: Equatable {
    var runID = ""
    var planDir = ""
    var windowID = 0

    /// The row in `rows` this target names: the run ID decides, and the plan
    /// directory is used only when no run ID was given.
    func row(in rows: [ASLRow]) -> ASLRow? {
        if !runID.isEmpty {
            return rows.first { $0.runID == runID }
        }
        guard !planDir.isEmpty else { return nil }
        let wanted = ASLResponse.canonicalDir(planDir)
        let matches = rows.filter { ASLResponse.canonicalDir($0.planDir) == wanted }
        return matches.count == 1 ? matches[0] : nil
    }
}

/// `asl.ActionTarget`: what a form is bound to. Copied out of the row when the
/// form opens and never rewritten while it is open, except for the token a
/// re-authorizing fetch re-mints.
struct ASLActionTarget: Equatable {
    var rowID = ""
    var planDir = ""
    var runID = ""
    var questionID = ""
    var actionToken = ""
    var label = ""
}

/// `asl.ResponseKind`: a live wait can be answered or closed answerlessly; a
/// paused run can only be answered, and answering it resumes the run.
enum ASLResponseKind: Equatable {
    case live
    case paused
}

/// `asl.ResponseButton`: one submission the form offers.
struct ASLResponseButton: Equatable {
    let label: String
    let method: String
    var resolution = ""
    /// Carries the operator's answer, so a blank editor is refused.
    var needsText = false
}

/// `asl.PendingSubmission`: the operation ID minted for one submission and the
/// exact call it was minted for. A manual retry after an ambiguous outcome
/// reuses it so the bridge replays its own receipt instead of applying the
/// mutation twice; another button mints a new one.
struct ASLPendingSubmission: Equatable {
    var operationID = ""
    var method = ""
    var resolution = ""
}

/// What the flow must do next.
enum ASLPanelEffect: Equatable {
    /// Fetch one question body; the reply is matched to `seq`.
    case fetch(seq: Int, planDir: String, questionID: String)
    /// Submit one action; the reply is matched to `seq`.
    case act(seq: Int, ASLRequestParams)
    /// Close the panel and refresh the menu, with a notice when the run moved.
    case close(notice: String?)
}

/// How trustworthy the projection a form would open against is
/// (`asl.ViewState`, without loading).
enum ASLView: Equatable {
    case live
    case stale
    case unavailable

    /// `RunsInput.View` for a snapshot that was just fetched.
    static func of(status: ASLStatus, meta: ASLSnapshotMeta?, hasRows: Bool) -> ASLView {
        let stale = meta?.stale ?? false
        if status.available, !stale { return .live }
        if hasRows, (meta?.ageSeconds ?? 0) <= ASLResponse.staleWindowSeconds { return .stale }
        return .unavailable
    }
}

enum ASLResponse {
    /// `aslbridge.StaleWindow`.
    static let staleWindowSeconds: Int64 = 10

    /// `asl.ResponseButtons`.
    static func buttons(_ kind: ASLResponseKind) -> [ASLResponseButton] {
        switch kind {
        case .paused:
            return [ASLResponseButton(label: "Reply and resume", method: ASLMethod.runResume, needsText: true)]
        case .live:
            return [
                ASLResponseButton(label: "Reply", method: ASLMethod.questionReply, needsText: true),
                ASLResponseButton(label: "Continue", method: ASLMethod.questionResolve, resolution: ASLResolution.continue),
                ASLResponseButton(label: "Pause", method: ASLMethod.questionResolve, resolution: ASLResolution.pause),
            ]
        }
    }

    /// `asl.ResponseKindFor`: permitted actions decide; the context kind only
    /// breaks the tie for a row that permits neither, so the refusal names the
    /// action the operator expected.
    static func kind(for row: ASLRow) -> ASLResponseKind {
        if row.actions.contains(ASLMethod.questionReply) { return .live }
        if row.actions.contains(ASLMethod.runResume) { return .paused }
        if row.question?.contextKind == "pause" { return .paused }
        return .live
    }

    /// `asl.ResponseGateMethod`: what a form of this kind must be permitted
    /// to invoke before it may open at all.
    static func gateMethod(_ kind: ASLResponseKind) -> String {
        kind == .paused ? ASLMethod.runResume : ASLMethod.questionReply
    }

    /// `asl.QuestionIDs`: the open question IDs a row projects, which the form
    /// navigates and which does not grow while it is open.
    static func questionIDs(_ row: ASLRow) -> [String] {
        guard let question = row.question else { return [] }
        if !question.questionIDs.isEmpty { return question.questionIDs }
        if !question.questionID.isEmpty { return [question.questionID] }
        return []
    }

    /// `asl.BodyMatchesTarget`: a body for another plan or question means the
    /// run moved under the form.
    static func bodyMatches(_ body: ASLQuestionBody, target: ASLActionTarget) -> Bool {
        if !body.planDir.isEmpty, !target.planDir.isEmpty,
           canonicalDir(body.planDir) != canonicalDir(target.planDir) {
            return false
        }
        if !target.questionID.isEmpty, !body.questionID.isEmpty, body.questionID != target.questionID {
            return false
        }
        return true
    }

    /// Absolute, standardized, symlinks resolved.
    static func canonicalDir(_ path: String) -> String {
        let trimmed = path.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else { return "" }
        return URL(fileURLWithPath: (trimmed as NSString).expandingTildeInPath)
            .standardizedFileURL.resolvingSymlinksInPath().path
    }

    /// `RunsInput.ActionAllowed`: nil when `method` may run against `row`
    /// now, else the fixed sentence saying why not. Availability comes only
    /// from the capability block and ASL's own projection of the row.
    static func refusal(row: ASLRow, method: String, view: ASLView, status: ASLStatus) -> String? {
        switch view {
        case .live: break
        case .stale: return "actions are unavailable: this snapshot is read-only"
        case .unavailable: return "actions are unavailable: agent-session-loop is not reachable"
        }
        if !status.mutationsEnabled {
            return "actions are unavailable: the daemon reports agent-session-loop mutations as disabled"
        }
        if row.actionToken.trimmingCharacters(in: .whitespaces).isEmpty {
            return "actions are unavailable: this run carries no action token"
        }
        if row.actions.contains(method) { return nil }
        let label = ASLRefusal.methodLabel(method)
        let reason = row.disabledReasons[method]?.trimmingCharacters(in: .whitespaces) ?? ""
        if !reason.isEmpty { return "\(label) is unavailable: \(reason)" }
        return "\(label) is unavailable for this run"
    }

    /// `asl.OperationState`: a receipt status in the body-free vocabulary.
    static func operationState(_ status: String) -> String {
        switch status {
        case ASLOperationStatus.accepted: return "accepted"
        case ASLOperationStatus.consumed: return "consumed"
        case ASLOperationStatus.expired: return "expired"
        case ASLOperationStatus.rejected, ASLOperationStatus.superseded: return "failed"
        default: return "submitting"
        }
    }

    /// `asl.NewOperationID`: opaque and unique per submission.
    static func newOperationID() -> String {
        // SystemRandomNumberGenerator is the system CSPRNG.
        var generator = SystemRandomNumberGenerator()
        return "amx-" + (0..<16).map { _ in String(format: "%02x", UInt8.random(in: .min ... .max, using: &generator)) }.joined()
    }
}

/// `tui/asl/refusal.go`: every refusal maps from a stable code to a fixed
/// sentence, so no bridge-authored text — which may quote a question or an
/// answer — can reach the panel, the menu, or a log.
enum ASLRefusal {
    /// `asl.TargetChangedText`, worded for the panel.
    static let targetChanged = "this run changed since it was selected: the panel was closed and the menu refreshed"

    /// The refusals that mean the run moved: nothing was applied.
    static let staleCodes: Set<String> = [
        "stale-action-token", "action-not-permitted", "no-active-run", "no-open-question",
        "question-mismatch", "run-mismatch", "not-paused", "stale-snapshot",
    ]

    /// The refusals that report a fault rather than a moved target.
    static let errorCodes: Set<String> = [
        "invalid-plan", "invalid-action", "empty-text", "question-body-too-large", "missing-question-body",
        "malformed-json", "invalid-request", "unsupported-protocol", "unknown-method", "frame-too-large",
        "internal-error", "mutation-failed", "workbench-unavailable", "contract-set-mismatch",
    ]

    /// The bridge code in a daemon refusal (`<code>: <message>`), or "" for
    /// anything that is not a known code.
    static func code(_ error: ASLRequestError) -> String {
        guard case let .refused(message) = error else { return "" }
        let code = message.range(of: ": ").map { String(message[..<$0.lowerBound]) } ?? message
        return staleCodes.contains(code) || errorCodes.contains(code) ? code : ""
    }

    /// `asl.StaleTarget`.
    static func isStale(_ error: ASLRequestError) -> Bool {
        staleCodes.contains(code(error))
    }

    /// `asl.RefusalText`. The bridge's own message is dropped.
    static func text(_ error: ASLRequestError) -> String {
        switch error {
        case .notRunning:
            return "the daemon is not running: nothing was sent"
        case .unsupported:
            return "Daemon too old to answer questions — run amx daemon restart"
        case .refused, .transport:
            break
        }
        switch code(error) {
        case "empty-text":
            return "the answer is empty: type a reply before submitting"
        case "question-body-too-large":
            return "this question is too large to show here: open it in the agent-session-loop Workbench"
        case "missing-question-body":
            return "this question carries no readable text: open it in the agent-session-loop Workbench"
        case "invalid-plan":
            return "agent-session-loop no longer recognizes this plan directory"
        case "mutation-failed":
            return "agent-session-loop could not record the action; nothing was applied"
        case "workbench-unavailable":
            return "the Workbench is unavailable in this bridge session"
        case "invalid-request", "invalid-action":
            return "the daemon refused the request as malformed"
        case "unknown-method", "unsupported-protocol":
            return "this agent-session-loop build does not support this action"
        case "contract-set-mismatch":
            return "agent mux and agent-session-loop were built from different contract sets: update the older one"
        case "malformed-json", "frame-too-large", "internal-error":
            return "agent-session-loop reported an internal error"
        default:
            return "the action did not complete; its outcome is unknown and it was not retried"
        }
    }

    /// Why a question body could not be read. A read applies nothing, so it
    /// never borrows an action's "outcome is unknown" wording.
    static func readText(_ error: ASLRequestError) -> String {
        let prefix = "could not load the question"
        switch error {
        case .notRunning: return "\(prefix): the daemon is not running"
        case .unsupported: return text(error)
        case .transport: return "\(prefix): agent-session-loop did not answer"
        case .refused: break
        }
        switch code(error) {
        case "question-body-too-large", "missing-question-body", "invalid-plan", "contract-set-mismatch",
             "unknown-method", "unsupported-protocol":
            return text(error)
        case "malformed-json", "frame-too-large", "internal-error":
            return "\(prefix): agent-session-loop reported an internal error"
        default:
            return prefix
        }
    }

    /// `asl.MethodLabel`.
    static func methodLabel(_ method: String) -> String {
        switch method {
        case ASLMethod.questionReply: return "reply"
        case ASLMethod.questionResolve: return "closing the question"
        case ASLMethod.runResume: return "resume"
        case ASLMethod.runStopAfterCurrent: return "stop after current"
        case ASLMethod.runAbort: return "abort"
        case ASLMethod.workbenchEnsure: return "workbench"
        case ASLMethod.runRedirect: return "redirect"
        default: return method
        }
    }
}

extension ASLRequestError: CustomStringConvertible {
    /// The code alone, so logging an error cannot log the bridge's message.
    var description: String {
        switch self {
        case .notRunning: return "daemon not running"
        case .unsupported: return "unsupported"
        case .refused:
            let code = ASLRefusal.code(self)
            return "refused(\(code.isEmpty ? "unrecognized" : code))"
        case .transport: return "transport"
        }
    }
}

/// `asl.ResponseState`: everything the panel's form owns. It is replaced
/// wholesale on close, on the last closure, and on a moved target, so the
/// question body and every draft go in one assignment rather than a
/// checklist a later field can fall off. Its descriptions and mirror are
/// body-free.
struct ASLResponseState: Equatable, CustomStringConvertible, CustomDebugStringConvertible, CustomReflectable {
    var active = false
    var kind = ASLResponseKind.live
    var target = ASLActionTarget()
    var contextKind = ""
    var askedAt = ""
    var questionIDs: [String] = []
    var questionIndex = 0
    var body = SecretText()
    var bodyBytes = 0
    var drafts: [String: SecretText] = [:]
    var fetching = false
    var fetchSeq = 0
    /// The last fetch failed, so there is no body to answer; Load Again
    /// fetches it again.
    var loadFailed = false
    var buttons: [ASLResponseButton] = []
    var pending = ASLPendingSubmission()
    /// The previous closure spent the captured token; the next fetch re-mints
    /// it, and nothing may be submitted until it has.
    var reauthorize = false
    var inFlight = false
    var actionSeq = 0
    var status = ""
    var notice = ""
    /// How the notice is shown; it means nothing while there is none.
    var noticeKind = PanelNoticeKind.info

    init() {}

    var description: String {
        "ASLResponseState{active:\(active) kind:\(kind) row:\(target.rowID) question:\(target.questionID) bodyBytes:\(bodyBytes) drafts:\(drafts.count) status:\(status)}"
    }

    var debugDescription: String { description }

    var customMirror: Mirror {
        Mirror(self, children: [
            "active": active, "kind": kind, "row": target.rowID, "question": target.questionID,
            "bodyBytes": bodyBytes, "drafts": drafts.count, "status": status,
        ], displayStyle: .struct)
    }

    private mutating func note(_ text: String, _ kind: PanelNoticeKind) {
        notice = text
        noticeKind = kind
    }

    // MARK: Opening

    enum Opening: Equatable {
        case opened(ASLResponseState, ASLPanelEffect)
        /// The fixed sentence saying why the form may not open.
        case refused(String)
    }

    /// `openASLResponse`: binds a form to `row` and asks for the selected
    /// question's body. Only that one body is fetched; the other IDs travel
    /// as metadata and are fetched as the operator moves to them.
    static func open(row: ASLRow, view: ASLView, status: ASLStatus) -> Opening {
        let kind = ASLResponse.kind(for: row)
        if let refusal = ASLResponse.refusal(row: row, method: ASLResponse.gateMethod(kind), view: view, status: status) {
            return .refused(refusal)
        }
        var state = ASLResponseState()
        state.active = true
        state.kind = kind
        state.target = ASLActionTarget(rowID: row.rowID, planDir: row.planDir, runID: row.runID,
                                       actionToken: row.actionToken, label: row.displayLabel)
        state.questionIDs = ASLResponse.questionIDs(row)
        state.buttons = ASLResponse.buttons(kind)
        if let question = row.question {
            state.contextKind = question.contextKind
            state.askedAt = question.askedAt
            if let index = state.questionIDs.firstIndex(of: question.questionID) {
                state.questionIndex = index
            }
        }
        if !state.questionIDs.isEmpty {
            state.target.questionID = state.questionIDs[state.questionIndex]
        }
        let effect = state.startFetch()
        return .opened(state, effect)
    }

    // MARK: Drafts

    /// The answer typed for the question the form is bound to now.
    var draft: SecretText { drafts[target.questionID] ?? SecretText() }

    /// Stores the answer under the current question's own ID, so moving away
    /// never discards a half-written answer.
    mutating func setDraft(_ text: SecretText) {
        drafts[target.questionID] = text
    }

    /// The drafts worth keeping when the panel closes before they are sent.
    var keptDrafts: [String: SecretText] { drafts.filter { !$0.value.isBlank } }

    /// Puts back drafts kept from an earlier panel on this run, for the
    /// questions it still has open; the others were answered or moved.
    mutating func restore(drafts kept: [String: SecretText]) {
        for (questionID, text) in kept where questionIDs.contains(questionID) && drafts[questionID] == nil {
            drafts[questionID] = text
        }
    }

    // MARK: Fetching

    mutating func startFetch() -> ASLPanelEffect {
        fetchSeq += 1
        fetching = true
        loadFailed = false
        body = SecretText()
        bodyBytes = 0
        notice = ""
        return .fetch(seq: fetchSeq, planDir: target.planDir, questionID: target.questionID)
    }

    /// `handleASLQuestionLoaded`. A reply for an earlier fetch is ignored.
    mutating func questionLoaded(seq: Int, _ result: Result<ASLQuestionBody, ASLRequestError>) -> ASLPanelEffect? {
        guard active, seq == fetchSeq else { return nil }
        fetching = false
        let loaded: ASLQuestionBody
        switch result {
        case let .failure(error):
            if ASLRefusal.isStale(error) { return invalidate() }
            loadFailed = true
            note(ASLRefusal.readText(error), .error)
            return nil
        case let .success(value):
            loaded = value
        }
        guard ASLResponse.bodyMatches(loaded, target: target) else { return invalidate() }
        if reauthorize {
            // The previous closure changed what the run's token fingerprints;
            // the fetch that proved this question is still open re-mints it.
            guard !loaded.actionToken.isEmpty else { return invalidate() }
            target.actionToken = loaded.actionToken
            reauthorize = false
        }
        body = loaded.body
        bodyBytes = loaded.bodyBytes
        if !loaded.contextKind.isEmpty { contextKind = loaded.contextKind }
        return nil
    }

    /// Load Again: fetches the body whose fetch failed. A re-authorizing
    /// fetch that failed re-mints the token this way too, so its buttons do
    /// not stay disabled.
    mutating func reload() -> ASLPanelEffect? {
        guard active, loadFailed, !fetching, !inFlight else { return nil }
        return startFetch()
    }

    // MARK: Navigating

    /// `moveASLQuestion`: walks the captured question set; each body is
    /// fetched on its own.
    mutating func move(by delta: Int) -> ASLPanelEffect? {
        guard questionIDs.count > 1, !inFlight else { return nil }
        let next = min(max(questionIndex + delta, 0), questionIDs.count - 1)
        guard next != questionIndex else { return nil }
        questionIndex = next
        target.questionID = questionIDs[next]
        pending = ASLPendingSubmission()
        status = ""
        return startFetch()
    }

    // MARK: Submitting

    /// Whether `button` may be pressed now.
    func canSubmit(_ button: ASLResponseButton) -> Bool {
        disabledReason(button) == nil
    }

    /// Why `button` may not be pressed now, in a few words for its tooltip
    /// and the caption under the reply; nil when it may.
    func disabledReason(_ button: ASLResponseButton) -> String? {
        if !active { return "Loading…" }
        if inFlight { return "Submitting…" }
        if fetching { return reauthorize ? "Re-authorizing…" : "Loading…" }
        if loadFailed { return "Load the question again first" }
        if reauthorize { return "Re-authorizing…" }
        if button.needsText, draft.isBlank { return "Type a reply" }
        return nil
    }

    /// `submitASLResponse`: refuses a double submit, a submit while the
    /// token is being re-minted, and a blank answer; otherwise mints or
    /// reuses the operation ID and returns the action to send.
    mutating func submit(_ button: ASLResponseButton, mint: () -> String = ASLResponse.newOperationID) -> ASLPanelEffect? {
        guard active, !inFlight else { return nil }
        if loadFailed {
            note("the question could not be loaded: load it again before answering", .error)
            return nil
        }
        if reauthorize {
            note("the previous answer was accepted: this question is still being re-authorized", .info)
            return nil
        }
        let text = draft
        if button.needsText, text.isBlank {
            // Nothing went wrong: the reply is still to be typed.
            note("the answer is empty: type a reply before submitting", .info)
            return nil
        }
        if pending.operationID.isEmpty || pending.method != button.method || pending.resolution != button.resolution {
            pending = ASLPendingSubmission(operationID: mint(), method: button.method, resolution: button.resolution)
        }
        var params = ASLRequestParams(
            method: button.method,
            planDir: target.planDir,
            rowID: target.rowID,
            runID: target.runID,
            questionID: target.questionID,
            operationID: pending.operationID,
            actionToken: target.actionToken,
            resolution: button.resolution
        )
        if button.needsText { params.text = text }
        inFlight = true
        actionSeq += 1
        status = "submitting"
        notice = ""
        return .act(seq: actionSeq, params)
    }

    /// `handleASLActionDone`. A refusal that means the run moved closes the
    /// form; any other failure keeps it, with its pending operation ID, so a
    /// manual retry of the same call replays rather than repeats.
    mutating func actionFinished(seq: Int, _ result: Result<ASLOperation, ASLRequestError>) -> ASLPanelEffect? {
        guard active, seq == actionSeq else { return nil }
        inFlight = false
        switch result {
        case let .failure(error):
            if ASLRefusal.isStale(error) { return invalidate() }
            status = "failed"
            note(ASLRefusal.text(error), .error)
            return nil
        case let .success(operation):
            status = ASLResponse.operationState(operation.status)
            return advance()
        }
    }

    /// `advanceASLResponse`: drops the answered question's draft and moves to
    /// a question still open in the captured set. A resume closes the form,
    /// because the run it resumed is no longer paused.
    private mutating func advance() -> ASLPanelEffect {
        let answered = target.questionID
        drafts[answered] = nil
        pending = ASLPendingSubmission()
        let remaining = questionIDs.filter { $0 != answered }
        if kind == .paused || remaining.isEmpty {
            self = ASLResponseState()
            return .close(notice: nil)
        }
        questionIDs = remaining
        questionIndex = min(max(questionIndex, 0), remaining.count - 1)
        target.questionID = remaining[questionIndex]
        reauthorize = true
        status = ""
        return startFetch()
    }

    /// `invalidateASLResponse`: the run moved. The submission is not retried
    /// under a fresh token; the operator re-selects deliberately.
    private mutating func invalidate() -> ASLPanelEffect {
        self = ASLResponseState()
        return .close(notice: ASLRefusal.targetChanged)
    }
}
