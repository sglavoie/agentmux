import Foundation

/// Where the operator put sessions: the order they dragged the sessions of
/// each project into, and the order of the sessions pinned above every
/// project. Whether a session is pinned is not kept here: it is the ★ in the
/// session's title, which the snapshot reports and the dashboard reads too.
///
/// It names sessions, never positions: a session that is not named, one
/// launched since the drag, follows the named ones in the order
/// `WorkspaceSidebar` gives it. A project nobody dragged in is not here.
struct WorkspaceOrder: Equatable {
    static let defaultsKey = "amxWorkspaceSessionOrder"
    static let pinnedDefaultsKey = "amxWorkspacePinnedSessions"
    /// Set once the pins this app kept by itself, before pins were shared,
    /// have been written to the sessions' titles.
    static let pinsSharedDefaultsKey = "amxWorkspacePinsShared"
    static let collapsedDefaultsKey = "amxWorkspaceCollapsedProjects"
    static let flatDefaultsKey = "amxWorkspaceFlatSessionOrder"
    static let visitedDefaultsKey = "amxWorkspaceVisitedSessions"

    /// The sessions of each project that was dragged in, first to last.
    private(set) var projects: [String: [String]] = [:]
    /// The pinned sessions, first to last: in the order they were pinned
    /// here, until they are dragged. Only their order: a session pinned
    /// elsewhere follows these, and one named here that is no longer pinned
    /// is not listed as pinned.
    private(set) var pinned: [String] = []
    private(set) var flat: [String] = []
    private(set) var visited: [String] = []
    /// The groups the operator collapsed (`groupToken`). A project with no
    /// session listed stays collapsed: projects are few, and one launched
    /// into again comes back the way it was left.
    private(set) var collapsed: Set<String> = []

    /// What a session is named by here: its runtime session.
    static func token(_ key: AttentionCycler.Key) -> String {
        key.runtimeSessionID
    }

    /// Records the order of a project's sessions, replacing the one it had.
    mutating func set(_ keys: [AttentionCycler.Key], for project: String) {
        projects[project] = keys.map(Self.token)
    }

    mutating func setFlat(_ keys: [AttentionCycler.Key]) {
        flat = keys.map(Self.token)
    }

    mutating func visit(_ key: AttentionCycler.Key) {
        let token = Self.token(key)
        visited.removeAll { $0 == token }
        visited.insert(token, at: 0)
    }

    func recentlyVisited(_ rows: [WorkspaceRow]) -> [WorkspaceRow] {
        let rank = Dictionary(visited.enumerated().map { ($1, $0) }, uniquingKeysWith: { first, _ in first })
        return rows.enumerated().sorted {
            let a = rank[Self.token($0.element.key)] ?? Int.max
            let b = rank[Self.token($1.element.key)] ?? Int.max
            return a != b ? a < b : $0.offset < $1.offset
        }.map(\.element)
    }

    func arrangedFlat(_ rows: [WorkspaceRow]) -> [WorkspaceRow] {
        let rank = Dictionary(flat.enumerated().map { ($1, $0) }, uniquingKeysWith: { first, _ in first })
        return rows.enumerated().sorted {
            let a = rank[Self.token($0.element.key)] ?? Int.max
            let b = rank[Self.token($1.element.key)] ?? Int.max
            return a != b ? a < b : $0.offset < $1.offset
        }.map(\.element)
    }

    /// A project's rows in the order they were dragged into.
    func arranged(_ rows: [WorkspaceRow], in project: String) -> [WorkspaceRow] {
        guard let tokens = projects[project] else { return rows }
        let rank = Dictionary(tokens.enumerated().map { ($1, $0) }, uniquingKeysWith: { first, _ in first })
        return rows.enumerated().sorted { lhs, rhs in
            let (a, b) = (rank[Self.token(lhs.element.key)] ?? Int.max, rank[Self.token(rhs.element.key)] ?? Int.max)
            return a != b ? a < b : lhs.offset < rhs.offset
        }.map(\.element)
    }

    /// Places a session pinned here below the ones already pinned, or takes
    /// an unpinned one out of their order.
    mutating func setPinned(_ key: AttentionCycler.Key, _ wanted: Bool) {
        let token = Self.token(key)
        pinned.removeAll { $0 == token }
        if wanted { pinned.append(token) }
    }

    /// Records the order of the pinned sessions, replacing the one they had.
    /// Every pin is named, so one left out is no longer pinned.
    mutating func setPinned(_ keys: [AttentionCycler.Key]) {
        pinned = keys.map(Self.token)
    }

    /// The listed sessions this app pinned by itself, before pins were the
    /// sessions' own, that are not pinned yet: what is pinned once, at the
    /// first live snapshot after upgrading, so no pin is lost. In the order
    /// they were pinned.
    func unsharedPins(_ listed: [WorkspaceRow]) -> [WorkspaceRow] {
        let byToken = Dictionary(listed.map { (Self.token($0.key), $0) }, uniquingKeysWith: { first, _ in first })
        return pinned.compactMap { byToken[$0] }.filter { !$0.pinned }
    }

