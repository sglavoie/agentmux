import Foundation
import Combine

struct WorkspaceRunDetail: Decodable {
    struct Session: Decodable, Identifiable { let id: String; let status: String; let path: String }
    struct Verification: Decodable {
        let event: String
        let at: String
        let sessionID: String
        let name: String?
        let status: String?
        let passed: Bool?
        let checkCount: Int?
        let passedCount: Int?
        let skippedCount: Int?
        let returncode: Int?
        let durationSeconds: Double?
        enum CodingKeys: String, CodingKey {
            case event, at, name, status, passed, returncode
            case sessionID = "session_id", checkCount = "check_count", passedCount = "passed_count"
            case skippedCount = "skipped_count", durationSeconds = "duration_seconds"
        }
        var summary: String {
            if event == "verification_check_skipped" { return "\(name ?? "Check"): skipped" }
            if event == "verification_finished" {
                return "Verification \(passed == true ? "passed" : passed == false ? "failed" : "result unavailable") · \(passedCount ?? 0)/\(checkCount ?? 0) passed · \(skippedCount ?? 0) skipped"
            }
            return "\(name ?? "Check"): \(status ?? "result unavailable")" + (returncode.map { " · exit \($0)" } ?? "")
        }
    }
    struct Runner: Decodable {
        struct LiveVerification: Decodable {
            let sessionID: String
            let currentCheckName: String
            let checkCount: Int
            let elapsedSeconds: Double?
            enum CodingKeys: String, CodingKey {
                case sessionID = "session_id", currentCheckName = "current_check_name"
                case checkCount = "check_count", elapsedSeconds = "elapsed_seconds"
            }
        }
        let runID: String
        let paused: Bool
        let reason: String
        let startedAt: String
        let finishedAt: String
        let durationSeconds: Double?
        let activeVerification: LiveVerification?
        enum CodingKeys: String, CodingKey {
            case paused, reason
            case runID = "run_id", startedAt = "run_started_at", finishedAt = "run_finished_at"
            case durationSeconds = "duration_seconds", activeVerification = "active_verification"
        }
    }
    let planDir: String
    let title: String
    let counts: [String: Int]
    let sessions: [Session]
    let runner: Runner
    let verification: [Verification]
    let verificationTruncated: Bool
    let verificationError: String?
    enum CodingKeys: String, CodingKey {
        case counts, sessions, verification
        case planDir = "plan_dir", title = "plan_title", runner = "runner_state"
        case verificationTruncated = "verification_truncated", verificationError = "verification_error"
    }
    var progressSummary: String {
        let total = counts.values.reduce(0) { $0 + max(0, $1) }
        var text = "\(counts["complete", default: 0]) of \(total) sessions complete"
        let unknown = counts["unknown", default: 0]
        if unknown > 0 { text += " · \(unknown) unknown" }
        return text
    }

    /// Scope evidence to the selected session and its latest completed attempt.
    /// A later passing attempt must never surface a historical failure as current.
    func blockingVerification(sessionID: String) -> [Verification] {
        let records = verification.filter { $0.sessionID == sessionID }
        guard runner.activeVerification == nil else { return [] }
        guard let finish = records.lastIndex(where: { $0.event == "verification_finished" }), finish == records.count - 1, records[finish].passed == false else { return [] }
        let previous = records[..<finish].lastIndex { $0.event == "verification_finished" }
        let start = previous.map { $0 + 1 } ?? 0
        let failed = records[start..<finish].filter {
            $0.event == "verification_check_finished" && ($0.status == "failed" || $0.passed == false || ($0.returncode ?? 0) != 0)
        }
        return failed.isEmpty ? [records[finish]] : Array(failed)
    }

}

