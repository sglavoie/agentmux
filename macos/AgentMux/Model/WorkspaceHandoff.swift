import Foundation
import Combine

/// The generated context is a point-in-time preview. Launch submits exactly
/// the operator's edited text, even if the source conversation has advanced.
@MainActor
final class WorkspaceHandoffModel: ObservableObject {
    static let title = "Continue with Another Agent…"
    let source: ConversationIdentity
    let preferredProfile: String
    @Published var profile = ""
    @Published var content = ""
    @Published var instruction = ""
    @Published private(set) var directory = ""
    @Published private(set) var profiles: [LaunchProfileChoice] = []
    @Published private(set) var loading = false
    @Published private(set) var launching = false
    @Published private(set) var submitted = false
    @Published private(set) var error: String?
    @Published private(set) var artifactPath = ""
    var onLaunched: ((String, String) -> Void)?
    var onSource: (() -> Void)?
    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner
    private var generation = 0

    struct Reply: Decodable {
        struct Details: Decodable {
            var content: String?
            var directory: String?
            var sourceHarness: String?
            var sourceSessionID: String?
            var runtimeSessionID: String?
            var path: String?
            var launched: Bool?
            enum CodingKeys: String, CodingKey {
                case content, directory, path, launched
                case sourceHarness = "source_harness", sourceSessionID = "source_session_id", runtimeSessionID = "runtime_session_id"
            }
        }
        var data: Details
        var message: String?
    }

    init(source: ConversationIdentity, preferredProfile: String = "", command: @escaping () -> AMXCommand?,
         runner: ASLWorkRunner = QueueWorkRunner.concurrent()) {
        self.source = source; self.preferredProfile = preferredProfile; self.command = command; self.runner = runner
    }

    var canLaunch: Bool {
        !loading && !launching && !submitted && !directory.isEmpty && source.isUsable
            && !content.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && content.utf8.count <= 1024 * 1024 && instruction.utf8.count <= 16 * 1024
            && profiles.contains { $0.name == profile }
    }

    func load() {
        guard !loading, !launching, !submitted, source.isUsable else { return }
        guard let command = command() else { error = AMXWording.current; return }
        generation += 1
        let request = generation, source = source
        loading = true; error = nil; directory = ""
        runner.run({
            (command.json([LaunchProfileChoice].self, ["config", "profiles"]).result,
             command.json(Reply.self, ["session", "handoff", "--preview", "--json", "--", source.encoded], timeout: 60))
        }) { [weak self] choices, preview in
            guard let self, request == self.generation else { return }
            self.loading = false
            switch (choices, preview) {
            case let (.success(profiles), .success(reply)):
                guard reply.data.sourceHarness == self.source.harness, reply.data.sourceSessionID == self.source.sessionID,
                      let content = reply.data.content, !content.isEmpty, let directory = reply.data.directory, !directory.isEmpty else {
                    self.error = "The handoff preview did not identify the selected conversation. Try loading it again."; return
                }
                self.profiles = profiles; self.content = content; self.directory = directory
                if !profiles.contains(where: { $0.name == self.profile }) {
                    self.profile = profiles.first(where: { $0.harness != self.source.harness })?.name
                        ?? profiles.first(where: { $0.name == self.preferredProfile })?.name ?? profiles.first?.name ?? ""
                }
                if profiles.isEmpty { self.error = "No agent profiles are configured. Add a profile in Settings." }
            case let (.failure(error), _), let (_, .failure(error)): self.error = error.message
            case (_, .partial): self.error = "The context preview is incomplete. Load it again before continuing."
            }
        }
    }

    func launch() {
        guard canLaunch else { return }
        guard let command = command() else { error = AMXWording.current; return }
        let source = source, profile = profile, content = content, instruction = instruction
        launching = true; submitted = true; error = nil
        runner.run({ () -> AMXCommandResult<Reply> in
            // Reviewed context stays out of argv and shell strings. A private temporary
            // directory protects the reviewed context until amx has read it.
            let temporary = FileManager.default.temporaryDirectory.appendingPathComponent("amx-handoff-" + UUID().uuidString)
            do {
                try FileManager.default.createDirectory(at: temporary, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
                defer { try? FileManager.default.removeItem(at: temporary) }
                let file = temporary.appendingPathComponent("context.md")
                try Data(content.utf8).write(to: file, options: .atomic)
                try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: file.path)
                return command.json(Reply.self, ["session", "handoff", "--json", "--backend", "tmux", "--profile", profile,
                    "--content-file", file.path, "--instruction", instruction, "--", source.encoded],
                    timeout: AMXCommand.launchTimeout, mutates: true)
            } catch {
                return .failure(.launchFailed(binary: command.binary.path, reason: "Could not prepare reviewed context: " + error.localizedDescription))
            }
        }) { [weak self] result in
            guard let self else { return }
            self.launching = false
            switch result {
            case let .success(reply):
                self.artifactPath = reply.data.path ?? ""
                guard reply.data.launched == true, let id = reply.data.runtimeSessionID, WorkspaceUtilityView.isRuntimeSessionID(id),
                      reply.data.sourceHarness == source.harness, reply.data.sourceSessionID == source.sessionID else {
                    self.error = "The launch did not confirm the receiving session. Check the session list before trying again."; return
                }
                self.onLaunched?(id, self.artifactPath)
            case let .partial(reply, _):
                self.artifactPath = reply.data.path ?? ""
                self.error = (reply.message ?? "The handoff could not confirm a launch.") + " Check the session list before launching again."
            case let .failure(error):
                self.error = error.message
                switch error {
                case .command, .launchFailed: self.submitted = false
                default: break
                }
            }
        }
    }

    func close() { generation += 1; loading = false }
}

/// Saved source links survive app relaunch and remain available in History
/// once the receiving agent reports its own conversation identity.
@MainActor
final class WorkspaceHandoffOrigins {
    struct Origin: Codable, Equatable {
        let source: String
        let artifactPath: String
        var identity: ConversationIdentity? { ConversationIdentity(encoded: source) }
    }
    private let defaults: UserDefaults
    private let key = "workspace.handoffOrigins.v1"
    private var origins: [String: Origin]
    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        origins = defaults.data(forKey: key).flatMap { try? JSONDecoder().decode([String: Origin].self, from: $0) } ?? [:]
    }
    func record(runtimeID: String, source: ConversationIdentity, artifactPath: String) {
        guard WorkspaceUtilityView.isRuntimeSessionID(runtimeID), source.isUsable else { return }
        origins["runtime:" + runtimeID] = Origin(source: source.encoded, artifactPath: artifactPath)
        save()
    }
    func observe(_ sessions: [CompanionSession]) {
        var changed = false
        for session in sessions {
            guard let origin = origins["runtime:" + (session.runtime?.sessionID ?? "")], let identity = session.conversation, identity.isUsable else { continue }
            let key = "conversation:" + identity.encoded
            if origins[key] != origin { origins[key] = origin; changed = true }
        }
        if changed { save() }
    }
    func origin(runtimeID: String = "", conversation: ConversationIdentity? = nil) -> Origin? {
        origins["runtime:" + runtimeID] ?? conversation.flatMap { origins["conversation:" + $0.encoded] }
    }
    private func save() { if let data = try? JSONEncoder().encode(origins) { defaults.set(data, forKey: key) } }
}
