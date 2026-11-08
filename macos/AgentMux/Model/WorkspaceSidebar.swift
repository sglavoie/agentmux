import Foundation

/// The sidebar as data, built from a snapshot so it can be tested without
/// AppKit: session rows organized by the selected hierarchy, each labeled
/// with its profile and current state.
///
/// State-first is the default; project-first keeps unpinned rows inside their
/// project, optionally nesting states or branches. Compact omits project headings.
/// Pinned sessions lead every layout. In state-first mode, waiting pins stay
/// there while the group is open. Other sessions needing attention follow them, above every
/// state section. The sections follow in `WorkspaceStateSection` order, each
/// heading the projects with a session in that state, so a project is listed
/// once in every state its sessions are in. Their underlying order is
/// preserved: projects by name and rows by profile, harness, and identity, or
/// the operator's dragged order. Acknowledgment restores that position
/// without changing saved preferences.
enum WorkspaceSidebar {
    static let attentionSection = "Needs attention"
    /// The heading the pinned sessions are listed under.
    static let pinnedSection = "Pinned"
    /// What a session with no profile and no harness is called.
    static let unlabeled = "session"

    static func groups(
        _ sessions: [CompanionSession],
        now: Date = Date(),
        glyphs: Glyphs = .bundled,
        order: WorkspaceOrder = WorkspaceOrder(),
        grouping: WorkspaceSidebarGrouping = .stateThenProject,
        sorting: WorkspaceSidebarSort = .manual
    ) -> [WorkspaceGroup] {
        groups(rows(sessions, now: now, glyphs: glyphs), order: order, grouping: grouping, sorting: sorting)
    }

    /// Use the complete snapshot, including collapsed and non-waiting sessions,
    /// so every surface gives an agent the same name. Ordering and age never
    /// participate in its identity. A branch distinguishes different worktrees;
    /// a short runtime identity distinguishes agents sharing the same branch.
    static func rows(_ sessions: [CompanionSession], now: Date = Date(),
                     glyphs: Glyphs = .bundled) -> [WorkspaceRow] {
        var rows = sessions.filter { $0.embeddedRuntimeSessionID != nil }
            .map { row($0, now: now, glyphs: glyphs) }
        let tokens = rows.map { $0.key.runtimeSessionID }
        let duplicates = Dictionary(grouping: rows.indices, by: { rows[$0].displayName })
        for indices in duplicates.values where indices.count > 1 {
            for index in indices {
                let branch = rows[index].branch
                if !branch.isEmpty, !branch.hasPrefix("#"), indices.filter({ rows[$0].branch == branch }).count == 1 {
                    rows[index].qualifier = branch
                    continue
                }
                let token = tokens[index]
                let others = indices.filter { $0 != index }.map { tokens[$0] }
                var length = min(6, token.count)
                while length < token.count && others.contains(where: { $0.hasSuffix(token.suffix(length)) }) { length += 1 }
                rows[index].qualifier = "#" + token.suffix(length)
            }
        }
        return rows
    }

    /// Rows in the selected hierarchy. State-first lists pinned, attention,
    /// then each state's projects. With promotion disabled this
    /// returns their saved arrangement, which dragging uses to preserve the
    /// places of temporarily promoted siblings. It needs no snapshot, so
    /// held rows can be grouped again.
    static func groups(_ rows: [WorkspaceRow], order: WorkspaceOrder, prioritizeAttention: Bool = true,
                       grouping: WorkspaceSidebarGrouping = .stateThenProject,
                       sorting: WorkspaceSidebarSort = .manual) -> [WorkspaceGroup] {
        let groups = manuallyGrouped(rows, order: order, prioritizeAttention: prioritizeAttention, grouping: grouping).map { group in
            var annotated = group
            if !group.isSectionHeading && !group.pinned && !group.needsAttention && !group.isFlat {
                let members = rows.filter { $0.project == group.project }
                let remaining = members.filter { !$0.review.accounted }.count
                if members.contains(where: { $0.review.reviewed }) {
                    annotated.reviewLabel = remaining == 0 ? "✓ Accounted for" : "\(remaining) needs review"
                }
                annotated.projectNote = members.first?.review.projectNote ?? ""
            }
            return annotated
        }
        guard sorting == .recentlyVisited else { return groups }
        return groups.map { group in
            var sorted = group
            sorted.rows = order.recentlyVisited(group.rows)
            return sorted
        }
    }

