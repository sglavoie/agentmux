import Foundation
import Combine

/// A bounded project view over the existing session list, companion snapshot,
/// and recap cache. It never synthesizes claims across conversations or starts
/// model requests as a side effect of opening or refreshing a project.
@MainActor
final class WorkspaceBriefingModel: ObservableObject {
    @Published private(set) var project = ""
    @Published private(set) var projects: [String] = []
    @Published private(set) var rows: [WorkspaceRow] = []
    @Published private(set) var waiting: [WorkspaceWaitingRow] = []
    @Published private(set) var feedLive = false
    @Published private(set) var recent: [RecentConversation] = []
    @Published private(set) var total = 0
    @Published private(set) var updatedAt: Date?
    @Published private(set) var loading = false
    @Published private(set) var error: String?
    @Published private(set) var acting = false
    @Published private(set) var actionError: String?
    @Published private(set) var recaps: [String: WorkspaceRecapModel] = [:]

    var onFocus: ((AttentionCycler.Key) -> Void)?
    var onConversation: ((ConversationIdentity, Int?) -> Void)?
    var onHandoff: ((RecentConversation) -> Void)?
    var onContinue: ((RecentConversation, @escaping (String?) -> Void) -> Void)?
    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner
    private var allRows: [WorkspaceRow] = []
    private var allWaiting: [WorkspaceWaitingRow] = []
    private var generation = 0
    private var active = false

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent()) {
        self.command = command; self.runner = runner
    }

    /// The project title has the same cross-profile/harness meaning as
    /// History's project filter. The CLI owns canonical project matching,
    /// including its existing worktree and ambiguous-metadata rules.
    func update(projects configured: [ProjectEntry], sessions: [CompanionSession], asl: [CompanionASLWaiting] = [],
                socket: String = "", live: Bool, order: WorkspaceOrder = WorkspaceOrder()) {
        feedLive = live
        allRows = WorkspaceSidebar.rows(sessions, snapshotSocket: socket)
        allWaiting = WorkspaceInspector.waiting(sessions, asl: asl, snapshotSocket: socket, order: order)
        var names = Set(configured.map(\.title).filter { !$0.isEmpty })
        names.formUnion(allRows.map(\.project))
        if !project.isEmpty { names.insert(project) }
        projects = names.sorted { $0.localizedStandardCompare($1) == .orderedAscending }
        if active, project.isEmpty, let first = projects.first { select(first); return }
        updateRows()
        if active { reconcileRecaps() }
    }

    func open(preferredProject: String?) {
        active = true
        let wanted = preferredProject.flatMap { projects.contains($0) ? $0 : nil }
            ?? (projects.contains(project) ? project : projects.first ?? "")
        select(wanted, refresh: false)
    }

    func select(_ name: String, refresh: Bool = true) {
        guard !acting, projects.contains(name) else { return }
        if name != project {
            generation += 1; loading = false
            recaps.values.forEach { $0.close() }; recaps = [:]
            project = name; recent = []; total = 0
            updatedAt = nil; error = nil; actionError = nil
        }
        updateRows()
        if refresh, active { self.refresh() }
    }

    private func updateRows() {
        rows = allRows.filter { $0.project == project }
        let keys = Set(rows.map(\.key))
        waiting = allWaiting.filter { keys.contains($0.key) && $0.category != .completed }
    }

    /// All known live conversation identities, then up to ten recent
    /// conversations, deduplicated by harness AND session ID.
    var conversations: [RecentConversation] {
        var result: [RecentConversation] = [], seen: Set<String> = []
        for row in rows {
            guard let identity = row.conversation, seen.insert(identity.encoded).inserted else { continue }
            result.append(recent.first { $0.identity == identity }
                ?? RecentConversation(id: identity.sessionID, harness: identity.harness, profile: row.label, project: project))
        }
        for item in recent.prefix(10) where item.identity.isUsable && seen.insert(item.selector).inserted { result.append(item) }
        return result
    }

    func liveRows(for conversation: RecentConversation) -> [WorkspaceRow] {
        rows.filter { $0.conversation == conversation.identity }
    }

    func refresh() {
        guard active, !project.isEmpty, !loading, !acting else { return }
        guard let command = command() else { error = AMXWording.current; return }
        generation += 1
        let request = generation, selected = project
        loading = true; error = nil
        let args = WorkspaceHistoryModel.arguments(query: "", project: selected, includeAutomated: true, limit: 10)
        runner.run({ command.json(WorkspaceHistorySearch.self, args, timeout: 60) }) { [weak self] result in
            guard let self, self.active, self.generation == request, self.project == selected else { return }
            self.loading = false
            switch result {
            case let .failure(error): self.error = error.message
            case let .success(value), let .partial(value, _):
                self.recent = value.results.prefix(10).map(\.conversation)
                self.total = value.total
                self.updatedAt = Date()
                if case .partial = result { self.error = "The conversation list is incomplete. Refresh to try again." }
            }
            self.reconcileRecaps(refresh: true)
        }
    }

    private func reconcileRecaps(refresh: Bool = false) {
        let items = conversations
        let keep = Set(items.map(\.selector))
        for (key, model) in recaps where !keep.contains(key) { model.close(); recaps.removeValue(forKey: key) }
        for item in items {
            let model: WorkspaceRecapModel
            if let existing = recaps[item.selector] { model = existing }
            else {
                model = WorkspaceRecapModel(command: command, runner: runner, automaticallyGenerate: false)
                model.onEvidence = { [weak self] identity, index in self?.onConversation?(identity, index) }
                recaps[item.selector] = model
            }
            let live = liveRows(for: item)
            let revision = String(item.lastAt?.timeIntervalSince1970 ?? 0) + live.map { "\($0.key):\($0.iconState):\($0.working)" }.joined()
            let canGenerate = feedLive && !live.contains(where: \.working)
            let unchanged = model.conversation == item.identity
            model.select(item.identity, revision: revision, canGenerate: canGenerate)
            if refresh, unchanged { model.check() }
        }
    }

    func focus(_ key: AttentionCycler.Key) {
        guard feedLive, rows.contains(where: { $0.key == key }) else { return }
        actionError = nil
        onFocus?(key)
    }

    func continueConversation(_ conversation: RecentConversation) {
        guard !acting, feedLive, conversations.contains(where: { $0.identity == conversation.identity }) else { return }
        if let live = liveRows(for: conversation).first { focus(live.key); return }
        guard let onContinue else { return }
        acting = true; actionError = nil
        onContinue(conversation) { [weak self] error in
            self?.acting = false; self?.actionError = error
        }
    }

    func close() {
        active = false; generation += 1; loading = false
        recaps.values.forEach { $0.close() }; recaps = [:]
        recent = []; updatedAt = nil; error = nil; actionError = nil
    }
}
