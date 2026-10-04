import AppKit
import os

final class AppDelegate: NSObject, NSApplicationDelegate, StatusItemDelegate {
    private static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "app")

    /// How often the daemon line is refreshed while the menu is closed.
    static let healthInterval: TimeInterval = 10

    private(set) var statusItem: StatusItemController?
    private var preferencesController: PreferencesWindowController?
    private var healthTimer: Timer?

    /// Health requests and amx runs block, so they run here, one at a time.
    private let work = DispatchQueue(label: "io.github.sglavoie.agentmux.work", qos: .utility)
    private var healthInFlight = false

    private(set) var daemonStatus: DaemonStatus?
    private(set) var preferences = PreferencesSnapshot()
    /// Set once the binary is resolved; amx commands run through it.
    private(set) var command: AMXCommand?

    /// A hosted test bundle launches this app too; it must not start the
    /// status item, spawn amx, or quit because the installed copy is running.
    static var isRunningTests: Bool {
        ProcessInfo.processInfo.environment["XCTestConfigurationFilePath"] != nil
    }

    func applicationWillFinishLaunching(_ notification: Notification) {
        // LSUIElement already keeps the app out of the Dock; this states it
        // for a build run straight from DerivedData.
        NSApp.setActivationPolicy(.accessory)
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        guard !Self.isRunningTests else { return }
        if let identifier = Bundle.main.bundleIdentifier {
            let others = SingleInstance.otherInstances(bundleIdentifier: identifier)
            if !others.isEmpty {
                Self.logger.notice("another Agent Mux.app is running (pid \(others.map(String.init).joined(separator: ","), privacy: .public)); exiting")
                NSApp.terminate(nil)
                return
            }
        }

        statusItem = StatusItemController(delegate: self)
        refreshDaemonStatus()
        let timer = Timer(timeInterval: Self.healthInterval, repeats: true) { [weak self] _ in
            self?.refreshDaemonStatus()
        }
        RunLoop.main.add(timer, forMode: .common)
        healthTimer = timer
        resolveBinary()
    }

    func applicationWillTerminate(_ notification: Notification) {
        healthTimer?.invalidate()
    }

    // MARK: Daemon health

    func refreshDaemonStatus() {
        guard !healthInFlight else { return }
        healthInFlight = true
        work.async { [weak self] in
            let status = DaemonClient().health()
            DispatchQueue.main.async {
                guard let self else { return }
                self.healthInFlight = false
                if status != self.daemonStatus {
                    Self.logger.notice("daemon: \(DaemonStatusLine.text(for: status), privacy: .public)")
                }
                self.daemonStatus = status
                self.preferences.daemonStatus = status
                self.statusItem?.refresh()
                self.preferencesController?.update(self.preferences)
            }
        }
    }

    // MARK: amx

    /// Reads the login shell's environment, asks the daemon which build it
    /// runs, resolves amx against both, and probes what it can do. The shell
    /// can take seconds, so none of it runs on the main thread.
    private func resolveBinary() {
        work.async { [weak self] in
            let environment = LaunchEnvironment.current()
            let build = DaemonClient().health().build
            let resolution = AMXBinaryResolver.live(searchPath: environment.path, daemonBuild: build).resolve()
            var snapshot = PreferencesSnapshot()
            var command: AMXCommand?
            switch resolution {
            case let .success(binary):
                let amx = AMXCommand(binary: binary.url, launchEnvironment: environment)
                command = amx
                snapshot.binary = binary
                snapshot.binaryVersion = AMXVersion.read(binary: binary.url.path)
                snapshot.capabilities = AMXCapabilities.probe(amx)
                Self.logger.notice("running \(binary.url.path, privacy: .public) (\(String(describing: binary.source), privacy: .public))")
            case let .failure(notFound):
                snapshot.binaryProblem = notFound.message
                Self.logger.error("\(notFound.message, privacy: .public)")
            }
            DispatchQueue.main.async {
                guard let self else { return }
                self.command = command
                snapshot.daemonStatus = self.daemonStatus
                self.preferences = snapshot
                self.preferencesController?.update(snapshot)
            }
        }
    }

    // MARK: StatusItemDelegate

    @objc func showPreferences(_ sender: Any?) {
        let controller = preferencesController ?? PreferencesWindowController()
        preferencesController = controller
        controller.update(preferences)
        controller.show()
    }
}