    private static func manuallyGrouped(_ rows: [WorkspaceRow], order: WorkspaceOrder, prioritizeAttention: Bool,
                                        grouping: WorkspaceSidebarGrouping) -> [WorkspaceGroup] {
        let pins = order.arrangedPins(rows.filter(\.pinned).sorted(by: rowOrder))
        let unpinned = rows.filter { !$0.pinned }
        if grouping != .stateThenProject {
            let compact = grouping == .compact
            let leading = pins.isEmpty ? [] : [WorkspaceGroup(
                project: pinnedSection, rows: pins, pinned: true,
                collapsed: !compact && order.isCollapsed(project: pinnedSection, pinned: true), isFlat: compact
            )]
            switch grouping {
            case .projectThenState:
                return leading + projectGroups(unpinned, order: order)
            case .project, .projectThenBranch:
                return leading + stableProjectGroups(unpinned, order: order, branches: grouping == .projectThenBranch)
            case .compact:
                let arranged = Set(unpinned.map(\.project)).sorted(by: projectOrder).flatMap {
                    projectRows(unpinned, in: $0, order: order)
                }
                return leading + (arranged.isEmpty ? [] : [WorkspaceGroup(
                    project: "Sessions", rows: order.arrangedFlat(arranged), isFlat: true
                )])
            case .stateThenProject: break
            }
        }
        let pinnedCollapsed = order.isCollapsed(project: pinnedSection, pinned: true)
        // An open Pinned group keeps its waiting rows: it already leads.
        func promoted(_ row: WorkspaceRow) -> Bool {
            prioritizeAttention && row.needsAttention && !(row.pinned && !pinnedCollapsed)
        }
        var waiting = pins.filter(promoted)
        let kept = pins.filter { !promoted($0) }
        var listed = kept.isEmpty ? [] : [WorkspaceGroup(
            project: pinnedSection, rows: kept, pinned: true, collapsed: pinnedCollapsed
        )]
        let projects = Set(rows.filter { !$0.pinned }.map(\.project)).sorted(by: projectOrder)
        let arranged = projects.map { projectRows(rows, in: $0, order: order) }
        var sections: [WorkspaceGroup] = []
        for section in WorkspaceStateSection.allCases {
            var groups: [WorkspaceGroup] = []
            for (project, rows) in zip(projects, arranged) {
                let inSection = rows.filter { WorkspaceStateSection(iconState: $0.iconState) == section }
                waiting += inSection.filter(promoted)
                let kept = inSection.filter { !promoted($0) }
                guard !kept.isEmpty else { continue }
                groups.append(WorkspaceGroup(project: project, rows: kept,
                                             collapsed: order.isCollapsed(project: project, pinned: false),
                                             stateSection: section))
            }
            guard !groups.isEmpty else { continue }
            sections.append(WorkspaceGroup(
                project: section.rawValue, rows: [], collapsed: order.isCollapsed(section),
                stateSection: section, isSectionHeading: true, sessions: groups.reduce(0) { $0 + $1.rows.count }
            ))
            sections += groups
        }
        if !waiting.isEmpty {
            listed.append(WorkspaceGroup(project: attentionSection, rows: waiting, needsAttention: true))
        }
        return listed + sections
    }

    /// Unpinned sessions remain in their project, including attention rows.
    private static func projectGroups(_ rows: [WorkspaceRow], order: WorkspaceOrder) -> [WorkspaceGroup] {
        Set(rows.map(\.project)).sorted(by: projectOrder).flatMap { project in
            let arranged = projectRows(rows, in: project, order: order)
            let heading = WorkspaceGroup(project: project, rows: [],
                                         collapsed: order.isCollapsed(project: project, pinned: false),
                                         sessions: arranged.count, isProjectHeading: true)
            let states = WorkspaceStateSection.allCases.compactMap { section -> WorkspaceGroup? in
                let members = arranged.filter { WorkspaceStateSection(iconState: $0.iconState) == section }
                guard !members.isEmpty else { return nil }
                var group = WorkspaceGroup(project: project, rows: members, stateSection: section,
                                           isSectionHeading: true, sessions: members.count, isNestedState: true)
                group.collapsed = order.collapsed.contains(WorkspaceOrder.groupToken(group))
                return group
            }
            return [heading] + states
        }
    }

