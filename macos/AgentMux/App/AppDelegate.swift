import AppKit
import SwiftUI
import os
import UserNotifications

final class AppDelegate: NSObject, NSApplicationDelegate, StatusItemDelegate, WorkspaceDelegate {
    private let reviewFeedbackHistory = WorkspaceFeedbackHistory()
    private nonisolated static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "app")

    /// How often the daemon line is refreshed while the companion feed is
    /// not live: with no daemon, or one too old to stream.
    static let healthInterval: TimeInterval = 10

    private(set) var statusItem: StatusItemController?
    private var preferencesController: PreferencesWindowController?
    /// The ASL answer panel, made the first time a question is answered.
    private var aslPanel: ASLQuestionPanelController?
    private var promptPanel: PromptAnswerPanelController?
    private var historyWindow: WorkspaceHistoryWindow?
    private var activityWindow: WorkspaceActivityWindow?
    private var runsWindow: WorkspaceRunsWindow?
    private var changesWindow: WorkspaceChangesWindow?
    private var paneChanges: WorkspaceChangesModel?
    /// The workspace window, made the first time it is shown.
    private(set) var workspace: WorkspaceWindowController?
    /// Set once the app is quitting, so the windows it takes down with it are
    /// not recorded as closed by the operator.
    private var isTerminating = false
    /// Session status questions asked before amx was resolved.
    private var statusQuestions: [(String, @MainActor (WorkspaceSessionStatus) -> Void)] = []
    let preferencesModel = PreferencesModel()
    private var dashboardHotKey: HotKeyCenter?
    private var attentionHotKey: HotKeyCenter?
    private var cycler = AttentionCycler()
    /// The login shell's environment, read once at launch; overrides are
    /// applied on top of it each time amx is resolved again.
    private var detectedEnvironment: LaunchEnvironment?
    private var pendingResolve: DispatchWorkItem?
    /// True while a Preferences recorder listens for a shortcut.
    private var isRecordingHotKey = false
    private var healthTimer: Timer?
    private var subscription: SubscriptionClient?
    private var wakeObserver: NSObjectProtocol?
    private var activationObserver: NSObjectProtocol?
    /// The Kitty the operator used last, where new tabs open.
    private var lastActiveKittyPID: pid_t?

    private var projects = ProjectCatalog()
    private var workspaceProjectMenu: NSMenu?
    private var recent = RecentSessions()
    /// False once the daemon answered that it has no notification_snooze.
    private var snoozeSupported = true
    /// The daemon a read-only snooze request has been sent to, which is how
    /// an old daemon's Notifications submenu is greyed out before it is used.
    private var snoozeProbedDaemon: String?
    private var snoozeProbeInFlight = false
    private var snoozeReapplyInFlight = false
    /// The daemon a re-apply was last attempted against, so a refused one is
    /// not retried on every snapshot.
    private var snoozeReapplyAttempted: String?

    /// Shows the daemon's notification frames; nil under tests.
    private(set) var notifications: NotificationPresenter?
    /// What the next subscribe carries: the notification claim and the
    /// resolved amx build, read on the subscription thread.
    private let subscribeRequest = SubscribeRequestSource(clientVersion: AppDelegate.appVersion)
    /// Dispositions are reported here, in order, so `delivered` always
    /// reaches the daemon before the click that follows it.
    private let dispositionQueue = DispatchQueue(label: "io.github.sglavoie.agentmux.dispositions", qos: .userInitiated)

    /// Resolving amx reads the login shell and probes the binary, which can
    /// take seconds, so it runs here, one at a time.
    private let work = DispatchQueue(label: "io.github.sglavoie.agentmux.work", qos: .utility)
    /// Health requests run here, so a slow resolve never holds up the daemon
    /// line.
    private let healthQueue = DispatchQueue(label: "io.github.sglavoie.agentmux.health", qos: .utility)
    /// Focus and snooze requests run here: each is one short round-trip on
    /// the daemon's socket, so a click never waits behind an amx subprocess,
    /// a health check, or a launch.
    private let focusQueue = DispatchQueue(label: "io.github.sglavoie.agentmux.focus", qos: .userInitiated)
    /// The quick amx commands (a session's status, naming, mute, the state
    /// keys, open-dashboard) run here, one at a time, so that two mutes of
    /// one session land in the order they were clicked, and an amx that takes
    /// its full ten seconds never holds up a focus.
    private let amxQueue = DispatchQueue(label: "io.github.sglavoie.agentmux.amx", qos: .userInitiated)
    /// Launches and continues run here, one at a time: each can take up to
    /// two minutes, and none of them should hold up a click.
    private let launchQueue = DispatchQueue(label: "io.github.sglavoie.agentmux.launch", qos: .userInitiated)
    /// Ending a session and restarting the daemon run here, one at a time,
    /// apart from launches: neither should wait minutes behind a launch that
    /// has nothing to do with it.
    private let controlQueue = DispatchQueue(label: "io.github.sglavoie.agentmux.control", qos: .userInitiated)
    /// The amx commands that change something and are running now, by what
    /// the quit confirmation calls them. Quitting closes their pipes, and amx
    /// can die of it halfway through.
    private var runningChanges: [Int: String] = [:]
    private var nextChange = 0
    /// The project and Recent lists load here, so a slow transcript refresh
    /// never holds up a health check or a click.
    private let listQueue = DispatchQueue(label: "io.github.sglavoie.agentmux.lists", qos: .utility, attributes: .concurrent)
    private var healthInFlight = false

    let store = SessionStore()
    private(set) var preferences = PreferencesSnapshot()
    /// Set once the binary is resolved; amx commands run through it.
    private(set) var command: AMXCommand?

    /// The embedded terminal runtime, made the first time a terminal is
    /// wanted: a companion used only beside Kitty never starts Ghostty.
    private(set) var terminals: Ghostty.App?

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
        // LSUIElement already keeps the app out of the Dock; this states it
        // for a build run straight from DerivedData.
        NSApp.setActivationPolicy(.accessory)
        UserDefaults.standard.register(defaults: Self.launchDefaults)
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

        // Settings and the answer panels need the Edit menu's keys (⌘C, ⌘V,
        // ⌘Z, ...) and ⌘W from the start, not once the workspace was opened.
        WorkspaceMenu.install(target: self)
        statusItem = StatusItemController(delegate: self)
        setUpPreferences()
        setUpHotKeys()
        store.onChange = { [weak self] in self?.storeChanged() }
        // The first subscribe waits for amx to be resolved, so it reports the
        // build doctor compares; a slow login shell holds it up no longer
        // than this, and the daemon line is polled meanwhile.
        DispatchQueue.main.asyncAfter(deadline: .now() + Self.firstSubscribeWait) { [weak self] in
            MainActor.assumeIsolated { self?.startSubscription() }
        }
        refreshDaemonStatus()
        let timer = Timer(timeInterval: Self.healthInterval, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.refreshDaemonStatus()
                self?.refreshProjects(force: true)
                self?.notifications?.refreshAuthorization()
            }
        }
        RunLoop.main.add(timer, forMode: .common)
        healthTimer = timer
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
                self.reconnectIfWaiting()
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
    private func restoreWorkspace() {
        let restoration = WorkspaceRestoration.load()
        guard restoration.windowOpen || preferencesModel.preferences.openWorkspaceAtLaunch else { return }
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

    /// The app was opened again while running (from the Dock, Finder,
    /// Spotlight, or `open`): that asks for its window. With only Settings
    /// open, as any app with a window open does, it brings that forward: the
    /// Dock icon is there for Settings.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        guard !Self.isRunningTests else { return false }
        if activityVisible && !workspaceVisible && !settingsVisible {
            activityWindow?.window?.makeKeyAndOrderFront(nil)
            return false
        }
        if runsVisible && !workspaceVisible && !settingsVisible {
            runsWindow?.window?.makeKeyAndOrderFront(nil)
            return true
        }
        if changesVisible && !workspaceVisible && !settingsVisible {
            changesWindow?.window?.makeKeyAndOrderFront(nil)
            return false
        }
        if historyVisible && !workspaceVisible && !settingsVisible {
            historyWindow?.window?.makeKeyAndOrderFront(nil)
            return false
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
    private func confirmViewsBeforeQuit(_ sender: NSApplication) -> Bool {
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
    private func askToQuitWhileChanging(_ running: [String]) -> Bool {
        askToQuit(Self.quitWhileChangingQuestion(running),
                  "Quitting now can stop it halfway. Wait for it to finish, or quit anyway and check the session list afterwards.")
    }

    private func askToQuit(_ question: String, _ message: String) -> Bool {
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
    private func beginChange(_ label: String) -> Int {
        nextChange += 1
        runningChanges[nextChange] = label
        return nextChange
    }

    private func endChange(_ token: Int) {
        runningChanges[token] = nil
    }

    func applicationWillTerminate(_ notification: Notification) {
        isTerminating = true
        // Before anything else goes: freeing a surface detaches its view,
        // and the sessions themselves live on in tmux.
        terminals?.shutdown()
        healthTimer?.invalidate()
        subscription?.stop()
        if let wakeObserver { NSWorkspace.shared.notificationCenter.removeObserver(wakeObserver) }
        if let activationObserver { NSWorkspace.shared.notificationCenter.removeObserver(activationObserver) }
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
        return Ghostty.SurfaceConfiguration(
            program: command.binary,
            arguments: view.arguments,
            environment: command.launchEnvironment.commandEnvironment
        )
    }

    // MARK: Workspace

    /// Shows the workspace window, on `runtimeSessionID`'s terminal when one
    /// is named. An empty workspace offers launch and history directly.
    func showWorkspace(focusing runtimeSessionID: String? = nil) {
        let controller = workspaceController()
        refreshProjects()
        if let runtimeSessionID {
            controller.focus(runtimeSessionID: runtimeSessionID)
        } else {
            controller.show()
            // A restore under way, or an agent said to be gone, is a place.
            if !controller.state.rows.isEmpty { controller.showDashboardUnlessPlaced() }
        }
    }

    private func workspaceController() -> WorkspaceWindowController {
        if let workspace { return workspace }
        let controller = WorkspaceWindowController(delegate: self)
        controller.confirmBeforeClosing = preferencesModel.preferences.confirmBeforeClosing
        workspace = controller
        if !Self.isRunningTests {
            controller.onLayoutChanged = { WorkspaceRestoration.saveLayout($0) }
            controller.makeRestoredReview = { [weak self] directory in
                guard let self else { return nil }
                let model = self.makeChangesModel()
                model.onOpenFile = { [weak self] path in
                    self?.showWorkspaceView(WorkspaceUtilityView(kind: .editor, target: path, title: (path as NSString).lastPathComponent))
                }
                self.paneChanges = model
                model.open(directory: directory)
                return .init(view: WorkspaceReviewHost(rootView: WorkspaceChangesView(model: model)),
                             canClose: { !model.landing }, close: { model.close() }, directory: { model.changes?.root ?? directory })
            }
            let restoration = WorkspaceRestoration.load()
            // Before anything makes the window, which opens at these widths.
            controller.sidebarWidth = restoration.sidebarWidth.map { CGFloat($0) }
            controller.inspectorWidth = restoration.inspectorWidth.map { CGFloat($0) }
            controller.setSidebarCollapsed(restoration.sidebarCollapsed)
            controller.setInspectorTab(restoration.inspectorTab)
            controller.setInspectorCollapsed(!restoration.inspectorShown)
            controller.order = WorkspaceOrder.load()
        }
        // The terminals offer their shortcuts to the main menu first. Launch
        // has installed it already; a hosted test run has not.
        WorkspaceMenu.install(target: self)
        return controller
    }

    /// A click on a utility view's terminal notice: opens the workspace on
    /// that view, or where the workspace was when the view is gone. Only
    /// navigation: a view never counts toward attention.
    func showWorkspace(onView id: String) {
        let controller = workspaceController()
        controller.show()
        guard !controller.showView(id) else { return }
        Self.logger.notice("terminal notice from view \(id, privacy: .public): the view is gone")
        controller.showDashboardUnlessPlaced()
    }

    /// Shows one of the workspace's utility views, opening the window for it.
    func showWorkspaceView(_ view: WorkspaceUtilityView) {
        let controller = workspaceController()
        controller.show()
        controller.open(view)
    }

    /// A view a surface the workspace embeds asked for, forwarded by the
    /// daemon. Such a surface lives in the workspace window, so a request
    /// that arrives while the window is closed is from nothing on screen and
    /// is dropped rather than raising the app.
    private func handle(_ request: WorkspaceViewRequest) {
        if request.raise, request.kind == WorkspaceViewRequest.sessionKind {
            // The operator's own click, by way of focus_session: unlike a
            // surface's request it opens the window.
            let controller = workspaceController()
            controller.show()
            controller.open(request)
            return
        }
        guard let workspace, workspace.isOpen else {
            Self.logger.notice("workspace view request (\(request.kind, privacy: .public)) with no workspace open; ignored")
            return
        }
        workspace.open(request)
    }

    func makeUtilityTerminal(_ view: WorkspaceUtilityView) -> Result<WorkspaceTerminal, WorkspaceTerminalProblem> {
        guard let configuration = utilityConfiguration(for: view) else {
            return .failure(WorkspaceTerminalProblem(message: preferences.binaryMenuProblem ?? "amx is still being resolved."))
        }
        guard let runtime = terminalRuntime() else {
            return .failure(WorkspaceTerminalProblem(message: "The embedded terminal could not start. See the log for why."))
        }
        guard let surface = runtime.makeSurface(configuration) else {
            return .failure(WorkspaceTerminalProblem(message: "The terminal could not be created."))
        }
        surface.delegate = workspace
        return .success(WorkspaceTerminal(view: surface, close: { surface.close() }, needsConfirm: { surface.needsConfirmQuit }))
    }

    func makeTerminal(runtimeSessionID: String, fontSize: Float?) -> Result<WorkspaceTerminal, WorkspaceTerminalProblem> {
        guard let configuration = attachmentConfiguration(runtimeSessionID: runtimeSessionID, fontSize: fontSize) else {
            return .failure(WorkspaceTerminalProblem(message: preferences.binaryMenuProblem ?? "amx is still being resolved."))
        }
        guard let runtime = terminalRuntime() else {
            return .failure(WorkspaceTerminalProblem(message: "The embedded terminal could not start. See the log for why."))
        }
        guard let surface = runtime.makeSurface(configuration) else {
            return .failure(WorkspaceTerminalProblem(message: "The terminal could not be created."))
        }
        surface.delegate = workspace
        return .success(WorkspaceTerminal(view: surface, close: { surface.close() }, fontSize: { surface.fontSize }))
    }

    func focusExternally(_ row: WorkspaceRow) {
        guard case let .kitty(socket, windowID) = row.target else { return }
        focus(MenuSessionRow(title: row.project, windowID: windowID, kittySocket: socket, toolTip: ""))
    }

    /// While the workspace is open the app is a regular one, with a Dock icon
    /// and a menu bar, so its window takes the keyboard like any other; with
    /// it closed the app is the menu bar accessory again.
    func workspaceVisibilityChanged(_ visible: Bool) {
        workspaceVisible = visible
        syncActivationPolicy()
        guard !Self.isRunningTests, !isTerminating else { return }
        WorkspaceRestoration.saveWindowOpen(visible)
    }

    private var workspaceVisible = false
    private var settingsVisible = false
    private var historyVisible = false
    private var activityVisible = false
    private var runsVisible = false
    private var changesVisible = false

    /// Regular while the workspace or Settings is open, so either can be
    /// found again from the Dock and ⌘Tab once it falls behind another
    /// app's window; the menu bar accessory otherwise. The answer panels
    /// float above every window and need neither.
    private func syncActivationPolicy() {
        NSApp.setActivationPolicy(Self.activationPolicy(workspace: workspaceVisible || historyVisible || changesVisible || runsVisible || activityVisible, settings: settingsVisible))
    }

    nonisolated static func activationPolicy(workspace: Bool, settings: Bool) -> NSApplication.ActivationPolicy {
        workspace || settings ? .regular : .accessory
    }

    func workspacePlaceChanged(_ place: WorkspaceRestoration.Place?) {
        guard !Self.isRunningTests, !isTerminating else { return }
        Self.logger.notice("workspace: now on \(place?.encoded ?? "nothing", privacy: .public)")
        WorkspaceRestoration.savePlace(place)
    }

    func workspaceConversationChanged(_ conversation: ConversationIdentity?) {
        guard !Self.isRunningTests, !isTerminating else { return }
        WorkspaceRestoration.saveConversation(conversation)
    }

    func workspaceSidebarCollapsedChanged(_ collapsed: Bool) {
        guard !Self.isRunningTests, !isTerminating else { return }
        WorkspaceRestoration.saveSidebarCollapsed(collapsed)
    }

    func workspaceFullScreenChanged(_ fullScreen: Bool) {
        guard !Self.isRunningTests, !isTerminating else { return }
        WorkspaceRestoration.saveFullScreen(fullScreen)
    }

    func workspaceInspectorChanged(collapsed: Bool, tab: WorkspaceInspectorTab) {
        guard !Self.isRunningTests, !isTerminating else { return }
        WorkspaceRestoration.saveInspector(shown: !collapsed, tab: tab)
    }

    func workspaceLayoutChanged(sidebarWidth: CGFloat?, inspectorWidth: CGFloat?) {
        guard !Self.isRunningTests, !isTerminating else { return }
        WorkspaceRestoration.saveWidths(sidebar: sidebarWidth.map(Double.init), inspector: inspectorWidth.map(Double.init))
    }

    /// The inspector's buttons do what ⌘R and the session function keys do.
    /// Answer… in the inspector: the panels the menu and the banners open.
    /// Whether anything may be sent is still each panel's to find out.
    func workspaceAnswer(_ answer: WorkspaceWaitingAnswer) {
        switch answer {
        case let .asl(target): self.answer(target)
        case let .prompt(runtimeSessionID): answerPrompt(PromptAnswerTarget(runtimeSessionID: runtimeSessionID))
        }
    }

    func workspaceInspectorAction(_ action: WorkspaceInspectorAction) {
        switch action {
        case .name: nameSession(nil)
        case .color: pickSessionColor(nil)
        case .icon: pickSessionIcon(nil)
        case .mute: toggleSessionMute(nil)
        case .clearState: clearSessionState(nil)
        case .markActive: markSessionActive(nil)
        case .reviewChanges: showChanges(nil)
        }
    }

    func workspaceOrderChanged(_ order: WorkspaceOrder) {
        guard !Self.isRunningTests, !isTerminating else { return }
        order.save()
    }

    /// Asks amx what the registry and the private tmux server say about a
    /// session. `amx attach --json` checks and reports; it attaches nothing.
    func sessionStatus(runtimeSessionID: String, completion: @escaping @MainActor (WorkspaceSessionStatus) -> Void) {
        guard let command else {
            // At launch the workspace can ask before amx is found. The
            // question waits for it instead of being answered "unknown".
            if let problem = preferences.binaryMenuProblem {
                completion(.unknown(problem))
            } else {
                statusQuestions.append((runtimeSessionID, completion))
            }
            return
        }
        amxQueue.async {
            let status = WorkspaceSessionStatus.from(command.json(AttachCheck.self, ["attach", runtimeSessionID]))
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    Self.logger.notice("session \(runtimeSessionID, privacy: .public): \(String(describing: status), privacy: .public)")
                    completion(status)
                }
            }
        }
    }

    /// Ends a session's agent with `amx close --yes`. The workspace has
    /// already asked the operator when there was something to ask.
    func closeSession(runtimeSessionID: String, completion: @escaping @MainActor (String?) -> Void) {
        guard let command else {
            completion(preferences.binaryMenuProblem ?? "amx has not been found yet.")
            return
        }
        let change = beginChange("ending a session")
        controlQueue.async { [weak self] in
            let problem = SessionClose.problem(command.json(
                SessionClose.self, ["close", runtimeSessionID, "--yes"], timeout: AMXCommand.actionTimeout, mutates: true
            ))
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    self?.endChange(change)
                    Self.logger.notice("close \(runtimeSessionID, privacy: .public): \(problem ?? "closed", privacy: .public)")
                    completion(problem)
                }
            }
        }
    }

    func workspaceStartDaemon() {
        startDaemon()
    }

    func workspaceTerminalNotification(_ notice: TerminalNotice, onScreen: Bool) {
        let snoozed = Self.terminalNoticesSnoozed(
            live: store.feed == .live ? store.snapshot.map { SnoozeState($0.notification) } : nil,
            mirrored: preferencesModel.preferences.snoozeUntil,
            now: Date()
        )
        let muted = Self.terminalNoticeMuted(notice.origin, in: store.sessions)
        let outcome = notifications?.presentTerminal(notice, snoozed: snoozed, muted: muted, onScreen: onScreen) ?? .unavailable
        if outcome != .shown {
            Self.logger.notice("terminal notification from \(notice.source, privacy: .public) not shown: \(String(describing: outcome), privacy: .public)")
        }
    }

    func workspaceShowing(_ origin: TerminalNotice.Origin) {
        notifications?.withdrawTerminal(origin)
    }

    /// Whether everything is snoozed, for a terminal notice: the daemon's
    /// snooze while the feed is live, else the one mirrored into
    /// `amxSnoozeUntil`.
    nonisolated static func terminalNoticesSnoozed(live: SnoozeState?, mirrored: Date?, now: Date) -> Bool {
        if let live { return live.isSnoozed(now: now) }
        guard let mirrored else { return false }
        return mirrored > now
    }

    /// Whether a terminal notice's session is muted. A view is never a
    /// session and is never muted.
    nonisolated static func terminalNoticeMuted(_ origin: TerminalNotice.Origin, in sessions: [CompanionSession]) -> Bool {
        guard case let .session(id) = origin else { return false }
        return session(runtimeSessionID: id, in: sessions)?.muted ?? false
    }

    /// The listed session the workspace embeds under a runtime session.
    nonisolated static func session(runtimeSessionID id: String, in sessions: [CompanionSession]) -> CompanionSession? {
        guard !id.isEmpty else { return nil }
        return sessions.first { $0.embeddedRuntimeSessionID == id }
    }

    /// Names a session with `amx name-session`; an empty name removes the
    /// one it has. The sidebar shows the change once the daemon has read it.
    func nameSession(runtimeSessionID: String, name: String, completion: @escaping @MainActor (String?) -> Void) {
        guard let command else {
            completion(preferences.binaryMenuProblem ?? "amx has not been found yet.")
            return
        }
        amxQueue.async {
            let result = command.json(NameSessionResult.self, NameSessionResult.arguments(runtimeSessionID: runtimeSessionID, name: name))
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    switch result {
                    case .success, .partial:
                        Self.logger.notice("name-session \(runtimeSessionID, privacy: .public): done")
                        completion(nil)
                    case let .failure(error):
                        Self.logger.error("name-session \(runtimeSessionID, privacy: .public): \(error.message, privacy: .public)")
                        completion(error.message)
                    }
                }
            }
        }
    }

    @objc func openWorkspace(_ sender: Any?) { showWorkspace() }
    @objc func nextAttention(_ sender: Any?) { focusNextAttention() }
    @objc func selectNextSession(_ sender: Any?) { workspace?.selectNeighbor(1) }
    @objc func selectPreviousSession(_ sender: Any?) { workspace?.selectNeighbor(-1) }
    @objc func selectNextProjectTab(_ sender: Any?) { workspace?.selectProjectTab(1) }
    @objc func selectPreviousProjectTab(_ sender: Any?) { workspace?.selectProjectTab(-1) }
    @objc func showNextSessionInKitty(_ sender: Any?) { workspace?.selectNeighbor(1, raising: true) }
    @objc func showPreviousSessionInKitty(_ sender: Any?) { workspace?.selectNeighbor(-1, raising: true) }
    /// ⌘1 to ⌘0: the item's tag is the session's place in the sidebar.
    @objc func goToSession(_ sender: Any?) {
        guard let item = sender as? NSMenuItem else { return }
        workspace?.selectSession(at: item.tag)
    }
    /// ⌘⇧1 to ⌘⇧0.
    @objc func showSessionInKitty(_ sender: Any?) {
        guard let item = sender as? NSMenuItem else { return }
        workspace?.selectSession(at: item.tag, raising: true)
    }
    /// The workspace's own Dashboard: the main menu's, as opposed to the
    /// global shortcut, which goes where `amxDashboardPlace` says.
    @objc func openDashboardFromMenu(_ sender: Any?) { showWorkspaceView(.dashboard) }
    @objc func openOverviewFromMenu(_ sender: Any?) { showWorkspaceView(.overview) }
    @objc func newShellFromMenu(_ sender: Any?) {
        let home = command?.launchEnvironment.home ?? NSHomeDirectory()
        let controller = workspaceController()
        controller.show()
        controller.openNewShell(in: home)
    }
    /// ⌘↩: a shell beside the session the workspace is on.
    @objc func newSessionTerminal(_ sender: Any?) {
        guard let workspace, workspace.shownSessionRow != nil else { return refuse(Self.noSessionShown) }
        if !workspace.openTerminalBesideSession() { refuse(WorkspaceWindowController.terminalNeedsEmbedded) }
    }
    /// ⌘W: closes what the workspace's pane is on, as the × on its row does,
    /// and never the workspace itself. Another window that has the keyboard,
    /// such as Preferences, closes as it would.
    @objc func closeWorkspaceView(_ sender: Any?) {
        if let key = NSApp.keyWindow, workspace?.isOpen != true || key !== workspace?.window {
            key.performClose(sender)
            return
        }
        if workspace?.closeShown() != true { NSSound.beep() }
    }
    /// What ⌘W closes: a view, a session attachment, or another window.
    var closeWorkspaceViewTitle: String {
        guard let workspace, workspace.isOpen, let key = NSApp.keyWindow, key === workspace.window else {
            return WorkspaceWindowController.closeTitle
        }
        return workspace.closeShownTitle
    }
    @objc func endWorkspaceSession(_ sender: Any?) {
        guard let workspace, let row = workspace.shownSessionRow, row.embeddedRuntimeSessionID != nil else { return refuse(Self.noSessionShown) }
        workspace.closeSession(row.key)
    }
    /// ⌘R: names the session or terminal the workspace's pane is on.
    @objc func nameSession(_ sender: Any?) {
        guard let workspace else { return refuse(Self.noSessionShown) }
        if !workspace.nameShownSession() { refuse(WorkspaceWindowController.namingNeedsEmbedded) }
    }
    /// Pins the session the workspace's pane is on above every project, or
    /// unpins it.
    @objc func togglePinSession(_ sender: Any?) {
        if workspace?.togglePinOfShownSession() != true { refuse(Self.noSessionShown) }
    }
    @objc func toggleWorkspaceSidebar(_ sender: Any?) { workspace?.toggleSidebar() }
    @objc func toggleWorkspaceInspector(_ sender: Any?) { workspace?.toggleInspector() }
    @objc func toggleRecentPlace(_ sender: Any?) {
        if workspace?.toggleRecentPlace() != true { NSSound.beep() }
    }
    /// ⌘P: the list of sessions and views to go to by name.
    @objc func showSwitcher(_ sender: Any?) {
        refreshProjects()
        refreshRecent()
        workspace?.toggleSwitcher()
    }

    @objc func showCommandPalette(_ sender: Any?) {
        refreshProjects()
        refreshRecent()
        workspace?.toggleSwitcher(actions: true)
    }

    @objc func splitWorkspaceShell(_ sender: Any?) {
        if workspace?.splitWithShell() != true { refuse("Select an embedded session to split with a shell.") }
    }

    @objc func focusOtherWorkspacePane(_ sender: Any?) { _ = workspace?.focusOtherPane() }
    @objc func swapWorkspacePanes(_ sender: Any?) { workspace?.swapPanes() }
    @objc func collapseWorkspacePane(_ sender: Any?) { workspace?.collapseOtherPane() }

    @objc func splitWorkspaceChanges(_ sender: Any?) {
        guard let workspace, let id = workspace.shownSessionRow?.embeddedRuntimeSessionID else {
            return refuse("Select an embedded session to review its checkout alongside it.")
        }
        guard paneChanges?.landing != true else { return refuse("Wait for the worktree landing to finish.") }
        let model = makeChangesModel()
        model.onOpenFile = { [weak self] path in
            self?.showWorkspaceView(WorkspaceUtilityView(kind: .editor, target: path, title: (path as NSString).lastPathComponent))
        }
        let view = WorkspaceReviewHost(rootView: WorkspaceChangesView(model: model))
        guard workspace.splitWithReview(view, canClose: { !model.landing }, onClose: { model.close() }) else { return }
        paneChanges = model
        workspace.reviewDirectory = { model.changes?.root }
        model.open(runtimeSessionID: id)
    }

    private func makeChangesModel() -> WorkspaceChangesModel {
        let model = WorkspaceChangesModel(command: { [weak self] in self?.command }, defaults: .standard, feedbackHistory: reviewFeedbackHistory)
        model.onFeedbackRecovery = { [weak self] directory in self?.inspectFeedbackCheckout(directory) }
        return model
    }

    private func inspectFeedbackCheckout(_ directory: String) {
        let controller = changesWindow ?? WorkspaceChangesWindow(model: makeChangesModel())
        guard !controller.model.landing else { controller.show(); return }
        controller.onVisibilityChanged = { [weak self] visible in
            self?.changesVisible = visible
            self?.syncActivationPolicy()
        }
        controller.model.onOpenFile = { [weak self] path in
            self?.showWorkspaceView(WorkspaceUtilityView(kind: .editor, target: path, title: (path as NSString).lastPathComponent))
        }
        changesWindow = controller
        controller.model.open(directory: directory)
        controller.show()
    }

    @objc func showChanges(_ sender: Any?) {
        let workspace = workspaceController()
        workspace.show()
        let controller = changesWindow ?? WorkspaceChangesWindow(model: makeChangesModel())
        guard !controller.model.landing else { controller.show(); return }
        controller.onVisibilityChanged = { [weak self] visible in
            self?.changesVisible = visible
            self?.syncActivationPolicy()
            if !visible, self?.workspaceVisible == true { self?.workspace?.window.makeKeyAndOrderFront(nil) }
        }
        controller.model.onOpenFile = { [weak self] path in
            self?.showWorkspaceView(WorkspaceUtilityView(kind: .editor, target: path, title: (path as NSString).lastPathComponent))
        }
        if let id = workspace.shownSessionRow?.embeddedRuntimeSessionID {
            controller.model.open(runtimeSessionID: id)
        } else {
            let picker = NSOpenPanel()
            picker.canChooseFiles = false
            picker.canChooseDirectories = true
            picker.allowsMultipleSelection = false
            picker.message = "Choose the checkout to review. Changes include every session using this directory."
            picker.prompt = "Review"
            guard picker.runModal() == .OK, let url = picker.url else { return }
            controller.model.open(directory: url.path)
        }
        changesWindow = controller
        controller.show()
    }

    @objc func showRuns(_ sender: Any?) { openRuns(launch: false) }
    @objc func runPlan(_ sender: Any?) { openRuns(launch: true) }

    private func openRuns(launch: Bool) {
        let controller = runsWindow ?? WorkspaceRunsWindow(
            model: WorkspaceRunsModel(command: { [weak self] in self?.command }, feedbackHistory: reviewFeedbackHistory, defaults: .standard),
            plan: WorkspaceRunPlanModel(command: { [weak self] in self?.command }, feedbackHistory: reviewFeedbackHistory))
        controller.onVisibilityChanged = { [weak self] visible in
            self?.runsVisible = visible
            self?.syncActivationPolicy()
            if !visible, self?.workspaceVisible == true { self?.workspace?.window.makeKeyAndOrderFront(nil) }
        }
        controller.model.onAnswer = { [weak self] target in self?.answer(target) }
        controller.model.onFocus = { [weak self] row in
            guard let self else { return }
            guard self.store.feed == .live,
                  let session = self.store.sessions.first(where: { $0.aslRunID == row.runID }) else {
                return self.refuse("No live session is associated with this ASL run in the current session list.")
            }
            self.focus(.focusing(session, title: row.displayLabel))
        }
        controller.plan.onStarted = { [weak self] id in
            self?.finishOpeningInWorkspace(.success(id), label: "start supervised plan", progress: "")
        }
        runsWindow = controller
        controller.show(launch: launch)
    }

    @objc func showActivity(_ sender: Any?) {
        let controller = activityWindow ?? WorkspaceActivityWindow(model: WorkspaceActivityModel(command: { [weak self] in self?.command }))
        controller.onVisibilityChanged = { [weak self] visible in
            self?.activityVisible = visible
            self?.syncActivationPolicy()
            if !visible, self?.workspaceVisible == true { self?.workspace?.window.makeKeyAndOrderFront(nil) }
        }
        controller.model.onConversation = { [weak self] identity, at in self?.openHistory(conversation: identity, eventTime: at) }
        controller.model.onFocus = { [weak self] entry in
            guard let self else { return }
            guard self.store.feed == .live else { return self.refuse("Waiting for a live session list.") }
            let live = Self.session(runtimeSessionID: entry.runtimeSessionID, in: self.store.sessions)
                ?? self.store.sessions.first { entry.conversation != nil && $0.conversation == entry.conversation }
            guard let live else { return self.refuse("No live session matches this recorded event. Open its conversation to inspect or resume it.") }
            self.focus(.focusing(live, title: entry.title))
        }
        activityWindow = controller
        controller.show()
    }

    @objc func showHistory(_ sender: Any?) { openHistory(conversation: nil) }

    @objc func searchConversation(_ sender: Any?) {
        guard workspaceIsKey, let identity = workspace?.shownSessionRow?.conversation else { return }
        openHistory(conversation: identity)
    }

    private func openHistory(conversation: ConversationIdentity?, eventTime: String? = nil) {
        let workspace = workspaceController()
        workspace.show()
        let controller = historyWindow ?? WorkspaceHistoryWindow(model: WorkspaceHistoryModel(command: { [weak self] in self?.command }, defaults: .standard, feedbackHistory: reviewFeedbackHistory))
        controller.onVisibilityChanged = { [weak self] visible in
            self?.historyVisible = visible
            self?.syncActivationPolicy()
            if !visible, let self {
                if eventTime != nil, let activity = self.activityWindow?.window, activity.isVisible {
                    activity.makeKeyAndOrderFront(nil)
                } else if self.workspaceVisible { self.workspace?.window.makeKeyAndOrderFront(nil) }
            }
        }
        controller.model.onAction = { [weak self] conversation, fork, completion in
            self?.openHistoryConversation(conversation, fork: fork, completion: completion)
        }
        controller.model.liveConversations = Set(store.feed == .live ? store.sessions.compactMap { $0.conversation?.encoded } : [])
        historyWindow = controller
        if conversation != nil {
            controller.model.query = ""
            controller.model.scope = .conversation
            controller.model.includeAutomated = true
        }
        controller.show(project: workspace.shownSessionRow?.project ?? "", conversation: conversation ?? workspace.shownSessionRow?.conversation, eventTime: eventTime)
    }

    private func openHistoryConversation(_ conversation: RecentConversation, fork: Bool, completion: @escaping (String?) -> Void) {
        if !fork, store.feed == .live, let live = store.sessions.first(where: { $0.conversation == conversation.identity }) {
            focus(.focusing(live, title: conversation.displayName))
            completion(nil)
            return
        }
        guard let command else { completion("amx is unavailable. Check Settings."); return }
        if let reason = gate.unavailableReason(.sessions) { completion(reason); return }
        let args = ["session", fork ? "fork" : "continue", "--backend", "tmux", "--json", "--", conversation.selector]
        launchQueue.async { [weak self] in
            let result = command.json(SessionContinueResult.self, args, timeout: AMXCommand.launchTimeout, mutates: true)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    switch result {
                    case let .failure(error): completion(error.message)
                    case let .success(value):
                        self?.finishOpeningInWorkspace(.success(value.runtimeSessionID), label: fork ? "fork conversation" : "resume conversation", progress: "")
                        completion(nil)
                    case let .partial(value, _):
                        self?.finishOpeningInWorkspace(.success(value.runtimeSessionID), label: "open conversation", progress: "")
                        completion("The conversation changed, but opening it did not fully complete. Check the workspace before retrying.")
                    }
                }
            }
        }
    }

    var workspaceCommands: [WorkspaceSwitcher.Entry] {
        func entry(_ command: WorkspaceCommand, _ title: String, _ detail: String = "", reason: String? = nil) -> WorkspaceSwitcher.Entry {
            WorkspaceSwitcher.Entry(place: .command(command), glyph: "›", title: title, detail: detail, unavailableReason: reason)
        }
        var entries = [
            entry(.newTerminal, "New Terminal Here", "⌘↩", reason: workspace?.canOpenTerminalBesideSession == true ? nil : "Select an embedded session first"),
            entry(.newShell, "New Shell", "⌘T"),
            entry(.splitShell, "Split with Shell", reason: workspace?.splitUnavailableReason ?? (workspace?.canOpenTerminalBesideSession == true ? nil : "Select an embedded session first")),
            entry(.splitChanges, "Split with Checkout Changes", reason: workspace?.splitUnavailableReason ?? (workspace?.shownSessionRow?.embeddedRuntimeSessionID != nil ? nil : "Select an embedded session first")),
            entry(.focusReview, "Focus Checkout Review", reason: workspace?.hasReview == true ? nil : "Open checkout review in a pane first"),
            entry(.focusOtherPane, "Focus Other Pane", reason: workspace?.isSplit == true ? nil : "Open a second pane first"),
            entry(.swapPanes, "Swap Panes", reason: workspace?.isSplit == true ? nil : "Open a second pane first"),
            entry(.collapsePane, "Hide Other Pane", reason: workspace?.isSplit == true ? (workspace?.canHideOtherPane == true ? nil : "Wait for the checkout operation to finish") : "Open a second pane first"),
            entry(.history, "Search Conversation History"),
            entry(.activity, "Since Last Looked"),
            entry(.runs, "Open ASL Runs"),
            entry(.runPlan, "Run a Plan…", reason: command == nil ? "amx is not available" : nil),
            entry(.changes, "Review Checkout Changes"),
            entry(.dashboard, "Open Dashboard", "⌘D"),
            entry(.overview, "Open Overview", "⌘O"),
            entry(.nextWaiting, "Next Waiting Session", "⌘⇧A", reason: store.feed != .live ? "Waiting for a live session list" : (AttentionCycler.ordered(store.sessions).isEmpty ? "No sessions are waiting" : nil)),
            entry(.settings, "Settings…", "⌘,"),
        ]
        entries += (workspace?.state.rows ?? []).filter { $0.embeddedRuntimeSessionID != nil }.map { row in
            entry(.splitSession(row.key), "Split with \(row.displayName)", row.project,
                  reason: workspace?.splitUnavailableReason ?? (workspace?.state.selection == row.key ? "Already in the active pane" : nil))
        }
        entries += (projects.state.value ?? []).map { project in
            entry(.launch(project), "Launch \(project.title)", "\(project.profile) · \(project.directory)",
                  reason: gate.unavailableReason(.project) ?? (projects.missingDirectories.contains(project.directory) ? "Project directory is missing" : nil))
        }
        entries += (recent.state.value ?? []).map { conversation in
            let live = store.feed == .live && store.sessions.contains { $0.conversation == conversation.identity }
            return entry(.resume(conversation), "\(live ? "Focus" : "Resume") \(conversation.displayName)",
                         "\(conversation.harness) · \(conversation.snippet)", reason: gate.unavailableReason(.sessions))
        }
        return entries
    }

    var workspaceLaunchDescription: String {
        let choice = preferencesModel.preferences.launchBackend
        return "Launch in \(choice.destination(cliBackend: preferences.backend.cliLaunchBackend)) · \(choice.source(cliSource: preferences.backend.cliLaunchBackendSource))"
    }

    var workspaceProjectGuidance: String {
        ProjectCatalog.guidance(projects.state, unavailable: gate.unavailableReason(.project))
    }

    func workspaceLaunchProjects(from view: NSView) {
        refreshProjects()
        let menu = NSMenu(title: "Launch Project")
        menu.autoenablesItems = false
        workspaceProjectMenu = menu
        updateWorkspaceProjectMenu()
        menu.popUp(positioning: nil, at: NSPoint(x: 0, y: view.bounds.height), in: view)
        workspaceProjectMenu = nil
    }

    private func updateWorkspaceProjectMenu() {
        guard let menu = workspaceProjectMenu else { return }
        menu.removeAllItems()
        let entries = gate.unavailableReason(.project).map { [MenuEntry.info($0), .action(.launchSettings, "Launch Settings…")] }
            ?? MenuBuilder.entries(for: .projects, actionsInput)
        MenuBuilder.items(for: entries, target: self, sessionAction: #selector(workspaceLaunchChosen(_:)),
                          actionSelector: #selector(workspaceLaunchChosen(_:))).forEach(menu.addItem)
    }

    @objc private func workspaceLaunchChosen(_ sender: NSMenuItem) {
        guard let box = sender.representedObject as? MenuBuilder.ActionBox else { return }
        perform(box.action)
    }

    func workspaceContext(_ row: WorkspaceWaitingRow, completion: @escaping (SecretText) -> Void) {
        guard store.feed == .live else { completion(SecretText("Waiting for a live session list.")); return }
        let command = self.command
        QueueWorkRunner.concurrent().run({
            WorkspaceWaitingContext.read(row, asl: DaemonClient()) { id in
                guard let command else {
                    return .failure(.launchFailed(binary: "amx", reason: "amx is still being resolved"))
                }
                return command.json(PromptLook.self, PromptLook.arguments(runtimeSessionID: id))
            }
        }, then: completion)
    }

    func workspacePerform(_ action: WorkspaceCommand) {
        // Entries may have changed while the palette was open.
        guard let entry = workspaceCommands.first(where: { $0.place == .command(action) }) else { return }
        if let reason = entry.unavailableReason { return refuse(reason) }
        switch action {
        case let .splitSession(key): _ = workspace?.split(with: key)
        case .splitShell: splitWorkspaceShell(nil)
        case .splitChanges: splitWorkspaceChanges(nil)
        case .focusOtherPane: focusOtherWorkspacePane(nil)
        case .focusReview: _ = workspace?.focusReview()
        case .swapPanes: swapWorkspacePanes(nil)
        case .collapsePane: collapseWorkspacePane(nil)
        case let .launch(project): launch(project)
        case let .resume(conversation): continueConversation(conversation)
        case .newTerminal: newSessionTerminal(nil)
        case .newShell: newShellFromMenu(nil)
        case .dashboard: openDashboardFromMenu(nil)
        case .overview: openOverviewFromMenu(nil)
        case .nextWaiting: nextAttention(nil)
        case .settings: showPreferences(nil)
        case .activity: showActivity(nil)
        case .history: showHistory(nil)
        case .runs: showRuns(nil)
        case .runPlan: runPlan(nil)
        case .changes: showChanges(nil)
        }
    }

    // MARK: Function keys

    // F1 to F11 do in the workspace what Kitty binds them to for its tabs.
    // The ones about a session act on the one the pane is on.

    @objc func showKeybindings(_ sender: Any?) { showWorkspaceView(.keybindings) }
    @objc func focusNextSession(_ sender: Any?) { workspace?.selectNeighbor(1, sessionsOnly: true) }
    @objc func focusPreviousSession(_ sender: Any?) { workspace?.selectNeighbor(-1, sessionsOnly: true) }
    @objc func pickSessionIcon(_ sender: Any?) { openPicker(.iconPicker) }
    @objc func pickSessionColor(_ sender: Any?) { openPicker(.colorPicker) }
    @objc func clearSessionState(_ sender: Any?) { sendOperatorEvent("user_clear") }
    @objc func markSessionActive(_ sender: Any?) { sendOperatorEvent("user_active") }

    static let noSessionShown = "No session is shown in the workspace."
    static let pickerNeedsEmbedded = "The icon and color pickers open here for sessions the workspace embeds; a Kitty session has them in Kitty."

    private func openPicker(_ kind: WorkspaceUtilityView.Kind) {
        guard let workspace, workspace.shownSessionRow != nil else { return refuse(Self.noSessionShown) }
        if !workspace.openPicker(kind) { refuse(Self.pickerNeedsEmbedded) }
    }

    /// What `amx hook-indicator` is given to name a session: its runtime
    /// session, or its window in the Kitty that issued it.
    nonisolated static func hookTarget(_ target: WorkspaceTarget) -> [String] {
        switch target {
        case let .embedded(id): return ["--runtime-session", id]
        case let .kitty(_, windowID): return ["--window-id", String(windowID)]
        }
    }

    /// F8 and F11: tells the daemon the operator cleared the shown session's
    /// state, or marked it active, the way Kitty's own keys do.
    private func sendOperatorEvent(_ event: String) {
        guard let row = workspace?.shownSessionRow else { return refuse(Self.noSessionShown) }
        guard let command else { return refuse(preferences.binaryMenuProblem ?? "amx is still being resolved; try again in a moment.") }
        var socket: String?
        if case let .kitty(rowSocket, _) = row.target {
            socket = rowSocket.isEmpty ? store.snapshot?.kitty.socket ?? "" : rowSocket
            guard socket?.isEmpty == false else { return refuse(KittyEndpoint.listenOnMissing) }
        }
        let arguments = ["hook-indicator", event] + Self.hookTarget(row.target) + ["--strict"]
        amxQueue.async { [weak self] in
            let result = command.run(arguments, kittyListenOn: socket)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self else { return }
                    switch result {
                    case let .success(output) where output.status == 0:
                        Self.logger.notice("\(event, privacy: .public) sent for \(row.project, privacy: .public)")
                    case let .success(output):
                        Self.logger.error("\(event, privacy: .public): amx exited \(output.status)")
                        self.store.show(notice: "The daemon did not take the change; is it running?")
                    case let .failure(error):
                        self.store.show(notice: error.message)
                    }
                }
            }
        }
    }

    /// F10: toggles the mute of the shown session.
    @objc func toggleSessionMute(_ sender: Any?) {
        guard let row = workspace?.shownSessionRow else { return refuse(Self.noSessionShown) }
        workspaceToggleMute(row)
    }

    /// Toggles the mute of a listed session: the one the pane is on, or the
    /// line a right-click was on.
    func workspaceToggleMute(_ row: WorkspaceRow) {
        switch row.target {
        case let .kitty(socket, windowID):
            toggleMute(MenuSessionRow(title: row.project, windowID: windowID, kittySocket: socket, toolTip: "", muted: row.muted))
        case let .embedded(id):
            guard command != nil else { return refuse(preferences.binaryMenuProblem ?? "amx is still being resolved; try again in a moment.") }
            toggleMute(runtimeSessionID: id)
        }
    }

    /// `amx mute-toggle --runtime-session`: a session the workspace embeds
    /// has no window to name. Like the Kitty path, it shows the change at
    /// once, so a second click (or a banner's Mute) before the daemon
    /// publishes it does not toggle the session back.
    private func toggleMute(runtimeSessionID id: String, banner: Bool = false) {
        guard let command else { return }
        let key = AttentionCycler.Key.embedded(id)
        if let listed = store.sessions.first(where: { $0.embeddedRuntimeSessionID == id }) {
            store.expectMute(key, muted: !listed.muted)
        }
        amxQueue.async { [weak self] in
            let result = command.json(MuteToggleResult.self, MuteToggleResult.arguments(runtimeSessionID: id))
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self else { return }
                    switch result {
                    case let .success(value), let .partial(value, _):
                        Self.logger.notice("mute-toggle \(id, privacy: .public): muted=\(value.muted)")
                        self.settleExpectedMute(value, key: key)
                    case let .failure(error):
                        Self.logger.error("mute-toggle \(id, privacy: .public): \(error.message, privacy: .public)")
                        self.store.dropExpectedMute(key)
                        self.actionFailed(Self.muteFailedTitle, error.message, banner: banner)
                    }
                }
            }
        }
    }

    static let muteFailedTitle = "Could not toggle mute"

    @objc func focusSessionList(_ sender: Any?) {
        showWorkspace()
        workspace?.focusSidebar()
    }

    @objc func focusWaiting(_ sender: Any?) {
        showWorkspace()
        workspace?.focusWaiting()
    }

    // MARK: Companion feed

    /// How long the first subscribe waits for amx to be resolved.
    static let firstSubscribeWait: TimeInterval = 3

    /// Opens the companion feed, once; later calls do nothing.
    private func startSubscription() {
        guard subscription == nil, !isTerminating else { return }
        let client = SubscriptionClient(request: subscribeRequest.params) { [weak self] event in
            DispatchQueue.main.async {
                MainActor.assumeIsolated { self?.handle(event) }
            }
        }
        subscription = client
        client.start()
    }

    private var lastFeed: FeedState?
    /// Whether the subscription is waiting out a retry delay, as against
    /// dialing: only then does a reconnect request start a dial sooner, and
    /// one sent mid-dial would drop the connection it was about to make.
    private var subscriptionWaiting = false
    private var lastDisconnect: SubscriptionClient.Disconnect?

    private func handle(_ event: SubscriptionClient.Event) {
        switch event {
        case .connecting:
            subscriptionWaiting = false
        case .subscribed:
            lastDisconnect = nil
        case let .disconnected(reason, retryIn):
            subscriptionWaiting = true
            notifications?.connectionEnded()
            // The raw reason, once per change; the store shows an operator
            // phrase.
            if reason != lastDisconnect {
                lastDisconnect = reason
                Self.logger.notice("feed disconnected: \(String(describing: reason), privacy: .public); retry in \(retryIn)s")
            }
            if case .bye = reason {
                // A restart is held as reconnecting; look again once the hold
                // is over, in case nothing else does.
                DispatchQueue.main.asyncAfter(deadline: .now() + SessionStore.byeHold + 0.1) { [weak self] in
                    MainActor.assumeIsolated { self?.refreshDaemonStatus() }
                }
            }
        case let .frame(.notification(_, data)):
            notifications?.present(frame: data)
        case let .frame(.notificationRetract(_, frame)):
            notifications?.retract(frame)
        case let .frame(.workspaceView(_, request)):
            handle(request)
        default:
            break
        }
        store.apply(event)
        followDaemonBuild()
        if store.feed != lastFeed {
            lastFeed = store.feed
            Self.logger.notice("feed: \(String(describing: self.store.feed), privacy: .public)")
            if store.feed != .live { refreshDaemonStatus() }
            if store.feed == .live { store.replace(notice: DaemonStart.failedNotice, with: nil) }
        }
    }

    private func storeChanged() {
        historyWindow?.model.liveConversations = Set(store.feed == .live ? store.sessions.compactMap { $0.conversation?.encoded } : [])
        reconcileSnooze()
        statusItem?.refresh()
        workspace?.refresh()
        // Only a live snapshot can say a question is no longer open.
        if store.feed == .live, let snapshot = store.snapshot {
            aslPanel?.flow.observe(waiting: snapshot.asl.waiting)
        }
        preferences.daemonStatus = store.daemonStatus
        publishPreferences()
    }

    /// Hands Preferences what it reports, without republishing an unchanged
    /// value on every snapshot.
    private func publishPreferences() {
        if preferencesModel.snapshot != preferences { preferencesModel.snapshot = preferences }
        let notification = store.feed == .live ? store.snapshot?.notification : nil
        if preferencesModel.daemonNotification != notification { preferencesModel.daemonNotification = notification }
        workspace?.refreshCommands()
    }

    // MARK: Daemon health

    /// Polls health while the feed is not live; a live feed carries the
    /// daemon's own status in every snapshot.
    func refreshDaemonStatus() {
        guard store.feed != .live, !healthInFlight else { return }
        healthInFlight = true
        healthQueue.async { [weak self] in
            let status = DaemonClient().health()
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self else { return }
                    self.healthInFlight = false
                    if status != self.store.health {
                        Self.logger.notice("daemon: \(DaemonStatusLine.text(for: status), privacy: .public)")
                    }
                    self.store.apply(health: status)
                    if Self.shouldReconnect(after: status, feed: self.store.feed) { self.reconnectIfWaiting() }
                }
            }
        }
    }

    /// Whether a health answer means the feed should dial now: the daemon
    /// answers, yet the feed is not live and not merely too old to subscribe.
    nonisolated static func shouldReconnect(after status: DaemonStatus, feed: FeedState) -> Bool {
        switch status {
        case .ready, .degraded: break
        case .notRunning, .unresponsive: return false
        }
        switch feed {
        case .notRunning, .reconnecting: return true
        case .live, .connecting, .daemonTooOld: return false
        }
    }

    /// Asks the subscription to dial at once if it is waiting to retry.
    private func reconnectIfWaiting() {
        guard subscriptionWaiting else { return }
        subscriptionWaiting = false
        subscription?.reconnectNow()
    }

    /// How often the feed redials after Start Daemon, for `DaemonStart.deadline`.
    static let startPollInterval: TimeInterval = 0.25

    /// Redials every `startPollInterval` until the feed is live or `deadline`
    /// passes, so a daemon that just started is listed within a moment of
    /// listening rather than after the backoff's next step. Each pass also
    /// decides whether the start has succeeded or failed.
    private func reconnectWhileStarting(until deadline: Date) {
        let passed = Date() >= deadline
        checkDaemonStart(deadlinePassed: passed)
        guard daemonStartPID != nil, store.feed != .live, !passed else { return }
        reconnectIfWaiting()
        DispatchQueue.main.asyncAfter(deadline: .now() + Self.startPollInterval) { [weak self] in
            MainActor.assumeIsolated { self?.reconnectWhileStarting(until: deadline) }
        }
    }

    // MARK: Focus

    /// Focuses a session where it is: an embedded one in the workspace, a
    /// Kitty one in Kitty, which is then brought forward.
    func focus(_ row: MenuSessionRow) {
        if !row.runtimeSessionID.isEmpty {
            focusSession(row.runtimeSessionID)
            return
        }
        let candidates = KittyEndpoint.candidates(
            override: preferencesModel.preferences.kittySocketOverride,
            rowSocket: row.kittySocket,
            snapshotSocket: store.snapshot?.kitty.socket ?? "",
            kittyPIDs: KittyEndpoint.runningKittyPIDs()
        )
        guard !candidates.isEmpty else {
            store.show(notice: KittyEndpoint.listenOnMissing)
            return
        }
        focusQueue.async { [weak self] in
            let client = DaemonClient()
            var outcome = FocusOutcome.unknownSocket
            var used = ""
            for socket in candidates {
                outcome = client.focusWindow(socket: socket, windowID: row.windowID)
                used = socket
                if outcome != .unknownSocket { break }
            }
            DispatchQueue.main.async {
                MainActor.assumeIsolated { self?.finishFocus(row, outcome: outcome, socket: used) }
            }
        }
    }

    /// Shows a session that has no Kitty window. The daemon is asked
    /// (`focus_session`) and answers this workspace with a view frame; when it
    /// cannot be asked, or shows nothing, the workspace shows the session
    /// itself, as it did before the daemon knew the request.
    func focusSession(_ runtimeSessionID: String) {
        focusQueue.async { [weak self] in
            let outcome = DaemonClient().focusSession(runtimeSessionID: runtimeSessionID)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self, outcome != .shown else { return }
                    Self.logger.notice("focus_session \(runtimeSessionID, privacy: .public): \(String(describing: outcome), privacy: .public); showing it here")
                    self.showWorkspace(focusing: runtimeSessionID)
                }
            }
        }
    }

    private func finishFocus(_ row: MenuSessionRow, outcome: FocusOutcome, socket: String) {
        switch outcome {
        case .focused:
            if !KittyActivator.activate(socket: socket) {
                store.show(notice: "Kitty is not running.")
            }
        case .staleTarget:
            store.markStale(socket: row.kittySocket, windowID: row.windowID)
            statusItem?.rebuildMenu()
        case .unknownSocket:
            store.show(notice: "The daemon does not know this session's Kitty socket.")
        case .unsupported:
            store.show(notice: "Daemon too old to focus sessions — \(MenuBuilder.restartHint)")
        case .notRunning:
            store.apply(health: .notRunning)
        case let .failed(message):
            Self.logger.error("focus_window \(row.windowID): \(message, privacy: .public)")
            store.show(notice: "Could not focus the session: \(message)")
        }
    }

    // MARK: Menu actions

    func perform(_ action: MenuAction) {
        switch action {
        case .startDaemon:
            startDaemon()
        case .restartDaemon:
            restartDaemon()
        case .openWorkspace:
            showWorkspace()
        case .openDashboard:
            openDashboard()
        case .launchSettings:
            showPreferences(nil)
            preferencesModel.launchSettingsRequest += 1
        case .nextAttention:
            focusNextAttention()
        case let .launchProject(project):
            launch(project)
        case let .launchProjectInFolder(project):
            launchInChosenFolder(project)
        case let .continueConversation(conversation):
            continueConversation(conversation)
        case let .snooze(choice):
            snooze(choice)
        case let .answerASL(target):
            answer(target)
        }
    }

    // MARK: ASL questions

    /// Answer… on a menu row or a banner: opens the answer panel on that run.
    func answer(_ target: ASLAnswerTarget) {
        let controller = aslPanel ?? ASLQuestionPanelController()
        if aslPanel == nil {
            controller.onFinish = { [weak self] notice in self?.finishAnswer(notice) }
            controller.workspaceFrame = { [weak self] in self?.openWorkspaceFrame }
            aslPanel = controller
        }
        Self.logger.notice("answer panel: run \(target.runID, privacy: .public)")
        controller.show(target)
    }

    /// The workspace window's frame while it is on screen; nil otherwise,
    /// without building a window that was never opened.
    private var openWorkspaceFrame: CGRect? {
        guard let workspace, workspace.isOpen, workspace.window.isVisible else { return nil }
        return workspace.window.frame
    }

    /// The panel closed itself, its answer accepted; the notice says so. The
    /// menu is rebuilt now; the snapshot that drops the ❓ follows from the
    /// daemon.
    private func finishAnswer(_ notice: String?) {
        if let notice { store.show(notice: notice) }
        statusItem?.rebuildMenu()
    }

    /// Runs `amx daemon start` detached, only ever because the operator chose
    /// "Start daemon": the app never starts the daemon on its own. Its
    /// standard error goes to a file the log quotes when the start fails.
    private func startDaemon() {
        guard let command else {
            lookAgainForAmx()
            store.show(notice: preferences.binaryMenuProblem ?? "amx is still being resolved; try again in a moment.")
            return
        }
        if let busy = daemonBusy { return report(busy) }
        let log = DaemonStart.stderrLog()
        let exited: @Sendable (pid_t, Int32) -> Void = { [weak self] pid, code in
            DispatchQueue.main.async {
                MainActor.assumeIsolated { self?.daemonStartExited(pid: pid, code: code) }
            }
        }
        guard let pid = command.spawnDetached(["daemon", "start"], extraEnvironment: DaemonStart.detachedEnvironment,
                                              stderrPath: log?.path, onExit: exited) else {
            store.show(notice: "Could not run \(command.binary.path) daemon start.")
            return
        }
        Self.logger.notice("started amx daemon (pid \(pid))")
        daemonStartPID = pid
        daemonStartExit = nil
        daemonStartLog = log
        store.show(notice: DaemonStart.startingNotice)
        reconnectWhileStarting(until: Date().addingTimeInterval(DaemonStart.deadline))
    }

    /// The `amx daemon start` the app is waiting on, until it has started or
    /// failed, and its exit code once it has exited.
    private var daemonStartPID: pid_t?
    private var daemonStartExit: Int32?
    private var daemonStartLog: URL?
    /// Set while `amx daemon restart` runs.
    private var daemonRestartRunning = false

    /// What the daemon start or restart under way is doing, or nil. While one
    /// runs, another is refused and the menu greys both out: a restart
    /// arriving behind a start, or a second restart, would stop the daemon
    /// the first had just brought up.
    var daemonBusy: String? {
        if daemonRestartRunning { return DaemonStart.restartingNotice }
        if daemonStartPID != nil { return DaemonStart.startingNotice }
        return nil
    }

    private func daemonStartExited(pid: pid_t, code: Int32) {
        Self.logger.notice("amx daemon start (pid \(pid)) exited \(code)")
        guard pid == daemonStartPID else { return }
        daemonStartExit = code
        checkDaemonStart(deadlinePassed: false)
    }

    /// Replaces the Starting notice once the start is decided: cleared when
    /// the daemon answers, "did not start" when it failed. The reason stays
    /// in the log, never in the menu.
    private func checkDaemonStart(deadlinePassed: Bool) {
        guard let pid = daemonStartPID else { return }
        switch DaemonStart.verdict(feed: store.feed, exitCode: daemonStartExit, deadlinePassed: deadlinePassed) {
        case .waiting:
            return
        case .started:
            daemonStartPID = nil
            store.replace(notice: DaemonStart.startingNotice, with: nil)
            statusItem?.refresh()
        case .failed:
            daemonStartPID = nil
            let exit = daemonStartExit.map { "exited \($0)" } ?? "still running"
            let tail = daemonStartLog.flatMap { try? Data(contentsOf: $0) }.map { DaemonStart.tail($0) } ?? ""
            Self.logger.error("amx daemon start (pid \(pid)) did not start the daemon (\(exit, privacy: .public)): \(tail, privacy: .public)")
            store.show(notice: DaemonStart.failedNotice)
        }
    }

    /// Restart Daemon, offered when the daemon is too old for this app:
    /// `amx daemon restart`, which returns once the new daemon answers, so
    /// the feed dials it at once.
    private func restartDaemon() {
        guard let command else {
            return report(preferences.binaryMenuProblem ?? "amx is still being resolved; try again in a moment.")
        }
        if let busy = daemonBusy { return report(busy) }
        daemonRestartRunning = true
        let change = beginChange("restarting the daemon")
        store.show(notice: DaemonStart.restartingNotice)
        controlQueue.async { [weak self] in
            let result = command.run(["daemon", "restart"], timeout: AMXCommand.actionTimeout, mutates: true)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self else { return }
                    self.endChange(change)
                    self.daemonRestartRunning = false
                    self.finishRestart(result)
                    self.statusItem?.refresh()
                }
            }
        }
    }

    private func finishRestart(_ result: Result<ProcessOutput, AMXCommandError>) {
        let failure: String?
        switch result {
        case let .failure(error):
            failure = error.message
        case let .success(output) where output.status != 0:
            failure = DaemonStart.tail(output.stderr, lines: 1, characters: 200)
            Self.logger.error("amx daemon restart exited \(output.status): \(DaemonStart.tail(output.stderr), privacy: .public)")
        case let .success(output):
            failure = nil
            Self.logger.notice("\(DaemonStart.tail(output.stdout), privacy: .public)")
        }
        guard let failure else {
            store.replace(notice: DaemonStart.restartingNotice, with: nil)
            subscription?.reconnectNow()
            return
        }
        reportFailure(title: "Could not restart the daemon", failure.isEmpty ? "amx daemon restart failed" : failure)
    }

    // MARK: amx

    /// Records the backend setup amx reported, so a missing or refused tmux is
    /// in the log with its fix, not only in Preferences.
    nonisolated private static func log(_ setup: BackendSetup) {
        switch setup {
        case .unknown:
            break
        case let .unavailable(reason):
            logger.error("backend: amx could not report the setup: \(reason, privacy: .public)")
        case let .status(status):
            logger.notice("backend: \(status.tmuxLine, privacy: .public) at \(status.tmux.path, privacy: .public); session server: \(status.serverLine, privacy: .public)")
            for problem in status.problems {
                let line = "backend: \(problem.severity) \(problem.code): \(problem.text)"
                if problem.needsAttention {
                    logger.error("\(line, privacy: .public)")
                } else {
                    logger.notice("\(line, privacy: .public)")
                }
            }
        }
    }

    /// Which resolve is the current one: what an older, slower one finds is
    /// dropped.
    private var resolveGeneration = 0
    private var resolveRunning = false
    /// The daemon build the current amx was chosen against; nil when no
    /// daemon answered then.
    private var resolvedDaemonBuild: DaemonBuild?
    /// The resolve or probe retry waiting its turn.
    private var pendingRetry: DispatchWorkItem?
    private var resolveRetries = 0
    private var probeRetries = 0
    /// Why what amx offers is not known, once every probe went unanswered.
    private var capabilityProblem: String?

    /// How long a resolve that found no amx, or a probe amx did not answer,
    /// waits before it is tried again: an amx installed, or a machine less
    /// busy, a moment later is picked up without a relaunch. Bounded, so an
    /// amx that is not coming is not looked for all day.
    nonisolated static let retryDelays: [TimeInterval] = [2, 5, 15, 30, 60]

    /// Reads the login shell's environment, asks the daemon which build it
    /// runs, and resolves amx against both. The shell can take seconds, so
    /// none of it runs on the main thread; it is read once, and a later
    /// resolve (after an override changes) reuses it.
    ///
    /// amx is usable as soon as it is found: what it offers (`--help`) and
    /// the backend report are asked for afterwards, so a slow answer there
    /// holds up only the items that depend on it. `--version` is read once,
    /// by the resolver when it compares copies, and reused.
    ///
    /// `daemonBuild` is the build a live feed reports, which saves asking;
    /// `retry` keeps the count of earlier attempts.
    private func resolveBinary(daemonBuild known: DaemonBuild? = nil, retry: Bool = false) {
        pendingRetry?.cancel()
        pendingRetry = nil
        if !retry { resolveRetries = 0 }
        resolveGeneration += 1
        let generation = resolveGeneration
        resolveRunning = true
        let detected = detectedEnvironment
        let overrides = preferencesModel.preferences.environment
        work.async { [weak self] in
            let shell = detected ?? LaunchEnvironment.current()
            let environment = shell.applying(overrides).namingBundledRuntime(resources: Bundle.main.resourceURL)
            let build = known ?? DaemonClient().health().build
            let versions = VersionCache()
            var resolver = AMXBinaryResolver.live(searchPath: environment.path, daemonBuild: build)
            resolver.identity = { versions.read($0) }
            let resolution = resolver.resolve()
            var command: AMXCommand?
            var binaryVersion: AMXVersion?
            var report: AMXBuildReport?
            switch resolution {
            case let .success(binary):
                command = AMXCommand(binary: binary.url, launchEnvironment: environment)
                binaryVersion = versions.read(binary.url.path)
                report = AMXBuildReport.of(binary: binary.url, version: binaryVersion)
                Self.logger.notice("running \(binary.url.path, privacy: .public) (\(String(describing: binary.source), privacy: .public))")
            case let .failure(notFound):
                Self.logger.error("\(notFound.message, privacy: .public)")
            }
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self, generation == self.resolveGeneration else { return }
                    self.finishResolve(resolution, command: command, version: binaryVersion, report: report,
                                       shell: shell, build: build, generation: generation)
                }
            }
        }
    }

    /// Publishes what a resolve found, then asks the amx it found what it
    /// offers, or schedules another look when it found none.
    private func finishResolve(
        _ resolution: Result<AMXBinaryResolver.Resolution, AMXBinaryResolver.NotFound>,
        command: AMXCommand?, version: AMXVersion?, report: AMXBuildReport?,
        shell: LaunchEnvironment, build: DaemonBuild?, generation: Int
    ) {
        resolveRunning = false
        resolvedDaemonBuild = build
        detectedEnvironment = shell
        let same = self.command?.binary == command?.binary && self.command?.launchEnvironment == command?.launchEnvironment
        if !same {
            projects.state.reset()
            projects.missingDirectories = []
            recent.state.reset()
        }
        var snapshot = PreferencesSnapshot()
        snapshot.detectedEnvironment = shell
        if case let .failure(notFound) = resolution {
            snapshot.binaryProblem = notFound.message
            snapshot.binaryMenuProblem = notFound.menuMessage
        } else {
            snapshot.binary = try? resolution.get()
            snapshot.binaryVersion = version
            // The same amx keeps what it was found to offer until it is
            // asked again.
            if same, command != nil {
                snapshot.capabilities = preferences.capabilities
                snapshot.backend = preferences.backend
            } else {
                capabilityProblem = nil
            }
        }
        self.command = command
        workspace?.retryTerminal()
        let waiting = statusQuestions
        statusQuestions = []
        for (id, completion) in waiting {
            if command == nil {
                completion(.unknown(snapshot.binaryMenuProblem ?? "amx was not found."))
            } else {
                sessionStatus(runtimeSessionID: id, completion: completion)
            }
        }
        // Doctor reads the build an app reports only to compare it with the
        // daemon's; the daemon decides nothing by it. A live stream is kept,
        // and the next subscribe carries it.
        if subscribeRequest.report(build: report), store.feed == .live {
            Self.logger.notice("amx build changed; reported at the next subscribe")
        }
        snapshot.daemonStatus = store.daemonStatus
        preferences = snapshot
        publishPreferences()
        statusItem?.refresh()
        // The first subscribe waited for this, so it reports the build.
        startSubscription()

        guard let command else {
            scheduleRetry(attempt: &resolveRetries, what: "resolving amx again") { $0.resolveBinary(retry: true) }
            return
        }
        resolveRetries = 0
        probeRetries = 0
        probe(command, generation: generation, backend: true)
        followDaemonBuild()
    }

    /// Asks `amx` what it offers, then (with `backend`) for its backend
    /// report, publishing each as it comes. An unanswered probe is asked
    /// again, a few times, rather than read as an amx that lacks everything.
    private func probe(_ amx: AMXCommand, generation: Int, backend: Bool) {
        work.async { [weak self] in
            let capabilities = AMXCapabilities.probe(amx)
            DispatchQueue.main.async {
                MainActor.assumeIsolated { self?.finishProbe(capabilities, generation: generation) }
            }
            guard backend else { return }
            let setup = BackendSetup(BackendStatus.fetch(amx))
            Self.log(setup)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self, generation == self.resolveGeneration, self.preferences.backend != setup else { return }
                    self.preferences.backend = setup
                    self.publishPreferences()
                }
            }
        }
    }

    private func finishProbe(_ capabilities: AMXCapabilities?, generation: Int) {
        guard generation == resolveGeneration, let command else { return }
        if let capabilities {
            probeRetries = 0
            capabilityProblem = nil
            preferences.capabilities = capabilities
        } else if preferences.capabilities == nil {
            Self.logger.notice("\(command.binary.path, privacy: .public) did not answer --help")
            let scheduled = scheduleRetry(attempt: &probeRetries, what: "asking amx what it offers again") {
                guard generation == $0.resolveGeneration, let amx = $0.command else { return }
                $0.probe(amx, generation: generation, backend: false)
            }
            if !scheduled {
                capabilityProblem = "\(command.binary.path) did not say which commands it has; see the log"
            }
        }
        publishPreferences()
        statusItem?.refresh()
        statusItem?.refreshSubmenus()
        // Listed and surveyed once the gate knows what this amx can do, so
        // the first menu opening already knows which projects can be
        // launched.
        refreshProjects()
    }

    /// Looks for amx again when none was found and the bounded retries are
    /// spent: an amx installed since is picked up the next time the operator
    /// opens the menu or asks for the daemon, without a relaunch.
    private func lookAgainForAmx() {
        guard command == nil, !resolveRunning, pendingRetry == nil else { return }
        Self.logger.notice("no amx yet; looking again")
        resolveBinary()
    }

    /// Runs `action` after the next of `retryDelays`; false, scheduling
    /// nothing, once they are spent.
    @discardableResult
    private func scheduleRetry(attempt: inout Int, what: String, _ action: @escaping @MainActor (AppDelegate) -> Void) -> Bool {
        guard attempt < Self.retryDelays.count else { return false }
        let delay = Self.retryDelays[attempt]
        attempt += 1
        Self.logger.notice("\(what, privacy: .public) in \(Int(delay))s")
        pendingRetry?.cancel()
        let item = DispatchWorkItem { [weak self] in
            MainActor.assumeIsolated {
                guard let self else { return }
                action(self)
            }
        }
        pendingRetry = item
        DispatchQueue.main.asyncAfter(deadline: .now() + delay, execute: item)
        return true
    }

    /// Resolves again when the live daemon runs another build than the one
    /// amx was chosen against: one started (or replaced) since may be the
    /// artifact `~/.local/bin/amx` is, which then runs instead of the
    /// bundled copy.
    private func followDaemonBuild() {
        guard store.feed == .live, !resolveRunning, let build = store.snapshot?.daemon.build,
              build != resolvedDaemonBuild else { return }
        Self.logger.notice("daemon runs build \(build.version, privacy: .public) (\(build.revision ?? "unknown", privacy: .public)); resolving amx again")
        resolveBinary(daemonBuild: build)
    }

    /// Reads `amx backend status` again for Settings, which otherwise shows
    /// what the last resolve saw. It runs on the work queue, behind any
    /// resolve already queued, and is dropped if amx changed meanwhile.
    private func refreshBackendStatus() {
        guard let amx = command else { return }
        work.async { [weak self] in
            let backend = BackendSetup(BackendStatus.fetch(amx))
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self, self.command?.binary == amx.binary,
                          self.command?.launchEnvironment == amx.launchEnvironment,
                          self.preferences.backend != backend else { return }
                    self.preferences.backend = backend
                    self.publishPreferences()
                }
            }
        }
    }

    /// Resolves again once typing in Preferences pauses, so an environment
    /// value typed one key at a time does not run amx per key. The binary
    /// path arrives whole, when its field is submitted or left.
    private func scheduleResolve() {
        pendingResolve?.cancel()
        let item = DispatchWorkItem { [weak self] in
            MainActor.assumeIsolated { self?.resolveBinary() }
        }
        pendingResolve = item
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.6, execute: item)
    }

    // MARK: Preferences

    private func setUpPreferences() {
        let model = preferencesModel
        // The login item is SMAppService's to keep; the stored choice mirrors
        // it, so one removed in System Settings is not silently re-added.
        model.refreshLoginItem()
        if model.preferences.launchAtLogin != model.launchAtLogin {
            model.preferences.launchAtLogin = model.launchAtLogin
        }
        applyMenuBarPreferences(model.preferences)
        model.onChange = { [weak self] old, new in self?.preferencesChanged(from: old, to: new) }
        model.onResumeSnooze = { [weak self] in self?.snooze(.resume) }
        model.onRefreshAuthorization = { [weak self] in self?.notifications?.refreshAuthorization() }
        model.onRefreshBackend = { [weak self] in self?.refreshBackendStatus() }
        model.onRecordingChange = { [weak self] recording in
            guard let self else { return }
            self.isRecordingHotKey = recording
            if recording {
                self.dashboardHotKey?.unregister()
                self.attentionHotKey?.unregister()
            } else {
                self.registerHotKeys()
            }
        }
    }

    private func preferencesChanged(from old: AppPreferences, to new: AppPreferences) {
        workspace?.confirmBeforeClosing = new.confirmBeforeClosing
        switch Self.terminalReload(from: old, to: new) {
        case .resetFontSize: applyDefaultFontSize()
        case .keepZoom: terminals?.reloadConfig()
        case nil: break
        }
        // While a recorder listens the shortcuts stay unregistered; it
        // registers them once, with what it recorded, when it stops.
        if !isRecordingHotKey,
           old.hotKeyDashboard != new.hotKeyDashboard || old.hotKeyNextAttention != new.hotKeyNextAttention {
            registerHotKeys()
        }
        if old.workspaceShortcuts != new.workspaceShortcuts || old.hotKeyDashboard != new.hotKeyDashboard || old.hotKeyNextAttention != new.hotKeyNextAttention,
           let menu = NSApp.mainMenu {
            WorkspaceShortcut.apply(new, to: menu)
        }
        applyMenuBarPreferences(new)
        if recent.follow(days: new.recentDays) {
            statusItem?.refreshSubmenus()
        }
        if old.binaryPath != new.binaryPath || old.environment != new.environment {
            scheduleResolve()
        }
        notifications?.enabled = new.useAppNotifications
    }

    /// What Settings forces on every terminal's Ghostty config.
    nonisolated static func terminalOverrides(_ preferences: AppPreferences) -> Ghostty.ConfigOverrides {
        Ghostty.ConfigOverrides(fontSize: preferences.defaultFontSize, theme: preferences.terminalTheme?.configValue)
    }

    enum TerminalReload: Equatable {
        /// A new default zoom: reload, then reset every terminal to it.
        case resetFontSize
        /// A new theme: every open terminal, shown or hidden, takes the new
        /// colors and keeps its own zoom.
        case keepZoom
    }

    /// How open terminals take a Settings change; nil when it is not theirs.
    nonisolated static func terminalReload(from old: AppPreferences, to new: AppPreferences) -> TerminalReload? {
        if old.defaultFontSize != new.defaultFontSize { return .resetFontSize }
        if old.terminalTheme != new.terminalTheme { return .keepZoom }
        return nil
    }

    /// Reload first so ⌘⇧- uses the new default, then reset even manually
    /// zoomed terminals, including the workspace's hidden session views.
    private func applyDefaultFontSize() {
        terminals?.reloadConfig(resetFontSize: true)
        workspace?.forgetFontSizes()
        refreshConfigFontSize()
    }

    private func refreshConfigFontSize() {
        guard preferencesModel.preferences.defaultFontSize == nil else { return }
        let config = terminals?.config ?? Ghostty.Config()
        if let size = config.get("font-size", as: Float.self, initial: 0), size.isFinite, size > 0 {
            preferencesModel.configFontSize = size
        }
    }

    // MARK: Notifications

    private func setUpNotifications() {
        let presenter = NotificationPresenter(
            center: SystemNotificationCenter(),
            enabled: preferencesModel.preferences.useAppNotifications
        )
        UNUserNotificationCenter.current().delegate = presenter
        if !Self.isRunningTests { presenter.removeEarlierLaunches() }
        subscribeRequest.claim = presenter.claim
        presenter.onClaimChange = { [weak self, unowned presenter] claim in
            guard let self else { return }
            Self.logger.notice("notifications: backend claim \(claim.backend ? "on" : "off", privacy: .public) (\(String(describing: presenter.authorization), privacy: .public))")
            // The daemon routes notifications by the role claimed in
            // subscribe, at once, so a changed claim needs a new subscribe;
            // an unchanged one keeps the stream.
            guard self.subscribeRequest.claim != claim else { return }
            self.subscribeRequest.claim = claim
            self.subscription?.reconnectNow()
        }
        presenter.report = { [weak self] id, disposition, error in
            self?.dispositionQueue.async {
                let params = DispositionParams(notificationID: id, disposition: disposition.rawValue, error: error)
                if let problem = DaemonClient().notificationDisposition(params) {
                    Self.logger.error("notification \(id, privacy: .public) \(disposition.rawValue, privacy: .public): \(problem, privacy: .public)")
                }
            }
        }
        presenter.onResponse = { [weak self] response in self?.respond(to: response) }
        presenter.onAuthorizationChange = { [weak self] authorization in
            self?.preferencesModel.notificationAuthorization = authorization
        }
        notifications = presenter
        presenter.refreshAuthorization()
    }

    /// What the app does after reporting a banner response. The daemon
    /// focuses a Kitty pane on `clicked` and the app brings that Kitty
    /// forward; a session with no Kitty window is asked for by
    /// `focus_session`. The app carries out the actions the daemon only names.
    private func respond(to response: NotificationPresenter.Response) {
        let origin = Self.origin(of: response, in: store.sessions, snapshotKitty: store.snapshot?.kitty ?? CompanionKitty())
        switch response.disposition {
        case .clicked:
            if response.notificationID.hasPrefix(NotificationPresenter.appPrefix) {
                // The app's own failure: its line is in the status menu,
                // which a click opens on it.
                return DispatchQueue.main.async { [weak self] in
                    MainActor.assumeIsolated { self?.statusItem?.openMenu() }
                }
            }
            if !response.embeddedRuntimeSessionID.isEmpty {
                return focusSession(response.embeddedRuntimeSessionID)
            }
            if !response.workspaceViewID.isEmpty {
                return showWorkspace(onView: response.workspaceViewID)
            }
            guard response.navigable, let socket = origin.socket else { return }
            _ = KittyActivator.activate(socket: socket)
        case .actionMute:
            // A session the workspace embeds has no window to match by.
            let embedded = Self.session(runtimeSessionID: response.embeddedRuntimeSessionID, in: store.sessions)
            guard let session = embedded ?? origin.session else {
                return reportFailure(title: "Could not mute", "The session is no longer listed.")
            }
            // mute-toggle toggles; a banner's Mute never unmutes. `muted`
            // includes a mute the app has asked for and not yet seen.
            guard !session.muted else { return }
            toggleMute(.focusing(session), banner: true)
        case .actionSnooze:
            // A banner's snooze only ever lengthens the one in force.
            let current = store.feed == .live ? store.snapshot.map { SnoozeState($0.notification) } : nil
            if let current, current.wouldShorten(to: .oneHour, now: Date()) {
                Self.logger.notice("snooze: banner kept the longer \(current.description(now: Date()), privacy: .public)")
                return
            }
            snooze(.oneHour, banner: true)
        case .actionAnswer:
            guard !response.runID.isEmpty else { return reportFailure(title: "Could not answer", "The notification names no run.") }
            answer(ASLAnswerTarget(runID: response.runID, windowID: response.windowID))
        case .actionRespond:
            guard !response.embeddedRuntimeSessionID.isEmpty else { return reportFailure(title: "Could not answer", "The notification names no session.") }
            answerPrompt(PromptAnswerTarget(runtimeSessionID: response.embeddedRuntimeSessionID))
        case .delivered, .dismissed, .failed:
            break
        }
    }

    /// Answer… on a permission banner: opens the prompt panel on that
    /// session. The panel reads the session's screen before it offers a key.
    func answerPrompt(_ target: PromptAnswerTarget) {
        let controller = promptPanel ?? PromptAnswerPanelController(flow: PromptAnswerFlow(service: AMXPromptAnswerService(
            command: { [weak self] in self?.command },
            problem: { [weak self] in self?.preferences.binaryMenuProblem ?? "amx has not been found yet." }
        )))
        controller.workspaceFrame = { [weak self] in self?.openWorkspaceFrame }
        controller.onLateResult = { [weak self] notice in self?.finishAnswer(notice) }
        promptPanel = controller
        Self.logger.notice("prompt panel: session \(target.runtimeSessionID, privacy: .public)")
        controller.show(target)
    }

    /// Where a notice came from, as far as it can be told apart from every
    /// other Kitty instance: the socket a click raises, and the listed session
    /// a Mute acts on. Either is nil when the notice cannot name it safely.
    struct NoticeOrigin: Equatable {
        let socket: String?
        let session: CompanionSession?
    }

    /// Resolves a response against the listed sessions. Window IDs repeat
    /// across Kitty instances, so a notice carrying the daemon-captured socket
    /// matches only a session in that instance (an empty row socket naming
    /// the snapshot's), and is raised through that socket even when its row
    /// is gone. A notice with no socket — one from an older daemon — is
    /// matched by window alone only while the snapshot knows a single Kitty;
    /// with several it names nothing rather than a guess.
    nonisolated static func origin(of response: NotificationPresenter.Response, in sessions: [CompanionSession],
                                   snapshotKitty: CompanionKitty) -> NoticeOrigin {
        if !response.kittySocket.isEmpty {
            let wanted = AttentionCycler.Key(socket: response.kittySocket, windowID: response.windowID)
            let matches = response.windowID > 0
                ? sessions.filter { AttentionCycler.key($0, snapshotSocket: snapshotKitty.socket) == wanted }
                : []
            return NoticeOrigin(socket: response.kittySocket, session: matches.count == 1 ? matches[0] : nil)
        }
        var instances = Set(snapshotKitty.sockets.filter { !$0.isEmpty })
        if !snapshotKitty.socket.isEmpty { instances.insert(snapshotKitty.socket) }
        for session in sessions where !session.kittySocket.isEmpty { instances.insert(session.kittySocket) }
        guard instances.count <= 1 else { return NoticeOrigin(socket: nil, session: nil) }
        let session = Self.session(windowID: response.windowID, in: sessions)
        return NoticeOrigin(socket: instances.first ?? "", session: session)
    }

    /// The listed session a notice's window names, when exactly one does:
    /// window IDs repeat across Kitty instances, and a guess could mute or
    /// raise the wrong one.
    nonisolated static func session(windowID: Int, in sessions: [CompanionSession]) -> CompanionSession? {
        guard windowID > 0 else { return nil }
        let matches = sessions.filter { $0.windowID == windowID }
        return matches.count == 1 ? matches[0] : nil
    }

    private func applyMenuBarPreferences(_ preferences: AppPreferences) {
        guard let statusItem else { return }
        if statusItem.titleStyle != preferences.statusStyle { statusItem.titleStyle = preferences.statusStyle }
        if statusItem.showWorking != preferences.showWorkingCount { statusItem.showWorking = preferences.showWorkingCount }
        statusItem.kittySocketOverride = preferences.kittySocketOverride
    }

    // MARK: Global shortcuts

    private func setUpHotKeys() {
        let dashboard = HotKeyCenter()
        dashboard.onPress = { [weak self] in MainActor.assumeIsolated { self?.openDashboard() } }
        dashboardHotKey = dashboard
        let attention = HotKeyCenter()
        attention.onPress = { [weak self] in MainActor.assumeIsolated { self?.focusNextAttention() } }
        attentionHotKey = attention
        registerHotKeys()
    }

    /// Registers both shortcuts from the current preferences, so a change in
    /// Preferences takes effect at once. The dashboard's wins a tie.
    private func registerHotKeys() {
        guard let dashboardHotKey, let attentionHotKey else { return }
        let preferences = preferencesModel.preferences
        var problems: [String: String] = [:]
        problems[HotKeyPreference.dashboard.defaultsKey] = HotKeyConflicts.problem(for: dashboardHotKey.register(preferences.hotKeyDashboard))
        let earlier = [(label: HotKeyPreference.dashboard.label, combo: preferences.hotKeyDashboard)]
        if let duplicate = HotKeyConflicts.duplicate(preferences.hotKeyNextAttention, earlier: earlier) {
            attentionHotKey.unregister()
            problems[HotKeyPreference.nextAttention.defaultsKey] = duplicate
        } else {
            problems[HotKeyPreference.nextAttention.defaultsKey] = HotKeyConflicts.problem(for: attentionHotKey.register(preferences.hotKeyNextAttention))
        }
        let menuKeys = HotKeyConflicts.menuKeys(NSApp.mainMenu ?? WorkspaceMenu.make(target: self))
        for (preference, combo) in [(HotKeyPreference.dashboard, preferences.hotKeyDashboard),
                                    (HotKeyPreference.nextAttention, preferences.hotKeyNextAttention)]
        where problems[preference.defaultsKey] == nil {
            problems[preference.defaultsKey] = HotKeyConflicts.menuCollision(combo, menuKeys: menuKeys)
        }
        for problem in problems.values {
            Self.logger.notice("hotkey: \(problem, privacy: .public)")
        }
        preferencesModel.hotKeyProblems = problems
    }

    /// ⌃⌥⌘A: shows the workspace's Dashboard, or, when Preferences sends it
    /// to Kitty, focuses the dashboard running there or opens one, then
    /// brings that Kitty forward. With no Kitty to ask, the dashboard is the
    /// workspace's own either way.
    func openDashboard() {
        guard preferencesModel.preferences.dashboardPlace == .kitty else {
            showWorkspaceView(.dashboard)
            return
        }
        guard let command else {
            refuse(preferences.binaryMenuProblem ?? "amx is still being resolved; try again in a moment.")
            return
        }
        if let capabilities = preferences.capabilities, !capabilities.openDashboard {
            refuse("\(command.binary.path) has no open-dashboard command; run just install.")
            return
        }
        let candidates = DashboardOpener.candidates(
            override: preferencesModel.preferences.kittySocketOverride,
            snapshot: store.snapshot,
            kittyPIDs: KittyEndpoint.runningKittyPIDs()
        )
        amxQueue.async { [weak self] in
            let outcome = DashboardOpener.open(candidates: candidates) { socket in
                command.json(OpenDashboardResult.self, ["open-dashboard"], kittyListenOn: socket)
            }
            DispatchQueue.main.async {
                MainActor.assumeIsolated { self?.finishOpenDashboard(outcome) }
            }
        }
    }

    private func finishOpenDashboard(_ outcome: DashboardOpener.Outcome) {
        switch DashboardOpener.destination(for: outcome) {
        case let .kitty(result, socket):
            Self.logger.notice("dashboard \(result.opened ? "opened" : "focused", privacy: .public) (window \(result.windowID))")
            if !KittyActivator.activate(socket: socket) {
                refuse("Kitty is not running.")
            }
        case .workspace:
            Self.logger.notice("dashboard: no Kitty socket to ask; showing the workspace's own")
            showWorkspaceView(.dashboard)
        case let .failed(message):
            refuse("Could not open the dashboard: \(message)")
        }
    }

    /// ⌃⌥⌘N: focuses the first waiting session in the workspace sidebar's
    /// order, then the next on each press, across every Kitty instance and
    /// the workspace's own sessions.
    func focusNextAttention() {
        guard store.feed == .live, let snapshot = store.snapshot else {
            refuse(DaemonPhrase.notConnected + ".")
            return
        }
        guard let session = cycler.advance(in: store.sessions, snapshotSocket: snapshot.kitty.socket, order: attentionOrder) else {
            Self.logger.notice("next attention: nothing is waiting")
            NSSound.beep()
            return
        }
        Self.logger.notice("next attention: \(MenuBuilder.place(of: session), privacy: .public) (\(session.iconState, privacy: .public))")
        focus(.focusing(session))
    }

    /// A shortcut that could not act: the menu shows why the next time it
    /// opens, and the beep says so now.
    private func refuse(_ message: String) {
        Self.logger.notice("shortcut: \(message, privacy: .public)")
        store.show(notice: message)
        NSSound.beep()
    }

    // MARK: Actions

    /// Remembers the Kitty that comes to the front, which is where Launch
    /// Project and Recent open their tabs.
    private func trackActiveKitty() {
        if let front = NSWorkspace.shared.frontmostApplication, front.bundleIdentifier == KittyEndpoint.bundleIdentifier {
            lastActiveKittyPID = front.processIdentifier
        }
        activationObserver = NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.didActivateApplicationNotification, object: nil, queue: .main
        ) { [weak self] notification in
            let app = notification.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication
            guard app?.bundleIdentifier == KittyEndpoint.bundleIdentifier, let pid = app?.processIdentifier else { return }
            MainActor.assumeIsolated { self?.lastActiveKittyPID = pid }
        }
    }

    private var gate: ActionGate {
        ActionGate(
            capabilities: preferences.capabilities,
            problem: command == nil ? preferences.binaryMenuProblem : capabilityProblem,
            binaryPath: command?.binary.path ?? "amx",
            resolved: command != nil
        )
    }

    /// The sidebar's arrangement, which both the shortcut and the menu's
    /// Needs attention follow.
    var attentionOrder: WorkspaceOrder {
        workspace?.order ?? (Self.isRunningTests ? WorkspaceOrder() : .load())
    }

    var actionsInput: MenuBuilder.ActionsInput {
        let live = store.feed == .live
        let prefs = preferencesModel.preferences
        return MenuBuilder.ActionsInput(
            gate: gate,
            dashboardPlace: prefs.dashboardPlace,
            launchBackend: prefs.launchBackend,
            cliLaunchBackend: preferences.backend.cliLaunchBackend,
            cliLaunchBackendSource: preferences.backend.cliLaunchBackendSource,
            // Only a shortcut the system took: one refused, taken by another
            // app, or set aside while a recorder listens does nothing.
            dashboardHotKey: dashboardHotKey?.registration.registeredCombo,
            nextAttentionHotKey: attentionHotKey?.registration.registeredCombo,
            feedLive: live,
            waiting: live ? AttentionCycler.ordered(store.sessions).count : 0,
            projects: projects.state,
            missingProjectDirectories: projects.missingDirectories,
            recent: recent.state,
            recentDays: prefs.recentDays,
            recentLimit: prefs.recentLimit,
            liveConversations: store.sessions.compactMap(\.conversation).filter { !$0.sessionID.isEmpty },
            notification: live ? store.snapshot?.notification : nil,
            snoozeSupported: snoozeSupported,
            daemonBusy: daemonBusy,
            configPath: AMXConfigPath.resolve(command?.launchEnvironment ?? detectedEnvironment),
            now: Date()
        )
    }

    func menuWillOpen() {
        lookAgainForAmx()
        notifications?.refreshAuthorization()
        refreshProjects()
        surveyProjectDirectories()
        refreshRecent()
    }

    private func refreshProjects(force: Bool = false) {
        guard let command, gate.unavailableReason(.project) == nil,
              (force || projects.needsRefresh(now: Date())), projects.state.begin() else { return }
        listQueue.async { [weak self] in
            let result = ProjectCatalog.fetch(command)
            // Surveyed before it is shown, so a fresh list never offers a
            // launch into a directory that is not there.
            let missing = (try? result.get()).map { ProjectCatalog.survey($0) }
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self, self.command?.binary == command.binary else { return }
                    if case let .failure(error) = result {
                        Self.logger.error("project list: \(error.message, privacy: .public)")
                    }
                    self.projects.state.finish(result, at: Date())
                    if let missing { self.projects.missingDirectories = missing }
                    self.updateWorkspaceProjectMenu()
                    self.workspace?.refreshCommands()
                    self.statusItem?.refreshSubmenus()
                }
            }
        }
    }

    /// Re-checks the cached projects' directories each time the menu opens,
    /// so a checkout cloned or removed since the last list is reflected
    /// without waiting for the list itself to go stale.
    private func surveyProjectDirectories() {
        guard let list = projects.state.value, !list.isEmpty, !projects.surveying else { return }
        projects.surveying = true
        listQueue.async { [weak self] in
            let missing = ProjectCatalog.survey(list)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self else { return }
                    self.projects.surveying = false
                    guard self.projects.missingDirectories != missing else { return }
                    self.projects.missingDirectories = missing
                    self.statusItem?.refreshSubmenus()
                }
            }
        }
    }

    private func refreshRecent() {
        let days = preferencesModel.preferences.recentDays
        recent.follow(days: days)
        guard let command, gate.unavailableReason(.sessions) == nil,
              recent.needsRefresh(now: Date()), recent.state.begin() else { return }
        statusItem?.refreshSubmenus()
        listQueue.async { [weak self] in
            let result = RecentSessions.fetch(command, days: days)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    // A list for another Recent days preference is not this one.
                    guard let self, self.command?.binary == command.binary, self.recent.days == days else { return }
                    if case let .failure(error) = result {
                        Self.logger.error("session list: \(error.message, privacy: .public)")
                    }
                    self.recent.state.finish(result, at: Date())
                    self.workspace?.refreshCommands()
                    self.statusItem?.refreshSubmenus()
                }
            }
        }
    }

    /// The sockets a command that opens a new tab tries, the Kitty used last
    /// first.
    private func newTabCandidates() -> [String] {
        KittyEndpoint.actionCandidates(
            override: preferencesModel.preferences.kittySocketOverride,
            lastActiveKittyPID: lastActiveKittyPID,
            snapshot: store.snapshot,
            kittyPIDs: KittyEndpoint.runningKittyPIDs()
        )
    }

    /// Read the same resolved default as the CLI at the moment of the action.
    /// A failed read never guesses a destination or starts an agent.
    private func resolveLaunchDestination(_ command: AMXCommand, completion: @escaping @MainActor (LaunchBackendPreference) -> Void) {
        let generation = resolveGeneration
        store.show(notice: "Checking launch destination…")
        launchQueue.async { [weak self] in
            let result = BackendStatus.fetch(command)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self, generation == self.resolveGeneration else { return }
                    switch result {
                    case let .success(status):
                        self.preferences.backend = .status(status)
                        self.publishPreferences()
                        self.statusItem?.refreshSubmenus()
                        guard let choice = LaunchBackendPreference.followCLI.resolved(cliBackend: status.launchBackend) else {
                            return self.reportFailure(title: Self.launchFailedTitle, "amx did not report a launch destination. Check amx backend status or choose Workspace or Kitty in Settings.")
                        }
                        completion(choice)
                    case let .failure(error):
                        self.reportFailure(title: Self.launchFailedTitle, "Could not read the CLI launch destination: \(error.message)")
                    }
                }
            }
        }
    }

    /// Launch Project: the project launches on tmux and the workspace shows
    /// it. Only when Preferences names Kitty is it `amx project launch` in
    /// the Kitty used last, which is then brought forward on the new tab. A
    /// `directory` replaces the project's configured one for this launch.
    private func launch(_ project: ProjectEntry, directory: String? = nil, destination: LaunchBackendPreference? = nil) {
        guard let command else {
            return reportFailure(title: Self.launchFailedTitle, preferences.binaryMenuProblem ?? "amx is still being resolved; try again in a moment.")
        }
        if let reason = gate.unavailableReason(.project) { return reportFailure(title: Self.launchFailedTitle, reason) }
        let choice = destination ?? preferencesModel.preferences.launchBackend
        if choice == .followCLI {
            resolveLaunchDestination(command) { [weak self] resolved in
                self?.launch(project, directory: directory, destination: resolved)
            }
            return
        }
        if choice == .workspace {
            return launchInWorkspace(project, directory: directory, command: command)
        }
        let candidates = newTabCandidates()
        let arguments = ProjectCatalog.kittyLaunchArguments(for: project, directory: directory)
        let progress = Self.launchingNotice(project.title)
        store.show(notice: progress)
        let change = beginChange(Self.launchingChange(project.title))
        launchQueue.async { [weak self] in
            let outcome = KittyTargetedCommand.run(candidates: candidates) { socket in
                command.json(ProjectLaunchResult.self, arguments, kittyListenOn: socket,
                             timeout: AMXCommand.launchTimeout, mutates: true)
            }
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    self?.endChange(change)
                    self?.finishOpening(outcome, label: "launch \(project.profile)/\(project.title)", progress: progress) { $0.windowID }
                }
            }
        }
    }

    /// Launch Project's ⌥ alternate: asks for the folder to launch in,
    /// starting from the project's own, and launches there. Cancelling
    /// launches nothing.
    private func launchInChosenFolder(_ project: ProjectEntry) {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "Launch"
        panel.message = "Choose the folder to launch \(project.title) in."
        if !project.directory.isEmpty {
            panel.directoryURL = URL(fileURLWithPath: ProjectCatalog.expandingTilde(project.directory), isDirectory: true)
        }
        NSApp.activate()
        guard panel.runModal() == .OK, let url = panel.url else { return }
        launch(project, directory: url.path)
    }

    /// Launches a project on the private tmux server and shows the workspace
    /// on it: the session's own view as soon as the daemon lists it, and the
    /// window meanwhile.
    private func launchInWorkspace(_ project: ProjectEntry, directory: String?, command: AMXCommand) {
        let arguments = ProjectCatalog.workspaceLaunchArguments(for: project, directory: directory)
        let label = "launch \(project.profile)/\(project.title)"
        let progress = Self.launchingNotice(project.title)
        store.show(notice: progress)
        let change = beginChange(Self.launchingChange(project.title))
        launchQueue.async { [weak self] in
            let result = command.json(ProjectLaunchResult.self, arguments, timeout: AMXCommand.launchTimeout, mutates: true)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    self?.endChange(change)
                    self?.finishOpeningInWorkspace(result.map(\.runtimeSessionID), label: label, progress: progress)
                }
            }
        }
    }

    /// Shows the workspace on the runtime session a tmux launch or continue
    /// reports: its own view as soon as the daemon lists it, and the window
    /// meanwhile.
    private func finishOpeningInWorkspace(_ result: AMXCommandResult<String>, label: String, progress: String) {
        store.replace(notice: progress, with: nil)
        switch result {
        case let .failure(error):
            Self.logger.error("\(label, privacy: .public) on tmux: \(error.message, privacy: .public)")
            reportFailure(title: "Could not \(label)", error.message)
        case let .success(runtimeSessionID), let .partial(runtimeSessionID, _):
            Self.logger.notice("\(label, privacy: .public) on tmux as \(runtimeSessionID, privacy: .public)")
            let controller = workspaceController()
            controller.show()
            guard !runtimeSessionID.isEmpty else { return }
            controller.open(WorkspaceViewRequest(
                kind: WorkspaceViewRequest.sessionKind, runtimeSessionID: runtimeSessionID, awaited: true
            ))
        }
    }

    /// Recent: a conversation a live session runs is focused where it runs,
    /// in the workspace or whichever Kitty that is; any other goes to
    /// `amx session continue`, which resumes it on tmux for the workspace,
    /// or, when Preferences names Kitty, in the Kitty used last. amx sees only
    /// the Kitty it is pointed at, so asking it to continue a conversation
    /// live in another instance would resume a second copy instead of
    /// focusing the first.
    private func continueConversation(_ conversation: RecentConversation, destination: LaunchBackendPreference? = nil) {
        if let live = store.sessions.first(where: { $0.conversation == conversation.identity }) {
            focus(.focusing(live, title: conversation.displayName))
            return
        }
        guard let command else {
            return reportFailure(title: Self.launchFailedTitle, preferences.binaryMenuProblem ?? "amx is still being resolved; try again in a moment.")
        }
        let choice = destination ?? preferencesModel.preferences.launchBackend
        if choice == .followCLI {
            resolveLaunchDestination(command) { [weak self] resolved in
                self?.continueConversation(conversation, destination: resolved)
            }
            return
        }
        // The operator reads the name; the selector goes to the log only.
        Self.logger.notice("continuing \(conversation.selector, privacy: .public)")
        let label = "continue \(conversation.displayName)"
        let progress = Self.launchingNotice(conversation.displayName)
        store.show(notice: progress)
        let change = beginChange(Self.launchingChange(conversation.displayName))
        if choice == .workspace {
            let arguments = RecentSessions.workspaceContinueArguments(for: conversation)
            launchQueue.async { [weak self] in
                let result = command.json(SessionContinueResult.self, arguments, timeout: AMXCommand.launchTimeout, mutates: true)
                DispatchQueue.main.async {
                    MainActor.assumeIsolated {
                        self?.endChange(change)
                        self?.finishOpeningInWorkspace(result.map(\.runtimeSessionID), label: label, progress: progress)
                    }
                }
            }
            return
        }
        let candidates = newTabCandidates()
        let arguments = RecentSessions.continueArguments(for: conversation) + ["--backend", "kitty"]
        launchQueue.async { [weak self] in
            let outcome = KittyTargetedCommand.run(candidates: candidates) { socket in
                command.json(SessionContinueResult.self, arguments, kittyListenOn: socket,
                             timeout: AMXCommand.launchTimeout, mutates: true)
            }
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    self?.endChange(change)
                    self?.finishOpening(outcome, label: label, progress: progress) { $0.windowID }
                }
            }
        }
    }

    /// Brings Kitty forward after a launch or continue, on the reported window
    /// when amx named one and the daemon knows that Kitty.
    private func finishOpening<T>(_ outcome: KittyTargetedCommand.Outcome<T>, label: String, progress: String,
                                  windowID: @escaping (T) -> Int) {
        store.replace(notice: progress, with: nil)
        switch outcome {
        case .noSocket:
            reportFailure(title: "Could not \(label)", KittyEndpoint.listenOnMissing)
        case let .unreachable(message):
            reportFailure(title: "Could not \(label)", "Could not reach Kitty: \(message)")
        case let .done(result, socket):
            switch result {
            case let .failure(error):
                Self.logger.error("\(label, privacy: .public): \(error.message, privacy: .public)")
                reportFailure(title: "Could not \(label)", error.message)
            case let .success(value):
                Self.logger.notice("\(label, privacy: .public) via \(socket, privacy: .public)")
                bringForward(socket: socket, windowID: windowID(value))
            case let .partial(value, status):
                Self.logger.notice("\(label, privacy: .public) via \(socket, privacy: .public) exited \(status)")
                store.show(notice: "\(label) finished, but a later step failed.")
                bringForward(socket: socket, windowID: windowID(value))
            }
        }
    }

    private func bringForward(socket: String, windowID: Int) {
        focusQueue.async { [weak self] in
            if windowID > 0 { _ = DaemonClient().focusWindow(socket: socket, windowID: windowID) }
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    if !KittyActivator.activate(socket: socket) { self?.store.show(notice: "Kitty is not running.") }
                }
            }
        }
    }

    /// ⌥ on a session row: `amx mute-toggle` on the row's runtime session
    /// when the workspace embeds it, else against that row's own Kitty.
    /// Window IDs mean something only inside the instance that issued them,
    /// so no other socket is tried.
    func toggleMute(_ row: MenuSessionRow) { toggleMute(row, banner: false) }

    /// `banner`: a notification's Mute, whose failures are reported where
    /// the operator sees them now rather than only on the menu line.
    private func toggleMute(_ row: MenuSessionRow, banner: Bool) {
        guard let command else {
            return actionFailed(Self.muteFailedTitle, preferences.binaryMenuProblem ?? "amx is still being resolved; try again in a moment.", banner: banner)
        }
        if let reason = gate.unavailableReason(.muteToggle) { return actionFailed(Self.muteFailedTitle, reason, banner: banner) }
        guard row.runtimeSessionID.isEmpty else { return toggleMute(runtimeSessionID: row.runtimeSessionID, banner: banner) }
        let socket = row.kittySocket.isEmpty ? store.snapshot?.kitty.socket ?? "" : row.kittySocket
        guard !socket.isEmpty else { return actionFailed(Self.muteFailedTitle, KittyEndpoint.listenOnMissing, banner: banner) }
        // Shown at once, so a menu reopened before the daemon publishes the
        // change does not offer the toggle the click already made.
        store.expectMute(socket: socket, windowID: row.windowID, muted: !row.muted)
        amxQueue.async { [weak self] in
            let result = command.json(MuteToggleResult.self, MuteToggleResult.arguments(windowID: row.windowID), kittyListenOn: socket)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self else { return }
                    switch result {
                    case let .success(value):
                        Self.logger.notice("mute-toggle window \(row.windowID): muted=\(value.muted)")
                        self.settleExpectedMute(value, socket: socket, windowID: row.windowID)
                    case let .partial(value, _):
                        self.settleExpectedMute(value, socket: socket, windowID: row.windowID)
                        self.store.show(notice: value.muted ? "Muted, but the tab title was not updated." : "Unmuted, but the tab title was not updated.")
                    case let .failure(error):
                        Self.logger.error("mute-toggle window \(row.windowID): \(error.message, privacy: .public)")
                        self.store.dropExpectedMute(socket: socket, windowID: row.windowID)
                        self.actionFailed(Self.muteFailedTitle, error.message, banner: banner)
                    }
                }
            }
        }
    }

    /// Replaces the guess `toggleMute` showed with what `amx` reports it did;
    /// a toggle that changed nothing (the window was gone) shows nothing.
    private func settleExpectedMute(_ value: MuteToggleResult, socket: String, windowID: Int) {
        let socket = socket.isEmpty ? store.snapshot?.kitty.socket ?? "" : socket
        settleExpectedMute(value, key: AttentionCycler.Key(socket: socket, windowID: windowID))
    }

    private func settleExpectedMute(_ value: MuteToggleResult, key: AttentionCycler.Key) {
        if value.changed {
            store.expectMute(key, muted: value.muted)
        } else {
            store.dropExpectedMute(key)
        }
    }

    // MARK: Snooze

    /// Notifications ▸, and Preferences' Resume Now: sets or clears the
    /// daemon's snooze and mirrors what the daemon answers into
    /// `amxSnoozeUntil`.
    /// `banner`: a notification's Snooze, whose failures are reported where
    /// the operator sees them now.
    private func snooze(_ choice: SnoozeChoice, banner: Bool = false) {
        guard let daemon = store.snapshot?.daemon, store.feed == .live else {
            return actionFailed(Self.snoozeFailedTitle, DaemonPhrase.notConnected + ".", banner: banner)
        }
        let request = choice.request(now: Date())
        focusQueue.async { [weak self] in
            let outcome = DaemonClient().notificationSnooze(request)
            DispatchQueue.main.async {
                MainActor.assumeIsolated { self?.finishSnooze(outcome, daemon: SnoozeMirror.identity(of: daemon), banner: banner) }
            }
        }
    }

    static let snoozeFailedTitle = "Could not change the snooze"

    private func finishSnooze(_ outcome: SnoozeOutcome, daemon: String, banner: Bool = false) {
        switch outcome {
        case let .state(state):
            Self.logger.notice("snooze: \(state.description(now: Date()), privacy: .public)")
            recordSnooze(state.mirrored(now: Date()), daemon: daemon)
        case .unsupported:
            snoozeSupported = false
            actionFailed(Self.snoozeFailedTitle, MenuBuilder.snoozeUnsupported, banner: banner)
        case .notRunning:
            store.apply(health: .notRunning)
            if banner { actionFailed(Self.snoozeFailedTitle, "The daemon is not running.", banner: true) }
        case let .failed(message):
            Self.logger.error("snooze: \(message, privacy: .public)")
            actionFailed(Self.snoozeFailedTitle, message, banner: banner)
        }
    }

    private func recordSnooze(_ until: Date?, daemon: String) {
        if !SnoozeMirror.same(preferencesModel.preferences.snoozeUntil, until) {
            preferencesModel.preferences.snoozeUntil = until
        }
        preferencesModel.defaults.set(daemon, forKey: SnoozeMirror.daemonKey)
    }

    /// Keeps `amxSnoozeUntil` in step with each snapshot, and re-applies the
    /// snooze to a daemon that restarted without it.
    private func reconcileSnooze() {
        guard store.feed == .live, let snapshot = store.snapshot, snapshot.daemon.pid > 0,
              !snoozeProbeInFlight, !snoozeReapplyInFlight else { return }
        let daemon = SnoozeMirror.identity(of: snapshot.daemon)
        guard snoozeProbedDaemon == daemon else { return probeSnooze(daemon) }
        guard snoozeSupported else { return }
        let now = Date()
        let defaults = preferencesModel.defaults
        let resumeQueued = defaults.bool(forKey: SnoozeMirror.resumeQueuedKey)
        let decision = SnoozeMirror.reconcile(
            daemon: daemon,
            live: SnoozeState(snapshot.notification),
            stored: preferencesModel.preferences.snoozeUntil,
            storedDaemon: defaults.string(forKey: SnoozeMirror.daemonKey),
            resumeQueued: resumeQueued,
            now: now
        )
        // Any decision but .resume means the daemon has nothing left to
        // resume, so a queued request is spent.
        if resumeQueued, decision != .resume { defaults.removeObject(forKey: SnoozeMirror.resumeQueuedKey) }
        switch decision {
        case .keep:
            if preferencesModel.defaults.string(forKey: SnoozeMirror.daemonKey) != daemon {
                preferencesModel.defaults.set(daemon, forKey: SnoozeMirror.daemonKey)
            }
        case let .adopt(until):
            recordSnooze(until, daemon: daemon)
        case .resume:
            snoozeReapplyInFlight = true
            Self.logger.notice("snooze: sending the resume queued while the feed was down")
            focusQueue.async { [weak self] in
                let outcome = DaemonClient().notificationSnooze(.resume)
                DispatchQueue.main.async {
                    MainActor.assumeIsolated {
                        guard let self else { return }
                        self.snoozeReapplyInFlight = false
                        // A daemon that went away is asked again when it is
                        // back; any answer from it spends the request.
                        if outcome != .notRunning {
                            self.preferencesModel.defaults.removeObject(forKey: SnoozeMirror.resumeQueuedKey)
                        }
                        self.finishSnooze(outcome, daemon: daemon)
                    }
                }
            }
        case let .reapply(request):
            guard snoozeReapplyAttempted != daemon else { return }
            snoozeReapplyAttempted = daemon
            snoozeReapplyInFlight = true
            Self.logger.notice("snooze: re-applying to restarted daemon \(daemon, privacy: .public)")
            focusQueue.async { [weak self] in
                let outcome = DaemonClient().notificationSnooze(request)
                DispatchQueue.main.async {
                    MainActor.assumeIsolated {
                        guard let self else { return }
                        self.snoozeReapplyInFlight = false
                        self.finishSnooze(outcome, daemon: daemon)
                    }
                }
            }
        }
    }

    /// Asks a newly seen daemon for its snooze without changing it, then
    /// reconciles once the answer says whether it has a snooze at all.
    private func probeSnooze(_ daemon: String) {
        snoozeProbeInFlight = true
        focusQueue.async { [weak self] in
            let outcome = DaemonClient().notificationSnooze(.read)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self else { return }
                    self.snoozeProbeInFlight = false
                    switch outcome {
                    case .state:
                        self.snoozeProbedDaemon = daemon
                        self.snoozeSupported = true
                    case .unsupported:
                        self.snoozeProbedDaemon = daemon
                        self.snoozeSupported = false
                        Self.logger.notice("snooze: daemon \(daemon, privacy: .public) has no notification_snooze")
                    case .notRunning, .failed:
                        // Asked again with the next snapshot.
                        return
                    }
                    self.reconcileSnooze()
                }
            }
        }
    }

    /// A menu action that could not run: the menu shows why the next time it
    /// opens.
    private func report(_ message: String) {
        Self.logger.notice("action: \(message, privacy: .public)")
        store.show(notice: message)
    }

    /// A launch, continue, or restart that failed after the menu closed: the
    /// menu line says why, and the operator hears of it at once, by a
    /// notification when app notifications are on and allowed, else by a
    /// beep and the icon blinking.
    /// A mute, snooze, or answer that failed: one a notification's button
    /// asked for is reported as `reportFailure` does, since no menu is open
    /// to show it; one from the menu or a shortcut on the menu line.
    private func actionFailed(_ title: String, _ message: String, banner: Bool) {
        if banner {
            reportFailure(title: title, message)
        } else {
            report(Self.failureLine(title: title, message: message))
        }
    }

    private func reportFailure(title: String, _ message: String) {
        let line = Self.failureLine(title: title, message: message)
        report(line)
        if notifications?.presentApp(title: title, body: message) != true {
            NSSound.beep()
            statusItem?.flash()
        }
    }

    static let launchFailedTitle = "Could not launch"

    /// The menu line while a launch or continue runs.
    nonisolated static func launchingNotice(_ name: String) -> String {
        name.isEmpty ? "Launching…" : "Launching \(name)…"
    }

    /// A launch or continue, as the quit confirmation names it.
    nonisolated static func launchingChange(_ name: String) -> String {
        name.isEmpty ? "launching a session" : "launching \(name)"
    }

    /// The menu line for a failure: the message, after the title unless the
    /// message already says it.
    nonisolated static func failureLine(title: String, message: String) -> String {
        if message.isEmpty { return title }
        if message.hasPrefix("Could not") { return message }
        return "\(title): \(message)"
    }

    // MARK: StatusItemDelegate

    @objc func showPreferences(_ sender: Any?) {
        let controller = preferencesController ?? PreferencesWindowController(model: preferencesModel)
        if preferencesController == nil {
            controller.onVisibilityChanged = { [weak self] visible in
                self?.settingsVisible = visible
                self?.syncActivationPolicy()
            }
        }
        preferencesController = controller
        publishPreferences()
        controller.show()
        refreshConfigFontSize()
    }
}

