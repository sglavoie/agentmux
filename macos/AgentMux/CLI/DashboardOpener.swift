import Foundation

/// `amx open-dashboard --json`'s data: the dashboard window it focused or
/// opened.
struct OpenDashboardResult: Decodable, Equatable {
    var windowID = 0
    var tabID = 0
    var opened = false

    init(windowID: Int = 0, tabID: Int = 0, opened: Bool = false) {
        self.windowID = windowID
        self.tabID = tabID
        self.opened = opened
    }

    enum CodingKeys: String, CodingKey {
        case opened
        case windowID = "window_id"
        case tabID = "tab_id"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        windowID = c.lenient(.windowID, 0)
        tabID = c.lenient(.tabID, 0)
        opened = c.lenient(.opened, false)
    }
}

/// Where the dashboard shortcut and the menu's Open Dashboard show the
/// dashboard (`amxDashboardPlace`).
enum DashboardPlacePreference: String, CaseIterable, Equatable {
    /// The workspace's own Dashboard view, whatever Kitty is running.
    case workspace
    /// A dashboard tab in Kitty when a Kitty is running, else the workspace.
    case kitty

    var label: String {
        switch self {
        case .workspace: return "The workspace"
        case .kitty: return "Kitty when it is running, else the workspace"
        }
    }
}

/// What the dashboard shortcut does when it is sent to Kitty: `amx open-dashboard`, which focuses the
/// running dashboard or opens one, against each candidate Kitty socket in
/// turn. amx dials the socket itself, so unlike focus_window any address Kitty
/// answers on will do; only `kitty_unavailable` — a socket nobody answers on,
/// such as one a crashed Kitty left behind — moves on to the next.
enum DashboardOpener {
    enum Outcome: Equatable {
        case opened(OpenDashboardResult, socket: String)
        case noSocket
        case failed(String)
    }

    static func open(
        candidates: [String],
        run: (_ socket: String) -> AMXCommandResult<OpenDashboardResult>
    ) -> Outcome {
        switch KittyTargetedCommand.run(candidates: candidates, run) {
        case .noSocket:
            return .noSocket
        case let .unreachable(message):
            return .failed(message)
        case let .done(.success(result), socket), let .done(.partial(result, _), socket):
            return .opened(result, socket: socket)
        case let .done(.failure(error), _):
            return .failed(error.message)
        }
    }

    /// Where the dashboard shortcut ends up.
    enum Destination: Equatable {
        /// A dashboard in the Kitty at `socket`, to bring forward.
        case kitty(OpenDashboardResult, socket: String)
        /// The workspace's own Dashboard view.
        case workspace
        case failed(String)
    }

    /// Only the absence of any Kitty to ask sends the shortcut to the
    /// workspace. A Kitty that was reached and refused, or sockets that
    /// exist and that nobody answers on, stay the failures they are: the
    /// operator has a Kitty and should hear what is wrong with it, not find
    /// a second dashboard somewhere else.
    static func destination(for outcome: Outcome) -> Destination {
        switch outcome {
        case let .opened(result, socket): return .kitty(result, socket: socket)
        case .noSocket: return .workspace
        case let .failed(message): return .failed(message)
        }
    }

    /// The sockets to try: Preferences' override, the running dashboard's own
    /// Kitty, the snapshot's, then every running Kitty's default address.
    static func candidates(override: String, snapshot: CompanionSnapshot?, kittyPIDs: [pid_t],
                           fileExists: (String) -> Bool = { FileManager.default.fileExists(atPath: $0) }) -> [String] {
        let dashboard = snapshot?.dashboard.open == true ? snapshot?.dashboard.kittySocket ?? "" : ""
        return KittyEndpoint.candidates(
            override: override, rowSocket: dashboard, snapshotSocket: snapshot?.kitty.socket ?? "",
            kittyPIDs: kittyPIDs, fileExists: fileExists
        )
    }
}
