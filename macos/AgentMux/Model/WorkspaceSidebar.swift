import Foundation
import CryptoKit

/// Where focusing a session goes.
enum WorkspaceTarget: Equatable {
    /// A terminal embedded in the workspace, attached to this runtime session.
    case embedded(runtimeSessionID: String)
    /// A Kitty window, brought forward in Kitty itself. An empty socket names
    /// the snapshot's own, as it does on a menu row.
    case kitty(socket: String, windowID: Int)
}

/// The color a session's line is drawn on, as the daemon publishes it.
struct WorkspaceTabColor: Equatable {
    let red: Double
    let green: Double
    let blue: Double

    /// Reads "#rrggbb"; nil for anything else, which is no color.
    init?(hex: String) {
        let digits = hex.dropFirst()
        guard hex.hasPrefix("#"), digits.utf8.count == 6, let value = UInt32(digits, radix: 16) else { return nil }
        red = Double((value >> 16) & 0xff) / 255
        green = Double((value >> 8) & 0xff) / 255
        blue = Double(value & 0xff) / 255
    }
}

/// One session in the workspace sidebar.
struct WorkspaceRow: Equatable {
    let key: AttentionCycler.Key
    let target: WorkspaceTarget
    /// The project the row is listed under.
    let project: String
    /// What tells the row apart inside its project: the profile it runs as.
    let label: String
    /// `claude · 4m · permission`.
    let detail: String
    /// The state glyph the tab title and the menu use; empty when idle.
    let glyph: String
    let attention: Bool
    let working: Bool
    let muted: Bool
    let toolTip: String
    /// What the line is drawn on while it is not the selected one: the color
    /// picked for the session, else its profile's. Only an embedded session has one; a Kitty
    /// session's color is on its tab, in Kitty.
    var tabColor: WorkspaceTabColor?
    /// The name the operator gave the session, shown on a line of its own
    /// above the label; empty for none, and the row then looks as it always
    /// did. Only an embedded session has one.
    var name = ""
    /// The session's harness icon, shown ahead of the label: its harness's
    /// own until the icon picker replaces or clears it. Empty for none, and
    /// only an embedded session has one; a Kitty session's is on its tab.
    var harnessIcon = ""
    /// The session's harness, which orders rows of one profile.
    var harness = ""
    /// Whether the session is pinned (the ★ in its title, which the dashboard
    /// reads too): it is then listed under `WorkspaceSidebar.pinnedSection`,
    /// above every project, instead of under its own, unless promoted for
    /// attention while that group is collapsed.
    var pinned = false
    /// Temporarily listed below pinned sessions and above projects until acknowledged.
    var needsAttention = false
    /// The conversation the session's agent is having, once the daemon knows
    /// it: what Past Sessions is opened on if the agent stops.
    var conversation: ConversationIdentity?
    /// A stable discriminator when several live rows have the same name.
    var qualifier = ""
    var branch = ""
    /// The session's icon state (`stop`, `bolt`, …); empty when idle. What
    /// the line's background under a project says.
    var iconState = ""

    var sessionLabel: String { qualified(label) }
    var namedLabel: String { name.isEmpty ? "" : qualified(name) }
    private func qualified(_ value: String) -> String {
        qualifier.isEmpty ? value : "\(value) · \(qualifier)"
    }

    /// What the session is called in titles, alerts, and the inspector:
    /// its name when it was given one, else its project and profile.
    var displayName: String {
        name.isEmpty ? "\(project) — \(sessionLabel)" : namedLabel
    }

    var embeddedRuntimeSessionID: String? {
        if case let .embedded(id) = target { return id }
        return nil
    }
}

/// The sessions of one project, or the pinned ones of every project.
struct WorkspaceGroup: Equatable {
    /// The project's name; `WorkspaceSidebar.pinnedSection` for the pinned.
    let project: String
    let rows: [WorkspaceRow]
    /// True for the group of pinned sessions, whose rows keep the project
    /// they belong to.
    var pinned = false
    /// True when the operator collapsed it: its header is listed, and still
    /// counts its waiting sessions, but its rows are not.
    var collapsed = false
    /// The temporary attention section is always visible and cannot collapse.
    var needsAttention = false