    /// State and attention updates never move rows in these hierarchies.
    private static func stableProjectGroups(_ rows: [WorkspaceRow], order: WorkspaceOrder, branches: Bool) -> [WorkspaceGroup] {
        Set(rows.map(\.project)).sorted(by: projectOrder).flatMap { project in
            let arranged = projectRows(rows, in: project, order: order)
            let names = Set(arranged.map(\.branch)).sorted(by: nameOrder)
            let collapsed = order.isCollapsed(project: project, pinned: false)
            guard branches, names.count > 1 else {
                return [WorkspaceGroup(project: project, rows: arranged, collapsed: collapsed)]
            }
            let heading = WorkspaceGroup(project: project, rows: [], collapsed: collapsed,
                                         sessions: arranged.count, isProjectHeading: true,
                                         waitingCount: arranged.filter(\.attention).count)
            return [heading] + names.map { branch in
                var group = WorkspaceGroup(project: project, rows: arranged.filter { $0.branch == branch }, branch: branch)
                group.collapsed = order.collapsed.contains(WorkspaceOrder.groupToken(group))
                return group
            }
        }
    }

    /// A project's sessions in saved drag order, across all states.
    static func projectRows(_ rows: [WorkspaceRow], in project: String, order: WorkspaceOrder, includePinned: Bool = false) -> [WorkspaceRow] {
        order.arranged(rows.filter { (includePinned || !$0.pinned) && $0.project == project }.sorted(by: rowOrder), in: project)
    }

    /// The sessions in the order `groups` lists them, which is the order the
    /// next-attention shortcut steps through the ones that wait.
    static func ordered(
        _ sessions: [CompanionSession],
        order: WorkspaceOrder = WorkspaceOrder(),
        grouping: WorkspaceSidebarGrouping = .stateThenProject,
        sorting: WorkspaceSidebarSort = .manual
    ) -> [CompanionSession] {
        let byKey = Dictionary(
            sessions.map { (AttentionCycler.key($0), $0) },
            uniquingKeysWith: { first, _ in first }
        )
        return groups(sessions, order: order, grouping: grouping, sorting: sorting).flatMap(\.rows).compactMap { byKey[$0.key] }
    }

    static func row(_ session: CompanionSession, now: Date, glyphs: Glyphs) -> WorkspaceRow {
        let key = AttentionCycler.key(session)
        let embedded = session.embeddedRuntimeSessionID
        let label = !session.profile.isEmpty ? session.profile : (!session.harness.isEmpty ? session.harness : unlabeled)

        var detail: [String] = []
        if !session.harness.isEmpty, session.harness != label { detail.append(session.harness) }
        if !session.branch.isEmpty { detail.append("\(session.branch)\(session.dirty ? "*" : "")") }
        if let changed = session.changedAt { detail.append(AgeFormatter.string(since: changed, now: now, style: .compact)) }
        // State remains readable without interpreting an emoji or profile color.
        if session.review.reviewed && !session.review.accounted { detail.append("Needs review") }
        detail.insert(session.reason.isEmpty ? WorkspaceInspector.word(for: session.iconState) : WorkspaceWording.status(session.reason), at: 0)

        return WorkspaceRow(
            key: key,
            target: .embedded(runtimeSessionID: embedded ?? ""),
            project: MenuBuilder.name(of: session),
            label: label,
            detail: detail.joined(separator: " · "),
            glyph: glyphs.glyph(for: session.iconState),
            attention: session.attention,
            working: session.isWorking,
            muted: session.muted,
            toolTip: ([MenuBuilder.place(of: session), session.review.label, session.review.note, session.review.projectNote].filter { !$0.isEmpty }).joined(separator: "\n"),
            tabColor: WorkspaceTabColor(hex: session.tabColor),
            name: session.name,
            harnessIcon: session.harnessIcon,
            harness: session.harness,
            profile: session.profile,
            pinned: session.pinned,
            needsAttention: session.needsAttention,
            conversation: session.conversation.flatMap { $0.isUsable ? $0 : nil },
            branch: session.branch,
            iconState: session.iconState,
            review: session.review
        )
    }

