import Foundation

/// The daemon line at the top of the menu: what the last health request found,
/// in a few words.
enum DaemonStatusLine {
    static func text(for status: DaemonStatus?) -> String {
        guard let status else { return "checking…" }
        switch status {
        case let .ready(build):
            return build.map { "ready · \($0.shortDescription)" } ?? "ready"
        case let .degraded(reason, _):
            guard let reason = reason?.trimmingCharacters(in: .whitespacesAndNewlines), !reason.isEmpty else {
                return "degraded"
            }
            return "degraded · \(reason)"
        case .notRunning:
            return "not running"
        case let .unresponsive(reason):
            return "unresponsive · \(reason)"
        }
    }

    /// The menu item's title.
    static func title(for status: DaemonStatus?) -> String {
        "Daemon: \(text(for: status))"
    }
}
