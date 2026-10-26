import AppKit
import SwiftUI
import os
import UserNotifications

/// The app: the menu bar item, the workspace window, Settings, the answer
/// panels, and the banners, over one daemon connection and one amx.
///
/// Its parts live beside it: what it owns outright is a type of its own
/// (`DaemonConnection`, `AMXLookup`, `SnoozeController`, `ConfigWatcher`),
/// and the rest is grouped by surface in `AppDelegate+….swift` extensions.
final class AppDelegate: NSObject, NSApplicationDelegate, StatusItemDelegate, WorkspaceDelegate {
    lazy var reviewFeedbackHistory: WorkspaceFeedbackHistory = {
        let history = WorkspaceFeedbackHistory(defaults: Self.isRunningTests ? nil : .standard)
        history.canOpen = { [weak self] destination in
            guard let self, self.command != nil else { return false }
            switch destination {
            case .conversation: return self.historyWindow?.model.acting != true
            case .checkout: return self.changesWindow?.model.landing != true
            case .runs: return self.runsWindow?.model.acting != true
            case .plan: return self.runsWindow?.plan.launching != true && self.runsWindow?.plan.checking != true
            }
        }
        history.open = { [weak self] destination in self?.inspectSavedFeedback(destination) }
        return history
    }()
    nonisolated static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "app")

    var statusItem: StatusItemController?
    var preferencesController: PreferencesWindowController?
    /// The ASL answer panel, made the first time a question is answered.
    var aslPanel: ASLQuestionPanelController?
    var promptPanel: PromptAnswerPanelController?
    var historyWindow: WorkspaceHistoryWindow?
    var resumePanel: WorkspaceResumePanel?
    var conversationSearchSequence = 0
    var activityWindow: WorkspaceActivityWindow?
    var runsWindow: WorkspaceRunsWindow?
    var changesWindow: WorkspaceChangesWindow?
    var paneChanges: WorkspaceChangesModel?
    /// The workspace window, made the first time it is shown.
    var workspace: WorkspaceWindowController?
    /// Set once the app is quitting, so the windows it takes down with it are
    /// not recorded as closed by the operator.
    var isTerminating = false
    /// Session status questions asked before amx was resolved.
    var statusQuestions: [(String, @MainActor (WorkspaceSessionStatus) -> Void)] = []
    let preferencesModel = PreferencesModel()
    var dashboardHotKey: HotKeyCenter?
    var attentionHotKey: HotKeyCenter?
    var cycler = AttentionCycler()
    /// The session Next Waiting Session last went to: the workspace reveals
    /// it in the sidebar when it shows it, as Reveal in Sidebar (⌘J) does.
    var revealOnShow: String?
    /// True while a Preferences recorder listens for a shortcut.
    var isRecordingHotKey = false
    var wakeObserver: NSObjectProtocol?
    var activationObserver: NSObjectProtocol?
    var becameActiveObserver: NSObjectProtocol?
    /// The Kitty the operator used last, where new tabs open.
    var lastActiveKittyPID: pid_t?

    var projects = ProjectCatalog()
    var workspaceProjectMenu: NSMenu?
    var recent = RecentSessions()
    /// Lists the projects again when `config.toml` changes.
    let workspaceConfirmationFile = WorkspaceConfirmationFile()
    var configWatcher: ConfigWatcher?

    var workspaceVisible = false
    var settingsVisible = false
    var historyVisible = false
    var activityVisible = false
    var runsVisible = false
    var changesVisible = false

    /// Shows the daemon's notification frames; nil under tests.
    var notifications: NotificationPresenter?
    /// What the next subscribe carries: the notification claim and the
    /// resolved amx build, read on the subscription thread.
    let subscribeRequest = SubscribeRequestSource(clientVersion: AppDelegate.appVersion)
    /// Dispositions are reported here, in order, so `delivered` always
    /// reaches the daemon before the click that follows it.
    let dispositionQueue = DispatchQueue(label: "io.github.sglavoie.agentmux.dispositions", qos: .userInitiated)

    /// Focus and snooze requests run here: each is one short round-trip on
    /// the daemon's socket, so a click never waits behind an amx subprocess,
    /// a health check, or a launch.
    let focusRunner = QueueWorkRunner(queue: DispatchQueue(label: "io.github.sglavoie.agentmux.focus", qos: .userInitiated))
    /// The quick amx commands (a session's status, naming, mute, the state
    /// keys, open-dashboard) run here, one at a time, so that two mutes of
    /// one session land in the order they were clicked, and an amx that takes
    /// its full ten seconds never holds up a focus.
    let amxRunner = QueueWorkRunner(queue: DispatchQueue(label: "io.github.sglavoie.agentmux.amx", qos: .userInitiated))
    /// Launches and continues run here, one at a time: each can take up to
    /// two minutes, and none of them should hold up a click.
    let launchRunner = QueueWorkRunner(queue: DispatchQueue(label: "io.github.sglavoie.agentmux.launch", qos: .userInitiated))
    /// Ending a session and restarting the daemon run here, one at a time,
    /// apart from launches: neither should wait minutes behind a launch that
    /// has nothing to do with it.
    let controlRunner = QueueWorkRunner(queue: DispatchQueue(label: "io.github.sglavoie.agentmux.control", qos: .userInitiated))
    /// The project and Recent lists, the waiting context, and conversation
    /// search load here, side by side, so a slow transcript refresh never
    /// holds up a health check or a click.
    let listRunner = QueueWorkRunner(queue: DispatchQueue(label: "io.github.sglavoie.agentmux.lists", qos: .utility, attributes: .concurrent))
    /// The amx commands that change something and are running now, by what
    /// the quit confirmation calls them. Quitting closes their pipes, and amx
    /// can die of it halfway through.
    var runningChanges: [Int: String] = [:]
    var nextChange = 0

    let store = SessionStore()
    var preferences = PreferencesSnapshot()
    /// Set once the binary is resolved; amx commands run through it.
    var command: AMXCommand?
    /// Finds that amx, and asks it what it offers.
    let lookup = AMXLookup()
    /// The companion feed, and the health checks while it is not live.
    private(set) lazy var connection = DaemonConnection(store: store, request: subscribeRequest.params, control: controlRunner)
    private(set) lazy var snoozes = SnoozeController(store: store, preferences: preferencesModel, runner: focusRunner)

    /// The embedded terminal runtime, made the first time a terminal is
    /// wanted: a companion used only beside Kitty never starts Ghostty.
    var terminals: Ghostty.App?

    /// A hosted test bundle launches this app too; it must not start the
    /// status item, spawn amx, or quit because the installed copy is running.
    nonisolated static var isRunningTests: Bool {
        ProcessInfo.processInfo.environment["XCTestConfigurationFilePath"] != nil
    }

    /// The app's own version, as subscribe reports it.
    nonisolated static var appVersion: String? {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String
    }

    /// AppKit defaults Ghostty's macOS app turns off at launch, as registered
    /// defaults so a value the user set still wins. Press-and-hold shows the
    /// accent picker instead of repeating a held key, which a terminal wants
    /// repeated; AutoFill's heuristics watch every text view and cost a
    /// terminal heavy CPU on macOS 26 and later.
    static let launchDefaults: [String: Any] = [
        "ApplePressAndHoldEnabled": false,
        "NSAutoFillHeuristicControllerEnabled": false,
    ]

    func applicationWillFinishLaunching(_ notification: Notification) {
        UserDefaults.standard.register(defaults: Self.launchDefaults)
        // Settings and the answer panels need the Edit menu's keys (⌘C, ⌘V,
        // ⌘Z, ...) and ⌘W from the start, not once the workspace was opened.
        // Before the activation policy: an app that starts regular with no
        // main menu is given AppKit's default one, which `install` keeps,
        // and the workspace's keys would then reach no menu.
        if !Self.isRunningTests { WorkspaceMenu.install(target: self) }
        // LSUIElement already keeps the app out of the Dock; this states it
        // for a build run straight from DerivedData. A launch that reopens
        // the workspace starts regular instead: becoming regular once the
        // app is already active puts it last in ⌘Tab, behind the app it
        // was launched from, and its window does not come forward.
        NSApp.setActivationPolicy(Self.launchActivationPolicy(
            reopensWorkspace: !Self.isRunningTests && launchReopensWorkspace && !isSecondInstance))
        // One workspace window and Settings: neither is a tab of the other,
        // whatever "Prefer tabs" says, and View has no tab bar items.
        NSWindow.allowsAutomaticWindowTabbing = false
        // The delegate must be in place before launch finishes, so a banner
        // clicked while the app was not running still reaches it.
        if !Self.isRunningTests { setUpNotifications() }
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        guard !Self.isRunningTests else { return }
        if let identifier = Bundle.main.bundleIdentifier {
            let others = SingleInstance.otherInstances(bundleIdentifier: identifier)
            if !others.isEmpty {
                Self.logger.notice("another Agent Mux.app is running (pid \(others.map(String.init).joined(separator: ","), privacy: .public)); asking it to come forward and exiting")
                SingleInstance.handOff(to: others, bundleIdentifier: identifier)
                NSApp.terminate(nil)
                return
            }
            SingleInstance.observeReopenRequests(bundleIdentifier: identifier) { [weak self] in
                self?.showWorkspace()
            }
        }

        statusItem = StatusItemController(delegate: self)
        setUpPreferences()
        setUpHotKeys()
        store.onChange = { [weak self] in self?.storeChanged() }
        connection.delegate = self
        lookup.delegate = self
        snoozes.onFailure = { [weak self] title, message, banner in self?.actionFailed(title, message, banner: banner) }
        // The first subscribe waits for amx to be resolved, so it reports the
        // build doctor compares; a slow login shell holds it up no longer
        // than this, and the daemon line is polled meanwhile (every
        // `DaemonConnection.healthInterval`, only while the feed is not live).
        DispatchQueue.main.asyncAfter(deadline: .now() + Self.firstSubscribeWait) { [weak self] in
            MainActor.assumeIsolated { self?.startSubscription() }
        }
        refreshDaemonStatus()
        // Coming back to the app is when a permission changed in System
        // Settings shows; nothing polls it.
        becameActiveObserver = NotificationCenter.default.addObserver(
            forName: NSApplication.didBecomeActiveNotification, object: nil, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated { self?.notifications?.refreshAuthorization() }
        }
        wakeObserver = NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.didWakeNotification, object: nil, queue: .main
        ) { [weak self] _ in
            // The daemon's socket is local, so it does not go half-open while
            // both ends sleep: a stream that is still good is kept (one that
            // is not ends at its EOF, or by the watchdog, which does not
            // count the sleep). A feed waiting out a retry dials now.
            MainActor.assumeIsolated {
                guard let self, self.store.feed != .live else { return }
                Self.logger.notice("woke from sleep; dialing the feed")
                self.connection.reconnectIfWaiting()
            }
        }
        trackActiveKitty()
        resolveBinary()
        restoreWorkspace()
    }

    /// Comes back to the workspace as the app last left it: open if it was
    /// open (after a quit, a crash, or an upgrade alike), on the session or
    /// view it was on. Nothing is started for it: a session that is no longer
    /// running is reported, not replaced.
    func restoreWorkspace() {
        let restoration = WorkspaceRestoration.load()
        guard launchReopensWorkspace else { return }
        Self.logger.notice("workspace: reopening at launch (was open: \(restoration.windowOpen), place: \(restoration.place?.encoded ?? "none", privacy: .public))")
        let controller = workspaceController()
        controller.show()
        if restoration.fullScreen { controller.enterFullScreen() }
        if let layout = restoration.layout {
            controller.restoreLayout(layout, conversation: restoration.conversation)
        } else if let place = restoration.place {
            controller.restore(place, conversation: restoration.conversation)
        } else {
            if !controller.state.rows.isEmpty { controller.showDashboardUnlessPlaced() }
        }
    }

    var launchReopensWorkspace: Bool {
        WorkspaceRestoration.load().windowOpen || preferencesModel.preferences.openWorkspaceAtLaunch
    }

    /// A copy that hands off to the one already running and quits never
    /// shows a window, so it never earns a Dock icon.
    var isSecondInstance: Bool {
        guard let identifier = Bundle.main.bundleIdentifier else { return false }
        return !SingleInstance.otherInstances(bundleIdentifier: identifier).isEmpty
    }

    nonisolated static func launchActivationPolicy(reopensWorkspace: Bool) -> NSApplication.ActivationPolicy {
        activationPolicy(workspace: reopensWorkspace, settings: false)
    }

    /// The app was opened again while running (from the Dock, Finder,
    /// Spotlight, or `open`): that asks for its window. With only Settings
    /// open, as any app with a window open does, it brings that forward: the
    /// Dock icon is there for Settings.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        guard !Self.isRunningTests else { return false }
        // With only review windows open, the first of them comes forward.
        if !workspaceVisible && !settingsVisible {
            let reviews: [(Bool, ReviewWindowController?)] = [(activityVisible, activityWindow), (runsVisible, runsWindow),
                                                              (changesVisible, changesWindow), (historyVisible, historyWindow)]
            if let open = reviews.first(where: { $0.0 && $0.1 != nil })?.1 {
                open.window?.makeKeyAndOrderFront(nil)
                return false
            }
        }
        switch Self.reopenTarget(workspace: workspaceVisible, settings: settingsVisible) {
        case .workspace: showWorkspace()
        case .settings: showPreferences(nil)
        }
        return false
    }

    enum ReopenTarget: Equatable { case workspace, settings }

    nonisolated static func reopenTarget(workspace: Bool, settings: Bool) -> ReopenTarget {
        settings && !workspace ? .settings : .workspace
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        var running = runningChanges.sorted { $0.key < $1.key }.map(\.value)
        if historyWindow?.model.acting == true { running.append("opening a conversation") }
        if runsWindow?.model.pendingWork == true { running.append("an ASL control operation") }
        if runsWindow?.plan.launching == true { running.append("starting a supervised plan") }
        if changesWindow?.model.landing == true || paneChanges?.landing == true { running.append("landing a worktree") }
        let unsent = aslPanel?.flow.unsent ?? (drafts: 0, sending: false)
        let reply = ASLQuestionFlow.quitQuestion(drafts: unsent.drafts, sending: unsent.sending)
        guard running.isEmpty, reply == nil else {
            Self.logger.notice("quit asked while amx is \(running.joined(separator: ", "), privacy: .public), \(unsent.drafts) unsent repl(ies), sending \(unsent.sending); asking")
            // Asked after this event, once AppKit has been told the answer
            // is coming.
            DispatchQueue.main.async { [weak self] in
                MainActor.assumeIsolated {
                    guard let self, running.isEmpty || self.askToQuitWhileChanging(running),
                          reply.map({ self.askToQuit($0.title, $0.message) }) ?? true else {
                        return sender.reply(toApplicationShouldTerminate: false)
                    }
                    if !self.confirmViewsBeforeQuit(sender) {
                        self.isTerminating = true
                        sender.reply(toApplicationShouldTerminate: true)
                    }
                }
            }
            return .terminateLater
        }
        if confirmViewsBeforeQuit(sender) { return .terminateLater }
        isTerminating = true
        return .terminateNow
    }

    /// One sheet names every shell, editor, or pager whose program would be
    /// lost; a session's terminal only detaches, so it never asks. True when
    /// it asked, in which case the answer goes to `sender` from here.
    func confirmViewsBeforeQuit(_ sender: NSApplication) -> Bool {
        workspace?.confirmQuit { [weak self] confirmed in
            // A window that closes because the app is quitting was open:
            // the next launch reopens it.
            if confirmed { self?.isTerminating = true }
            sender.reply(toApplicationShouldTerminate: confirmed)
        } ?? false
    }

    /// Whether to quit while amx is still launching, continuing, ending, or
    /// restarting something: quitting closes its pipes, and amx can die of
    /// it halfway, leaving a session started but never shown, or half ended.
    func askToQuitWhileChanging(_ running: [String]) -> Bool {
        askToQuit(Self.quitWhileChangingQuestion(running),
                  "Quitting now can stop it halfway. Wait for it to finish, or quit anyway and check the session list afterwards.")
    }

    func askToQuit(_ question: String, _ message: String) -> Bool {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = question
        alert.informativeText = message
        alert.addButton(withTitle: "Quit Anyway").hasDestructiveAction = true
        alert.addButton(withTitle: "Cancel")
        NSApp.activate()
        return alert.runModal() == .alertFirstButtonReturn
    }

    /// `Quit while amx is still launching api?`
    nonisolated static func quitWhileChangingQuestion(_ running: [String]) -> String {
        switch running.count {
        case 0: return "Quit Agent Mux?"
        case 1: return "Quit while amx is still \(running[0])?"
        default:
            return "Quit while amx is still \(running.dropLast().joined(separator: ", ")) and \(running[running.count - 1])?"
        }
    }

    /// Records an amx command that changes something, for the quit
    /// confirmation; `endChange` with the token it returns once it is done.
    func beginChange(_ label: String) -> Int {
        nextChange += 1
        runningChanges[nextChange] = label
        return nextChange
    }

    func endChange(_ token: Int) {
        runningChanges[token] = nil
    }

    func applicationWillTerminate(_ notification: Notification) {
        isTerminating = true
        // Before anything else goes: freeing a surface detaches its view,
        // and the sessions themselves live on in tmux.
        terminals?.shutdown()
        connection.stop()
        configWatcher?.stop()
        workspaceConfirmationFile.remove()
        if let wakeObserver { NSWorkspace.shared.notificationCenter.removeObserver(wakeObserver) }
        if let activationObserver { NSWorkspace.shared.notificationCenter.removeObserver(activationObserver) }
        if let becameActiveObserver { NotificationCenter.default.removeObserver(becameActiveObserver) }
    }

    // MARK: Embedded terminals

    /// The runtime every embedded terminal is made from, or nil when Ghostty
    /// could not start. Surfaces come from its `makeSurface(_:)`, each with
    /// its own command and environment.
    func terminalRuntime() -> Ghostty.App? {
        if let terminals { return terminals }
        let model = preferencesModel
        let runtime = Ghostty.App {
            Ghostty.Config(overrides: Self.terminalOverrides(model.preferences))
        }
        guard runtime.readiness == .ready else {
            Self.logger.error("embedded terminals are unavailable: Ghostty did not start")
            return nil
        }
        runtime.delegate = self
        terminals = runtime
        refreshConfigFontSize()
        Self.logger.notice("embedded terminals use Ghostty \(Ghostty.versionDescription, privacy: .public)")
        return runtime
    }

    /// What a terminal attached to a runtime session runs: the resolved amx,
    /// with the environment every other amx invocation gets. Nil until amx is
    /// resolved.
    func attachmentConfiguration(runtimeSessionID: String, fontSize: Float? = nil) -> Ghostty.SurfaceConfiguration? {
        guard let command else { return nil }
        var configuration = Ghostty.SurfaceConfiguration.attachment(
            amx: command.binary,
            runtimeSessionID: runtimeSessionID,
            environment: command.launchEnvironment.commandEnvironment
        )
        configuration.fontSize = fontSize
        return configuration
    }

    /// What a utility view's terminal runs: `amx workspace-view …`, which
    /// decides the program and strips every session and Kitty variable from
    /// its environment first. Nil until amx is resolved.
    func utilityConfiguration(for view: WorkspaceUtilityView) -> Ghostty.SurfaceConfiguration? {
        guard let command else { return nil }
        var environment = command.launchEnvironment.commandEnvironment
        environment.removeValue(forKey: WorkspaceConfirmationFile.environmentKey)
        if [.dashboard, .overview, .history].contains(view.kind) {
            do { try workspaceConfirmationFile.write(preferencesModel.preferences.confirmationPolicy) }
            catch {
                try? FileManager.default.removeItem(at: workspaceConfirmationFile.url)
                Self.logger.error("Could not write workspace confirmation policy: \(error.localizedDescription, privacy: .public)")
            }
            // A missing file makes embedded views confirm conservatively.
            environment[WorkspaceConfirmationFile.environmentKey] = workspaceConfirmationFile.url.path
        }
        return Ghostty.SurfaceConfiguration(program: command.binary, arguments: view.arguments, environment: environment)
    }

}

extension AppDelegate: GhosttyAppDelegate {
    /// A `quit` binding typed into a terminal closes nothing: the companion
    /// and its other terminals outlive any one of them.
    nonisolated func ghosttyAppDidRequestQuit(_ app: Ghostty.App) {
        Self.logger.debug("ignored a quit request from a terminal")
    }
}