extension AppDelegate: NSMenuItemValidation {
    /// ⌘W's title follows what it would close; the Session menu's places
    /// name the sessions they go to and are greyed out past the last one, and
    /// its items about the shown session are greyed out with none shown.
    /// Every other item the app targets stays enabled.
    /// The items that act on what the workspace shows, and only make sense
    /// while it has the keyboard: with Settings or an answer panel in front,
    /// they would act on a window behind it, or one that is not even open.
    /// What opens the workspace (the Dashboard, the Overview, a shell, the
    /// next session needing attention, the session list) stays enabled, and
    /// ⌘W closes whichever window has the keyboard.
    static let workspaceOnlyActions: Set<Selector> = [
        #selector(showSwitcher(_:)), #selector(showCommandPalette(_:)),
        #selector(splitWorkspaceShell(_:)), #selector(splitWorkspaceChanges(_:)),
        #selector(focusOtherWorkspacePane(_:)), #selector(swapWorkspacePanes(_:)), #selector(collapseWorkspacePane(_:)),
        #selector(goToSession(_:)), #selector(showSessionInKitty(_:)),
        #selector(selectNextSession(_:)), #selector(selectPreviousSession(_:)),
        #selector(selectNextProjectTab(_:)), #selector(selectPreviousProjectTab(_:)),
        #selector(showNextSessionInKitty(_:)), #selector(showPreviousSessionInKitty(_:)),
        #selector(toggleRecentPlace(_:)),
        #selector(toggleWorkspaceSidebar(_:)), #selector(toggleWorkspaceInspector(_:)),
        #selector(newSessionTerminal(_:)), #selector(nameSession(_:)), #selector(togglePinSession(_:)),
        #selector(endWorkspaceSession(_:)), #selector(searchConversation(_:)),
        #selector(focusNextSession(_:)), #selector(focusPreviousSession(_:)),
        #selector(pickSessionIcon(_:)), #selector(pickSessionColor(_:)),
        #selector(clearSessionState(_:)), #selector(toggleSessionMute(_:)), #selector(markSessionActive(_:)),
    ]