    /// Forgets the pins of sessions that are not among `listed`: a session
    /// that ended is never listed again, and its pin would be kept forever.
    mutating func forgetPins(notAmong listed: [AttentionCycler.Key]) {
        let tokens = Set(listed.map(Self.token))
        pinned.removeAll { !tokens.contains($0) }
    }

    /// Forgets what is kept of sessions that are not among `listed`, and of
    /// projects none of them is in: their pins, and their places in the
    /// orders projects were dragged into. A project left with no session
    /// listed is forgotten too. Only a live snapshot may say what is listed.
    mutating func forget(notAmong listed: [WorkspaceRow]) {
        forgetPins(notAmong: listed.map(\.key))
        let tokens = Set(listed.map { Self.token($0.key) })
        flat.removeAll { !tokens.contains($0) }
        visited.removeAll { !tokens.contains($0) }
        let names = Set(listed.map(\.project))
        projects = projects.reduce(into: [:]) { kept, entry in
            guard names.contains(entry.key) else { return }
            let remaining = entry.value.filter(tokens.contains)
            if !remaining.isEmpty { kept[entry.key] = remaining }
        }
    }

    /// What a group is named by among the collapsed: the group of pinned
    /// sessions apart from a project that happens to be called "Pinned".
    static func groupToken(project: String, pinned: Bool) -> String {
        pinned ? "pinned" : "project:\(project)"
    }

    /// What a state section is named by among the collapsed.
    static func groupToken(section: WorkspaceStateSection) -> String {
        "state:\(section.rawValue)"
    }

    /// What a listed group is named by among the collapsed. A project's
    /// groups in every state section share one: collapsing it in one
    /// collapses it in all, so a session changing state never opens it.
    static func groupToken(_ group: WorkspaceGroup) -> String {
        if group.isFlat { return "flat" }
        if let branch = group.branch {
            return "project-branch:\(group.project.utf8.count):\(group.project):\(branch)"
        }
        if group.isNestedState, let section = group.stateSection {
            return "project-state:\(group.project.utf8.count):\(group.project):\(section.rawValue)"
        }
        if group.isSectionHeading, let section = group.stateSection { return groupToken(section: section) }
        return groupToken(project: group.project, pinned: group.pinned)
    }

    func isCollapsed(project: String, pinned: Bool) -> Bool {
        collapsed.contains(Self.groupToken(project: project, pinned: pinned))
    }

    func isCollapsed(_ section: WorkspaceStateSection) -> Bool {
        collapsed.contains(Self.groupToken(section: section))
    }

    /// Collapses a group, or expands it.
    mutating func setCollapsed(project: String, pinned: Bool, _ wanted: Bool) {
        setCollapsed(token: Self.groupToken(project: project, pinned: pinned), wanted)
    }

    /// Collapses a state section to its heading, or expands it.
    mutating func setCollapsed(_ section: WorkspaceStateSection, _ wanted: Bool) {
        setCollapsed(token: Self.groupToken(section: section), wanted)
    }

    mutating func setCollapsed(_ group: WorkspaceGroup, _ wanted: Bool) {
        setCollapsed(token: Self.groupToken(group), wanted)
    }

    private mutating func setCollapsed(token: String, _ wanted: Bool) {
        if wanted { collapsed.insert(token) } else { collapsed.remove(token) }
    }

    /// The pinned rows in the order they were pinned or dragged into.
    func arrangedPins(_ rows: [WorkspaceRow]) -> [WorkspaceRow] {
        let rank = Dictionary(pinned.enumerated().map { ($1, $0) }, uniquingKeysWith: { first, _ in first })
        return rows.enumerated().sorted { lhs, rhs in
            let (a, b) = (rank[Self.token(lhs.element.key)] ?? Int.max, rank[Self.token(rhs.element.key)] ?? Int.max)
            return a != b ? a < b : lhs.offset < rhs.offset
        }.map(\.element)
    }

    static func load(from defaults: UserDefaults = .standard) -> WorkspaceOrder {
        WorkspaceOrder(
            projects: defaults.dictionary(forKey: defaultsKey) as? [String: [String]] ?? [:],
            pinned: defaults.stringArray(forKey: pinnedDefaultsKey) ?? [],
            flat: defaults.stringArray(forKey: flatDefaultsKey) ?? [],
            visited: defaults.stringArray(forKey: visitedDefaultsKey) ?? [],
            collapsed: Set(defaults.stringArray(forKey: collapsedDefaultsKey) ?? [])
        )
    }

    func save(to defaults: UserDefaults = .standard) {
        defaults.set(projects, forKey: Self.defaultsKey)
        defaults.set(pinned, forKey: Self.pinnedDefaultsKey)
        defaults.set(flat, forKey: Self.flatDefaultsKey)
        defaults.set(visited, forKey: Self.visitedDefaultsKey)
        defaults.set(collapsed.sorted(), forKey: Self.collapsedDefaultsKey)
    }
}
