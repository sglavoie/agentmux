import Foundation

/// Whether the resolved amx can run a menu action, and if not, why: the
/// reason becomes the greyed-out item's tooltip, so an older amx shows what
/// it lacks instead of failing when the item is chosen.
struct ActionGate: Equatable {
    enum Feature: Equatable {
        /// `amx project list|launch`.
        case project
        /// `amx open-dashboard`.
        case openDashboard
        /// `amx mute-toggle --json`.
        case muteToggle
        /// `amx session list|continue`, which every amx the app runs has.
        case sessions
    }

    /// nil until the binary is resolved and probed.
    var capabilities: AMXCapabilities?
    /// Why no amx could be resolved at all.
    var problem: String?
    var binaryPath = "amx"

    init(capabilities: AMXCapabilities? = nil, problem: String? = nil, binaryPath: String = "amx") {
        self.capabilities = capabilities
        self.problem = problem
        self.binaryPath = binaryPath
    }

    /// nil when `feature` is available.
    func unavailableReason(_ feature: Feature) -> String? {
        if let problem { return problem }
        guard let capabilities else { return "amx is still being resolved" }
        let missing: String?
        switch feature {
        case .project: missing = capabilities.project ? nil : "project"
        case .openDashboard: missing = capabilities.openDashboard ? nil : "open-dashboard"
        case .muteToggle: missing = capabilities.muteToggleJSON ? nil : "mute-toggle --json"
        case .sessions: missing = nil
        }
        return missing.map { "\(binaryPath) has no `\($0)`; run `just install`" }
    }
}

/// `amx mute-toggle --json`'s data.
struct MuteToggleResult: Decodable, Equatable {
    var windowID = 0
    var muted = false
    var changed = false

    init(windowID: Int = 0, muted: Bool = false, changed: Bool = false) {
        self.windowID = windowID
        self.muted = muted
        self.changed = changed
    }

    enum CodingKeys: String, CodingKey {
        case muted, changed
        case windowID = "window_id"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        windowID = c.lenient(.windowID, 0)
        muted = c.lenient(.muted, false)
        changed = c.lenient(.changed, false)
    }

    static func arguments(windowID: Int) -> [String] {
        ["mute-toggle", "--window-id", String(windowID)]
    }
}

/// An amx command that dials Kitty itself, run against each candidate socket
/// in turn. Only `kitty_unavailable` — a socket nobody answers on, such as one
/// a crashed Kitty left behind — moves on to the next: any other answer,
/// success or not, means amx reached a Kitty and acted, or refused to.
enum KittyTargetedCommand {
    enum Outcome<T> {
        case done(AMXCommandResult<T>, socket: String)
        case noSocket
        case unreachable(String)
    }

    static let unavailableCode = "kitty_unavailable"

    static func run<T>(candidates: [String], _ run: (_ socket: String) -> AMXCommandResult<T>) -> Outcome<T> {
        guard !candidates.isEmpty else { return .noSocket }
        var last = ""
        for socket in candidates {
            let result = run(socket)
            if case let .failure(.command(error, _)) = result, error.code == unavailableCode {
                last = error.message.isEmpty ? error.code : error.message
                continue
            }
            return .done(result, socket: socket)
        }
        return .unreachable(last)
    }
}

/// Where amx reads its configuration, as the Notifications submenu names it:
/// `AMX_CONFIG` from the environment amx runs with, else
/// `~/.config/agent-mux/config.toml` (`config.DefaultConfigPath`).
enum AMXConfigPath {
    static func resolve(_ environment: LaunchEnvironment?, home: String = NSHomeDirectory()) -> String {
        let home = environment?.home ?? home
        var path = home + "/.config/agent-mux/config.toml"
        if let configured = environment?.value(of: "AMX_CONFIG"), !configured.isEmpty {
            if configured == "~" {
                path = home
            } else if configured.hasPrefix("~/") {
                path = home + configured.dropFirst(1)
            } else {
                path = configured
            }
        }
        return path.hasPrefix(home + "/") ? "~" + path.dropFirst(home.count) : path
    }
}
