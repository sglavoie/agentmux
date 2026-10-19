import Foundation
import Combine

struct DaemonSettingsStatus: Decodable, Equatable {
    var known: Bool
    var changed: [String]
    var message: String
}

@MainActor
final class DaemonSettingsModel: ObservableObject {
    @Published private(set) var status: DaemonSettingsStatus?
    @Published private(set) var busy = false
    @Published private(set) var error: String?
    var command: () -> AMXCommand? = { nil }
    var restart: ((@escaping (Bool) -> Void) -> Void)?
    private let runner: ASLWorkRunner
    private var sequence = 0

    init(runner: ASLWorkRunner = QueueWorkRunner.concurrent()) { self.runner = runner }

    func refresh() { guard !busy else { return }; read(verifying: false) }

    private func read(verifying: Bool) {
        guard let amx = command() else { error = "amx is still being resolved."; busy = false; return }
        sequence += 1
        let request = sequence
        busy = true; error = nil
        runner.run({ amx.json(DaemonSettingsStatus.self, ["config", "daemon-status", "--json"]) }, then: { [weak self] result in
            guard let self, self.sequence == request else { return }
            self.busy = false
            guard let current = self.command(), current.binary == amx.binary,
                  current.launchEnvironment == amx.launchEnvironment else {
                self.error = "The amx binary or environment changed. Check settings again."
                return
            }
            switch result {
            case let .success(status):
                self.status = status
                if verifying && (!status.known || !status.changed.isEmpty) {
                    self.error = "Restart finished, but the saved settings have not been verified as applied. " + status.message
                }
            case let .failure(error): self.error = error.message
            case .partial: self.error = "The settings check did not complete. Check again before restarting."
            }
        })
    }

    var canRestart: Bool { !busy && error == nil && status?.known == true && status?.changed.isEmpty == false }

    func apply() {
        guard canRestart, let restart else { return }
        sequence += 1
        busy = true; error = nil
        restart { [weak self] succeeded in
            guard let self else { return }
            if succeeded { self.read(verifying: true) }
            else { self.busy = false; self.error = "Daemon restart failed. Check connection health, then check settings again." }
        }
    }
}