    /// Whether the workspace is open and is the window with the keyboard.
    var workspaceIsKey: Bool {
        guard let workspace, workspace.isOpen, let key = NSApp.keyWindow else { return false }
        return key === workspace.window
    }

    func validateMenuItem(_ item: NSMenuItem) -> Bool {
        let enabled = validateWorkspaceItem(item)
        guard let action = item.action, Self.workspaceOnlyActions.contains(action) else { return enabled }
        return enabled && workspaceIsKey
    }

    private func validateWorkspaceItem(_ item: NSMenuItem) -> Bool {
        switch item.action {
        case #selector(nameSession(_:)):
            item.title = workspace?.nameShownTitle ?? WorkspaceWindowController.nameSessionTitle
            return workspace?.canNameShown == true
        case #selector(selectNextProjectTab(_:)), #selector(selectPreviousProjectTab(_:)):
            return workspace?.canSelectProjectTab == true
        case #selector(searchConversation(_:)):
            item.toolTip = "Search the selected agent's recorded conversation, including messages outside the visible terminal screen."
            return workspace?.shownSessionRow?.conversation != nil
        case #selector(endWorkspaceSession(_:)):
            return workspace?.shownSessionRow?.embeddedRuntimeSessionID != nil
        case #selector(closeWorkspaceView(_:)):
            item.title = closeWorkspaceViewTitle
        case #selector(goToSession(_:)), #selector(showSessionInKitty(_:)):
            let name = workspace?.sessionName(at: item.tag)
            let inKitty = item.action == #selector(showSessionInKitty(_:)) && workspace?.sessionIsInKitty(at: item.tag) == true
            item.title = WorkspaceWindowController.sessionItemTitle(at: item.tag, name: name, inKitty: inKitty)
            return name != nil
        case #selector(togglePinSession(_:)):
            item.title = workspace?.pinShownTitle ?? WorkspaceWindowController.pinShownSessionTitle
            return workspace?.shownSessionRow != nil
        case #selector(newSessionTerminal(_:)):
            return workspace?.canOpenTerminalBesideSession == true
        case #selector(toggleWorkspaceSidebar(_:)):
            item.title = Self.sidebarItemTitle(shown: workspace.map { !$0.isSidebarCollapsed } ?? false)
        case #selector(toggleWorkspaceInspector(_:)):
            item.title = Self.inspectorItemTitle(shown: workspace.map { !$0.isInspectorCollapsed } ?? false)
        default:
            break
        }
        return true
    }

    /// What the sidebar's item does now, as a Mac app's View menu says it.
    nonisolated static func sidebarItemTitle(shown: Bool) -> String {
        shown ? "Hide Sidebar" : "Show Sidebar"
    }

    /// What the inspector's item does now.
    nonisolated static func inspectorItemTitle(shown: Bool) -> String {
        shown ? "Hide Inspector" : "Show Inspector"
    }
}

extension AppDelegate: NSMenuDelegate {
    /// The Session menu, as it opens: its Show in Kitty submenu follows the
    /// sessions the digits go to.
    func menuNeedsUpdate(_ menu: NSMenu) {
        WorkspaceMenu.updateKittyItems(in: menu, workspace: workspace)
    }
}

extension AppDelegate: GhosttyAppDelegate {
    /// A `quit` binding typed into a terminal closes nothing: the companion
    /// and its other terminals outlive any one of them.
    nonisolated func ghosttyAppDidRequestQuit(_ app: Ghostty.App) {
        Self.logger.debug("ignored a quit request from a terminal")
    }
}
