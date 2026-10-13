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
    /// The session's harness, which orders rows of one profile.
    var harness = ""
    /// Whether the operator pinned the session: it is then listed under
    /// `WorkspaceSidebar.pinnedSection`, above every project, instead of
    /// under its own.
    var pinned = false
    /// The conversation the session's agent is having, once the daemon knows
    /// it: what Past Sessions is opened on if the agent stops.
    var conversation: ConversationIdentity?

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

    /// How many of the project's sessions wait on a human.
    var attention: Int { rows.filter(\.attention).count }
}

/// Where the operator put sessions: the order they dragged the sessions of
/// each project into, and the sessions they pinned above every project.
///
/// It names sessions, never positions: a session that is not named, one
/// launched since the drag, follows the named ones in the order
/// `WorkspaceSidebar` gives it. A project nobody dragged in is not here.
struct WorkspaceOrder: Equatable {
    static let defaultsKey = "amxWorkspaceSessionOrder"
    static let pinnedDefaultsKey = "amxWorkspacePinnedSessions"

    /// The sessions of each project that was dragged in, first to last.
    private(set) var projects: [String: [String]] = [:]
    /// The pinned sessions, first to last: in the order they were pinned,
    /// until they are dragged.
    private(set) var pinned: [String] = []

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

    func isPinned(_ key: AttentionCycler.Key) -> Bool {
        pinned.contains(Self.token(key))
    }

    /// Pins a session below the ones already pinned, or unpins it.
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

    /// Forgets the pins of sessions that are not among `listed`: a session
    /// that ended is never listed again, and its pin would be kept forever.
    mutating func forgetPins(notAmong listed: [AttentionCycler.Key]) {
        let tokens = Set(listed.map(Self.token))
        pinned.removeAll { !tokens.contains($0) }
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
            pinned: defaults.stringArray(forKey: pinnedDefaultsKey) ?? []
        )
    }

    func save(to defaults: UserDefaults = .standard) {
        defaults.set(projects, forKey: Self.defaultsKey)
        defaults.set(pinned, forKey: Self.pinnedDefaultsKey)
    }
}

/// The sidebar as data, built from a snapshot so it can be tested without
/// AppKit: sessions grouped by project, each labeled with its profile.
///
/// The order never follows attention; the next-attention shortcut follows
/// it (`AttentionCycler`). Projects sort by name, past any icon
/// the name leads with, and rows by
/// profile, harness, and identity, unless the operator dragged a project's
/// rows into an order of their own (`WorkspaceOrder`). A session the
/// operator pinned leaves its project for a group of the pinned, listed
/// first. Either way a session
/// keeps its place while its state changes; the glyph and the group's count
/// are what draw the eye.
enum WorkspaceSidebar {
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
        groups(sessions.map { row($0, snapshotSocket: snapshotSocket, now: now, glyphs: glyphs) }, order: order)
    }

    /// Rows as the sidebar groups them: the pinned ones first, under a
    /// heading of their own, then each project's. It needs no snapshot, so
    /// held rows can be grouped again.
    static func groups(_ rows: [WorkspaceRow], order: WorkspaceOrder) -> [WorkspaceGroup] {
        var pinned: [WorkspaceRow] = []
        var byProject: [String: [WorkspaceRow]] = [:]
        for var row in rows.sorted(by: rowOrder) {
            row.pinned = order.isPinned(row.key)
            if row.pinned {
                pinned.append(row)
            } else {
                byProject[row.project, default: []].append(row)
            }
        }
        let projects = byProject.keys.sorted(by: projectOrder).map { project in
            WorkspaceGroup(project: project, rows: order.arranged(byProject[project] ?? [], in: project))
        }
        guard !pinned.isEmpty else { return projects }
        return [WorkspaceGroup(project: pinnedSection, rows: order.arrangedPins(pinned), pinned: true)] + projects
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
            harnessIcon: embedded == nil ? "" : session.harnessIcon,
            harness: session.harness,
            conversation: session.conversation.flatMap { $0.isUsable ? $0 : nil }
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
