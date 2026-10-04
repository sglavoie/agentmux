import CryptoKit
import Foundation

/// Finds the `amx` binary the app runs, first match wins: the `UserDefaults`
/// override, the copy bundled in `Contents/Resources/bin`, `~/.local/bin/amx`
/// (where `just install` puts it), then `PATH`.
///
/// `~/.local/bin/amx` runs instead of a bundled copy when it is the artifact
/// the running daemon reports, since the CLI and daemon builds are meant to
/// match, or when it is newer. `built` is the commit time rather than the link
/// time, so two builds of one dirty tree tie on it; the tie goes to the local
/// copy only when its content differs and its file is the more recently
/// written.
struct AMXBinaryResolver {
    /// The `UserDefaults` key holding an explicit binary path.
    static let overrideDefaultsKey = "amxBinaryPath"

    enum Source: Equatable {
        case userDefaultsOverride
        case bundled
        case localBin
        /// `~/.local/bin/amx`, chosen over the bundled copy because it is the
        /// build the running daemon reports.
        case localBinMatchingDaemon
        /// `~/.local/bin/amx`, chosen over an older bundled copy.
        case newerLocalBin
        case path
    }

    struct Resolution: Equatable {
        let url: URL
        let source: Source
    }

    /// Nothing resolved. An override that names no executable fails here
    /// rather than quietly running some other `amx`.
    struct NotFound: Error, Equatable {
        /// The override that was set but does not name an executable file.
        let invalidOverride: String?
        /// Every candidate path checked, in order.
        let tried: [String]

        var message: String {
            var lines: [String] = []
            if let invalidOverride {
                lines.append("The amx binary override does not name an executable file:")
                lines.append("  \(invalidOverride)")
                lines.append("")
                lines.append("Fix it, or remove it to use the default search:")
                lines.append("  defaults delete \(AMXBinaryResolver.defaultsDomain) \(AMXBinaryResolver.overrideDefaultsKey)")
            } else {
                lines.append("Agent Mux.app could not find the amx binary. Looked in:")
                lines.append(contentsOf: tried.map { "  \($0)" })
                lines.append("")
                lines.append("Install it with `just install`, or point Agent Mux.app at one:")
                lines.append("  defaults write \(AMXBinaryResolver.defaultsDomain) \(AMXBinaryResolver.overrideDefaultsKey) /path/to/amx")
            }
            return lines.joined(separator: "\n")
        }
    }

    static let defaultsDomain = "io.github.sglavoie.agentmux"

    var overridePath: String?
    var bundleResourcesURL: URL?
    var homeDirectory: URL
    /// A colon-separated search path, normally the login shell's `PATH`.
    var searchPath: String
    /// What the running daemon reports in `health.build`; nil when no daemon
    /// answered.
    var daemonBuild: DaemonBuild?
    var isExecutableFile: (String) -> Bool = AMXBinaryResolver.isExecutableRegularFile
    /// A binary's `--version`; nil when it cannot say, which never displaces
    /// the bundled copy.
    var identity: (String) -> AMXVersion? = { _ in nil }
    /// The full lowercase sha256 of a file's content.
    var contentHash: (String) -> String? = AMXBinaryResolver.sha256
    var modificationDate: (String) -> Date? = AMXBinaryResolver.modificationDate

    /// The resolver for this process: its bundle and the standard defaults.
    static func live(
        searchPath: String,
        daemonBuild: DaemonBuild?,
        defaults: UserDefaults = .standard
    ) -> AMXBinaryResolver {
        AMXBinaryResolver(
            overridePath: defaults.string(forKey: overrideDefaultsKey),
            bundleResourcesURL: Bundle.main.resourceURL,
            homeDirectory: FileManager.default.homeDirectoryForCurrentUser,
            searchPath: searchPath,
            daemonBuild: daemonBuild,
            identity: { AMXVersion.read(binary: $0) }
        )
    }

