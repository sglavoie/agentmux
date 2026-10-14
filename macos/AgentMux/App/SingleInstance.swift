import AppKit

/// One Agent Mux.app per login: a second copy (a double launch, or a build
/// started beside the installed app) would show a second status item and, once
/// notifications arrive, compete for the daemon's notification backend.
/// The second copy does not quit silently: it asks the running one to come
/// forward as a reopen would, and that one shows its workspace.
enum SingleInstance {
    /// Posted by a copy that found another running; the running copy answers
    /// it as it answers a reopen.
    static let reopenRequest = Notification.Name("io.github.sglavoie.agentmux.reopen")

    /// Asks the running copy of `bundleIdentifier` to come forward, and
    /// activates it.
    static func handOff(
        to others: [pid_t],
        bundleIdentifier: String,
        center: DistributedNotificationCenter = .default()
    ) {
        center.postNotificationName(reopenRequest, object: bundleIdentifier, userInfo: nil, deliverImmediately: true)
        for pid in others {
            NSRunningApplication(processIdentifier: pid)?.activate()
        }
    }

    /// Calls `reopen` whenever another copy of `bundleIdentifier` asks.
    @discardableResult
    static func observeReopenRequests(
        bundleIdentifier: String,
        center: DistributedNotificationCenter = .default(),
        reopen: @escaping @MainActor () -> Void
    ) -> NSObjectProtocol {
        center.addObserver(forName: reopenRequest, object: bundleIdentifier, queue: .main) { _ in
            MainActor.assumeIsolated { reopen() }
        }
    }

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
