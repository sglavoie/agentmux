import Foundation

/// A list the app fetches from amx and keeps between menu openings: the last
/// good value, when it arrived, whether a fetch is running, and why the last
/// one failed. A failed refresh keeps the value it already had.
struct FetchState<Value: Equatable>: Equatable {
    private(set) var value: Value?
    private(set) var fetchedAt: Date?
    private(set) var loading = false
    private(set) var error: String?

    init(value: Value? = nil, fetchedAt: Date? = nil, loading: Bool = false, error: String? = nil) {
        self.value = value
        self.fetchedAt = fetchedAt
        self.loading = loading
        self.error = error
    }

    /// Whether a fetch should start now: none is running, and there is no
    /// value or it is older than `maxAge`.
    func needsRefresh(now: Date, maxAge: TimeInterval) -> Bool {
        guard !loading else { return false }
        guard let fetchedAt, value != nil else { return true }
        return now.timeIntervalSince(fetchedAt) >= maxAge
    }

    /// Marks a fetch as running; false when one already is.
    mutating func begin() -> Bool {
        guard !loading else { return false }
        loading = true
        return true
    }

    mutating func finish(_ result: Result<Value, AMXCommandError>, at now: Date) {
        loading = false
        switch result {
        case let .success(value):
            self.value = value
            fetchedAt = now
            error = nil
        case let .failure(failure):
            error = failure.message
        }
    }

    /// Forgets everything, as when amx is resolved to another binary.
    mutating func reset() { self = FetchState() }
}

extension AMXCommandResult {
    /// The value, partial or not, or the failure.
    var result: Result<T, AMXCommandError> {
        switch self {
        case let .success(value), let .partial(value, _): return .success(value)
        case let .failure(error): return .failure(error)
        }
    }
}

/// One configured project, as `amx project list --json` reports it.
struct ProjectEntry: Decodable, Equatable, Hashable {
    var profile = ""
    var title = ""
    var harness = ""
    var directory = ""
    var groups: [String] = []

    init(profile: String, title: String, harness: String = "", directory: String = "", groups: [String] = []) {
        self.profile = profile
        self.title = title
        self.harness = harness
        self.directory = directory
        self.groups = groups
    }

    enum CodingKeys: String, CodingKey { case profile, title, harness, directory, groups }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        profile = c.lenient(.profile, "")
        title = c.lenient(.title, "")
        harness = c.lenient(.harness, "")
        directory = c.lenient(.directory, "")
        groups = c.lenient(.groups, [])
    }
}

/// `amx project launch --json`'s data. The tab and window are reported only
/// when amx could tell which new tab the launch opened.
struct ProjectLaunchResult: Decodable, Equatable {
    var changed = false
    var message = ""
    var project: ProjectEntry?
    var tabID = 0
    var windowID = 0
    /// The runtime session a launch on the tmux backend registered; empty
    /// for a Kitty launch, which reports a tab and a window instead.
    var runtimeSessionID = ""

    init(changed: Bool = true, message: String = "", project: ProjectEntry? = nil, tabID: Int = 0, windowID: Int = 0,
         runtimeSessionID: String = "") {
        self.changed = changed
        self.message = message
        self.project = project
        self.tabID = tabID
        self.windowID = windowID
        self.runtimeSessionID = runtimeSessionID
    }

    enum CodingKeys: String, CodingKey {
        case changed, message, project
        case tabID = "tab_id"
        case windowID = "window_id"
        case runtimeSessionID = "runtime_session_id"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        changed = c.lenient(.changed, false)
        message = c.lenient(.message, "")
        project = c.lenient(.project)
        tabID = c.lenient(.tabID, 0)
        windowID = c.lenient(.windowID, 0)
        runtimeSessionID = c.lenient(.runtimeSessionID, "")
    }
}

/// The configured projects the Launch Project submenu offers, cached from
/// `amx project list --json` and refreshed when the menu opens on a list
/// older than a minute. Configuration changes rarely, and listing it is a
/// process launch, so the menu never waits for it.
struct ProjectCatalog: Equatable {
    static let maxAge: TimeInterval = 60
    static let listArguments = ["project", "list"]

    var state = FetchState<[ProjectEntry]>()
    /// The project directories the last survey found missing. Configuration
    /// may name a checkout that is not on this machine (yet); the menu keeps
    /// such projects listed but greyed out instead of offering a launch that
    /// would open a tab in a directory that is not there.
    var missingDirectories: Set<String> = []
    /// Whether a survey is running, so menu openings do not pile them up.
    var surveying = false

    func needsRefresh(now: Date) -> Bool { state.needsRefresh(now: now, maxAge: Self.maxAge) }

    /// The directories of `projects` that are not directories on disk. A
    /// project naming no directory is not missing one. It stats the disk, so
    /// callers run it off the main thread: a stalled network mount must not
    /// freeze the menu.
    static func survey(_ projects: [ProjectEntry], isDirectory: (String) -> Bool = directoryExists) -> Set<String> {
        var missing: Set<String> = []
        for directory in Set(projects.map(\.directory)) where !directory.isEmpty && !isDirectory(directory) {
            missing.insert(directory)
        }
        return missing
    }

    static func directoryExists(_ path: String) -> Bool {
        var isDirectory: ObjCBool = false
        return FileManager.default.fileExists(atPath: expandingTilde(path), isDirectory: &isDirectory) && isDirectory.boolValue
    }

    /// A configured directory as a path on disk. amx lists it as written,
    /// and configuration may start it with `~`, which amx expands itself.
    static func expandingTilde(_ path: String) -> String {
        (path as NSString).expandingTildeInPath
    }

    /// The projects by profile, profiles in the order configuration first
    /// names them and projects in configuration order within each.
    static func grouped(_ projects: [ProjectEntry]) -> [(profile: String, projects: [ProjectEntry])] {
        var order: [String] = []
        var byProfile: [String: [ProjectEntry]] = [:]
        for project in projects {
            if byProfile[project.profile] == nil { order.append(project.profile) }
            byProfile[project.profile, default: []].append(project)
        }
        return order.map { ($0, byProfile[$0] ?? []) }
    }

    /// `amx project launch --title T --profile P`: the profile is always
    /// named, so a title shared across profiles is never ambiguous. A
    /// `directory` replaces the project's configured one for this launch.
    static func launchArguments(for project: ProjectEntry, directory: String? = nil) -> [String] {
        var arguments = ["project", "launch", "--title", project.title]
        if !project.profile.isEmpty { arguments += ["--profile", project.profile] }
        if let directory { arguments += ["--directory", directory] }
        return arguments
    }

    /// The same launch on the private tmux server, for the workspace to
    /// embed: what Launch Project runs when there is no Kitty to open a tab in.
    static func workspaceLaunchArguments(for project: ProjectEntry, directory: String? = nil) -> [String] {
        launchArguments(for: project, directory: directory) + ["--backend", "tmux"]
    }

    static func fetch(_ command: AMXCommand) -> Result<[ProjectEntry], AMXCommandError> {
        command.json([ProjectEntry].self, listArguments).result
    }
}
