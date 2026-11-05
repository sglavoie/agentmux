import Foundation
import Combine

struct ConversationRecap: Decodable, Equatable {
    struct Claim: Decodable, Equatable {
        let text: SecretText
        let messages: [Int]
    }
    let goal, latest, outstanding: Claim
    let generatedAt: String
    let profile, model: String
    let partial: Bool
    enum CodingKeys: String, CodingKey {
        case goal, latest, outstanding, profile, model, partial
        case generatedAt = "generated_at"
    }
}

struct ConversationRecapReport: Decodable, Equatable {
    let conversation, status: String
    let recap: ConversationRecap?
    let warning: String?
    let retryAfter: String?
    enum CodingKeys: String, CodingKey {
        case conversation, status, recap, warning
        case retryAfter = "retry_after"
    }
}

/// Each visible surface owns a selection. Cache and model-request deduplication
/// live in amx so History, the inspector and CLI share the same saved result.
@MainActor
final class WorkspaceRecapModel: ObservableObject {
    @Published private(set) var report: ConversationRecapReport?
    @Published private(set) var loading = false
    @Published private(set) var error: String?
    @Published private(set) var conversation: ConversationIdentity?
    private(set) var canGenerate = true
    /// Briefings read many saved recaps at once; only an explicit per-card
    /// request may generate one. Other surfaces keep selection generation.
    let automaticallyGenerate: Bool
    var command: () -> AMXCommand?
    var onEvidence: ((ConversationIdentity, Int) -> Void)?
    private let runner: ASLWorkRunner
    private var sequence = 0
    private var revision = ""
    private var sourceRetries = 0
    private var pending: DispatchWorkItem?
    /// Injectable debounce/poll scheduling keeps tests independent of wall time.
    var schedule: (TimeInterval, DispatchWorkItem) -> Void = { delay, work in
        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: work)
    }

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent(), automaticallyGenerate: Bool = true) {
        self.command = command; self.runner = runner; self.automaticallyGenerate = automaticallyGenerate
    }

    func select(_ identity: ConversationIdentity?, revision: String, canGenerate: Bool = true) {
        guard identity != conversation || revision != self.revision || canGenerate != self.canGenerate else { return }
        pending?.cancel(); sequence += 1; loading = false
        sourceRetries = 0
        if identity != conversation { report = nil }
        conversation = identity; self.revision = revision; self.canGenerate = canGenerate
        error = nil
        guard identity != nil else { return }
        read(cached: true)
    }

    func refresh() {
        guard conversation != nil, !loading, canGenerate else { return }
        pending?.cancel(); sequence += 1
        sourceRetries = 0
        read(cached: false, force: true)
    }

    /// Checks for completed work from another view/process while this selection
    /// remains visible. An unchanged ready result never starts another model.
    func check() {
        guard conversation != nil, !loading else { return }
        read(cached: true)
    }

    private func read(cached: Bool, force: Bool = false) {
        guard let identity = conversation else { return }
        guard let command = command() else { error = AMXWording.current; return }
        let generation = sequence
        loading = true; error = nil
        var args = ["session", "recap"]
        if cached { args.append("--cached") }
        if force { args.append("--refresh") }
        args += ["--", identity.encoded]
        let interval = WorkspaceResponsiveness.begin("Recap read")
        runner.run({ command.json(ConversationRecapReport.self, args, timeout: cached ? 30 : 125) }) { [weak self] result in
            defer { WorkspaceResponsiveness.end("Recap read", interval) }
            guard let self, self.sequence == generation, self.conversation == identity else { return }
            self.loading = false
            switch result {
            case let .failure(error): self.error = error.message
            case let .success(value), let .partial(value, _):
                guard value.conversation == identity.encoded else {
                    self.error = "The recap did not match the selected conversation."; return
                }
                self.report = value
                if value.status == "pending" {
                    self.enqueue(after: 2) { $0.check() }
                } else if cached, self.automaticallyGenerate, self.canGenerate, ["missing", "stale"].contains(value.status), value.retryAfter == nil {
                    self.enqueue(after: 0.4) { $0.read(cached: false) }
                } else if !cached, self.automaticallyGenerate, self.canGenerate, value.status == "stale", value.retryAfter == nil, self.sourceRetries == 0 {
                    // A stop hook can arrive before the transcript's final
                    // write. Retry that race once without making the user
                    // refresh; continuously changing input cannot loop bills.
                    self.sourceRetries += 1
                    self.enqueue(after: 1) { $0.check() }
                }
            }
        }
    }

    private func enqueue(after delay: TimeInterval, _ action: @escaping (WorkspaceRecapModel) -> Void) {
        pending?.cancel()
        let generation = sequence
        let item = DispatchWorkItem { [weak self] in
            guard let self, self.sequence == generation, self.conversation != nil else { return }
            action(self)
        }
        pending = item; schedule(delay, item)
    }

    func reveal(_ index: Int) {
        guard let conversation else { return }
        onEvidence?(conversation, index)
    }

    func close() {
        pending?.cancel(); sequence += 1
        conversation = nil; report = nil; error = nil; loading = false; revision = ""
    }
}
