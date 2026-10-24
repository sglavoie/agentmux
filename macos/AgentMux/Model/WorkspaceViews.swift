import Foundation

/// `protocol.WorkspaceViewRequest`: a view the daemon forwards on behalf of a
/// surface the workspace embeds. It names a kind and a target, never a
/// command line.
struct WorkspaceViewRequest: Decodable, Equatable {
    var kind = ""
    var runtimeSessionID = ""
    /// The session was just launched: show it once the snapshot lists it.
    var awaited = false
    var directory = ""
    var path = ""
    var title = ""
    /// The operator asked for the session from outside the workspace
    /// (`focus_session`): it is shown even when the window is closed.
    var raise = false

    static let sessionKind = "session"

    init(kind: String = "", runtimeSessionID: String = "", awaited: Bool = false,
         directory: String = "", path: String = "", title: String = "", raise: Bool = false) {
        self.kind = kind
        self.runtimeSessionID = runtimeSessionID
        self.awaited = awaited
        self.directory = directory
        self.path = path
        self.title = title
        self.raise = raise
    }

    enum CodingKeys: String, CodingKey {
        case kind, directory, path, title, raise
        case awaited = "await"
        case runtimeSessionID = "runtime_session_id"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        kind = c.lenient(.kind, "")
        runtimeSessionID = c.lenient(.runtimeSessionID, "")
        awaited = c.lenient(.awaited, false)
        directory = c.lenient(.directory, "")
        path = c.lenient(.path, "")
        title = c.lenient(.title, "")
        raise = c.lenient(.raise, false)
    }
}

/// A terminal the workspace runs beside the agents: its Dashboard, its
/// Overview, a shell, editor, or pager one of those asked for, or Past
/// Sessions opened on the conversation of an agent that stopped.
///
/// A utility view is not a session. It has no `AttentionCycler.Key`, no
/// runtime session of its own, and no state glyph; nothing about it is read
/// from a snapshot, so it can never be counted as waiting or be the target of
/// the next-attention shortcut. A pager names the session whose scrollback it
/// reads and is still not that session, and so does a terminal opened beside
/// one: it is a shell where that session's agent works, listed under it.
///
/// The keybinding reference and the icon and color pickers are what Kitty
/// shows as overlays. Here each is a view that takes the pane until its
/// program ends, and then the pane goes back to where it was.
struct WorkspaceUtilityView: Equatable, Codable {
    enum Kind: String, Equatable, Codable {
        case dashboard, overview, shell, editor, pager, terminal, setup
        /// Past Sessions on one conversation, `harness:session_id`, or on
        /// every conversation when the target is empty.
        case history
        case keybindings
        case iconPicker = "icon-picker"
        case colorPicker = "color-picker"
    }

    let kind: Kind
    /// The directory, file, or runtime session the view is about; empty for
    /// the Dashboard and the Overview.
    let target: String
    /// The default sidebar label, restored when the operator clears the name.
    let title: String
    /// Tells apart the terminals opened beside one session, from 1, and the
    /// shells New Shell opened in one directory after the first, from 2; 0
    /// for every other view.
    var ordinal = 0
    /// Optional so layouts saved before terminal naming still decode.
    var name: String? = nil
    var historyQuery: String? = nil

    var canName: Bool { kind == .terminal || kind == .shell }
    var displayName: String { name.flatMap { $0.isEmpty ? nil : $0 } ?? title }

    /// What tells one view from another: its kind and what it is about.
    var id: String {
        if kind == .history, let historyQuery, !historyQuery.isEmpty { return "history:\(target)#query:\(historyQuery)" }
        if target.isEmpty { return kind.rawValue }
        return ordinal == 0 ? "\(kind.rawValue):\(target)" : "\(kind.rawValue):\(target)#\(ordinal)"
    }

    /// The runtime session a terminal was opened beside; nil for every other
    /// kind.
    var besideRuntimeSessionID: String? { kind == .terminal ? target : nil }

