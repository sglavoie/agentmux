import Foundation

// The companion stream's payloads (`internal/daemon/protocol/companion.go`).
// Every field decodes leniently: a field the daemon omits, or one this app
// does not expect the type of, falls back to its zero value, and fields the
// app does not know are ignored, so a newer daemon's additive growth never
// costs a snapshot.

/// `protocol.StreamFrame` without its payload: what a reader looks at first.
struct StreamFrameHeader: Decodable, Equatable {
    let v: Int
    let seq: UInt64
    let type: String

    enum CodingKeys: String, CodingKey { case v, seq, type }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        v = c.lenient(.v, 0)
        seq = c.lenient(.seq, 0)
        // A frame with no type is not a frame.
        type = try c.decode(String.self, forKey: .type)
    }
}

/// A frame's payload, decoded once the header named its type.
struct StreamFramePayload<T: Decodable>: Decodable {
    let data: T
}

/// `protocol.Bye`.
struct ByeFrame: Decodable, Equatable {
    let reason: String

    enum CodingKeys: String, CodingKey { case reason }

    init(reason: String) { self.reason = reason }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        reason = c.lenient(.reason, "")
    }
}

/// `protocol.NotificationRetract`.
struct NotificationRetractFrame: Decodable, Equatable {
    let notificationID: String
    let reason: String

    enum CodingKeys: String, CodingKey {
        case notificationID = "notification_id"
        case reason
    }

    init(notificationID: String, reason: String = "") {
        self.notificationID = notificationID
        self.reason = reason
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        notificationID = c.lenient(.notificationID, "")
        reason = c.lenient(.reason, "")
    }
}

/// `protocol.CompanionSnapshot`: everything the menu renders, replaced
/// wholesale on every snapshot frame.
struct CompanionSnapshot: Decodable, Equatable {
    var v: Int = 1
    var daemon = CompanionDaemon()
    var kitty = CompanionKitty()
    var notification = CompanionNotification()
    var dashboard = CompanionDashboard()
    var attention = CompanionAttention()
    var asl = CompanionASL()
    var sessions: [CompanionSession] = []
    var truncated = false

    init() {}

    enum CodingKeys: String, CodingKey {
        case v, daemon, kitty, notification, dashboard, attention, asl, sessions, truncated
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        v = c.lenient(.v, 0)
        daemon = c.lenient(.daemon, CompanionDaemon())
        kitty = c.lenient(.kitty, CompanionKitty())
        notification = c.lenient(.notification, CompanionNotification())
        dashboard = c.lenient(.dashboard, CompanionDashboard())
        attention = c.lenient(.attention, CompanionAttention())
        asl = c.lenient(.asl, CompanionASL())
        sessions = c.lenient(.sessions, [])
        truncated = c.lenient(.truncated, false)
    }
}

struct CompanionDaemon: Decodable, Equatable {
    var pid = 0
    var startedAt: Int64 = 0
    var build: DaemonBuild?
    var ready = false
    var degradedReason: String?

    init() {}

    enum CodingKeys: String, CodingKey {
        case pid, build, ready
        case startedAt = "started_at"
        case degradedReason = "degraded_reason"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        pid = c.lenient(.pid, 0)
        startedAt = c.lenient(.startedAt, 0)
        build = c.lenient(.build)
        ready = c.lenient(.ready, false)
        degradedReason = c.lenient(.degradedReason)
    }

    /// The daemon line's view of this daemon.
    var status: DaemonStatus {
        ready ? .ready(build) : .degraded(reason: degradedReason, build: build)
    }
}

struct CompanionKitty: Decodable, Equatable {
    var ready = false
    var stale = false
    var socket = ""
    var sockets: [String] = []

    init() {}

    enum CodingKeys: String, CodingKey { case ready, stale, socket, sockets }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        ready = c.lenient(.ready, false)
        stale = c.lenient(.stale, false)
        socket = c.lenient(.socket, "")
        sockets = c.lenient(.sockets, [])
    }
}

struct CompanionNotification: Decodable, Equatable {
    var backend = ""
    var appConnected = false
    var snoozedUntil: Int64 = 0
    var indefinite = false
    var quietHours = ""
    var quietNow = false

    init() {}

    enum CodingKeys: String, CodingKey {
        case backend, indefinite
        case appConnected = "app_connected"
        case snoozedUntil = "snoozed_until"
        case quietHours = "quiet_hours"
        case quietNow = "quiet_now"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        backend = c.lenient(.backend, "")
        appConnected = c.lenient(.appConnected, false)
        snoozedUntil = c.lenient(.snoozedUntil, 0)
        indefinite = c.lenient(.indefinite, false)
        quietHours = c.lenient(.quietHours, "")
        quietNow = c.lenient(.quietNow, false)
    }
}

