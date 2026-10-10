import Foundation

/// Where the workspace was, kept across launches so a crash, a quit, or an
/// upgrade comes back to the same window on the same session.
///
/// Only the place is kept, never a program: restoring it shows a session that
/// is still running and never starts one that is not.
struct WorkspaceRestoration: Equatable {
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

    /// Whether the window was open when the app last ran.
    var windowOpen = false
    var place: Place?

    static func load(from defaults: UserDefaults = .standard) -> WorkspaceRestoration {
        WorkspaceRestoration(
            windowOpen: defaults.bool(forKey: windowOpenKey),
            place: defaults.string(forKey: placeKey).flatMap(Place.init(encoded:))
        )
    }

    static func saveWindowOpen(_ open: Bool, to defaults: UserDefaults = .standard) {
        defaults.set(open, forKey: windowOpenKey)
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
    /// The session was ended on purpose, or is not known at all.
    case ended(String)
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
            case "runtime_session_ended", "not_found": return .ended(error.message)
            default: return .unknown(error.message.isEmpty ? error.code : error.message)
            }
        case let .failure(error):
            return .unknown(error.message)
        }
    }

    /// The pane's title and message for a session that is not running. Nil
    /// for one that is, or that could not be asked about.
    func notice(for name: String) -> (title: String, message: String)? {
        switch self {
        case .live, .unknown:
            return nil
        case let .exited(reason):
            return ("Agent exited", "\(name) is no longer running: its agent ended on its own. \(Self.detail(reason))It was not restarted. Start it again, or resume its conversation, from the Dashboard.")
        case let .lost(reason):
            return ("Agent lost", "\(name) can no longer be found: the terminal it ran in is gone, as after a restart of the computer or of the tmux server. \(Self.detail(reason))It was not restarted. Resume its conversation from the Dashboard.")
        case .ended:
            return ("Session ended", "\(name) was closed. Nothing was restarted.")
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
