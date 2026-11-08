import Foundation

/// One section of the sidebar as the overview line above the sessions
/// counts it. A click on it goes to the section's first session.
struct WorkspaceSectionCount: Equatable {
    enum Kind: Equatable {
        case attention
        case pinned
        case state(WorkspaceStateSection)
    }

    let title: String
    let kind: Kind
    let count: Int
    /// The session listed first in the section, collapsed or not.
    let first: AttentionCycler.Key
}

/// What the workspace shows and which session is selected in it.
///
/// The rows are replaced only by a live snapshot. While the feed is down —
/// the daemon restarting, the socket half-open after sleep — the last rows
/// stay listed, marked not current, and the selection stays where it is: the
/// sessions themselves live in tmux and did not go anywhere. The
/// first snapshot after the reconnect then decides what is still there.
struct WorkspaceState: Equatable {
    private(set) var groups: [WorkspaceGroup] = []
    private(set) var selection: AttentionCycler.Key?
    /// False while `groups` is what the last live snapshot listed rather
    /// than what the daemon says now.
    private(set) var current = false

    var rows: [WorkspaceRow] { groups.flatMap(\.rows) }

    /// Groups whose parent heading is expanded. A group's own collapsed
    /// flag hides its rows while keeping its heading visible.
    var listedGroups: [WorkspaceGroup] {
        let foldedProjects = Set(groups.filter { $0.isProjectHeading && $0.collapsed }.map(\.project))
        let folded = Set(groups.filter { $0.isSectionHeading && !$0.isNestedState && $0.collapsed }.compactMap(\.stateSection))
        return groups.filter {
            if $0.isNested { return !foldedProjects.contains($0.project) }
            return $0.isSectionHeading || $0.stateSection.map { !folded.contains($0) } ?? true
        }
    }

    /// The rows listed under a group: a project's, or those of every
    /// project in a state section for its heading.
    func rows(under group: WorkspaceGroup) -> [WorkspaceRow] {
        if group.isProjectHeading {
            return groups.filter { $0.isNested && $0.project == group.project }.flatMap(\.rows)
        }
        guard group.isSectionHeading && !group.isNestedState else { return group.rows }
        return groups.filter { !$0.isSectionHeading && $0.stateSection == group.stateSection }.flatMap(\.rows)
    }

    /// The sections the sidebar lists, in its order, each with how many
    /// sessions it holds: what the overview line above them counts.
    var sectionCounts: [WorkspaceSectionCount] {
        if !groups.contains(where: { $0.needsAttention || ($0.isSectionHeading && !$0.isNested) }) {
            let pins = groups.filter(\.pinned).flatMap(\.rows)
            let pinCounts = pins.first.map {
                [WorkspaceSectionCount(title: WorkspaceSidebar.pinnedSection, kind: .pinned, count: pins.count, first: $0.key)]
            } ?? []
            return pinCounts + WorkspaceStateSection.allCases.compactMap { section in
                let members = rows.filter { !$0.pinned && WorkspaceStateSection(iconState: $0.iconState) == section }
                guard let first = members.first else { return nil }
                return WorkspaceSectionCount(title: section.rawValue, kind: .state(section), count: members.count, first: first.key)
            }
        }
        return groups.compactMap { group in
            guard group.pinned || group.needsAttention || group.isSectionHeading,
                  let first = rows(under: group).first else { return nil }
            let kind: WorkspaceSectionCount.Kind = group.needsAttention ? .attention
                : group.pinned ? .pinned : .state(group.stateSection ?? .idle)
            return WorkspaceSectionCount(title: group.project, kind: kind,
                                         count: rows(under: group).count, first: first.key)
        }
    }

    func row(_ key: AttentionCycler.Key?) -> WorkspaceRow? {
        guard let key else { return nil }
        return rows.first { $0.key == key }
    }

    var selectedRow: WorkspaceRow? { row(selection) }

    /// The runtime sessions the listed rows embed.
    var embeddedRuntimeSessionIDs: Set<String> {
        Set(rows.compactMap(\.embeddedRuntimeSessionID))
    }

    /// Takes what the feed has now: the groups of a live snapshot, or nil
    /// when there is none. Returns true when the selected session is no
    /// longer listed, which only a live snapshot can decide.
    @discardableResult
    mutating func apply(_ live: [WorkspaceGroup]?) -> Bool {
        guard let live else {
            current = false
            return false
        }
        groups = live
        current = true
        guard selection != nil, selectedRow == nil else { return false }
        selection = nil
        return true
    }

    /// Puts the listed rows where they were dragged or pinned. It lists
    /// nothing new, so it works on held rows too.
    mutating func arrange(_ order: WorkspaceOrder, grouping: WorkspaceSidebarGrouping = .stateThenProject, sorting: WorkspaceSidebarSort = .manual) {
        groups = WorkspaceSidebar.groups(rows, order: order, grouping: grouping, sorting: sorting)
    }

    /// Selects a listed session, or nothing; false when `key` is not listed.
    @discardableResult
    mutating func select(_ key: AttentionCycler.Key?) -> Bool {
        guard let key else {
            selection = nil
            return true
        }
        guard row(key) != nil else { return false }
        selection = key
        return true
    }

    /// The session `offset` rows from the selected one in sidebar order,
    /// wrapping; the first (or last) row when nothing is selected.
    func neighbor(_ offset: Int) -> WorkspaceRow? {
        let rows = self.rows
        guard !rows.isEmpty else { return nil }
        guard let index = rows.firstIndex(where: { $0.key == selection }) else {
            return offset < 0 ? rows[rows.count - 1] : rows[0]
        }
        return rows[((index + offset) % rows.count + rows.count) % rows.count]
    }
}