    /// The runtime session the view is about: the one a terminal is beside,
    /// a pager reads, or a picker edits. Nil for every other kind.
    var aboutRuntimeSessionID: String? {
        switch kind {
        case .terminal, .pager, .iconPicker, .colorPicker: return target
        case .dashboard, .overview, .shell, .editor, .history, .keybindings, .setup: return nil
        }
    }

    /// A view that stands in for a Kitty overlay: it is passed through, not
    /// somewhere the operator was, so going back never lands on one.
    var isOverlay: Bool { kind == .keybindings || kind == .iconPicker || kind == .colorPicker }

    /// The Dashboard and the Overview are always listed; the others exist
    /// only while their program runs.
    var isPermanent: Bool { kind == .dashboard || kind == .overview }

    /// Whether closing the view asks first while its program needs it: a
    /// shell, an editor, or a pager may hold work that closing would lose.
    /// The Dashboard and the Overview start again when shown, Past Sessions
    /// and the overlays hold nothing to lose, and an attachment is not a
    /// utility view at all: closing one only detaches.
    var asksBeforeClosing: Bool {
        switch kind {
        case .shell, .editor, .pager, .terminal, .setup: return true
        case .dashboard, .overview, .history, .keybindings, .iconPicker, .colorPicker: return false
        }
    }

    /// What `amx` runs in the view's terminal. amx decides the program: the
    /// workspace passes only the kind and the target.
    var arguments: [String] {
        switch kind {
        case .setup: return ["workspace-view", "setup"]
        case .dashboard: return ["workspace-view", "dashboard"]
        case .overview: return ["workspace-view", "overview"]
        case .shell: return ["workspace-view", "shell", target]
        case .editor: return ["workspace-view", "edit", target]
        case .pager: return ["workspace-view", "pager", target]
        case .terminal: return ["workspace-view", "session-shell", target]
        case .history:
            var args = ["workspace-view", "history"]
            if let historyQuery, !historyQuery.isEmpty { args += ["--query", historyQuery] }
            if !target.isEmpty { args += ["--", target] }
            return args
        case .keybindings: return ["workspace-view", "keybindings"]
        case .iconPicker: return ["workspace-view", "icon-picker", target]
        case .colorPicker: return ["workspace-view", "color-picker", target]
        }
    }

    var glyph: String {
        switch kind {
        case .setup: return "⚙"
        case .dashboard: return "▦"
        case .overview: return "▤"
        case .shell, .terminal: return "❯"
        case .editor: return "✎"
        case .pager: return "≡"
        case .history: return "↺"
        case .keybindings: return "⌨"
        case .iconPicker, .colorPicker: return "◉"
        }
    }

    /// The line under the title.
    var detail: String {
        switch kind {
        case .setup: return "Set up your first profile and project"
        case .dashboard: return "Projects, sessions, plans"
        case .overview: return "Every live session at once"
        case .shell: return "Shell · \(Self.abbreviated(target))"
        case .editor: return "Editor · \(Self.abbreviated(target))"
        case .pager: return "Scrollback"
        case .terminal: return "Shell"
        case .history: return target.isEmpty ? "Every conversation, to continue or fork" : "Continue or fork a conversation"
        case .keybindings: return "What the function keys do"
        case .iconPicker: return "Harness icon"
        case .colorPicker: return "Session color"
        }
    }

    /// The line under the title, naming the session the view is about when
    /// its name is known: every pager is "Scrollback" and every terminal
    /// "Shell" until it says whose.
    func detail(session name: String?) -> String {
        guard aboutRuntimeSessionID != nil, let name, !name.isEmpty else { return detail }
        return "\(detail) · \(name)"
    }

    /// The line under the title once the view's program set a title: that
    /// title, still followed by the session's name when the view is about
    /// one, so terminals with the same program stay told apart.
    func detail(session name: String?, program: String?) -> String {
        guard let program, !program.isEmpty else { return detail(session: name) }
        guard aboutRuntimeSessionID != nil, let name, !name.isEmpty else { return program }
        return "\(program) · \(name)"
    }

