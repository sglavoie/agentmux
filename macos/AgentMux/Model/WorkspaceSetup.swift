import Foundation
import Combine

struct WorkspaceSetupDetection: Decodable {
    struct Harness: Decodable {
        let harness: String
        let cliAvailable: Bool
        let cliPath: String?
        let home: String
        let homeExists: Bool
        enum CodingKeys: String, CodingKey {
            case harness, home
            case cliAvailable = "cli_available", cliPath = "cli_path", homeExists = "home_exists"
        }
        var name: String {
            switch harness {
            case "claude": return "Claude Code"
            case "codex": return "Codex"
            case "copilot": return "GitHub Copilot CLI"
            default: return harness
            }
        }
    }
    struct Project: Decodable {
        let directory: String
    }
    let harnesses: [Harness]
    let recentProjects: [Project]
    let warnings: [String]
    enum CodingKeys: String, CodingKey {
        case harnesses, warnings
        case recentProjects = "recent_projects"
    }
}

struct WorkspaceSetupResult: Decodable {
    let saved: Bool
    let hooksStatus: String
    let hooksError: String?
    let summary: String
    enum CodingKeys: String, CodingKey {
        case saved, summary
        case hooksStatus = "hooks_status", hooksError = "hooks_error"
    }
}

@MainActor
final class WorkspaceSetupModel: ObservableObject {
    @Published var harness = ""
    @Published var directory = ""
    @Published var syncHooks = true
    @Published private(set) var detection: WorkspaceSetupDetection?
    @Published private(set) var loading = false
    @Published private(set) var saving = false
    @Published private(set) var submitted = false
    @Published private(set) var saved = false
    @Published private(set) var error: String?
    var onSaved: (() -> Void)?
    var onFinished: (() -> Void)?
    var onManual: (() -> Void)?
    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner
    private var generation = 0

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent()) {
        self.command = command; self.runner = runner
    }

    var choices: [WorkspaceSetupDetection.Harness] {
        detection?.harnesses.filter { $0.cliAvailable || $0.homeExists } ?? []
    }
    var selected: WorkspaceSetupDetection.Harness? { choices.first { $0.harness == harness } }
    var directories: [String] {
        let recent = detection?.recentProjects.map(\.directory) ?? []
        return directory.isEmpty || recent.contains(directory) ? recent : [directory] + recent
    }
    var searchedCLIs: String {
        let names = detection?.harnesses.map(\.harness) ?? []
        return (names.isEmpty ? ["claude", "codex", "copilot"] : names).joined(separator: ", ")
    }
    var canSetUp: Bool {
        !loading && !saving && !submitted && selected != nil && (directory as NSString).isAbsolutePath
    }
    var arguments: [String] {
        guard let selected else { return [] }
        return ["config", "setup", "--json", "--harness", selected.harness,
                "--home", selected.home, "--directory", directory, "--backend", "keep"]
            + (syncHooks ? ["--sync-hooks"] : [])
    }

    func detect() {
        guard !loading, !saving, !submitted else { return }
        guard let command = command() else { error = AMXWording.current; return }
        generation += 1
        let expected = generation
        loading = true; error = nil
        runner.run({ command.json(WorkspaceSetupDetection.self, ["config", "setup", "--detect", "--json"], timeout: 30) }) { [weak self] result in
            guard let self, self.generation == expected else { return }
            self.loading = false
            switch result {
            case let .success(value):
                self.detection = value
                if self.selected == nil { self.harness = self.choices.first?.harness ?? "" }
                if self.directory.isEmpty { self.directory = value.recentProjects.first?.directory ?? "" }
            case let .failure(error): self.error = error.message
            case .partial: self.error = "Detection did not finish. Retry or set up manually."
            }
        }
    }

    func setUp() {
        guard canSetUp else { return }
        guard let command = command() else { error = AMXWording.current; return }
        let args = arguments
        saving = true; submitted = true; error = nil
        runner.run({ command.json(WorkspaceSetupResult.self, args, timeout: 60, mutates: true) }) { [weak self] result in
            guard let self else { return }
            self.saving = false
            switch result {
            case let .success(value): self.accept(value, succeeded: true)
            case let .partial(value, _): self.accept(value, succeeded: false)
            case let .failure(error):
                self.error = error.message
                // Only known pre-save refusals and failure to start are safe to retry.
                switch error {
                case .command, .launchFailed: self.submitted = false
                default:
                    self.error = "\(error.message)\nSetup may have saved configuration. Check Settings before trying setup again."
                }
            }
        }
    }

    private func accept(_ value: WorkspaceSetupResult, succeeded: Bool) {
        saved = value.saved
        if saved { onSaved?() }
        if succeeded && saved && value.hooksStatus != "failed" {
            onFinished?()
        } else {
            error = [saved ? "Configuration saved. Do not repeat setup; retry hook sync for this profile." : "Setup did not finish.",
                     value.hooksError ?? "", value.summary].filter { !$0.isEmpty }.joined(separator: "\n\n")
        }
    }

    func manual() {
        guard !saving else { return }
        close()
        onManual?()
    }

    func close() {
        guard !saving else { return }
        generation += 1
        loading = false
    }
}