struct CompanionDashboard: Decodable, Equatable {
    var open = false
    var kittySocket = ""
    var osWindowID = 0
    var tabID = 0
    var windowID = 0

    init() {}

    enum CodingKeys: String, CodingKey {
        case open
        case kittySocket = "kitty_socket"
        case osWindowID = "os_window_id"
        case tabID = "tab_id"
        case windowID = "window_id"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        open = c.lenient(.open, false)
        kittySocket = c.lenient(.kittySocket, "")
        osWindowID = c.lenient(.osWindowID, 0)
        tabID = c.lenient(.tabID, 0)
        windowID = c.lenient(.windowID, 0)
    }
}

struct CompanionAttention: Decodable, Equatable {
    var total = 0
    var stop = 0
    var key = 0
    var error = 0
    var question = 0
    var working = 0
    var unseen = 0

    init(total: Int = 0, stop: Int = 0, key: Int = 0, error: Int = 0, question: Int = 0, working: Int = 0, unseen: Int = 0) {
        self.total = total
        self.stop = stop
        self.key = key
        self.error = error
        self.question = question
        self.working = working
        self.unseen = unseen
    }

    enum CodingKeys: String, CodingKey { case total, stop, key, error, question, working, unseen }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        total = c.lenient(.total, 0)
        stop = c.lenient(.stop, 0)
        key = c.lenient(.key, 0)
        error = c.lenient(.error, 0)
        question = c.lenient(.question, 0)
        working = c.lenient(.working, 0)
        unseen = c.lenient(.unseen, 0)
    }
}

struct CompanionASL: Decodable, Equatable {
    var waiting: [CompanionASLWaiting] = []

    init() {}

    enum CodingKeys: String, CodingKey { case waiting }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        waiting = c.lenient(.waiting, [])
    }
}

struct CompanionASLWaiting: Decodable, Equatable {
    init() {}

    var runID = ""
    var planDir = ""
    var windowID = 0
    /// The runtime session the run belongs to, when the daemon knows one; a
    /// tmux session has no window ID to name it by.
    var runtimeSessionID = ""
    var unanswered = 0
    var latestQuestionID = ""
    var latestQuestionCreatedAt: Int64 = 0

    enum CodingKeys: String, CodingKey {
        case unanswered
        case runID = "run_id"
        case planDir = "plan_dir"
        case windowID = "window_id"
        case runtimeSessionID = "runtime_session_id"
        case latestQuestionID = "latest_question_id"
        case latestQuestionCreatedAt = "latest_question_created_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        runID = c.lenient(.runID, "")
        planDir = c.lenient(.planDir, "")
        windowID = c.lenient(.windowID, 0)
        runtimeSessionID = c.lenient(.runtimeSessionID, "")
        unanswered = c.lenient(.unanswered, 0)
        latestQuestionID = c.lenient(.latestQuestionID, "")
        latestQuestionCreatedAt = c.lenient(.latestQuestionCreatedAt, 0)
    }
}

/// `conversation.Identity`.
struct ConversationIdentity: Decodable, Equatable {
    var harness = ""
    var sessionID = ""

    init(harness: String = "", sessionID: String = "") {
        self.harness = harness
        self.sessionID = sessionID
    }

    enum CodingKeys: String, CodingKey {
        case harness
        case sessionID = "session_id"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        harness = c.lenient(.harness, "")
        sessionID = c.lenient(.sessionID, "")
    }
}

/// `protocol.CompanionRuntime`: the runtime session a listed session is, and
/// the backend target it runs on. Only a snapshot negotiated with the
/// `runtime_sessions` feature carries it.
struct CompanionRuntime: Decodable, Equatable {
    /// The backend whose sessions are windows of another app.
    static let kittyBackend = "kitty"

    var sessionID = ""
    var backend = ""
    var instance = ""
    var generation = ""
    var pane = ""

    init(sessionID: String = "", backend: String = "", pane: String = "") {
        self.sessionID = sessionID
        self.backend = backend
        self.pane = pane
    }

    enum CodingKeys: String, CodingKey {
        case backend, instance, generation, pane
        case sessionID = "session_id"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        sessionID = c.lenient(.sessionID, "")
        backend = c.lenient(.backend, "")
        instance = c.lenient(.instance, "")
        generation = c.lenient(.generation, "")
        pane = c.lenient(.pane, "")
    }
}