    /// What the pointer shows over the view's line: the session it is about
    /// by name, else what it is about.
    func toolTip(session name: String?) -> String {
        if aboutRuntimeSessionID != nil, let name, !name.isEmpty { return detail(session: name) }
        return target.isEmpty ? detail : target
    }

    static let setup = WorkspaceUtilityView(kind: .setup, target: "", title: "Set Up First Project")
    static let dashboard = WorkspaceUtilityView(kind: .dashboard, target: "", title: "Dashboard")
    static let overview = WorkspaceUtilityView(kind: .overview, target: "", title: "Overview")
    static let keybindings = WorkspaceUtilityView(kind: .keybindings, target: "", title: "Keybindings")
    /// Past Sessions over every conversation (⌘Y), without the Dashboard.
    static let pastSessions = WorkspaceUtilityView(kind: .history, target: "", title: "Past Sessions")

    /// Past Sessions on a conversation, where the operator continues or
    /// forks it. The workspace only opens the list: it resumes nothing.
    static func history(_ conversation: ConversationIdentity) -> WorkspaceUtilityView {
        WorkspaceUtilityView(kind: .history, target: conversation.encoded, title: "Past Sessions")
    }

    /// The icon or color picker for a session; nil for any other kind.
    static func picker(_ kind: Kind, runtimeSessionID: String) -> WorkspaceUtilityView? {
        switch kind {
        case .iconPicker: return WorkspaceUtilityView(kind: kind, target: runtimeSessionID, title: "Icon")
        case .colorPicker: return WorkspaceUtilityView(kind: kind, target: runtimeSessionID, title: "Color")
        default: return nil
        }
    }

    static func abbreviated(_ path: String, home: String = NSHomeDirectory()) -> String {
        if path == home { return "~" }
        return path.hasPrefix(home + "/") ? "~" + path.dropFirst(home.count) : path
    }

    /// The utility view a request asks for; nil for a session view, an
    /// unknown kind, or a target that is not what its kind needs.
    static func from(_ request: WorkspaceViewRequest) -> WorkspaceUtilityView? {
        guard let kind = Kind(rawValue: request.kind) else { return nil }
        let label = request.title.trimmingCharacters(in: .whitespacesAndNewlines)
        switch kind {
        case .dashboard: return .dashboard
        case .overview: return .overview
        case .shell:
            guard isAbsolute(request.directory) else { return nil }
            return WorkspaceUtilityView(kind: .shell, target: request.directory,
                                        title: label.isEmpty ? lastComponent(request.directory) : label)
        case .editor:
            guard isAbsolute(request.path) else { return nil }
            return WorkspaceUtilityView(kind: .editor, target: request.path,
                                        title: label.isEmpty ? lastComponent(request.path) : label)
        case .pager:
            guard isRuntimeSessionID(request.runtimeSessionID) else { return nil }
            return WorkspaceUtilityView(kind: .pager, target: request.runtimeSessionID,
                                        title: label.isEmpty ? "Scrollback" : label)
        case .terminal, .history, .keybindings, .iconPicker, .colorPicker, .setup:
            // Only the operator opens one, from the workspace itself.
            return nil
        }
    }

    private static func isAbsolute(_ path: String) -> Bool {
        path.hasPrefix("/") && !path.contains("\0") && !path.contains("\n")
    }

    private static func lastComponent(_ path: String) -> String {
        let name = (path as NSString).lastPathComponent
        return name.isEmpty ? path : name
    }

    /// `rs-` and 32 lowercase hex digits, as `runtimesession.ParseID` accepts.
    static func isRuntimeSessionID(_ id: String) -> Bool {
        guard id.hasPrefix("rs-"), id.count == 35 else { return false }
        return id.dropFirst(3).allSatisfy { $0.isASCII && ($0.isNumber || ("a"..."f").contains($0)) }
    }
}

/// The utility views the workspace lists: the Dashboard and the Overview
/// always, then the ones that were opened, in the order they were.
struct WorkspaceViews: Equatable {
    private(set) var opened: [WorkspaceUtilityView] = []

