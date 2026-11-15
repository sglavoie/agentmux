import Foundation
import Combine

/// Coordinates the existing readers; their read markers, stale-response guards,
/// and explicit conversation actions remain owned by their original models.
@MainActor
final class WorkspaceCatchUpModel: ObservableObject {
    let briefing: WorkspaceBriefingModel
    let activity: WorkspaceActivityModel
    let summary: WorkspaceReturnSummary
    private var subscriptions = Set<AnyCancellable>()
    private var rows: [WorkspaceRow] = []
    private var active = false

    init(briefing: WorkspaceBriefingModel, activity: WorkspaceActivityModel, summary: WorkspaceReturnSummary) {
        self.briefing = briefing; self.activity = activity; self.summary = summary
        for publisher in [briefing.objectWillChange, activity.objectWillChange, summary.objectWillChange] {
            publisher.sink { [weak self] in self?.objectWillChange.send() }.store(in: &subscriptions)
        }
        activity.$report.sink { [weak self] report in
            guard let self, self.active else { return }
            self.summary.showActivity(report?.entries ?? [], rows: self.rows, current: self.briefing.feedLive)
        }.store(in: &subscriptions)
    }

    func update(projects: [ProjectEntry], sessions: [CompanionSession], asl: [CompanionASLWaiting], live: Bool,
                order: WorkspaceOrder, pendingAnswers: [WorkspaceWaitingRow] = []) {
        rows = WorkspaceSidebar.rows(sessions)
        briefing.update(projects: projects, sessions: sessions, asl: asl, live: live, order: order)
        briefing.excludePendingAnswers(pendingAnswers)
        guard active else { return }
        activity.updateLiveScope(rows: rows)
        summary.showActivity(activity.report?.entries ?? [], rows: rows, current: live)
    }

    func open(project: String? = nil) {
        guard !briefing.acting else { return }
        active = true
        briefing.open(preferredProject: project)
        select(project ?? "", refresh: false)
    }

    func select(_ project: String, refresh: Bool = true) {
        guard !briefing.acting else { return }
        briefing.select(project, refresh: false)
        activity.clearFlightFilter()
        activity.setProject(briefing.project, rows: rows)
        if refresh { briefing.refresh() }
    }

    func openFlight(_ session: FlightSession, kind: String) {
        guard !briefing.acting else { return }
        open()
        activity.filterFlightSession(session, kind: kind)
    }

    func refresh() {
        briefing.refresh()
        summary.recaps.values.forEach { $0.check() }
        activity.setProject(briefing.project, rows: rows)
        activity.refresh()
    }

    func close() {
        active = false
        briefing.close(); activity.close(); summary.close()
    }
}
