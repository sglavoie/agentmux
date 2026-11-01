import Foundation
import Combine

/// A fresh session from the selected session's project, with a launch-only
/// profile and directory. Reads and launches use the CLI's existing contracts.
@MainActor
final class WorkspaceNewSessionModel: ObservableObject {
    let source: WorkspaceRow
    @Published var profile = ""
    @Published var directory = ""
    @Published private(set) var defaultDirectory = ""
    @Published private(set) var profiles: [LaunchProfileChoice] = []
    @Published private(set) var project: ProjectEntry?
    @Published private(set) var loading = false
    @Published private(set) var launching = false
    @Published private(set) var error: String?
    @Published private(set) var submitted = false
    var onLaunched: ((String) -> Void)?
    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner

    init(source: WorkspaceRow, command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent()) {
        self.source = source; self.command = command; self.runner = runner
    }

    var launchDirectory: String {
        let typed = directory.trimmingCharacters(in: .whitespacesAndNewlines)
        if typed.isEmpty { return defaultDirectory }
        let expanded = ProjectCatalog.expandingTilde(typed)
        if (expanded as NSString).isAbsolutePath { return expanded }
        return URL(fileURLWithPath: defaultDirectory, isDirectory: true).appendingPathComponent(expanded).standardizedFileURL.path
    }

    var canLaunch: Bool {
        !loading && !launching && !submitted && project != nil && !launchDirectory.isEmpty
            && profiles.contains { $0.name == profile }
    }

    var arguments: [String] {
        guard let project else { return [] }
        return ["project", "launch", "--json", "--backend", "tmux", "--profile", project.profile,
                "--launch-profile", profile, "--directory", launchDirectory, "--", project.title]
    }

    func load() {
        guard !loading, !launching, !submitted, let id = source.embeddedRuntimeSessionID else { return }
        guard let command = command() else { error = AMXWording.current; return }
        loading = true; error = nil; project = nil
        runner.run({
            (ProjectCatalog.fetch(command), command.json([LaunchProfileChoice].self, ["config", "profiles"]).result,
             command.json(AttachCheck.self, ["attach", id]).result)
        }) { [weak self] projects, profiles, location in
            guard let self else { return }
            self.loading = false
            switch (projects, profiles, location) {
            case let (.success(projects), .success(profiles), .success(location)):
                guard location.runtimeSessionID == id, location.state == "live" else {
                    self.error = "The source session is no longer available. Select a live session and try again."
                    return
                }
                let candidates = projects.filter { $0.title == self.source.project }
                guard let project = candidates.first(where: { $0.profile == self.source.label }) ?? candidates.first else {
                    self.error = "This session's project is no longer configured. Add it in Settings before launching another session."
                    return
                }
                self.project = project; self.profiles = profiles
                self.defaultDirectory = ProjectCatalog.expandingTilde(location.directory.isEmpty ? project.directory : location.directory)
                if !profiles.contains(where: { $0.name == self.profile }) {
                    self.profile = profiles.first(where: { $0.name == self.source.label })?.name
                        ?? profiles.first(where: { $0.name == project.profile })?.name ?? profiles.first?.name ?? ""
                }
                if profiles.isEmpty { self.error = "No harness profiles are configured. Add a profile in Settings." }
            case let (.failure(error), _, _), let (_, .failure(error), _), let (_, _, .failure(error)):
                self.error = error.message
            }
        }
    }

    func launch() {
        guard canLaunch else { return }
        guard let command = command() else { error = AMXWording.current; return }
        let args = arguments
        launching = true; submitted = true; error = nil
        runner.run({ command.json(ProjectLaunchResult.self, args, timeout: AMXCommand.launchTimeout, mutates: true) }) { [weak self] result in
            guard let self else { return }
            self.launching = false
            switch result {
            case let .success(value), let .partial(value, _):
                guard !value.runtimeSessionID.isEmpty else {
                    self.error = "The launch returned no session ID. Check the session list before launching again."
                    return
                }
                self.onLaunched?(value.runtimeSessionID)
            case let .failure(error):
                self.error = error.message
                // A structured rejection or a failure to start can be retried.
                // A timeout or unreadable reply may have launched an agent.
                switch error {
                case .command, .launchFailed: self.submitted = false
                default: break
                }
            }
        }
    }
}