    /// How many of the project's sessions wait on a human.
    var attention: Int { rows.filter(\.attention).count }
}

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

    /// The sessions of each project that was dragged in, first to last.
    private(set) var projects: [String: [String]] = [:]
    /// The pinned sessions, first to last: in the order they were pinned
    /// here, until they are dragged. Only their order: a session pinned
    /// elsewhere follows these, and one named here that is no longer pinned
    /// is not listed as pinned.
    private(set) var pinned: [String] = []
    /// The groups the operator collapsed (`groupToken`). A project with no
    /// session listed stays collapsed: projects are few, and one launched
    /// into again comes back the way it was left.
    private(set) var collapsed: Set<String> = []

    /// What a session is named by here: its runtime session, or its Kitty
    /// socket and window.
    static func token(_ key: AttentionCycler.Key) -> String {
        key.runtimeSessionID.isEmpty ? "kitty:\(key.windowID):\(key.socket)" : key.runtimeSessionID
    }

    /// Records the order of a project's sessions, replacing the one it had.
    mutating func set(_ keys: [AttentionCycler.Key], for project: String) {
        projects[project] = keys.map(Self.token)
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

    func isCollapsed(project: String, pinned: Bool) -> Bool {
        collapsed.contains(Self.groupToken(project: project, pinned: pinned))
    }

    /// Collapses a group, or expands it.
    mutating func setCollapsed(project: String, pinned: Bool, _ wanted: Bool) {
        let token = Self.groupToken(project: project, pinned: pinned)
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
            collapsed: Set(defaults.stringArray(forKey: collapsedDefaultsKey) ?? [])
        )
    }

    func save(to defaults: UserDefaults = .standard) {
        defaults.set(projects, forKey: Self.defaultsKey)
        defaults.set(pinned, forKey: Self.pinnedDefaultsKey)
        defaults.set(collapsed.sorted(), forKey: Self.collapsedDefaultsKey)
    }
}

/// The sidebar as data, built from a snapshot so it can be tested without
/// AppKit: sessions grouped by project, each labeled with its profile.
///
/// Pinned sessions lead the list, waiting ones included while the group is
/// open. Other sessions needing attention temporarily follow them, above every
/// project. Their underlying order is preserved: projects by name and rows
/// by profile, harness, and identity, or the operator's dragged order.
/// Acknowledgment restores that position without changing saved preferences.
enum WorkspaceSidebar {
    static let attentionSection = "Needs attention"
    /// The heading the pinned sessions are listed under.
    static let pinnedSection = "Pinned"
    /// What a session with no profile and no harness is called.
    static let unlabeled = "session"
    /// Marks a row whose terminal is a Kitty window.
    static let kittyBadge = "Kitty"

    static func groups(
        _ sessions: [CompanionSession],
        snapshotSocket: String = "",
        now: Date = Date(),
        glyphs: Glyphs = .bundled,
        order: WorkspaceOrder = WorkspaceOrder()
    ) -> [WorkspaceGroup] {
        groups(rows(sessions, snapshotSocket: snapshotSocket, now: now, glyphs: glyphs), order: order)
    }

