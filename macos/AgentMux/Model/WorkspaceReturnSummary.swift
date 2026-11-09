import Foundation
import Combine

/// Absence is separate from read acknowledgements: opening this summary never
/// marks journal events read. Only the last presence timestamp is persisted.
@MainActor
final class WorkspaceReturnSummary: ObservableObject {
    struct Update: Identifiable {
        let id: String
        var row: WorkspaceRow?
        let event: WorkspaceActivityEntry?
        var title: String { row?.displayName ?? event?.title ?? "Session" }
        var detail: String { row.map { $0.name.isEmpty ? $0.detail : $0.project + " · " + $0.detail } ?? event?.detail ?? "" }
    }
    @Published private(set) var since: Date?
    @Published private(set) var refreshedAt: Date?
    @Published private(set) var updates: [Update] = []
    @Published private(set) var loading = false
    @Published private(set) var problem: String?
    @Published private(set) var current = false
    private(set) var recaps: [String: WorkspaceRecapModel] = [:]
    var onReturn: (() -> Void)?
    var onOpen: ((Update) -> Void)?
    var onAnswer: ((AttentionCycler.Key) -> Void)?
    var onReview: ((AttentionCycler.Key) -> Void)?
    var onActivity: (() -> Void)?
    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner
    private let defaults: UserDefaults?
    private var rows: [WorkspaceRow] = []
    private var departedAt: Date?
    private var present = false
    private var generation = 0
    static let minimumAbsence: TimeInterval = 120
    static let presenceKey = "workspace.lastPresence.v1"

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent(), defaults: UserDefaults? = nil) {
        self.command = command; self.runner = runner; self.defaults = defaults
        departedAt = defaults?.object(forKey: Self.presenceKey) as? Date
    }

    func leave(at date: Date = .now) {
        guard present else { return }
        present = false; departedAt = date
        defaults?.set(date, forKey: Self.presenceKey)
    }

    func arrive(at date: Date = .now, automatically: Bool) {
        guard !present else { return }
        present = true
        let departure = departedAt
        departedAt = nil
        defaults?.set(date, forKey: Self.presenceKey)
        guard automatically, let departure, date.timeIntervalSince(departure) >= Self.minimumAbsence else { return }
        since = departure
        refresh(now: date)
        onReturn?()
    }

    func observe(rows: [WorkspaceRow], current: Bool, active: Bool, now: Date = .now) {
        self.rows = rows; self.current = current
        if present && active { defaults?.set(now, forKey: Self.presenceKey) }
        // Preserve ordering while the summary is being read, but never offer an
        // action based on an expired session or status.
        updates = updates.map { update in
            var update = update
            if let key = update.row?.key {
                let next = rows.first { $0.key == key }
                if next?.conversation != update.row?.conversation { recaps[update.id]?.close() }
                update.row = next
            }
            return update
        }
    }

    func refresh(now: Date = .now) {
        generation += 1
        let request = generation
        guard let command = command() else { loading = false; problem = "Recent activity is unavailable."; return }
        let from = since ?? now.addingTimeInterval(-24 * 3600)
        let days = min(90, max(1, Int(ceil(now.timeIntervalSince(from) / 86400))))
        loading = true; problem = nil
        runner.run({ command.json(WorkspaceActivityReport.self, ["session", "activity", "--json", "--days", String(days), "--limit", "1000"], timeout: 20) }) { [weak self] result in
            guard let self, self.generation == request else { return }
            self.loading = false
            switch result {
            case let .failure(error): self.problem = error.message
            case let .success(report), let .partial(report, _):
                self.refreshedAt = now
                let entries = report.entries.filter { ($0.date ?? .distantPast) >= from }.sorted { ($0.date ?? .distantPast) > ($1.date ?? .distantPast) }
                var seen = Set<String>()
                var updates: [Update] = []
                // Waiting items lead, including ones already waiting when you left.
                for row in self.rows where row.attention {
                    let id = row.key.runtimeSessionID
                    guard seen.insert(id).inserted else { continue }
                    updates.append(Update(id: id, row: row, event: entries.first { $0.runtimeSessionID == id }))
                }
                for event in entries {
                    let id = event.runtimeSessionID.isEmpty ? event.id : event.runtimeSessionID
                    guard seen.insert(id).inserted else { continue }
                    updates.append(Update(id: id, row: self.rows.first { $0.key.runtimeSessionID == event.runtimeSessionID }, event: event))
                }
                for row in self.rows where row.working && seen.insert(row.key.runtimeSessionID).inserted {
                    updates.append(Update(id: row.key.runtimeSessionID, row: row, event: nil))
                }
                self.recaps.values.forEach { $0.close() }; self.recaps = [:]
                for update in updates.prefix(6) {
                    if let identity = update.row?.conversation ?? update.event?.conversation {
                        let recap = WorkspaceRecapModel(command: self.command, runner: self.runner, automaticallyGenerate: false)
                        self.recaps[update.id] = recap
                        recap.select(identity, revision: "return", canGenerate: false)
                    }
                }
                self.updates = Array(updates.prefix(6))
                let incomplete = report.truncated || report.corruptLines > 0 || now.timeIntervalSince(from) > 90 * 86400
                    || (WorkspaceActivityEntry.date(report.earliestAvailable).map { $0 > from } ?? false)
                if incomplete || updates.count > 6 {
                    self.problem = "Showing a partial summary. Open Activity for more recorded updates."
                }
            }
        }
    }

    func close() {
        generation += 1; loading = false
        recaps.values.forEach { $0.close() }
    }
}