/// Lists the full ASL projection, including pauses with no question. Run controls
/// reauthorize against a fresh snapshot, and an uncertain call keeps its exact
/// operation ID for an explicit receipt retry. No action is retried by polling.
@MainActor
final class WorkspaceRunsModel: ObservableObject {
    let feedbackHistory: WorkspaceFeedbackHistory
    @Published private(set) var listRefreshedAt: Date?
    @Published private(set) var detailRefreshedAt: Date?
    @Published private(set) var rows: [ASLRow] = []
    @Published private(set) var selection: String?
    @Published private(set) var detail: WorkspaceRunDetail?
    @Published private(set) var loading = false
    @Published private(set) var reading = false
    @Published private(set) var acting = false
    @Published private(set) var stale = true
    @Published private(set) var error: String?
    @Published private(set) var detailError: String?
    @Published private(set) var actionFeedback: WorkspaceFeedback? {
        didSet {
            guard let actionFeedback else { return }
            let operation = pending?.operationID
            let target = actionTarget
            feedbackHistory.record(actionFeedback, available: { [weak self] in
                guard let self, !self.acting else { return false }
                return actionFeedback.recovery != .receipt || (operation != nil && self.pending?.operationID == operation)
            }) { [weak self] in
                guard let self else { return }
                if actionFeedback.recovery == .receipt {
                    if operation != nil && self.pending?.operationID == operation { self.retryReceipt() }
                } else {
                    self.feedbackSelection = target
                    self.refresh()
                }
                self.revealFeedback?()
            }
        }
    }
    var actionNotice: String? { actionFeedback?.message }
    @Published private(set) var pending: ASLRequestParams?
    @Published private(set) var warnings: [String] = []
    @Published private(set) var truncated = false
    var revealFeedback: (() -> Void)?
    var onAnswer: ((ASLAnswerTarget) -> Void)?
    var onFocus: ((ASLRow) -> Void)?
    private let service: ASLService
    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner
    private var status = ASLStatus()
    private var generation = 0
    private var detailGeneration = 0
    private var timer: Timer?
    private var started = false
    private var automaticRefreshEnabled = true
    private let now: () -> Date
    private var detailRow: ASLRow?
    // Summary projections need not change when plan files or verification logs do.
    // Bound that blind spot while avoiding a CLI process on every unchanged tick.
    static let detailRefreshInterval: TimeInterval = 30
    private struct FeedbackTarget { let rowID: String; let runID: String }
    private var actionTarget: FeedbackTarget?
    private var feedbackSelection: FeedbackTarget?

    init(service: ASLService = DaemonClient(), command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent(), feedbackHistory: WorkspaceFeedbackHistory? = nil, now: @escaping () -> Date = Date.init) {
        self.feedbackHistory = feedbackHistory ?? WorkspaceFeedbackHistory()
        self.service = service; self.command = command; self.runner = runner
        self.now = now
    }

    var selected: ASLRow? { rows.first { $0.rowID == selection } }
    var hasQuestion: Bool { !(selected?.question?.questionID ?? "").isEmpty }
    var verificationBlocked: Bool {
        selected?.pauseReason.replacingOccurrences(of: "_", with: "-") == "verification-failed"
    }
    enum NextAction { case answer, inspect, resume, focus }
    var nextAction: NextAction {
        if hasQuestion { return .answer }
        if verificationBlocked { return .inspect }
        if disabledReason(ASLMethod.runResume) == nil { return .resume }
        return .focus
    }
    func sessionStatus(_ session: WorkspaceRunDetail.Session) -> String {
        if verificationBlocked, session.id == selected?.sessionID, session.status == "pending" {
            return "Incomplete · verification failed"
        }
        return WorkspaceWording.status(session.status)
    }
    var pendingWork: Bool { acting || pending != nil }

    func start() {
        started = true
        refresh()
        scheduleRefresh()
    }

    func setAutomaticRefreshEnabled(_ enabled: Bool) {
        guard automaticRefreshEnabled != enabled else { return }
        automaticRefreshEnabled = enabled
        timer?.invalidate(); timer = nil
        if enabled && started {
            refresh()
            scheduleRefresh()
        }
    }

