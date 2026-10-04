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
        return app.activate()
    }
}