    var all: [WorkspaceUtilityView] { [.dashboard, .overview] + opened }

    func view(_ id: String?) -> WorkspaceUtilityView? {
        guard let id else { return nil }
        return all.first { $0.id == id }
    }

    /// What the one sheet shown before quitting asks, naming every view
    /// whose program is still running. A title shared by several views is
    /// named once, with how many there are.
    static func quitQuestion(_ titles: [String]) -> String {
        if titles.count == 1 { return "A process is still running in \(titles[0]). Quit anyway?" }
        var order: [String] = []
        var counts: [String: Int] = [:]
        for title in titles {
            if counts[title] == nil { order.append(title) }
            counts[title, default: 0] += 1
        }
        let names = order.map { counts[$0]! > 1 ? "\($0) (\(counts[$0]!))" : $0 }
        let list = names.count == 1
            ? names[0]
            : names.dropLast().joined(separator: ", ") + " and " + names[names.count - 1]
        return "Processes are still running in \(list). Quit anyway?"
    }

    /// Lists `view`. One view per kind and target: opening the same shell or
    /// file again returns the one already listed.
    @discardableResult
    mutating func open(_ view: WorkspaceUtilityView) -> WorkspaceUtilityView {
        if let listed = self.view(view.id) { return listed }
        opened.append(view)
        return view
    }

    /// Names only shells and terminals, preserving their identity and program.
    mutating func name(_ id: String, _ answer: String) {
        guard let index = opened.firstIndex(where: { $0.id == id && $0.canName }) else { return }
        let name = answer.trimmingCharacters(in: .whitespacesAndNewlines)
        opened[index].name = name.isEmpty ? nil : name
    }

    /// What New Shell opens in `directory`: the shell there when none is
    /// listed yet, else one more after the ones it has. A surface's request
    /// for a shell still goes back to the one listed (`open`).
    func nextShell(in directory: String) -> WorkspaceUtilityView {
        let listed = opened.filter { $0.kind == .shell && $0.target == directory }
        guard !listed.isEmpty else { return WorkspaceUtilityView(kind: .shell, target: directory, title: "Shell") }
        let ordinal = listed.map { max($0.ordinal, 1) }.max()! + 1
        return WorkspaceUtilityView(kind: .shell, target: directory, title: "Shell \(ordinal)", ordinal: ordinal)
    }

    /// The terminals opened beside a session, in the order they were.
    func terminals(beside runtimeSessionID: String?) -> [WorkspaceUtilityView] {
        guard let runtimeSessionID else { return [] }
        return opened.filter { $0.besideRuntimeSessionID == runtimeSessionID }
    }

    /// Lists one more terminal beside a session, after the ones it has.
    mutating func openTerminal(beside runtimeSessionID: String) -> WorkspaceUtilityView {
        let ordinal = (terminals(beside: runtimeSessionID).map(\.ordinal).max() ?? 0) + 1
        return open(WorkspaceUtilityView(
            kind: .terminal, target: runtimeSessionID,
            title: ordinal == 1 ? "Terminal" : "Terminal \(ordinal)", ordinal: ordinal
        ))
    }

    /// Moves a terminal among the ones beside its session: to before
    /// `other`, or after the last of them when nil. It never leaves its
    /// session.
    mutating func moveTerminal(_ id: String, before other: String?) {
        guard id != other, let from = opened.firstIndex(where: { $0.id == id }),
              let session = opened[from].besideRuntimeSessionID else { return }
        if let other, view(other)?.besideRuntimeSessionID != session { return }
        let moved = opened.remove(at: from)
        let to = other.flatMap { other in opened.firstIndex { $0.id == other } }
            ?? opened.lastIndex { $0.besideRuntimeSessionID == session }.map { $0 + 1 }
            ?? opened.count
        opened.insert(moved, at: to)
    }

    /// Drops an opened view; the Dashboard and the Overview stay.
    mutating func close(_ id: String) {
        opened.removeAll { $0.id == id }
    }
}
