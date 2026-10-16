import Foundation

/// Where Launch Project starts an agent (`amxLaunchBackend`).
enum LaunchBackendPreference: String, CaseIterable, Equatable {
    /// The workspace. A running Kitty changes nothing: Kitty is launched
    /// into only when chosen.
    case automatic
    /// Always a Kitty tab; with no Kitty the launch fails and says so.
    case kitty
    /// Always the private tmux server, shown in the workspace.
    case workspace

    var label: String {
        switch self {
        case .automatic: return "Automatic (the workspace)"
        case .kitty: return "Kitty"
        case .workspace: return "The workspace (tmux)"
        }
    }

    var destination: String { launchesInWorkspace ? "workspace (tmux)" : "Kitty" }

    func explanation(cliBackend: String?) -> String {
        let cli = cliBackend == "tmux" ? "tmux" : cliBackend == "kitty" ? "Kitty" : "unknown (check amx backend status)"
        return "Launch Project uses \(destination). This app preference overrides config.toml for that action. Command-line launches use \(cli) unless --backend overrides it. Run a Plan always uses the workspace."
    }

    /// Whether a launch goes to the workspace. What happens to be running
    /// never decides it.
    var launchesInWorkspace: Bool {
        switch self {
        case .automatic, .workspace: return true
        case .kitty: return false
        }
    }
}

/// `amx backend status --json`: which tmux the private server runs, whether a
/// server is running, and what the operator has to do when something is
/// missing. Decoded leniently, so a newer amx can say more.
struct BackendStatus: Decodable, Equatable {
    struct Tmux: Decodable, Equatable {
        var available = false
        var path = ""
        var version = ""
        var source = ""
        var verified = false
        var minVersion = ""

        enum CodingKeys: String, CodingKey {
            case available, path, version, source, verified
            case minVersion = "min_version"
        }

        init() {}

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            available = c.lenient(.available, false)
            path = c.lenient(.path, "")
            version = c.lenient(.version, "")
            source = c.lenient(.source, "")
            verified = c.lenient(.verified, false)
            minVersion = c.lenient(.minVersion, "")
        }
    }

    struct Server: Decodable, Equatable {
        var running = false
        var binary = ""
        var sessions = 0
        var agents = 0

        enum CodingKeys: String, CodingKey { case running, binary, sessions, agents }

        init() {}

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            running = c.lenient(.running, false)
            binary = c.lenient(.binary, "")
            sessions = c.lenient(.sessions, 0)
            agents = c.lenient(.agents, 0)
        }
    }

    struct Problem: Decodable, Equatable {
        var code = ""
        /// `error`, `warning`, or `info`.
        var severity = ""
        var message = ""
        var hint = ""

        enum CodingKeys: String, CodingKey { case code, severity, message, hint }

        init(code: String, severity: String, message: String, hint: String = "") {
            self.code = code
            self.severity = severity
            self.message = message
            self.hint = hint
        }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            code = c.lenient(.code, "")
            severity = c.lenient(.severity, "")
            message = c.lenient(.message, "")
            hint = c.lenient(.hint, "")
        }

        /// Something to act on, as opposed to something to know.
        var needsAttention: Bool { severity != "info" }

        /// The message and what to do about it, as one note.
        var text: String { hint.isEmpty ? message : "\(message)\n\(hint)" }
    }

    var launchBackend = ""
    var tmux = Tmux()
    var server = Server()
    var problems: [Problem] = []

    enum CodingKeys: String, CodingKey {
        case tmux, server, problems
        case launchBackend = "launch_backend"
    }

    init() {}

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        launchBackend = c.lenient(.launchBackend, "")
        tmux = c.lenient(.tmux, Tmux())
        server = c.lenient(.server, Server())
        problems = c.lenient(.problems, [])
    }

    static let arguments = ["backend", "status"]

    static func fetch(_ command: AMXCommand) -> Result<BackendStatus, AMXCommandError> {
        command.json(BackendStatus.self, arguments).result
    }

    /// Which tmux runs, in one line.
    var tmuxLine: String {
        guard tmux.available else {
            return tmux.minVersion.isEmpty ? "Not available" : "Not available (needs \(tmux.minVersion) or newer)"
        }
        let origin: String
        switch tmux.source {
        case "bundled": origin = tmux.verified ? "bundled with Agent Mux, verified" : "bundled with Agent Mux"
        case "installed": origin = "installed by Agent Mux"
        case "path": origin = "found on PATH"
        case "config": origin = "set in config.toml"
        case "environment": origin = "set by AMX_TMUX"
        default: origin = tmux.source
        }
        return "tmux \(tmux.version) (\(origin))"
    }

    /// The private server, in one line.
    var serverLine: String {
        guard server.running else { return "Not running; the first workspace launch starts it" }
        let agents = server.agents == 1 ? "1 agent running" : "\(server.agents) agents running"
        return "Running, \(agents)"
    }
}

/// What Preferences shows about the backend setup: the report, or why there
/// is none.
enum BackendSetup: Equatable {
    case unknown
    case status(BackendStatus)
    /// amx could not report: it is older than the command, or it failed.
    case unavailable(String)

    init(_ result: Result<BackendStatus, AMXCommandError>) {
        switch result {
        case let .success(status): self = .status(status)
        case let .failure(error): self = .unavailable(error.message)
        }
    }

    var lines: [(String, String)] {
        switch self {
        case .unknown: return [("tmux", "checking…")]
        case let .status(status): return [("tmux", status.tmuxLine), ("Session server", status.serverLine)]
        case .unavailable: return [("tmux", "unknown")]
        }
    }

    /// Notes under the rows, each with whether it is a warning.
    var notes: [(text: String, warning: Bool)] {
        switch self {
        case .unknown: return []
        case let .status(status): return status.problems.map { ($0.text, $0.needsAttention) }
        case let .unavailable(reason):
            return [("This amx could not report the backend setup: \(reason)", true)]
        }
    }
}

extension LaunchEnvironment {
    /// The variable amx reads to find the tmux runtime shipped in a bundle
    /// (`internal/tmux.RuntimeEnv`).
    static let tmuxRuntimeVariable = "AMX_TMUX_RUNTIME"

    /// This environment naming the tmux runtime inside the app bundle, so
    /// every amx the app runs — whichever copy was resolved — uses the pinned
    /// tmux. A value the operator set (in the shell or in Preferences) is
    /// kept, and a bundle without a runtime adds nothing.
    func namingBundledRuntime(
        resources: URL?,
        fileExists: (String) -> Bool = { FileManager.default.fileExists(atPath: $0) }
    ) -> LaunchEnvironment {
        guard forwarded[Self.tmuxRuntimeVariable] == nil, let resources else { return self }
        let runtime = resources.appendingPathComponent("tmux")
        guard fileExists(runtime.appendingPathComponent("SHA256SUMS").path),
              fileExists(runtime.appendingPathComponent("bin/tmux").path) else { return self }
        var copy = self
        copy.forwarded[Self.tmuxRuntimeVariable] = runtime.path
        return copy
    }
}
