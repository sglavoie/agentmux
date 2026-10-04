import AppKit

/// Brings the right Kitty to the front once the daemon has focused a window
/// inside it.
///
/// Kitty's remote control moves focus within Kitty but cannot make Kitty the
/// active application, and a menu bar click leaves this app holding
/// activation. So the app yields activation to Kitty and then asks Kitty to
/// activate, which is the cooperative-activation handshake macOS 14 requires
/// for one app to bring another forward.
enum KittyActivator {
    struct RunningKitty: Equatable {
        let pid: pid_t
        let bundleIdentifier: String?
    }

    /// Which Kitty process owns `socket`: the one its address names when that
    /// process is a running Kitty, else the only or first running Kitty; nil
    /// when none runs.
    static func target(socket: String, running: [RunningKitty]) -> pid_t? {
        let kitties = running.filter { $0.bundleIdentifier == KittyEndpoint.bundleIdentifier }
        if let pid = KittyEndpoint.pid(fromSocket: socket), kitties.contains(where: { $0.pid == pid }) {
            return pid
        }
        return kitties.first?.pid
    }

    /// Activates the Kitty that owns `socket`. Returns whether one was found.
    ///
    /// The cooperative handshake alone is not enough when this app was never
    /// active — a global shortcut pressed over another app, for one — so when
    /// the target is the only Kitty running it is also opened through
    /// `NSWorkspace` with `activates`, which brings a running app forward
    /// regardless. With several Kitty instances that call cannot say which
    /// one, so it is not made.
    @MainActor
    @discardableResult
    static func activate(socket: String) -> Bool {
        let apps = NSRunningApplication.runningApplications(withBundleIdentifier: KittyEndpoint.bundleIdentifier)
        let running = apps.map { RunningKitty(pid: $0.processIdentifier, bundleIdentifier: $0.bundleIdentifier) }
        guard let pid = target(socket: socket, running: running),
              let app = apps.first(where: { $0.processIdentifier == pid }) else {
            return false
        }
        NSApp.yieldActivation(to: app)
        app.activate()
        if shouldOpen(running: running), let url = app.bundleURL {
            let configuration = NSWorkspace.OpenConfiguration()
            configuration.activates = true
            NSWorkspace.shared.openApplication(at: url, configuration: configuration)
        }
        return true
    }

    /// Whether opening Kitty's bundle can only bring the intended instance
    /// forward: exactly one Kitty runs.
    static func shouldOpen(running: [RunningKitty]) -> Bool {
        running.filter { $0.bundleIdentifier == KittyEndpoint.bundleIdentifier }.count == 1
    }
}