/// `protocol.CompanionSession`: one live managed session.
struct CompanionSession: Decodable, Equatable {
    var windowID = 0
    var tabID = 0
    var osWindowID = 0
    var kittySocket = ""
    var harness = ""
    var profile = ""
    var project = ""
    var baseTitle = ""
    var conversation: ConversationIdentity?
    var aslRunID = ""
    var iconState = ""
    var reason = ""
    var unseen = false
    var attention = false
    var muted = false
    var locked = false
    var looping = false
    var busySinceAt: Int64 = 0
    var lastEventAt: Int64 = 0
    var presentationChangedAtMS: Int64 = 0
    var branch = ""
    var dirty = false
    /// The background a session with no Kitty tab is listed on, "#rrggbb":
    /// the color picked for it, else its profile's, bright enough for
    /// black text. Empty for none.
    var tabColor = ""
    /// The name the operator gave a session with no Kitty tab
    /// (`amx name-session`). Empty for none.
    var name = ""
    /// The harness badge a session with no Kitty tab is listed with: its
    /// harness's configured emoji, or the one the icon picker (F5) put in
    /// its title. Empty for none.
    var harnessIcon = ""
    /// The session's runtime identity; nil for a Kitty session no runtime
    /// session claims, and on a v1 snapshot.
    var runtime: CompanionRuntime?

    init(windowID: Int = 0) {
        self.windowID = windowID
    }

    enum CodingKeys: String, CodingKey {
        case harness, profile, project, conversation, reason, unseen, attention, muted, locked, looping, branch, dirty, name
        case runtime
        case windowID = "window_id"
        case tabID = "tab_id"
        case osWindowID = "os_window_id"
        case kittySocket = "kitty_socket"
        case baseTitle = "base_title"
        case aslRunID = "asl_run_id"
        case iconState = "icon_state"
        case busySinceAt = "busy_since_at"
        case lastEventAt = "last_event_at"
        case presentationChangedAtMS = "presentation_changed_at_ms"
        case tabColor = "tab_color"
        case harnessIcon = "harness_icon"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        windowID = c.lenient(.windowID, 0)
        tabID = c.lenient(.tabID, 0)
        osWindowID = c.lenient(.osWindowID, 0)
        kittySocket = c.lenient(.kittySocket, "")
        harness = c.lenient(.harness, "")
        profile = c.lenient(.profile, "")
        project = c.lenient(.project, "")
        baseTitle = c.lenient(.baseTitle, "")
        conversation = c.lenient(.conversation)
        aslRunID = c.lenient(.aslRunID, "")
        iconState = c.lenient(.iconState, "")
        reason = c.lenient(.reason, "")
        unseen = c.lenient(.unseen, false)
        attention = c.lenient(.attention, false)
        muted = c.lenient(.muted, false)
        locked = c.lenient(.locked, false)
        looping = c.lenient(.looping, false)
        busySinceAt = c.lenient(.busySinceAt, 0)
        lastEventAt = c.lenient(.lastEventAt, 0)
        presentationChangedAtMS = c.lenient(.presentationChangedAtMS, 0)
        branch = c.lenient(.branch, "")
        dirty = c.lenient(.dirty, false)
        tabColor = c.lenient(.tabColor, "")
        name = c.lenient(.name, "")
        harnessIcon = c.lenient(.harnessIcon, "")
        runtime = c.lenient(.runtime)
    }

    /// The runtime session whose terminal the workspace embeds: one on a
    /// backend other than Kitty. A Kitty session, with or without a runtime
    /// session, is a window of Kitty's and is focused there.
    var embeddedRuntimeSessionID: String? {
        guard let runtime, !runtime.sessionID.isEmpty, !runtime.backend.isEmpty,
              runtime.backend != CompanionRuntime.kittyBackend else { return nil }
        return runtime.sessionID
    }

    /// When the session last changed what it shows, falling back to its last
    /// hook event; nil when the daemon reported neither.
    var changedAt: Date? {
        if presentationChangedAtMS > 0 {
            return Date(timeIntervalSince1970: TimeInterval(presentationChangedAtMS) / 1000)
        }
        if lastEventAt > 0 {
            return Date(timeIntervalSince1970: TimeInterval(lastEventAt))
        }
        return nil
    }

    /// The working icons, the same set the daemon counts as `working`.
    static let workingStates: Set<String> = ["bolt", "subagent", "compact"]

    var isWorking: Bool { Self.workingStates.contains(iconState) }
}
