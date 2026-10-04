import Foundation

// The subset of the daemon wire (`internal/daemon/protocol`) the app reads.
// Decoding is tolerant: unknown fields are ignored, and every field a daemon
// may omit is optional, so an older or newer daemon still decodes.

/// `buildinfo.Info`: the artifact a daemon is running.
struct DaemonBuild: Decodable, Equatable {
    let version: String
    let revision: String?
    let dirty: Bool
    let buildTime: String?
    let goVersion: String?
    let binaryHash: String?

    init(version: String, revision: String? = nil, dirty: Bool = false, buildTime: String? = nil,
         goVersion: String? = nil, binaryHash: String? = nil) {
        self.version = version
        self.revision = revision
        self.dirty = dirty
        self.buildTime = buildTime
        self.goVersion = goVersion
        self.binaryHash = binaryHash
    }

    enum CodingKeys: String, CodingKey {
        case version, revision, dirty
        case buildTime = "build_time"
        case goVersion = "go_version"
        case binaryHash = "binary_hash"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        version = try c.decodeIfPresent(String.self, forKey: .version) ?? "dev"
        revision = try c.decodeIfPresent(String.self, forKey: .revision)
        dirty = try c.decodeIfPresent(Bool.self, forKey: .dirty) ?? false
        buildTime = try c.decodeIfPresent(String.self, forKey: .buildTime)
        goVersion = try c.decodeIfPresent(String.self, forKey: .goVersion)
        binaryHash = try c.decodeIfPresent(String.self, forKey: .binaryHash)
    }

    /// `0.8.0 (52189e529adf-dirty)`.
    var shortDescription: String {
        "\(version) (\(revisionLabel(revision: revision, dirty: dirty)))"
    }
}

/// A daemon request: `{"type":…}` plus the type's own block. A nil block is
/// left off the wire.
struct DaemonRequest: Encodable, Equatable {
    let type: String
    var subscribe: SubscribeParams?
    var focusWindow: FocusWindowParams?

    enum CodingKeys: String, CodingKey {
        case type, subscribe
        case focusWindow = "focus_window"
    }

    static let health = DaemonRequest(type: "health")

    static func subscribing(_ params: SubscribeParams) -> DaemonRequest {
        DaemonRequest(type: "subscribe", subscribe: params)
    }

    static func focusing(socket: String, windowID: Int) -> DaemonRequest {
        DaemonRequest(type: "focus_window", focusWindow: FocusWindowParams(kittySocket: socket, windowID: windowID))
    }
}

/// `protocol.SubscribeRequest`.
struct SubscribeParams: Encodable, Equatable {
    /// The companion protocol this app was written against.
    static let protocolVersion = 1
    /// What the app calls itself to health and doctor.
    static let clientName = "Agent Mux.app"

    var protocolVersion: Int = SubscribeParams.protocolVersion
    var client: String = SubscribeParams.clientName
    var clientVersion: String?
    var notificationBackend: Bool = false
    var notificationsAuthorized: Bool = false

    enum CodingKeys: String, CodingKey {
        case protocolVersion = "protocol"
        case client
        case clientVersion = "client_version"
        case notificationBackend = "notification_backend"
        case notificationsAuthorized = "notifications_authorized"
    }
}

/// `protocol.FocusWindowRequest`.
struct FocusWindowParams: Encodable, Equatable {
    let kittySocket: String
    let windowID: Int

    enum CodingKeys: String, CodingKey {
        case kittySocket = "kitty_socket"
        case windowID = "window_id"
    }
}

extension KeyedDecodingContainer {
    /// The value at `key`, or `fallback` when it is absent, null, or of a
    /// type this app does not expect. One malformed field degrades to its
    /// default instead of failing the whole document.
    func lenient<T: Decodable>(_ key: Key, _ fallback: T) -> T {
        ((try? decodeIfPresent(T.self, forKey: key)) ?? nil) ?? fallback
    }