    /// Use the complete snapshot, including collapsed and non-waiting sessions,
    /// so every surface gives an agent the same name. Ordering and age never
    /// participate in its identity. A branch distinguishes different worktrees;
    /// a short runtime identity distinguishes agents sharing the same branch.
    static func rows(_ sessions: [CompanionSession], snapshotSocket: String = "", now: Date = Date(),
                     glyphs: Glyphs = .bundled) -> [WorkspaceRow] {
        var rows = sessions.map { row($0, snapshotSocket: snapshotSocket, now: now, glyphs: glyphs) }
        // Kitty's identity contains a socket path. Hash that tuple so a long
        // common path cannot hide the distinguishing window number in a row.
        let tokens = rows.map { row -> String in
            if !row.key.runtimeSessionID.isEmpty { return row.key.runtimeSessionID }
            return SHA256.hash(data: Data(WorkspaceOrder.token(row.key).utf8)).map { String(format: "%02x", $0) }.joined()
        }
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

    /// Rows as the sidebar groups them: pinned, attention, then projects.
    /// With promotion disabled this returns their saved arrangement, which
    /// dragging uses to preserve the places of temporarily promoted siblings.
    /// It needs no snapshot, so held rows can be grouped again.
    static func groups(_ rows: [WorkspaceRow], order: WorkspaceOrder, prioritizeAttention: Bool = true) -> [WorkspaceGroup] {
        var pinned: [WorkspaceRow] = []
        var byProject: [String: [WorkspaceRow]] = [:]
        for row in rows.sorted(by: rowOrder) {
            if row.pinned {
                pinned.append(row)
            } else {
                byProject[row.project, default: []].append(row)
            }
        }
        let projects = byProject.keys.sorted(by: projectOrder).map { project in
            WorkspaceGroup(project: project, rows: order.arranged(byProject[project] ?? [], in: project),
                           collapsed: order.isCollapsed(project: project, pinned: false))
        }
        let base = (pinned.isEmpty ? [] : [WorkspaceGroup(
            project: pinnedSection, rows: order.arrangedPins(pinned), pinned: true,
            collapsed: order.isCollapsed(project: pinnedSection, pinned: true)
        )]) + projects
        guard prioritizeAttention else { return base }
        // An open Pinned group keeps its waiting rows: it already leads.
        func promoted(_ row: WorkspaceRow, in group: WorkspaceGroup) -> Bool {
            row.needsAttention && !(group.pinned && !group.collapsed)
        }
        let waiting = base.flatMap { group in group.rows.filter { promoted($0, in: group) } }
        guard !waiting.isEmpty else { return base }
        let remaining = base.compactMap { group -> WorkspaceGroup? in
            let rows = group.rows.filter { !promoted($0, in: group) }
            guard !rows.isEmpty else { return nil }
            return WorkspaceGroup(project: group.project, rows: rows, pinned: group.pinned, collapsed: group.collapsed)
        }
        let attention = WorkspaceGroup(project: attentionSection, rows: waiting, needsAttention: true)
        let pins = remaining.prefix { $0.pinned }
        return pins + [attention] + remaining.dropFirst(pins.count)
    }

    /// The sessions in the order `groups` lists them, which is the order the
    /// next-attention shortcut steps through the ones that wait.
    static func ordered(
        _ sessions: [CompanionSession],
        snapshotSocket: String = "",
        order: WorkspaceOrder = WorkspaceOrder()
    ) -> [CompanionSession] {
        let byKey = Dictionary(
            sessions.map { (AttentionCycler.key($0, snapshotSocket: snapshotSocket), $0) },
            uniquingKeysWith: { first, _ in first }
        )
        return groups(sessions, snapshotSocket: snapshotSocket, order: order).flatMap(\.rows).compactMap { byKey[$0.key] }
    }

    static func row(_ session: CompanionSession, snapshotSocket: String, now: Date, glyphs: Glyphs) -> WorkspaceRow {
        let key = AttentionCycler.key(session, snapshotSocket: snapshotSocket)
        let embedded = session.embeddedRuntimeSessionID
        let label = !session.profile.isEmpty ? session.profile : (!session.harness.isEmpty ? session.harness : unlabeled)

        var detail: [String] = []
        if !session.harness.isEmpty, session.harness != label { detail.append(session.harness) }
        if !session.branch.isEmpty { detail.append("\(session.branch)\(session.dirty ? "*" : "")") }
        if let changed = session.changedAt { detail.append(AgeFormatter.string(since: changed, now: now, style: .compact)) }
        // State remains readable without interpreting an emoji or profile color.
        detail.insert(session.reason.isEmpty ? WorkspaceInspector.word(for: session.iconState) : WorkspaceWording.status(session.reason), at: 0)
        if embedded == nil { detail.append(kittyBadge) }

        var tip = [MenuBuilder.place(of: session)]
        if !session.baseTitle.isEmpty { tip.append(session.baseTitle) }
        if embedded == nil, !session.kittySocket.isEmpty { tip.append(session.kittySocket) }

        return WorkspaceRow(
            key: key,
            target: embedded.map { .embedded(runtimeSessionID: $0) }
                ?? .kitty(socket: session.kittySocket, windowID: session.windowID),
            project: MenuBuilder.name(of: session),
            label: label,
            detail: detail.joined(separator: " · "),
            glyph: glyphs.glyph(for: session.iconState),
            attention: session.attention,
            working: session.isWorking,
            muted: session.muted,
            toolTip: tip.joined(separator: " · "),
            tabColor: embedded == nil ? nil : WorkspaceTabColor(hex: session.tabColor),
            name: embedded == nil ? "" : session.name,
            harnessIcon: embedded == nil ? "" : session.harnessIcon,
            harness: session.harness,
            pinned: session.pinned,
            needsAttention: session.needsAttention,
            conversation: session.conversation.flatMap { $0.isUsable ? $0 : nil },
            branch: session.branch,
            iconState: session.iconState
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
        let (a, b) = (lhs.key, rhs.key)
        if a.socket != b.socket { return a.socket < b.socket }
        if a.windowID != b.windowID { return a.windowID < b.windowID }
        return a.runtimeSessionID < b.runtimeSessionID
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

/// What the workspace shows and which session is selected in it.
///
/// The rows are replaced only by a live snapshot. While the feed is down —
/// the daemon restarting, the socket half-open after sleep — the last rows
/// stay listed, marked not current, and the selection stays where it is: the
/// sessions themselves live in tmux and Kitty and did not go anywhere. The
/// first snapshot after the reconnect then decides what is still there.
struct WorkspaceState: Equatable {
    private(set) var groups: [WorkspaceGroup] = []
    private(set) var selection: AttentionCycler.Key?
    /// False while `groups` is what the last live snapshot listed rather
    /// than what the daemon says now.
    private(set) var current = false

    var rows: [WorkspaceRow] { groups.flatMap(\.rows) }

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
    mutating func arrange(_ order: WorkspaceOrder) {
        groups = WorkspaceSidebar.groups(rows, order: order)
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