    private func scheduleRefresh() {
        guard started, automaticRefreshEnabled, timer == nil else { return }
        timer = Timer.scheduledTimer(withTimeInterval: 5, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.automaticRefresh() }
        }
    }

    func automaticRefresh() {
        guard started, automaticRefreshEnabled else { return }
        refresh(forceDetail: false)
    }

    func close() {
        started = false
        timer?.invalidate(); timer = nil
        generation += 1; detailGeneration += 1
        loading = false; reading = false; stale = true
        detailRefreshedAt = nil
        detail = nil
        detailRow = nil
    }

    func refresh(forceDetail: Bool = true) {
        guard !loading else { return }
        loading = true
        generation += 1
        let seq = generation
        let service = self.service
        runner.run({ service.snapshot() }) { [weak self] result in
            guard let self, seq == self.generation else { return }
            self.loading = false
            switch result {
            case let .failure(error):
                self.stale = true; self.error = ASLRefusal.text(error)
            case let .success(result):
                let previous = self.selected?.runID
                self.status = result.status
                self.stale = result.status.snapshotStale || ASLView.of(status: result.status, meta: result.meta, hasRows: result.snapshot?.rows.isEmpty == false) != .live
                if !self.stale { self.listRefreshedAt = self.now() }
                // A stale projection is not a fresh read. Keep the last known
                // rows and their observation time until a live snapshot arrives.
                if !self.stale || self.rows.isEmpty {
                    self.rows = result.snapshot?.rows ?? []
                }
                self.warnings = result.snapshot?.warnings ?? []
                self.truncated = result.snapshot?.truncated ?? false
                self.error = self.stale ? "ASL is unavailable or the snapshot is stale. Controls are read-only." : nil
                if let target = self.feedbackSelection, !self.stale {
                    self.feedbackSelection = nil
                    let found = self.rows.first { $0.rowID == target.rowID && $0.runID == target.runID }
                    self.select(found?.rowID)
                    if found == nil { self.error = "The original run \(target.runID) is no longer listed. Its outcome remains in Action feedback." }
                } else if self.selected == nil { self.select(self.rows.first?.rowID) }
                else if previous != self.selected?.runID { self.select(self.selection) }
                else if !self.reading && !self.stale && (forceDetail || self.detailNeedsRefresh) { self.readDetail() }
            }
        }
    }

    private var detailNeedsRefresh: Bool {
        guard var current = selected, var previous = detailRow, let at = detailRefreshedAt,
              detail != nil, detailError == nil else { return true }
        // Authorization tokens can rotate without changing displayed evidence.
        current.actionToken = ""; previous.actionToken = ""
        return current != previous || now().timeIntervalSince(at) >= Self.detailRefreshInterval
    }

    func select(_ id: String?) {
        selection = id
        detailGeneration += 1
        detailRefreshedAt = nil
        detailRow = nil
        detail = nil; detailError = nil; reading = false
        if !stale { readDetail() }
    }

    func readDetail() {
        guard let row = selected, !reading else { return }
        guard let command = command() else { detailError = "amx is not available."; return }
        reading = true; detailError = nil
        detailGeneration += 1
        let seq = detailGeneration
        runner.run({ command.json(WorkspaceRunDetail.self, ["plans", "status", "--json", "--", row.planDir], timeout: 25) }) { [weak self] result in
            guard let self, seq == self.detailGeneration, self.selected?.runID == row.runID else { return }
            self.reading = false
            switch result {
            case let .success(detail):
                guard detail.runner.runID == row.runID,
                      ASLResponse.canonicalDir(detail.planDir) == ASLResponse.canonicalDir(row.planDir) else {
                    self.detailRefreshedAt = nil; self.detail = nil; self.detailError = "This plan now reports a different run. Refresh the run list."; return
                }
                self.detailRefreshedAt = self.now()
                self.detailRow = row
                self.detail = detail
            case let .failure(error): self.detailError = error.message
            case .partial: self.detailError = "ASL returned an incomplete status report. Refresh to try again."
            }
        }
    }

    func disabledReason(_ method: String) -> String? {
        if acting { return "An operation is in progress." }
        if pending != nil { return "Resolve the uncertain operation using Retry Receipt first." }
        guard let row = selected else { return "Select a run." }
        if method == ASLMethod.runResume && hasQuestion { return "Answer the open question to resume this run." }
        return ASLResponse.refusal(row: row, method: method, view: stale ? .stale : .live, status: status)
    }

    func answer() {
        guard !stale, let row = selected, hasQuestion else { return }
        onAnswer?(ASLAnswerTarget(runID: row.runID, planDir: row.planDir))
    }

    func perform(_ method: String, target: ASLRow? = nil) {
        if let target, (selected?.rowID != target.rowID || selected?.runID != target.runID) {
            actionTarget = FeedbackTarget(rowID: target.rowID, runID: target.runID)
            actionFeedback = WorkspaceFeedback(target: target.displayLabel + " · " + target.runID, message: "The selected run changed. Select it again before using this control.", kind: .warning)
            return
        }
        guard [ASLMethod.runResume, ASLMethod.runStopAfterCurrent, ASLMethod.runAbort].contains(method),
              disabledReason(method) == nil, let row = selected else { return }
        actionTarget = FeedbackTarget(rowID: row.rowID, runID: row.runID)
        acting = true; actionFeedback = nil
        let service = self.service
        runner.run({ service.snapshot() }) { [weak self] result in
            guard let self else { return }
            guard case let .success(snapshot) = result else {
                self.acting = false
                if case let .failure(error) = result { self.actionFeedback = WorkspaceFeedback(target: row.displayLabel + " · " + row.runID, message: ASLRefusal.text(error), nextStep: "Refresh the run before trying this control again.") }
                return
            }
            guard let current = snapshot.snapshot?.rows.first(where: { $0.rowID == row.rowID && $0.runID == row.runID && $0.planDir == row.planDir }),
                  !snapshot.status.snapshotStale,
                  ASLResponse.refusal(row: current, method: method,
                    view: ASLView.of(status: snapshot.status, meta: snapshot.meta, hasRows: true), status: snapshot.status) == nil,
                  method != ASLMethod.runResume || (current.question?.questionID ?? "").isEmpty else {
                self.acting = false; self.actionFeedback = WorkspaceFeedback(target: row.displayLabel + " · " + row.runID, message: "The run changed or this control is no longer permitted. Refresh and select it again.", kind: .warning); self.refresh(); return
            }
            self.submit(ASLRequestParams(method: method, planDir: current.planDir, rowID: current.rowID,
                runID: current.runID, operationID: ASLResponse.newOperationID(), actionToken: current.actionToken))
        }
    }

    func retryReceipt() {
        guard !acting, let pending else { return }
        acting = true
        submit(pending)
    }

    private func receiptFeedback(_ params: ASLRequestParams, _ message: String) -> WorkspaceFeedback {
        WorkspaceFeedback(target: params.planDir + " · " + params.runID, message: message, kind: .warning,
            nextStep: "Retry Receipt checks operation \(params.operationID). It does not create a new request.", recovery: .receipt)
    }

    private func submit(_ params: ASLRequestParams) {
        pending = params
        actionTarget = FeedbackTarget(rowID: params.rowID, runID: params.runID)
        let service = self.service
        runner.run({ service.act(params) }) { [weak self] result in
            guard let self else { return }
            self.acting = false
            switch result {
            case let .failure(error):
                if case .transport = error {
                    self.actionFeedback = self.receiptFeedback(params, "Outcome unknown for \(ASLRefusal.methodLabel(params.method)) on run \(params.runID). Retry Receipt checks the same operation; it does not create a second request.")
                } else { self.pending = nil; self.actionFeedback = WorkspaceFeedback(target: params.planDir + " · " + params.runID, message: ASLRefusal.text(error), nextStep: "Refresh to check the run before trying a control again.") }
            case let .success(receipt):
                guard receipt.operationID == params.operationID, receipt.method == params.method else {
                    self.actionFeedback = self.receiptFeedback(params, "The receipt did not match this operation. Its outcome is unknown; use Retry Receipt."); return
                }
                if ["accepted", "consumed", "rejected", "expired", "superseded"].contains(receipt.status) {
                    self.pending = nil
                    self.actionFeedback = WorkspaceFeedback(target: params.planDir + " · " + params.runID,
                        message: "\(ASLRefusal.methodLabel(params.method).capitalized): \(receipt.status).",
                        kind: ["accepted", "consumed"].contains(receipt.status) ? .success : .warning,
                        nextStep: "Refresh to inspect the run's current state.")
                } else { self.actionFeedback = self.receiptFeedback(params, "The operation has no final receipt. Use Retry Receipt.") }
            }
            if self.timer != nil { self.refresh() }
        }
    }
}
