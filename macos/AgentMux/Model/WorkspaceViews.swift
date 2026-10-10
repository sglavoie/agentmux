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

    static let sessionKind = "session"

    init(kind: String = "", runtimeSessionID: String = "", awaited: Bool = false,
         directory: String = "", path: String = "", title: String = "") {
        self.kind = kind
        self.runtimeSessionID = runtimeSessionID
        self.awaited = awaited
        self.directory = directory
        self.path = path
        self.title = title
    }

    enum CodingKeys: String, CodingKey {
        case kind, directory, path, title
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
    }
}

/// A terminal the workspace runs beside the agents: its Dashboard, its
/// Overview, or a shell, editor, or pager one of those asked for.
///
/// A utility view is not a session. It has no `AttentionCycler.Key`, no
/// runtime session of its own, and no state glyph; nothing about it is read
/// from a snapshot, so it can never be counted as waiting or be the target of
/// the next-attention shortcut. A pager names the session whose scrollback it
/// reads and is still not that session.
struct WorkspaceUtilityView: Equatable {
    enum Kind: String, Equatable {
        case dashboard, overview, shell, editor, pager
    }

    let kind: Kind
    /// The directory, file, or runtime session the view is about; empty for
    /// the Dashboard and the Overview.
    let target: String
    /// The sidebar label.
    let title: String

    /// What tells one view from another: its kind and what it is about.
    var id: String { target.isEmpty ? kind.rawValue : "\(kind.rawValue):\(target)" }

    /// The Dashboard and the Overview are always listed; the others exist
    /// only while their program runs.
    var isPermanent: Bool { kind == .dashboard || kind == .overview }

    /// What `amx` runs in the view's terminal. amx decides the program: the
    /// workspace passes only the kind and the target.
    var arguments: [String] {
        switch kind {
        case .dashboard: return ["workspace-view", "dashboard"]
        case .overview: return ["workspace-view", "overview"]
        case .shell: return ["workspace-view", "shell", target]
        case .editor: return ["workspace-view", "edit", target]
        case .pager: return ["workspace-view", "pager", target]
        }
    }

    var glyph: String {
        switch kind {
        case .dashboard: return "▦"
        case .overview: return "▤"
        case .shell: return "❯"
        case .editor: return "✎"
        case .pager: return "≡"
        }
    }

    /// The line under the title.
    var detail: String {
        switch kind {
        case .dashboard: return "Projects, sessions, plans"
        case .overview: return "Every live session at once"
        case .shell: return "Shell · \(Self.abbreviated(target))"
        case .editor: return "Editor · \(Self.abbreviated(target))"
        case .pager: return "Scrollback"
        }
    }

    static let dashboard = WorkspaceUtilityView(kind: .dashboard, target: "", title: "Dashboard")
    static let overview = WorkspaceUtilityView(kind: .overview, target: "", title: "Overview")

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

    /// Lists `view`. One view per kind and target: opening the same shell or
    /// file again returns the one already listed.
    @discardableResult
    mutating func open(_ view: WorkspaceUtilityView) -> WorkspaceUtilityView {
        if let listed = self.view(view.id) { return listed }
        opened.append(view)
        return view
    }

    /// Drops an opened view; the Dashboard and the Overview stay.
    mutating func close(_ id: String) {
        opened.removeAll { $0.id == id }
    }
}
