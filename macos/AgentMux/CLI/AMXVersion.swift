import Foundation

/// What `amx --version` reports:
/// `amx version V (revision R[-dirty], built T, goX, sha256:H)`.
///
/// Every parenthesized part but the revision is optional, since `buildinfo`
/// omits what a build did not stamp. `built` is the VCS commit time
/// (`vcs.time`), not the moment the binary was linked, so two dirty builds of
/// one commit share it; `AMXBinaryResolver` breaks that tie itself.
struct AMXVersion: Equatable {
    /// The whole first line, for display.
    let line: String
    let version: String
    /// The abbreviated revision (at most 12 characters); nil for `unknown`.
    let revision: String?
    let dirty: Bool
    /// The commit time; nil for a build without a VCS stamp.
    let built: Date?
    let goVersion: String?
    /// The binary's sha256 as printed: an abbreviated lowercase prefix.
    let sha256Prefix: String?

    static let prefix = "amx version "

    static func parse(_ output: String) -> AMXVersion? {
        guard let line = output.split(whereSeparator: \.isNewline).first.map(String.init),
              line.hasPrefix(prefix) else {
            return nil
        }
        let rest = line.dropFirst(prefix.count)
        let version: String
        var parts: [Substring] = []
        if let open = rest.firstIndex(of: "("), let close = rest.lastIndex(of: ")"), open < close {
            version = rest[..<open].trimmingCharacters(in: .whitespaces)
            parts = rest[rest.index(after: open)..<close]
                .split(separator: ",")
                .map { $0.drop { $0 == " " } }
        } else {
            version = rest.trimmingCharacters(in: .whitespaces)
        }
        guard !version.isEmpty, !version.contains(" ") else { return nil }

        var revision: String?
        var dirty = false
        var built: Date?
        var goVersion: String?
        var sha256: String?
        for part in parts {
            if part.hasPrefix("revision ") {
                var value = String(part.dropFirst("revision ".count))
                if value.hasSuffix("-dirty") {
                    dirty = true
                    value.removeLast("-dirty".count)
                }
                revision = value == "unknown" || value.isEmpty ? nil : value
            } else if part.hasPrefix("built ") {
                built = ISO8601DateFormatter().date(from: String(part.dropFirst("built ".count)))
            } else if part.hasPrefix("sha256:") {
                let value = part.dropFirst("sha256:".count).lowercased()
                sha256 = value.isEmpty ? nil : value
            } else if part.hasPrefix("go") {
                goVersion = String(part)
            }
        }
        return AMXVersion(
            line: line, version: version, revision: revision, dirty: dirty,
            built: built, goVersion: goVersion, sha256Prefix: sha256
        )
    }

    /// `0.8.0 (52189e529adf-dirty)`: what the status line shows.
    var shortDescription: String {
        "\(version) (\(revisionLabel(revision: revision, dirty: dirty)))"
    }

    /// Whether this is the artifact a daemon reports in `health.build`. The
    /// content hash decides when both sides carry one; otherwise the version,
    /// revision, and dirty marker must all agree, as `buildinfo.SameArtifact`
    /// falls back to.
    func matches(_ build: DaemonBuild) -> Bool {
        if let sha256Prefix, let hash = build.binaryHash?.lowercased(), !hash.isEmpty {
            return hash.hasPrefix(sha256Prefix)
        }
        guard version == build.version, dirty == build.dirty else { return false }
        switch (revision, build.revision) {
        case (nil, nil):
            return true
        case let (mine?, theirs?):
            return theirs.hasPrefix(mine)
        default:
            return false
        }
    }

    /// Whether both are clean builds of one version and revision, so they were
    /// compiled from the same source and can differ only by what was done to
    /// the file afterwards, such as a code signature. A dirty build never
    /// qualifies: two of them share a revision without sharing their source.
    func sameCleanBuild(as other: AMXVersion) -> Bool {
        guard !dirty, !other.dirty, version == other.version,
              let revision, let otherRevision = other.revision else {
            return false
        }
        return revision.hasPrefix(otherRevision) || otherRevision.hasPrefix(revision)
    }

    /// Runs `binary --version`; nil when it fails, prints something else, or
    /// takes longer than `timeout`.
    static func read(binary: String, timeout: TimeInterval = 2) -> AMXVersion? {
        guard let output = ProcessOutput.run(binary, arguments: ["--version"], timeout: timeout),
              output.status == 0 else {
            return nil
        }
        return parse(String(decoding: output.stdout, as: UTF8.self))
    }
}

/// `52189e529adf-dirty`, or `unknown` without a revision, as `buildinfo` prints it.
func revisionLabel(revision: String?, dirty: Bool) -> String {
    var label = revision.map { String($0.prefix(12)) } ?? "unknown"
    if label.isEmpty { label = "unknown" }
    return dirty ? label + "-dirty" : label
}
