import Foundation
import Combine

/// A read-only summary. Opening or dismissing it never acknowledges activity.
@MainActor
final class WorkspaceWelcomeModel: ObservableObject {
    struct Receipt: Equatable {
        let title: String
        let detail: String
        var milestone: WorkspaceMilestoneWatch.Milestone?
    }
    @Published private(set) var receipt: Receipt?
    @Published private(set) var summary: String?
    @Published private(set) var waiting = 0
    @Published private(set) var working = 0
    @Published var pendingAnswers = 0
    var remainingWaiting: Int { max(0, waiting - pendingAnswers) }
    @Published private(set) var live = false
    @Published private(set) var dismissed = false
    var isEnabled = false {
        didSet {
            guard oldValue != isEnabled, !isEnabled else { return }
            generation += 1
            summary = nil
        }
    }
    var command: () -> AMXCommand?
    var onReceipt: (Receipt) -> Void = { _ in }
    var onCaughtUp: () -> Void = {}
    var onAction: (WorkspaceCommand) -> Void = { _ in }
    private let runner: ASLWorkRunner
    private let readStore: WorkspaceActivityReadStore
    private var generation = 0
    private var previousWaiting: Int?
    private var hasShownWelcome = false
    private let defaults: UserDefaults?
    private var departedAt: Date?
    private var present = false
    static let presenceKey = "workspace.lastPresence.v1"
    static let minimumAbsence: TimeInterval = 120
    private var expiry: DispatchWorkItem?
    var schedule: (TimeInterval, DispatchWorkItem) -> Void = { delay, work in
        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: work)
    }

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent(),
         readStore: WorkspaceActivityReadStore = .standard, defaults: UserDefaults? = nil) {
        self.command = command; self.runner = runner; self.readStore = readStore
        self.defaults = defaults
        departedAt = defaults?.object(forKey: Self.presenceKey) as? Date
    }

    var visible: Bool { !dismissed && (receipt != nil || summary != nil) }
    var title: String { receipt?.title ?? "Welcome back" }
    var detail: String { receipt?.detail ?? summary ?? "" }
    var attentionText: String {
        guard live else { return "Waiting status unavailable" }
        return waiting == 0 ? "Nothing waiting on you" : "\(waiting) \(waiting == 1 ? "session needs" : "sessions need") you"
    }

    func observe(waiting: Int, working: Int, live: Bool, celebrate: Bool) {
        self.waiting = waiting; self.working = working; self.live = live
        guard live else {
            previousWaiting = nil
            receipt = nil
            return
        }
        if celebrate, let previousWaiting, previousWaiting > 0, waiting == 0 {
            allCaughtUp()
        } else if remainingWaiting > 0, receipt?.title == "All caught up" {
            receipt = nil
        }
        previousWaiting = waiting
    }

    func allCaughtUp() {
        showReceipt("All caught up", detail: working == 0 ? "Nothing waiting on you." :
            "\(working) \(working == 1 ? "session is" : "sessions are") still working.")
        onCaughtUp()
    }

    func milestoneUnlocked(_ milestone: WorkspaceMilestoneWatch.Milestone) {
        guard var value = receipt, value.title == "All caught up" else { return }
        value.milestone = milestone
        receipt = value
        expire(value, after: 10)
    }

    func answerSent() {
        guard receipt?.title != "All caught up" else { return }
        showReceipt("Answer sent", detail: "Waiting status updates when the agent responds.")
    }

    private func showReceipt(_ title: String, detail: String) {
        expiry?.cancel()
        generation += 1
        summary = nil
        let value = Receipt(title: title, detail: detail)
        receipt = value; dismissed = false
        onReceipt(value)
        expire(value, after: 6)
    }

    private func expire(_ value: Receipt, after delay: TimeInterval) {
        expiry?.cancel()
        let work = DispatchWorkItem { [weak self] in
            guard self?.receipt == value else { return }
            self?.receipt = nil
        }
        expiry = work; schedule(delay, work)
    }

    /// The app retains this model with its workspace controller across closes.
    func showWelcomeOnce() {
        guard isEnabled, !hasShownWelcome, command() != nil else { return }
        hasShownWelcome = true
        refresh()
    }

    func leave(at date: Date = .now) {
        guard present else { return }
        present = false; departedAt = date
        defaults?.set(date, forKey: Self.presenceKey)
    }

    func arrive(at date: Date = .now) {
        guard !present else { return }
        present = true
        let departure = departedAt
        departedAt = nil
        defaults?.set(date, forKey: Self.presenceKey)
        guard isEnabled, let departure, date.timeIntervalSince(departure) >= Self.minimumAbsence else { return }
        refresh(since: departure)
    }

    private func refresh(since: Date? = nil) {
        generation += 1
        let expected = generation
        guard let command = command() else { return }
        dismissed = false
        receipt = nil; expiry?.cancel()
        summary = "Reading recent activity…"
        let store = readStore
        runner.run({ () -> (AMXCommandResult<WorkspaceActivityReport>, WorkspaceActivityReadState?) in
            let markers = try? store.load()
            return (command.json(WorkspaceActivityReport.self,
                ["session", "activity", "--json", "--days", "7", "--limit", "1000"], timeout: 20), markers)
        }) { [weak self] result in
            guard let self, self.generation == expected else { return }
            switch result.0 {
            case .failure: self.summary = "Recent activity is unavailable. Open Briefing to retry."
            case let .success(report), let .partial(report, _):
                guard let markers = result.1 else {
                    self.summary = "Read status is unavailable. Open Briefing to review recent events."
                    return
                }
                let count = Set(report.entries.filter {
                    markers.read[$0.id] == nil && (($0.date ?? .distantPast) >= (since ?? .distantPast))
                }.map(\.id)).count
                let qualifier = report.truncated || report.corruptLines > 0 ? "At least " : ""
                let period = since == nil ? "in the past 7 days" : "since you stepped away (past 7 days)"
                self.summary = "\(qualifier)\(count) unread \(count == 1 ? "update" : "updates") \(period)."
            }
        }
    }

    func dismiss() { dismissed = true; receipt = nil; expiry?.cancel() }
    func close() { generation += 1; expiry?.cancel(); receipt = nil; summary = nil }
}
