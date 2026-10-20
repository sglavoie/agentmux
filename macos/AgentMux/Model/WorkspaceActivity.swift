import Foundation
import Combine

struct WorkspaceActivityEntry: Decodable, Identifiable, Equatable {
    let id: String
    let at: String
    let kind: String
    let title: String
    let detail: String
    let event: String
    let harness: String
    let profile: String
    let sessionID: String
    let runtimeSessionID: String
    let runID: String
    let traceID: String
    enum CodingKeys: String, CodingKey {
        case id, at, kind, title, detail, event, harness, profile
        case sessionID = "session_id", runtimeSessionID = "runtime_session_id", runID = "run_id", traceID = "trace_id"
    }
    var conversation: ConversationIdentity? { ConversationIdentity(encoded: "\(harness):\(sessionID)") }
    var date: Date? { Self.date(at) }
    static func date(_ text: String) -> Date? {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter.date(from: text) ?? ISO8601DateFormatter().date(from: text)
    }
}

struct WorkspaceActivityReport: Decodable {
    let entries: [WorkspaceActivityEntry]
    let since: String
    let earliestAvailable: String
    let truncated: Bool
    let corruptLines: Int
    let matched: Int
    enum CodingKeys: String, CodingKey {
        case entries, since, truncated, matched
        case earliestAvailable = "earliest_available", corruptLines = "corrupt_lines"
    }
}

/// Exact IDs, rather than an event timestamp watermark, keep late-spooled records unread.
struct WorkspaceActivityReadState: Codable {
    var read: [String: Date] = [:]
    var lastMarkedAt: Date?
}

struct WorkspaceActivityReadStore {
    var load: () throws -> WorkspaceActivityReadState
    var save: (WorkspaceActivityReadState) throws -> Void

    static func file(_ url: URL) -> Self {
        Self(load: {
            guard FileManager.default.fileExists(atPath: url.path) else { return WorkspaceActivityReadState() }
            return try JSONDecoder().decode(WorkspaceActivityReadState.self, from: Data(contentsOf: url))
        }, save: { state in
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true,
                                                    attributes: [.posixPermissions: 0o700])
            try JSONEncoder().encode(state).write(to: url, options: .atomic)
            try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
        })
    }
    static var standard: Self {
        file(FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support/AgentMux/activity-read.json"))
    }
}

@MainActor
final class WorkspaceActivityModel: ObservableObject {
    private let reviewState: WorkspaceReviewState
    @Published var layout: WorkspaceReviewState.SidebarLayout { didSet { reviewState.activityLayout = layout } }
    @Published var unreadOnly = true
    @Published var days = 7
    @Published private(set) var reportRefreshedAt: Date?
    @Published private(set) var transcriptRefreshedAt: Date?
    @Published private(set) var report: WorkspaceActivityReport?
    @Published private(set) var readState = WorkspaceActivityReadState()
    @Published private(set) var selection: String?
    @Published private(set) var transcript: WorkspaceTranscript?
    @Published private(set) var loading = false
    @Published private(set) var reading = false
    @Published private(set) var error: String?
    @Published private(set) var readError: String?
    @Published private(set) var transcriptError: String?
    var onConversation: ((ConversationIdentity, String) -> Void)?
    func openConversation() {
        guard let entry = selected, let identity = entry.conversation else { return }
        onConversation?(identity, entry.at)
    }
    var onFocus: ((WorkspaceActivityEntry) -> Void)?
    private let command: () -> AMXCommand?
    private let runner: ASLWorkRunner
    private let readStore: WorkspaceActivityReadStore
    private var generation = 0
    private var transcriptGeneration = 0
    private var messageLimit = 50
    private struct ReadUndo {
        let state: WorkspaceActivityReadState
        let selection: String?
    }
    private var readUndo: ReadUndo?
    @Published private(set) var canUndoRead = false
    var canMarkSelectedRead: Bool {
        !loading && error == nil && selected.map { readState.read[$0.id] == nil } == true
    }
    var unreadResultCount: Int { visible.filter { readState.read[$0.id] == nil }.count }

    init(command: @escaping () -> AMXCommand?, runner: ASLWorkRunner = QueueWorkRunner.concurrent(), readStore: WorkspaceActivityReadStore = .standard,
         defaults: UserDefaults? = nil) {
        reviewState = WorkspaceReviewState(defaults: defaults)
        layout = reviewState.activityLayout
        self.command = command
        self.runner = runner
        self.readStore = readStore
        do { readState = try readStore.load() }
        catch { readError = "Could not load read markers. Events are shown as unread: \(error.localizedDescription)" }
    }

