import Foundation
import Combine

@MainActor
final class WorkspaceSessionPeekModel: ObservableObject {
    @Published private(set) var row: WorkspaceRow?
    @Published private(set) var waiting: WorkspaceWaitingRow?
    @Published private(set) var lastObserved: Date?
    @Published private(set) var current = false
    @Published private(set) var reading = WorkspaceWaitingContext.Reading()
    let recap: WorkspaceRecapModel
    var readContext: ((WorkspaceWaitingRow, @escaping (WorkspaceWaitingContext.Result) -> Void) -> Void)?
    private var generation = 0

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent()) {
        recap = WorkspaceRecapModel(command: command, runner: runner, automaticallyGenerate: false)
    }

    func show(_ row: WorkspaceRow, waiting: WorkspaceWaitingRow?, current: Bool, lastObserved: Date? = nil) {
        let changed = self.row?.key != row.key || self.row?.conversation != row.conversation
            || self.waiting?.contextVersion != waiting?.contextVersion
            || self.waiting?.changedAt != waiting?.changedAt || self.current != current
        self.lastObserved = lastObserved
        self.row = row; self.waiting = waiting; self.current = current
        recap.select(row.conversation, revision: row.iconState + (waiting?.contextVersion ?? ""), canGenerate: false)
        if changed {
            generation += 1; reading = .init()
            if waiting != nil && current { refreshQuestion() }
        }
    }

    func refreshQuestion() {
        guard current, let waiting, let readContext, !reading.loading else { return }
        generation += 1
        let request = generation
        reading.begin()
        readContext(waiting) { [weak self] result in
            guard let self, self.generation == request, self.current,
                  self.waiting?.sameContext(as: waiting) == true else { return }
            self.reading.finish(result)
        }
    }

    func close() {
        generation += 1; row = nil; waiting = nil; reading = .init(); recap.close()
    }
}