    func resolve() -> Result<Resolution, NotFound> {
        if let raw = overridePath?.trimmingCharacters(in: .whitespacesAndNewlines), !raw.isEmpty {
            let path = expandingTilde(raw)
            guard isExecutableFile(path) else {
                return .failure(NotFound(invalidOverride: path, tried: [path]))
            }
            return .success(Resolution(url: URL(fileURLWithPath: path), source: .userDefaultsOverride))
        }

        var candidates: [(String, Source)] = []
        if let bundleResourcesURL {
            candidates.append((bundleResourcesURL.appendingPathComponent("bin/amx").path, .bundled))
        }
        candidates.append((localBinPath, .localBin))
        for dir in searchPath.split(separator: ":") where !dir.isEmpty {
            candidates.append((URL(fileURLWithPath: expandingTilde(String(dir))).appendingPathComponent("amx").path, .path))
        }

        var tried: [String] = []
        for (path, source) in candidates where !tried.contains(path) {
            tried.append(path)
            guard isExecutableFile(path) else { continue }
            if source == .bundled, let preferred = localBinPreferred(over: path) {
                return .success(Resolution(url: URL(fileURLWithPath: localBinPath), source: preferred))
            }
            return .success(Resolution(url: URL(fileURLWithPath: path), source: source))
        }
        return .failure(NotFound(invalidOverride: nil, tried: tried))
    }

    /// Why `~/.local/bin/amx` should run instead of `bundled`, or nil to keep
    /// the bundled copy.
    func localBinPreferred(over bundled: String) -> Source? {
        let local = localBinPath
        guard isExecutableFile(local), let localVersion = identity(local) else { return nil }
        if let daemonBuild, localVersion.matches(daemonBuild) {
            return .localBinMatchingDaemon
        }
        guard let bundledVersion = identity(bundled) else { return nil }
        switch (localVersion.built, bundledVersion.built) {
        case let (localBuilt?, bundledBuilt?) where localBuilt > bundledBuilt:
            return .newerLocalBin
        case let (localBuilt?, bundledBuilt?) where localBuilt < bundledBuilt:
            return nil
        case (nil, .some), (.some, nil):
            return nil
        default:
            // A tie on the commit time, or neither carries one.
            break
        }
        guard let localHash = contentHash(local), let bundledHash = contentHash(bundled),
              localHash != bundledHash,
              let localModified = modificationDate(local),
              let bundledModified = modificationDate(bundled),
              localModified > bundledModified else {
            return nil
        }
        return .newerLocalBin
    }

    var localBinPath: String {
        homeDirectory.appendingPathComponent(".local/bin/amx").path
    }

    private func expandingTilde(_ path: String) -> String {
        if path == "~" { return homeDirectory.path }
        if path.hasPrefix("~/") { return homeDirectory.appendingPathComponent(String(path.dropFirst(2))).path }
        return path
    }

    /// `isExecutableFile(atPath:)` alone accepts a searchable directory.
    static func isExecutableRegularFile(_ path: String) -> Bool {
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: path, isDirectory: &isDirectory), !isDirectory.boolValue else {
            return false
        }
        return FileManager.default.isExecutableFile(atPath: path)
    }

    static func sha256(_ path: String) -> String? {
        guard let handle = FileHandle(forReadingAtPath: path) else { return nil }
        defer { try? handle.close() }
        var hasher = SHA256()
        do {
            // `read(upToCount:)` answers nil, or empty data, at end of file.
            while let chunk = try handle.read(upToCount: 1 << 20), !chunk.isEmpty {
                hasher.update(data: chunk)
            }
        } catch {
            return nil
        }
        return hasher.finalize().map { String(format: "%02x", $0) }.joined()
    }

    static func modificationDate(_ path: String) -> Date? {
        (try? FileManager.default.attributesOfItem(atPath: path))?[.modificationDate] as? Date
    }
}
