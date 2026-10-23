import Foundation

/// Where the workspace was, kept across launches so a crash, a quit, or an
/// upgrade comes back to the same window on the same session.
///
/// Only the place is kept, never a program: restoring it shows a session that
/// is still running and never starts one that is not.
struct WorkspaceRestoration: Equatable {
    enum Pane: Codable, Equatable {
        case session(String)
        case utility(WorkspaceUtilityView)
        case review(String)
    }
    struct Layout: Codable, Equatable {
        var active: Pane
        var other: Pane?
        var activeOnRight: Bool
        var fraction: Double? = nil
    }
    static let layoutKey = "amxWorkspaceLayout"
    var layout: Layout?

    static func saveLayout(_ layout: Layout?, to defaults: UserDefaults = .standard) {
        if let layout, let data = try? JSONEncoder().encode(layout) { defaults.set(data, forKey: layoutKey) }
        else { defaults.removeObject(forKey: layoutKey) }
    }
    /// Something the pane can be restored to.
    enum Place: Equatable {
        /// A session the workspace embeds.
        case session(runtimeSessionID: String)
        /// A session that is a Kitty window.
        case kitty(socket: String, windowID: Int)
        /// The Dashboard or the Overview. An opened shell, editor, or pager
        /// is a program that ended with the app and is not brought back.
        case view(id: String)

        /// The place as one preference string.
        var encoded: String {
            switch self {
            case let .session(id): return "session:\(id)"
            case let .kitty(socket, windowID): return "kitty:\(windowID):\(socket)"
            case let .view(id): return "view:\(id)"
            }
        }

        /// Reads a preference string back; nil for anything that is not a
        /// place this app would have written.
        init?(encoded: String) {
            guard let colon = encoded.firstIndex(of: ":") else { return nil }
            let rest = String(encoded[encoded.index(after: colon)...])
            switch encoded[..<colon] {
            case "session":
                guard WorkspaceUtilityView.isRuntimeSessionID(rest) else { return nil }
                self = .session(runtimeSessionID: rest)
            case "kitty":
                let parts = rest.split(separator: ":", maxSplits: 1, omittingEmptySubsequences: false)
                guard parts.count == 2, let windowID = Int(parts[0]), windowID > 0 else { return nil }
                self = .kitty(socket: String(parts[1]), windowID: windowID)
            case "view":
                guard rest == WorkspaceUtilityView.dashboard.id || rest == WorkspaceUtilityView.overview.id else { return nil }
                self = .view(id: rest)
            default:
                return nil
            }
        }

        /// The session key the place names, when it names a session.
        var sessionKey: AttentionCycler.Key? {
            switch self {
            case let .session(id): return .embedded(id)
            case let .kitty(socket, windowID): return AttentionCycler.Key(socket: socket, windowID: windowID)
            case .view: return nil
            }
        }
    }

    static let windowOpenKey = "amxWorkspaceWindowOpen"
    static let placeKey = "amxWorkspacePlace"
    static let conversationKey = "amxWorkspaceConversation"
    static let sidebarCollapsedKey = "amxWorkspaceSidebarCollapsed"
    static let fullScreenKey = "amxWorkspaceFullScreen"
    static let inspectorShownKey = "amxWorkspaceInspectorShown"
    static let inspectorTabKey = "amxWorkspaceInspectorTab"
    static let sidebarWidthKey = "amxWorkspaceSidebarWidth"
    static let inspectorWidthKey = "amxWorkspaceInspectorWidth"

    /// Whether the window was open when the app last ran.
    var windowOpen = false
    var place: Place?
    /// The conversation of the session the pane was on, so that one found
    /// gone at launch — after a reboot, say — can still be resumed from Past
    /// Sessions.
    var conversation: ConversationIdentity?
    /// Whether the sidebar was hidden.
    var sidebarCollapsed = false
    /// Whether the window was in full screen.
    var fullScreen = false
    /// Whether the inspector was shown; it is hidden until asked for.
    var inspectorShown = false
    var inspectorTab = WorkspaceInspectorTab.waiting
    /// The widths the sidebar and the inspector were last dragged to while
    /// shown; nil when nothing is saved, and the window's defaults apply.
    var sidebarWidth: Double?
    var inspectorWidth: Double?

    static func load(from defaults: UserDefaults = .standard) -> WorkspaceRestoration {
        WorkspaceRestoration(
            layout: defaults.data(forKey: layoutKey).flatMap { try? JSONDecoder().decode(Layout.self, from: $0) },
            windowOpen: defaults.bool(forKey: windowOpenKey),
            place: defaults.string(forKey: placeKey).flatMap(Place.init(encoded:)),
            conversation: defaults.string(forKey: conversationKey).flatMap(ConversationIdentity.init(encoded:)),
            sidebarCollapsed: defaults.bool(forKey: sidebarCollapsedKey),
            fullScreen: defaults.bool(forKey: fullScreenKey),
            inspectorShown: defaults.bool(forKey: inspectorShownKey),
            inspectorTab: defaults.string(forKey: inspectorTabKey).flatMap(WorkspaceInspectorTab.init(rawValue:)) ?? .waiting,
            sidebarWidth: width(sidebarWidthKey, in: defaults),
            inspectorWidth: width(inspectorWidthKey, in: defaults)
        )
    }

    static func saveInspector(shown: Bool, tab: WorkspaceInspectorTab, to defaults: UserDefaults = .standard) {
        defaults.set(shown, forKey: inspectorShownKey)
        defaults.set(tab.rawValue, forKey: inspectorTabKey)
    }

