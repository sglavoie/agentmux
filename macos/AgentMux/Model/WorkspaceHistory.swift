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
    let start: Int
    let matchMessages: [Int]
    let matchTerms: [String]
    let focusMessage: Int?

    var end: Int { start + messages.count }

    init(session: RecentConversation, messages: [Message], total: Int, start: Int,
         matchMessages: [Int], matchTerms: [String], focusMessage: Int?) {
        self.session = session; self.messages = messages; self.total = total; self.start = start
        self.matchMessages = matchMessages; self.matchTerms = matchTerms; self.focusMessage = focusMessage
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        session = try c.decode(RecentConversation.self, forKey: .session)
        messages = try c.decodeIfPresent([Message].self, forKey: .messages) ?? []
        total = try c.decodeIfPresent(Int.self, forKey: .total) ?? messages.count
        start = try c.decodeIfPresent(Int.self, forKey: .start) ?? max(0, total - messages.count)
        matchMessages = try c.decodeIfPresent([Int].self, forKey: .matchMessages) ?? []
        matchTerms = try c.decodeIfPresent([String].self, forKey: .matchTerms) ?? []
        focusMessage = try c.decodeIfPresent(Int.self, forKey: .focusMessage)
    }

    enum CodingKeys: String, CodingKey {
        case session, messages
        case total = "messages_total"
        case start = "messages_start"
        case matchMessages = "match_messages"
        case matchTerms = "match_terms"
        case focusMessage = "focus_message"
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
    @Published private(set) var resultsQuery = ""
    @Published private(set) var matchIndex = 0
    @Published private(set) var scrollRequest: ScrollRequest?
    struct ScrollRequest: Equatable { let sequence: Int; let message: Int }
    private var scrollSequence = 0
    private var loadedQuery = ""
    private var pendingSearch: DispatchWorkItem?
    private let reviewState: WorkspaceReviewState
    private(set) var visibleMessage: Int?

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent(), defaults: UserDefaults? = nil) {
        self.command = command
        self.runner = runner
        reviewState = WorkspaceReviewState(defaults: defaults)
        if let saved = reviewState.history {
            query = saved.query
            scope = WorkspaceHistoryScope(rawValue: saved.scope) ?? .all
            includeAutomated = saved.automated
            project = saved.project
            currentConversation = saved.conversation.flatMap(ConversationIdentity.init(encoded:))
            selection = saved.selection
            visibleMessage = saved.message.flatMap { $0 >= 0 ? $0 : nil }
            loadedQuery = saved.resultsQuery ?? saved.query
        }
    }

    func rememberVisibleMessage(_ message: Int?) {
        guard let message, let transcript, (transcript.start..<transcript.end).contains(message) else { return }
        visibleMessage = message
        saveReview()
    }

    private func saveReview() {
        reviewState.history = .init(query: query, scope: scope.rawValue, automated: includeAutomated,
                                    project: project, conversation: currentConversation?.encoded,
                                    selection: selection, message: visibleMessage, resultsQuery: loadedQuery)
    }

    var selected: WorkspaceHistoryEntry? { entries.first { $0.id == selection } }
    var canAct: Bool { selected != nil && !acting && !loading && error == nil }
    var canLoadEarlier: Bool { (transcript?.start ?? 0) > 0 && !reading }
    var canLoadLater: Bool { guard let transcript else { return false }; return transcript.end < transcript.total && !reading }
    var matchingMessages: [Int] { transcript?.matchMessages ?? [] }

    func moveMatch(_ delta: Int) {
        guard !reading, !matchingMessages.isEmpty else { return }
        matchIndex = (matchIndex + delta + matchingMessages.count) % matchingMessages.count
        let message = matchingMessages[matchIndex]
        if let transcript, (transcript.start..<transcript.end).contains(message) { scroll(to: message) }
        else { loadTranscript() }
    }

    private func scroll(to message: Int) {
        scrollSequence += 1
        scrollRequest = ScrollRequest(sequence: scrollSequence, message: message)
    }

    func open(project: String, conversation: ConversationIdentity?) {
        if !project.isEmpty { self.project = project }
        if let conversation { currentConversation = conversation }
        if scope == .project && self.project.isEmpty || scope == .conversation && currentConversation == nil { scope = .all }
        search()
    }

    func scheduleSearch() {
        pendingSearch?.cancel()
        // Invalidate old reads immediately, before the debounce elapses.
        searchGeneration += 1
        transcriptGeneration += 1
        reading = false
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
        saveReview()
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
                self.resultsQuery = query
                self.entries = entries.filter { wanted == nil || $0.conversation.identity == wanted }
                let keep = self.selection.flatMap { id in self.entries.contains { $0.id == id } ? id : nil }
                self.select(keep ?? self.entries.first?.id)
            }
        }
    }

    func select(_ id: String?) {
        if selection == id, loadedQuery == resultsQuery, transcript != nil { return }
        let anchor = selection == id && loadedQuery == resultsQuery ? visibleMessage : nil
        selection = id
        visibleMessage = anchor
        loadedQuery = resultsQuery
        transcript = nil
        transcriptError = nil
        actionNotice = nil
        matchIndex = 0
        scrollRequest = nil
        saveReview()
        if let anchor { readWindow(from: max(0, anchor - 20), anchor: anchor) }
        else { loadTranscript() }
    }

    func loadEarlier(anchor: Int? = nil) {
        guard canLoadEarlier, let transcript else { return }
        readWindow(from: max(0, transcript.start - 100), count: min(100, transcript.start),
                   retaining: transcript, anchor: anchor ?? transcript.start)
    }

    func loadLater(anchor: Int? = nil) {
        guard canLoadLater, let transcript else { return }
        readWindow(from: transcript.end, count: 100, retaining: transcript, anchor: anchor ?? max(transcript.start, transcript.end - 1))
    }

    func loadTranscript() { readWindow() }

    private func readWindow(from: Int? = nil, count: Int = 50, retaining: WorkspaceTranscript? = nil, anchor: Int? = nil) {
        transcriptGeneration += 1
        let generation = transcriptGeneration
        reading = false
        guard let selected, let command = command() else { return }
        reading = true
        transcriptError = nil
        var args = ["session", "show", "--json"]
        if !loadedQuery.isEmpty { args += ["--find", loadedQuery, "--match", String(matchIndex + 1)] }
        if let from { args += ["--from", String(from), "--count", String(count)] }
        else if loadedQuery.isEmpty { args += ["--last", "50"] }
        args += ["--", selected.id]
        runner.run({ command.json(WorkspaceTranscript.self, args, timeout: 30) }) { [weak self] result in
            guard let self, generation == self.transcriptGeneration, self.selection == selected.id else { return }
            self.reading = false
            switch result {
            case let .failure(error): self.transcriptError = error.message
            case let .success(value), let .partial(value, _):
                if let retaining, value.end == retaining.start || retaining.end == value.start {
                    let messages = value.start < retaining.start ? value.messages + retaining.messages : retaining.messages + value.messages
                    self.transcript = WorkspaceTranscript(session: value.session, messages: messages, total: value.total,
                        start: min(value.start, retaining.start), matchMessages: value.matchMessages,
                        matchTerms: value.matchTerms, focusMessage: value.focusMessage)
                } else { self.transcript = value }
                if let focus = value.focusMessage, let index = value.matchMessages.firstIndex(of: focus) { self.matchIndex = index }
                if let anchor, value.total > 0 { self.scroll(to: min(anchor, value.total - 1)) }
                else if let focus = value.focusMessage { self.scroll(to: focus) }
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
        saveReview()
        pendingSearch?.cancel()
        searchGeneration += 1
        transcriptGeneration += 1
        loading = false
        reading = false
        transcript = nil
        entries = []
    }
}