    /// Like `lenient(_:_:)` for a field with no meaningful default.
    func lenient<T: Decodable>(_ key: Key) -> T? {
        (try? decodeIfPresent(T.self, forKey: key)) ?? nil
    }
}

/// The fields every daemon response shares, plus the companion blocks the app
/// reads.
struct DaemonResponse: Decodable, Equatable {
    let ok: Bool
    let error: String?
    let subscription: SubscriptionAck?
    let focused: FocusWindowResult?

    init(ok: Bool, error: String? = nil, subscription: SubscriptionAck? = nil, focused: FocusWindowResult? = nil) {
        self.ok = ok
        self.error = error
        self.subscription = subscription
        self.focused = focused
    }

    enum CodingKeys: String, CodingKey { case ok, error, subscription, focused }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        ok = c.lenient(.ok, false)
        error = c.lenient(.error)
        subscription = c.lenient(.subscription)
        focused = c.lenient(.focused)
    }
}

/// `protocol.SubscriptionAck`: the line that opens a stream.
struct SubscriptionAck: Decodable, Equatable {
    let protocolVersion: Int
    let serverProtocolMax: Int
    let subscriberID: String
    let heartbeatMS: Int64
    let features: [String]

    init(protocolVersion: Int = 1, serverProtocolMax: Int = 1, subscriberID: String = "", heartbeatMS: Int64 = 15000, features: [String] = []) {
        self.protocolVersion = protocolVersion
        self.serverProtocolMax = serverProtocolMax
        self.subscriberID = subscriberID
        self.heartbeatMS = heartbeatMS
        self.features = features
    }

    enum CodingKeys: String, CodingKey {
        case protocolVersion = "protocol"
        case serverProtocolMax = "server_protocol_max"
        case subscriberID = "subscriber_id"
        case heartbeatMS = "heartbeat_ms"
        case features
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        protocolVersion = c.lenient(.protocolVersion, 0)
        serverProtocolMax = c.lenient(.serverProtocolMax, 0)
        subscriberID = c.lenient(.subscriberID, "")
        heartbeatMS = c.lenient(.heartbeatMS, 0)
        features = c.lenient(.features, [])
    }
}

/// `protocol.FocusWindowResult`.
struct FocusWindowResult: Decodable, Equatable {
    let windowID: Int
    let tabID: Int
    let osWindowID: Int

    init(windowID: Int, tabID: Int = 0, osWindowID: Int = 0) {
        self.windowID = windowID
        self.tabID = tabID
        self.osWindowID = osWindowID
    }

    enum CodingKeys: String, CodingKey {
        case windowID = "window_id"
        case tabID = "tab_id"
        case osWindowID = "os_window_id"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        windowID = c.lenient(.windowID, 0)
        tabID = c.lenient(.tabID, 0)
        osWindowID = c.lenient(.osWindowID, 0)
    }
}

/// The fields of a `health` response the app reads.
struct HealthResponse: Decodable, Equatable {
    let ok: Bool
    let error: String?
    let ready: Bool
    let degradedReason: String?
    let pid: Int?
    let build: DaemonBuild?

    init(ok: Bool, error: String? = nil, ready: Bool, degradedReason: String? = nil, pid: Int? = nil, build: DaemonBuild? = nil) {
        self.ok = ok
        self.error = error
        self.ready = ready
        self.degradedReason = degradedReason
        self.pid = pid
        self.build = build
    }

    enum CodingKeys: String, CodingKey {
        case ok, error, ready, pid, build
        case degradedReason = "degraded_reason"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        ok = try c.decodeIfPresent(Bool.self, forKey: .ok) ?? false
        error = try c.decodeIfPresent(String.self, forKey: .error)
        ready = try c.decodeIfPresent(Bool.self, forKey: .ready) ?? false
        degradedReason = try c.decodeIfPresent(String.self, forKey: .degradedReason)
        pid = try c.decodeIfPresent(Int.self, forKey: .pid)
        build = try c.decodeIfPresent(DaemonBuild.self, forKey: .build)
    }
}