    /// What a project sorts by: its name after the icon it leads with, as
    /// `session.ProjectSortKey` reads it for the dashboard. A name that is
    /// all symbols sorts as it is written.
    static func projectSortKey(_ project: String) -> String {
        let name = project.unicodeScalars.drop { !CharacterSet.alphanumerics.contains($0) }
        return name.isEmpty ? project : String(String.UnicodeScalarView(name))
    }

    private static func projectOrder(_ lhs: String, _ rhs: String) -> Bool {
        let (a, b) = (projectSortKey(lhs), projectSortKey(rhs))
        return a == b ? nameOrder(lhs, rhs) : nameOrder(a, b)
    }

    private static func nameOrder(_ lhs: String, _ rhs: String) -> Bool {
        let folded = lhs.localizedCaseInsensitiveCompare(rhs)
        return folded == .orderedSame ? lhs < rhs : folded == .orderedAscending
    }

    private static func rowOrder(_ lhs: WorkspaceRow, _ rhs: WorkspaceRow) -> Bool {
        if lhs.label != rhs.label { return nameOrder(lhs.label, rhs.label) }
        if lhs.harness != rhs.harness { return lhs.harness < rhs.harness }
        return lhs.key.runtimeSessionID < rhs.key.runtimeSessionID
    }

    /// What the sidebar says about a feed that is not delivering snapshots;
    /// nil while it is. A daemon too old for the app sends the operator to
    /// Restart Daemon, as the menu bar's notices do (`MenuBuilder.restartHint`).
    static func feedNotice(_ feed: FeedState) -> String? {
        switch feed {
        case .live: return nil
        case .connecting: return "Connecting to the daemon…"
        case .notRunning: return "Daemon not running"
        case .daemonTooOld: return "Daemon too old — \(MenuBuilder.restartHint)"
        case let .reconnecting(reason): return "Reconnecting… (\(reason))"
        }
    }

    /// What the operator can do from the workspace about a feed that is not
    /// delivering snapshots: the Start Daemon and Restart Daemon the menu
    /// bar offers for it. Neither starts or ends an agent.
    enum DaemonAction: Equatable {
        case start
        case restart

        var title: String {
            switch self {
            case .start: return "Start Daemon"
            case .restart: return "Restart Daemon"
            }
        }
    }

    /// The daemon action offered with the feed's notice; nil when there is
    /// nothing to do but wait for the daemon to answer. A daemon that takes
    /// the connection but stops answering, or keeps sending what the app
    /// cannot read, would be waited on for good: that is restarted.
    static func daemonAction(_ feed: FeedState) -> DaemonAction? {
        switch feed {
        case .notRunning: return .start
        case .daemonTooOld: return .restart
        case let .reconnecting(reason) where stuckReasons.contains(reason): return .restart
        case .live, .connecting, .reconnecting: return nil
        }
    }

    /// The reconnecting reasons waiting will not cure.
    static let stuckReasons: Set<String> = [DaemonPhrase.notAnswering, DaemonPhrase.unreadableSnapshot]

    /// What the pane says when it has nothing to show and the feed is not
    /// delivering snapshots, and what it can offer to do about the daemon;
    /// nil while the feed is live.
    static func feedExplanation(_ feed: FeedState) -> (title: String, message: String, action: DaemonAction?)? {
        guard let notice = feedNotice(feed) else { return nil }
        let action = daemonAction(feed)
        switch feed {
        case .live: return nil
        case .connecting:
            return (notice, "Sessions are listed once the daemon answers.", action)
        case .notRunning:
            return (notice, "Nothing is listed until the daemon runs. Starting it starts no agent.", action)
        case .daemonTooOld:
            return (notice, "This daemon cannot list sessions for the workspace. Restarting it ends no agent.", action)
        case .reconnecting:
            // Said only with nothing listed: the daemon has no sessions to
            // hold, so the list fills when it answers.
            if action == .restart {
                return ("Reconnecting…", "Waiting may not help: the daemon stopped answering, or sends what this app cannot read. Restarting it ends no agent.", action)
            }
            return ("Reconnecting…", "The daemon is reconnecting. Sessions are listed once it answers; any that are running keep running meanwhile.", action)
        }
    }
}
