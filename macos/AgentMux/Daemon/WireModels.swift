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

/// A daemon request: `{"type":…}` plus the type's own block.
struct DaemonRequest: Encodable, Equatable {
    let type: String

    static let health = DaemonRequest(type: "health")
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
