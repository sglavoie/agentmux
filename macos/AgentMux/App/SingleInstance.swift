import AppKit

/// One Agent Mux.app per login: a second copy (a double launch, or a build
/// started beside the installed app) would show a second status item and, once
/// notifications arrive, compete for the daemon's notification backend.
enum SingleInstance {
    /// The process IDs of other running copies of `bundleIdentifier`.
    static func otherInstances(
        bundleIdentifier: String,
        currentPID: pid_t = ProcessInfo.processInfo.processIdentifier,
        running: (String) -> [pid_t] = { id in
            NSRunningApplication.runningApplications(withBundleIdentifier: id).map(\.processIdentifier)
        }
    ) -> [pid_t] {
        running(bundleIdentifier).filter { $0 != currentPID && $0 > 0 }
    }
}
