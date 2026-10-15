import Foundation
import Combine

enum WorkspaceHistoryScope: String, CaseIterable {
    case all = "All history"
    case project = "This project"
    case conversation = "This conversation"
}

struct WorkspaceHistoryEntry: Decodable, Equatable, Identifiable {
    let conversation: RecentConversation
    let preview: SecretText
    let matches: Int?
    var id: String { conversation.selector }

    init(_ conversation: RecentConversation) {
        self.conversation = conversation
        preview = SecretText(conversation.snippet)
        matches = nil
    }

    enum CodingKeys: String, CodingKey { case preview, matches }
    init(from decoder: Decoder) throws {
        conversation = try RecentConversation(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        preview = try c.decodeIfPresent(SecretText.self, forKey: .preview) ?? SecretText(conversation.snippet)
        matches = try c.decodeIfPresent(Int.self, forKey: .matches)
    }
}

struct WorkspaceHistorySearch: Decodable {
    let results: [WorkspaceHistoryEntry]
}

struct WorkspaceTranscript: Decodable, Equatable {
    struct Message: Decodable, Equatable {
        let role: String
        let text: SecretText
        let timestamp: String?
    }
    let session: RecentConversation
    let messages: [Message]
    let total: Int

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        session = try c.decode(RecentConversation.self, forKey: .session)
        messages = try c.decodeIfPresent([Message].self, forKey: .messages) ?? []
        total = try c.decodeIfPresent(Int.self, forKey: .total) ?? messages.count
    }

    enum CodingKeys: String, CodingKey {
        case session, messages
        case total = "messages_total"
    }
}

/// All process work is injected and run away from the UI. Separate generations
/// prevent a late search or transcript from replacing a newer selection.
@MainActor
final class WorkspaceHistoryModel: ObservableObject {
    @Published var query = ""
    @Published var scope = WorkspaceHistoryScope.all
    @Published var includeAutomated = false
    @Published private(set) var entries: [WorkspaceHistoryEntry] = []
    @Published private(set) var selection: String?
    @Published private(set) var transcript: WorkspaceTranscript?
    @Published private(set) var loading = false
    @Published private(set) var reading = false
    @Published private(set) var acting = false
    @Published private(set) var error: String?
    @Published private(set) var transcriptError: String?
    @Published private(set) var actionNotice: String?
    @Published var liveConversations: Set<String> = []
    @Published private(set) var project = ""
    @Published private(set) var currentConversation: ConversationIdentity?
    var onAction: ((RecentConversation, Bool, @escaping (String?) -> Void) -> Void)?

    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner
    private var searchGeneration = 0
    private var transcriptGeneration = 0
    private var messageLimit = 50
    private var pendingSearch: DispatchWorkItem?

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent()) {
        self.command = command
        self.runner = runner
    }

    var selected: WorkspaceHistoryEntry? { entries.first { $0.id == selection } }
    var canAct: Bool { selected != nil && !acting && !loading && error == nil }
    var canLoadEarlier: Bool { (transcript?.total ?? 0) > (transcript?.messages.count ?? 0) && !reading }

    func open(project: String, conversation: ConversationIdentity?) {
        self.project = project
        currentConversation = conversation
        if scope == .project && project.isEmpty || scope == .conversation && conversation == nil { scope = .all }
        search()
    }

    func scheduleSearch() {
        pendingSearch?.cancel()
        // Invalidate old reads immediately, before the debounce elapses.
        searchGeneration += 1
        loading = true
        let pending = DispatchWorkItem { [weak self] in self?.search() }
        pendingSearch = pending
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.25, execute: pending)
    }

    static func arguments(query: String, project: String?, includeAutomated: Bool) -> [String] {
        let query = query.trimmingCharacters(in: .whitespacesAndNewlines)
        var args = ["session", query.isEmpty ? "list" : "search", "--json"]
        if includeAutomated { args.append("--all") }
        if let project, !project.isEmpty {
            // --project is a CSV flag. Quote one title even if it contains a comma.
            args += ["--project", "\"" + project.replacingOccurrences(of: "\"", with: "\"\"") + "\""]
        }
        if !query.isEmpty { args += ["--", query] }
        return args
    }

    func search() {
        pendingSearch?.cancel()
        searchGeneration += 1
        let generation = searchGeneration
        error = nil
        guard let command = command() else {
            loading = false
            error = "amx is unavailable. Check Settings and try again."
            return
        }
        guard scope != .conversation || currentConversation != nil, scope != .project || !project.isEmpty else {
            loading = false
            error = "Select a session in the workspace before using this search scope."
            return
        }
        loading = true
        let wanted = scope == .conversation ? currentConversation : nil
        let query = query.trimmingCharacters(in: .whitespacesAndNewlines)
        let args = Self.arguments(query: query, project: scope == .project ? project : nil, includeAutomated: includeAutomated)
        runner.run({ () -> Result<[WorkspaceHistoryEntry], AMXCommandError> in
            if query.isEmpty {
                return command.json(SessionListData.self, args, timeout: 30).result.map { $0.sessions.map(WorkspaceHistoryEntry.init) }
            }
            return command.json(WorkspaceHistorySearch.self, args, timeout: 30).result.map(\.results)
        }) { [weak self] result in
            guard let self, generation == self.searchGeneration else { return }
            self.loading = false
            switch result {
            case let .failure(error): self.error = error.message
            case let .success(entries):
                self.entries = entries.filter { wanted == nil || $0.conversation.identity == wanted }
                let keep = self.selection.flatMap { id in self.entries.contains { $0.id == id } ? id : nil }
                self.select(keep ?? self.entries.first?.id)
            }
        }
    }

    func select(_ id: String?) {
        selection = id
        transcript = nil
        transcriptError = nil
        messageLimit = 50
        loadTranscript()
    }

    func loadEarlier() {
        guard canLoadEarlier else { return }
        messageLimit += 100
        loadTranscript()
    }

    func loadTranscript() {
        transcriptGeneration += 1
        let generation = transcriptGeneration
        reading = false
        guard let selected, let command = command() else { return }
        reading = true
        transcriptError = nil
        let args = ["session", "show", "--json", "--last", String(messageLimit), "--", selected.id]
        runner.run({ command.json(WorkspaceTranscript.self, args, timeout: 30) }) { [weak self] result in
            guard let self, generation == self.transcriptGeneration, self.selection == selected.id else { return }
            self.reading = false
            switch result {
            case let .failure(error): self.transcriptError = error.message
            case let .success(value), let .partial(value, _): self.transcript = value
            }
        }
    }

    func act(fork: Bool) {
        guard canAct, let selected, let onAction else { return }
        acting = true
        actionNotice = nil
        onAction(selected.conversation, fork) { [weak self] problem in
            self?.acting = false
            self?.actionNotice = problem ?? (fork ? "Fork opened in the workspace." : "Conversation opened.")
        }
    }

    func close() {
        pendingSearch?.cancel()
        searchGeneration += 1
        transcriptGeneration += 1
        loading = false
        reading = false
        transcript = nil
        entries = []
        selection = nil
    }
}