    /// Saves the width of whichever is shown; nil leaves the saved one as it
    /// is, so hiding and showing again keeps it.
    static func saveWidths(sidebar: Double?, inspector: Double?, to defaults: UserDefaults = .standard) {
        if let sidebar { defaults.set(sidebar, forKey: sidebarWidthKey) }
        if let inspector { defaults.set(inspector, forKey: inspectorWidthKey) }
    }

    private static func width(_ key: String, in defaults: UserDefaults) -> Double? {
        guard defaults.object(forKey: key) != nil else { return nil }
        let width = defaults.double(forKey: key)
        return width.isFinite && width > 0 ? width : nil
    }

    static func saveSidebarCollapsed(_ collapsed: Bool, to defaults: UserDefaults = .standard) {
        defaults.set(collapsed, forKey: sidebarCollapsedKey)
    }

    static func saveFullScreen(_ fullScreen: Bool, to defaults: UserDefaults = .standard) {
        defaults.set(fullScreen, forKey: fullScreenKey)
    }

    static func saveWindowOpen(_ open: Bool, to defaults: UserDefaults = .standard) {
        defaults.set(open, forKey: windowOpenKey)
    }

    static func saveConversation(_ conversation: ConversationIdentity?, to defaults: UserDefaults = .standard) {
        if let conversation {
            defaults.set(conversation.encoded, forKey: conversationKey)
        } else {
            defaults.removeObject(forKey: conversationKey)
        }
    }

    static func savePlace(_ place: Place?, to defaults: UserDefaults = .standard) {
        if let place {
            defaults.set(place.encoded, forKey: placeKey)
        } else {
            defaults.removeObject(forKey: placeKey)
        }
    }
}

/// What the registry and the private tmux server say about a session, asked
/// through `amx attach --json`, which checks and never attaches.
enum WorkspaceSessionStatus: Equatable {
    /// The agent is running; `clients` terminals are attached to it now.
    case live(clients: Int)
    /// The agent's process ended where it ran; the reason says how.
    case exited(String)
    /// The place the agent ran is gone, so what became of it was not seen.
    case lost(String)
    /// The session was ended on purpose.
    case ended(String)
    /// amx knows no such session: its record is gone.
    case unknownSession(String)
    /// amx could not say.
    case unknown(String)

    /// What an `amx attach --json` answer means.
    static func from(_ result: AMXCommandResult<AttachCheck>) -> WorkspaceSessionStatus {
        switch result {
        case let .success(check), let .partial(check, _):
            return .live(clients: check.clients)
        case let .failure(.command(error, _)):
            switch error.code {
            case "runtime_session_exited": return .exited(error.message)
            case "runtime_session_lost": return .lost(error.message)
            case "runtime_session_ended": return .ended(error.message)
            case "not_found": return .unknownSession(error.message)
            default: return .unknown(error.message.isEmpty ? error.code : error.message)
            }
        case let .failure(error):
            return .unknown(error.message)
        }
    }

    /// The pane's title and message for a session that is not running. Nil
    /// for one that is, or that could not be asked about. With `resumable`,
    /// the pane offers native recovery of its recorded conversation.
    func notice(for name: String, resumable: Bool = false) -> (title: String, message: String)? {
        let resume = "Resume Conversation shows its recorded profile and destination before you resume it."
        switch self {
        case .live, .unknown:
            return nil
        case let .exited(reason):
            return ("Agent exited", "\(name) is no longer running: its agent ended on its own. \(Self.detail(reason))It was not restarted. \(resumable ? resume : "Start it again, or resume its conversation, from the Dashboard.")")
        case let .lost(reason):
            return ("Agent lost", "\(name) can no longer be found: the terminal it ran in is gone, as after a restart of the computer or of the tmux server. \(Self.detail(reason))It was not restarted. \(resumable ? resume : "Resume its conversation from the Dashboard.")")
        case .ended:
            return ("Session ended", "\(name) was ended. Nothing was restarted. \(resumable ? resume : "")".trimmingCharacters(in: .whitespaces))
        case .unknownSession:
            return ("No longer known", "\(name) is no longer listed, and amx has no record of it. Nothing was restarted. \(resumable ? resume : "Start it again from the Dashboard.")")
        }
    }

    private static func detail(_ reason: String) -> String {
        let trimmed = reason.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? "" : "(\(trimmed)) "
    }
}

/// The payload of `amx attach RUNTIME_SESSION_ID --json`.
struct AttachCheck: Decodable, Equatable {
    var runtimeSessionID = ""
    var state = ""
    /// Terminals attached to the session right now.
    var clients = 0

    enum CodingKeys: String, CodingKey {
        case runtimeSessionID = "runtime_session_id"
        case state, clients
    }

    init(runtimeSessionID: String = "", state: String = "", clients: Int = 0) {
        self.runtimeSessionID = runtimeSessionID
        self.state = state
        self.clients = clients
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runtimeSessionID = try c.decodeIfPresent(String.self, forKey: .runtimeSessionID) ?? ""
        state = try c.decodeIfPresent(String.self, forKey: .state) ?? ""
        clients = try c.decodeIfPresent(Int.self, forKey: .clients) ?? 0
    }
}

/// The payload of `amx close RUNTIME_SESSION_ID --yes --json`.
struct SessionClose: Decodable, Equatable {
    /// Whether a tmux session was killed; false when nothing was left of it.
    var closed = false

    enum CodingKeys: String, CodingKey { case closed }

    init(closed: Bool = false) { self.closed = closed }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        closed = try c.decodeIfPresent(Bool.self, forKey: .closed) ?? false
    }

    /// Why an `amx close` answer means the session is still there; nil when
    /// it is closed or was already gone.
    static func problem(_ result: AMXCommandResult<SessionClose>) -> String? {
        switch result {
        case .success, .partial: return nil
        case let .failure(error): return error.message
        }
    }
}
