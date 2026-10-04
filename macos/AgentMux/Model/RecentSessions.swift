import Foundation

/// One indexed conversation, as `amx session list --json` reports it.
struct RecentConversation: Decodable, Equatable, Hashable {
    var id = ""
    var harness = ""
    var profile = ""
    var project = ""
    var cwd = ""
    var lastAt: Date?
    var snippet = ""

    init(id: String, harness: String, profile: String = "", project: String = "", cwd: String = "",
         lastAt: Date? = nil, snippet: String = "") {
        self.id = id
        self.harness = harness
        self.profile = profile
        self.project = project
        self.cwd = cwd
        self.lastAt = lastAt
        self.snippet = snippet
    }

    enum CodingKeys: String, CodingKey {
        case id, harness, profile, project, cwd, snippet
        case lastAt = "last_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = c.lenient(.id, "")
        harness = c.lenient(.harness, "")
        profile = c.lenient(.profile, "")
        project = c.lenient(.project, "")
        cwd = c.lenient(.cwd, "")
        snippet = c.lenient(.snippet, "")
        lastAt = (c.lenient(.lastAt) as String?).flatMap(Self.parseTime)
    }

    /// The `harness:session-id` selector `amx session continue` takes.
    var selector: String { "\(harness):\(id)" }

    /// The conversation identity a live session reports.
    var identity: ConversationIdentity { ConversationIdentity(harness: harness, sessionID: id) }

    /// Go's RFC 3339 time, with or without fractional seconds; the zero time
    /// Go writes for "never" reads as nil.
    static func parseTime(_ text: String) -> Date? {
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let plain = ISO8601DateFormatter()
        guard let date = fractional.date(from: text) ?? plain.date(from: text) else { return nil }
        return date.timeIntervalSince1970 > 0 ? date : nil
    }
}

/// `amx session list --json`'s data.
struct SessionListData: Decodable, Equatable {
    var sessions: [RecentConversation] = []

    enum CodingKeys: String, CodingKey { case sessions }

    init(sessions: [RecentConversation] = []) { self.sessions = sessions }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        sessions = c.lenient(.sessions, [])
    }
}

/// `amx session continue --json`'s data: whether the conversation was
/// focused where it already runs or resumed in a new tab.
struct SessionContinueResult: Decodable, Equatable {
    var changed = false
    var message = ""
    var continuation = ""
    var tabID = 0
    var windowID = 0

    init(changed: Bool = true, message: String = "", continuation: String = "", tabID: Int = 0, windowID: Int = 0) {
        self.changed = changed
        self.message = message
        self.continuation = continuation
        self.tabID = tabID
        self.windowID = windowID
    }

    private struct Details: Decodable {
        var continuation: String?
        var tabID: Int?
        var windowID: Int?

        enum CodingKeys: String, CodingKey {
            case continuation
            case tabID = "tab_id"
            case windowID = "window_id"
        }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            continuation = c.lenient(.continuation)
            tabID = c.lenient(.tabID)
            windowID = c.lenient(.windowID)
        }
    }

    enum CodingKeys: String, CodingKey { case changed, message, data }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        changed = c.lenient(.changed, false)
        message = c.lenient(.message, "")
        let details: Details? = c.lenient(.data)
        continuation = details?.continuation ?? ""
        tabID = details?.tabID ?? 0
        windowID = details?.windowID ?? 0
    }
}

/// The closed conversations the Recent submenu offers, from
/// `amx session list --since <N>d --json`. The list refreshes the transcript
/// index, which can take a while on a large corpus, so it runs off the main
/// thread with its own timeout while the menu shows the previous result.
struct RecentSessions: Equatable {
    static let timeout: TimeInterval = 15
    /// A menu opened again this soon reuses the result it already has.
    static let minimumInterval: TimeInterval = 5

    var state = FetchState<[RecentConversation]>()

    func needsRefresh(now: Date) -> Bool { state.needsRefresh(now: now, maxAge: Self.minimumInterval) }

    static func listArguments(days: Int) -> [String] {
        ["session", "list", "--since", "\(max(1, days))d"]
    }

    static func continueArguments(for conversation: RecentConversation) -> [String] {
        ["session", "continue", conversation.selector]
    }

    /// The `limit` most recently active conversations, newest first.
    static func top(_ conversations: [RecentConversation], limit: Int) -> [RecentConversation] {
        let ordered = conversations.enumerated().sorted { lhs, rhs in
            switch (lhs.element.lastAt, rhs.element.lastAt) {
            case let (l?, r?) where l != r: return l > r
            case (_?, nil): return true
            case (nil, _?): return false
            default: return lhs.offset < rhs.offset
            }
        }
        return ordered.prefix(max(0, limit)).map(\.element)
    }

    static func fetch(_ command: AMXCommand, days: Int) -> Result<[RecentConversation], AMXCommandError> {
        command.json(SessionListData.self, listArguments(days: days), timeout: timeout).result.map(\.sessions)
    }
}
