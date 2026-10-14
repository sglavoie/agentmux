import Foundation

/// What became of Start Daemon. The app runs `amx daemon start` detached and
/// redials until the feed is live, the child exits with a failure, or the
/// deadline passes with the feed still down; only then does the "Starting"
/// notice give way to one that says it failed.
enum DaemonStart {
    static let startingNotice = "Starting the daemon…"
    static let failedNotice = "The daemon did not start — run amx doctor"
    static let restartingNotice = "Restarting the daemon…"

    /// Set for the child, so `amx daemon start` keeps its log in its own
    /// journal instead of copying every line to the standard error the app
    /// keeps in a file. A daemon that cannot start still writes why there.
    static let detachedEnvironment = ["AMX_DAEMON_DETACHED": "1"]

    /// How long the app waits for the feed before it says the start failed:
    /// as long as `amx daemon restart` waits, which covers a slow restore.
    static let deadline: TimeInterval = 10

    enum Verdict: Equatable {
        case waiting
        case started
        case failed
    }

    /// `exitCode` is the child's once it has exited, nil while it runs. A
    /// daemon that answers, even one too old to subscribe, has started.
    static func verdict(feed: FeedState, exitCode: Int32?, deadlinePassed: Bool) -> Verdict {
        switch feed {
        case .live, .daemonTooOld:
            return .started
        case .connecting, .notRunning, .reconnecting:
            if let exitCode, exitCode != 0 { return .failed }
            return deadlinePassed ? .failed : .waiting
        }
    }

    /// Where the child's standard error goes: one file, replaced on every
    /// start, in the app's caches. nil when there is no caches directory.
    static func stderrLog(fileManager: FileManager = .default) -> URL? {
        guard let caches = fileManager.urls(for: .cachesDirectory, in: .userDomainMask).first else { return nil }
        let directory = caches.appendingPathComponent(AMXBinaryResolver.defaultsDomain, isDirectory: true)
        guard (try? fileManager.createDirectory(at: directory, withIntermediateDirectories: true)) != nil else { return nil }
        return directory.appendingPathComponent("daemon-start.log")
    }

    /// The last few lines of what the child wrote, bounded, for the log.
    static func tail(_ data: Data, lines: Int = 5, characters: Int = 1024) -> String {
        let text = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        let last = text.split(whereSeparator: \.isNewline).suffix(lines).joined(separator: "\n")
        return String(last.suffix(characters))
    }
}
