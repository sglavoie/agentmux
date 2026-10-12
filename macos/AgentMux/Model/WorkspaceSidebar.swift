import Foundation

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

    var embeddedRuntimeSessionID: String? {
        if case let .embedded(id) = target { return id }
        return nil
    }
}

/// The sessions of one project.
struct WorkspaceGroup: Equatable {
    let project: String
    let rows: [WorkspaceRow]

    /// How many of the project's sessions wait on a human.
    var attention: Int { rows.filter(\.attention).count }
}

/// The order the operator dragged the sessions of each project into.
///
/// It names sessions, never positions: a session that is not named, one
/// launched since the drag, follows the named ones in the order
/// `WorkspaceSidebar` gives it. A project nobody dragged in is not here.
struct WorkspaceOrder: Equatable {
    static let defaultsKey = "amxWorkspaceSessionOrder"

    /// The sessions of each project that was dragged in, first to last.
    private(set) var projects: [String: [String]] = [:]

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

    static func load(from defaults: UserDefaults = .standard) -> WorkspaceOrder {
        WorkspaceOrder(projects: defaults.dictionary(forKey: defaultsKey) as? [String: [String]] ?? [:])
    }

    func save(to defaults: UserDefaults = .standard) {
        defaults.set(projects, forKey: Self.defaultsKey)
    }
}

/// The sidebar as data, built from a snapshot so it can be tested without
/// AppKit: sessions grouped by project, each labeled with its profile.
///
/// The order never follows attention. Projects sort by name and rows by
/// profile, harness, and identity, unless the operator dragged a project's
/// rows into an order of their own (`WorkspaceOrder`). Either way a session
/// keeps its place while its state changes; the glyph and the group's count
/// are what draw the eye.
enum WorkspaceSidebar {
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
        var byProject: [String: [(CompanionSession, WorkspaceRow)]] = [:]
        for session in sessions {
            let row = row(session, snapshotSocket: snapshotSocket, now: now, glyphs: glyphs)
            byProject[row.project, default: []].append((session, row))
        }
        return byProject.keys.sorted(by: nameOrder).map { project in
            let members = byProject[project] ?? []
            let rows = members.sorted { rowOrder($0, $1) }.map(\.1)
            return WorkspaceGroup(project: project, rows: order.arranged(rows, in: project))
        }
    }

    static func row(_ session: CompanionSession, snapshotSocket: String, now: Date, glyphs: Glyphs) -> WorkspaceRow {
        let key = AttentionCycler.key(session, snapshotSocket: snapshotSocket)
        let embedded = session.embeddedRuntimeSessionID
        let label = !session.profile.isEmpty ? session.profile : (!session.harness.isEmpty ? session.harness : unlabeled)

        var detail: [String] = []
        if !session.harness.isEmpty, session.harness != label { detail.append(session.harness) }
        if !session.branch.isEmpty { detail.append("\(session.branch)\(session.dirty ? "*" : "")") }
        if let changed = session.changedAt { detail.append(MenuBuilder.age(since: changed, now: now)) }
        if session.attention, !session.reason.isEmpty { detail.append(session.reason) }
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
            harnessIcon: embedded == nil ? "" : session.harnessIcon
        )
    }

    private static func nameOrder(_ lhs: String, _ rhs: String) -> Bool {
        let folded = lhs.localizedCaseInsensitiveCompare(rhs)
        return folded == .orderedSame ? lhs < rhs : folded == .orderedAscending
    }

    private static func rowOrder(_ lhs: (CompanionSession, WorkspaceRow), _ rhs: (CompanionSession, WorkspaceRow)) -> Bool {
        if lhs.1.label != rhs.1.label { return nameOrder(lhs.1.label, rhs.1.label) }
        if lhs.0.harness != rhs.0.harness { return lhs.0.harness < rhs.0.harness }
        let (a, b) = (lhs.1.key, rhs.1.key)
        if a.socket != b.socket { return a.socket < b.socket }
        if a.windowID != b.windowID { return a.windowID < b.windowID }
        return a.runtimeSessionID < b.runtimeSessionID
    }

    /// What the sidebar says about a feed that is not delivering snapshots;
    /// nil while it is.
    static func feedNotice(_ feed: FeedState) -> String? {
        switch feed {
        case .live: return nil
        case .connecting: return "Daemon: connecting…"
        case .notRunning: return DaemonStatusLine.title(for: .notRunning)
        case .daemonTooOld: return "daemon too old — run `amx daemon restart`"
        case let .reconnecting(reason): return "Daemon: reconnecting · \(reason)"
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

    /// Puts the listed rows in the order they were dragged into. It lists
    /// nothing new, so it works on held rows too.
    mutating func arrange(_ order: WorkspaceOrder) {
        groups = groups.map { WorkspaceGroup(project: $0.project, rows: order.arranged($0.rows, in: $0.project)) }
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
