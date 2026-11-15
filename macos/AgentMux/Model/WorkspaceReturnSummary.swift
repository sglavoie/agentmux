import Foundation
import Combine

/// Cached outcomes for Briefing's retained activity. Never generates or acknowledges.
@MainActor
final class WorkspaceReturnSummary: ObservableObject {
    struct Update: Identifiable {
        let id: String
        var row: WorkspaceRow?
        let event: WorkspaceActivityEntry?
        var title: String { row?.displayName ?? event?.title ?? "Session" }
        var detail: String { row.map { $0.name.isEmpty ? $0.detail : $0.project + " · " + $0.detail } ?? event?.detail ?? "" }
    }
    @Published private(set) var updates: [Update] = []
    @Published private(set) var current = false
    private(set) var recaps: [String: WorkspaceRecapModel] = [:]
    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent()) {
        self.command = command; self.runner = runner
    }

    func showActivity(_ entries: [WorkspaceActivityEntry], rows: [WorkspaceRow], current: Bool) {
        self.current = current
        var seen = Set<String>()
        let next = entries.filter { seen.insert($0.groupID).inserted }.map { entry in
            Update(id: entry.groupID, row: rows.first { !$0.key.runtimeSessionID.isEmpty && $0.key.runtimeSessionID == entry.runtimeSessionID }, event: entry)
        }
        let keep = Set(next.map(\.id))
        for (id, recap) in recaps where !keep.contains(id) { recap.close(); recaps.removeValue(forKey: id) }
        for update in next {
            guard let identity = update.event?.conversation ?? update.row?.conversation else { continue }
            let recap = recaps[update.id] ?? WorkspaceRecapModel(command: command, runner: runner, automaticallyGenerate: false)
            recaps[update.id] = recap
            recap.select(identity, revision: update.event?.id ?? "", canGenerate: false)
        }
        updates = next
    }

    func close() {
        recaps.values.forEach { $0.close() }
    }
}