    var visible: [WorkspaceActivityEntry] { (report?.entries ?? []).filter { !unreadOnly || readState.read[$0.id] == nil } }
    var selected: WorkspaceActivityEntry? { report?.entries.first { $0.id == selection } }
    var canLoadEarlier: Bool { !reading && (transcript?.total ?? 0) > (transcript?.messages.count ?? 0) }
    var excerpt: WorkspaceTranscript.Message? {
        guard let date = selected?.date else { return nil }
        // An excerpt is supporting output available at the event's recorded time,
        // never newer output presented as though it caused an earlier event.
        return transcript?.messages.filter {
            $0.role == "assistant" && $0.timestamp.flatMap(WorkspaceActivityEntry.date).map { $0 <= date } == true
        }.max { ($0.timestamp.flatMap(WorkspaceActivityEntry.date) ?? .distantPast) < ($1.timestamp.flatMap(WorkspaceActivityEntry.date) ?? .distantPast) }
    }

    func refresh() {
        generation += 1
        let expected = generation
        guard let command = command() else { loading = false; error = AMXWording.current; return }
        loading = true
        error = nil
        let args = ["session", "activity", "--json", "--days", String(days), "--limit", "1000"]
        runner.run({ command.json(WorkspaceActivityReport.self, args, timeout: 20) }) { [weak self] result in
            guard let self, expected == self.generation else { return }
            self.loading = false
            switch result {
            case let .failure(error): self.error = error.message
            case let .success(report), let .partial(report, _):
                self.reportRefreshedAt = Date()
                self.report = report
                if !self.visible.contains(where: { $0.id == self.selection }) { self.select(self.visible.first?.id) }
            }
        }
    }

    func filterChanged() { if !visible.contains(where: { $0.id == selection }) { select(visible.first?.id) } }

    func markVisibleRead(now: Date = Date()) {
        markRead(visible.map(\.id), now: now)
    }

    func markSelectedRead(now: Date = Date()) {
        guard canMarkSelectedRead, let selection else { return }
        markRead([selection], now: now)
    }

    private func markRead(_ ids: [String], now: Date) {
        let unread = ids.filter { readState.read[$0] == nil }
        guard !loading, error == nil, !unread.isEmpty else { return }
        let undo = ReadUndo(state: readState, selection: selection)
        var next = readState
        // The backend supports at most 90 days. Retain IDs for that whole window;
        // pruning by mark time cannot hide a newly arriving old event.
        next.read = next.read.filter { $0.value >= now.addingTimeInterval(-91 * 86400) }
        for id in unread { next.read[id] = now }
        next.lastMarkedAt = now
        do {
            try readStore.save(next)
            readUndo = undo
            canUndoRead = true
            readState = next
            readError = nil
            filterChanged()
        } catch { readError = "Read markers were not saved: \(error.localizedDescription)" }
    }

    func undoMarkRead() {
        guard let undo = readUndo else { return }
        do {
            try readStore.save(undo.state)
            readState = undo.state
            readUndo = nil
            canUndoRead = false
            readError = nil
            if let id = undo.selection, visible.contains(where: { $0.id == id }) { select(id) }
            else { filterChanged() }
        } catch { readError = "Read markers could not be restored: \(error.localizedDescription). Try Undo again." }
    }

    func select(_ id: String?) {
        selection = id
        transcript = nil
        transcriptRefreshedAt = nil
        transcriptError = nil
        messageLimit = 50
        loadTranscript()
    }

    func loadEarlier() { guard canLoadEarlier else { return }; messageLimit += 100; loadTranscript() }

    func loadTranscript() {
        transcriptGeneration += 1
        let expected = transcriptGeneration
        reading = false
        guard let entry = selected, let identity = entry.conversation, let command = command() else { return }
        reading = true
        transcriptError = nil
        let args = ["session", "show", "--json", "--last", String(messageLimit), "--", identity.encoded]
        runner.run({ command.json(WorkspaceTranscript.self, args, timeout: 30) }) { [weak self] result in
            guard let self, expected == self.transcriptGeneration, self.selection == entry.id else { return }
            self.reading = false
            switch result {
            case let .failure(error): self.transcriptError = error.message
            case let .success(value), let .partial(value, _):
                guard value.session.identity == identity else { self.transcriptError = "The transcript identity did not match this event."; return }
                self.transcriptRefreshedAt = Date()
                self.transcript = value
            }
        }
    }

    func close() {
        generation += 1
        transcriptGeneration += 1
        loading = false
        reading = false
        transcript = nil
        transcriptRefreshedAt = nil
        selection = nil
    }
}
