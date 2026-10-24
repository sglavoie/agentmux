import Foundation

enum WorkspaceConfirmationPolicy: String, CaseIterable {
    case followCLI = "follow-cli"
    case always, never
    var title: String {
        switch self {
        case .followCLI: return "Follow config.toml"
        case .always: return "Always — app override"
        case .never: return "Never — app override"
        }
    }
}

/// Embedded TUIs read this small file at action time, so changing the setting
/// affects running views without restarting them or touching the user's config.
final class WorkspaceConfirmationFile {
    static let environmentKey = "AMX_WORKSPACE_CONFIRMATION_FILE"
    let url: URL
    init(directory: URL = FileManager.default.temporaryDirectory) {
        url = directory.appendingPathComponent("amx-confirmation-" + UUID().uuidString)
    }
    func write(_ policy: WorkspaceConfirmationPolicy) throws {
        try Data(policy.rawValue.utf8).write(to: url, options: .atomic)
    }
    deinit { try? FileManager.default.removeItem(at: url) }
}

extension AppDelegate {
    /// Read immediately before a destructive action. Failure to read retains a
    /// confirmation; it never licenses a destructive action without one.
    func readWorkspaceConfirmation(_ done: @escaping (Bool) -> Void) {
        let policy = preferencesModel.preferences.confirmationPolicy
        guard policy == .followCLI else { done(policy == .always); return }
        guard let command else { done(true); return }
        struct Report: Decodable { let required: Bool }
        QueueWorkRunner.concurrent().run({ command.json(Report.self, ["config", "confirmation", "--json"]) }) { [weak self] result in
            guard let self else { return }
            guard self.preferencesModel.preferences.confirmationPolicy == policy else {
                self.readWorkspaceConfirmation(done); return
            }
            guard let current = self.command else { done(true); return }
            guard current.binary == command.binary, current.launchEnvironment == command.launchEnvironment else {
                self.readWorkspaceConfirmation(done); return
            }
            if case let .success(report) = result { done(report.required) }
            else { done(true) }
        }
    }
}
