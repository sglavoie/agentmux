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
    var snooze: SnoozeRequest?
    var disposition: DispositionParams?
    var asl: ASLRequestParams?

    enum CodingKeys: String, CodingKey {
        case type, subscribe, snooze, disposition, asl
        case focusWindow = "focus_window"
    }

    static let health = DaemonRequest(type: "health")

    static func subscribing(_ params: SubscribeParams) -> DaemonRequest {
        DaemonRequest(type: "subscribe", subscribe: params)
    }

    static func focusing(socket: String, windowID: Int) -> DaemonRequest {
        DaemonRequest(type: "focus_window", focusWindow: FocusWindowParams(kittySocket: socket, windowID: windowID))
    }

    static func snoozing(_ request: SnoozeRequest) -> DaemonRequest {
        DaemonRequest(type: "notification_snooze", snooze: request)
    }

    static func disposing(_ params: DispositionParams) -> DaemonRequest {
        DaemonRequest(type: "notification_disposition", disposition: params)
    }

    static let aslSnapshot = DaemonRequest(type: "asl_snapshot")

    static func aslQuestion(planDir: String, questionID: String) -> DaemonRequest {
        DaemonRequest(type: "asl_question", asl: ASLRequestParams(planDir: planDir, questionID: questionID))
    }

    static func aslAction(_ params: ASLRequestParams) -> DaemonRequest {
        DaemonRequest(type: "asl_action", asl: params)
    }
}

/// `protocol.DispositionRequest`: what became of one notification the app
/// was asked to show.
struct DispositionParams: Encodable, Equatable {
    let notificationID: String
    let disposition: String
    var error: String?

    enum CodingKeys: String, CodingKey {
        case notificationID = "notification_id"
        case disposition, error
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
    /// The amx the app runs, so doctor can tell it apart from the daemon's;
    /// omitted until one is resolved.
    var amxBuild: AMXBuildReport?
    var notificationBackend: Bool = false
    var notificationsAuthorized: Bool = false

    enum CodingKeys: String, CodingKey {
        case protocolVersion = "protocol"
        case client
        case clientVersion = "client_version"
        case amxBuild = "amx_build"
        case notificationBackend = "notification_backend"
        case notificationsAuthorized = "notifications_authorized"
    }
}

/// `buildinfo.Info` for the amx the app resolved, as subscribe reports it.
///
/// `amx --version` abbreviates both the revision and the sha256, and
/// `buildinfo.SameArtifact` compares hashes whole, so the hash here is always
/// the full one read from the executable itself: a printed prefix would never
/// equal the daemon's and would report every app as a mismatch.
struct AMXBuildReport: Encodable, Equatable {
    let version: String
    var revision: String?
    var dirty = false
    var buildTime: String?
    var goVersion: String?
    let binaryHash: String

    enum CodingKeys: String, CodingKey {
        case version, revision, dirty
        case buildTime = "build_time"
        case goVersion = "go_version"
        case binaryHash = "binary_hash"
    }

    /// The report for `version`, read from a binary whose content hashes to
    /// `binaryHash`; nil when either is missing, since a report without the
    /// hash would be compared on the abbreviated revision and never match.
    static func of(version: AMXVersion?, binaryHash: String?) -> AMXBuildReport? {
        guard let version, let hash = binaryHash?.lowercased(), !hash.isEmpty else { return nil }
        return AMXBuildReport(
            version: version.version,
            revision: version.revision,
            dirty: version.dirty,
            buildTime: version.built.map { ISO8601DateFormatter().string(from: $0) },
            goVersion: version.goVersion,
            binaryHash: hash
        )
    }

    /// The report for the executable at `binary`, hashing its content.
    static func of(binary: URL, version: AMXVersion?) -> AMXBuildReport? {
        guard version != nil else { return nil }
        return of(version: version, binaryHash: AMXBinaryResolver.sha256(binary.path))
    }
}

/// What the app's subscribe says about itself. The subscription thread reads
/// it on every (re)subscribe; the main thread updates it, and reconnects when
/// an update changed what the daemon would be told.
final class SubscribeRequestSource {
    let clientVersion: String?
    private let lock = NSLock()
    private var currentClaim = NotificationClaim()
    private var currentBuild: AMXBuildReport?

    init(clientVersion: String?) {
        self.clientVersion = clientVersion
    }

    var claim: NotificationClaim {
        get { lock.lock(); defer { lock.unlock() }; return currentClaim }
        set { lock.lock(); currentClaim = newValue; lock.unlock() }
    }

    var build: AMXBuildReport? {
        lock.lock(); defer { lock.unlock() }
        return currentBuild
    }

    /// Records the amx now selected, or nil when none is; true when that
    /// differs from what the last subscribe reported, so the caller has to
    /// subscribe again for the daemon's client record to follow.
    @discardableResult
    func report(build: AMXBuildReport?) -> Bool {
        lock.lock(); defer { lock.unlock() }
        guard build != currentBuild else { return false }
        currentBuild = build
        return true
    }

    func params() -> SubscribeParams {
        lock.lock(); defer { lock.unlock() }
        return SubscribeParams(
            clientVersion: clientVersion,
            amxBuild: currentBuild,
            notificationBackend: currentClaim.backend,
            notificationsAuthorized: currentClaim.authorized
        )
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
    let snooze: SnoozeState?
    let aslResult: ASLResult?

    init(ok: Bool, error: String? = nil, subscription: SubscriptionAck? = nil, focused: FocusWindowResult? = nil,
         snooze: SnoozeState? = nil, aslResult: ASLResult? = nil) {
        self.ok = ok
        self.error = error
        self.subscription = subscription
        self.focused = focused
        self.snooze = snooze
        self.aslResult = aslResult
    }

    enum CodingKeys: String, CodingKey {
        case ok, error, subscription, focused, snooze
        case aslResult = "asl_result"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        ok = c.lenient(.ok, false)
        error = c.lenient(.error)
        subscription = c.lenient(.subscription)
        focused = c.lenient(.focused)
        snooze = c.lenient(.snooze)
        aslResult = c.lenient(.aslResult)
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
