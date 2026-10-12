import AppKit
import os
import UserNotifications

final class AppDelegate: NSObject, NSApplicationDelegate, StatusItemDelegate, WorkspaceDelegate {
    private nonisolated static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "app")

    /// How often the daemon line is refreshed while the companion feed is
    /// not live: with no daemon, or one too old to stream.
    static let healthInterval: TimeInterval = 10

    private(set) var statusItem: StatusItemController?
    private var preferencesController: PreferencesWindowController?
    /// The ASL answer panel, made the first time a question is answered.
    private var aslPanel: ASLQuestionPanelController?
    private var promptPanel: PromptAnswerPanelController?
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
    private var healthTimer: Timer?
    private var subscription: SubscriptionClient?
    private var wakeObserver: NSObjectProtocol?
    private var activationObserver: NSObjectProtocol?
    /// The Kitty the operator used last, where new tabs open.
    private var lastActiveKittyPID: pid_t?

    private var projects = ProjectCatalog()
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

    /// Health requests and amx runs block, so they run here, one at a time.
    private let work = DispatchQueue(label: "io.github.sglavoie.agentmux.work", qos: .utility)
    /// Focus requests run here, so a click never waits behind a health check.
    private let focusQueue = DispatchQueue(label: "io.github.sglavoie.agentmux.focus", qos: .userInitiated)
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

    func applicationWillFinishLaunching(_ notification: Notification) {
        // LSUIElement already keeps the app out of the Dock; this states it
        // for a build run straight from DerivedData.
        NSApp.setActivationPolicy(.accessory)
        // The delegate must be in place before launch finishes, so a banner
        // clicked while the app was not running still reaches it.
        if !Self.isRunningTests { setUpNotifications() }
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
        setUpPreferences()
        setUpHotKeys()
        store.onChange = { [weak self] in self?.storeChanged() }
        startSubscription()
        refreshDaemonStatus()
        let timer = Timer(timeInterval: Self.healthInterval, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.refreshDaemonStatus()
                self?.notifications?.refreshAuthorization()
            }
        }
        RunLoop.main.add(timer, forMode: .common)
        healthTimer = timer
        wakeObserver = NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.didWakeNotification, object: nil, queue: .main
        ) { [weak self] _ in
            // A socket held across sleep may be half-open; do not wait for
            // the heartbeat watchdog to find out.
            MainActor.assumeIsolated {
                Self.logger.notice("woke from sleep; reconnecting the feed")
                self?.subscription?.reconnectNow()
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
        if let place = restoration.place {
            controller.restore(place)
        } else if controller.state.selection == nil, controller.viewSelection == nil {
            controller.showView(WorkspaceUtilityView.dashboard.id)
        }
    }

    /// The app was opened again while running (from Finder, Spotlight, or
    /// `open`): that asks for its window.
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        guard !Self.isRunningTests else { return false }
        showWorkspace()
        return false
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        // A window that closes because the app is quitting was open: the
        // next launch reopens it.
        isTerminating = true
        return .terminateNow
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
            Ghostty.Config(overrides: Ghostty.ConfigOverrides(fontSize: model.preferences.defaultFontSize))
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
    func attachmentConfiguration(runtimeSessionID: String) -> Ghostty.SurfaceConfiguration? {
        guard let command else { return nil }
        return .attachment(
            amx: command.binary,
            runtimeSessionID: runtimeSessionID,
            environment: command.launchEnvironment.commandEnvironment
        )
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
    /// is named. Opened on nothing in particular, it lands on the Dashboard.
    func showWorkspace(focusing runtimeSessionID: String? = nil) {
        let controller = workspaceController()
        if let runtimeSessionID {
            controller.focus(runtimeSessionID: runtimeSessionID)
        } else {
            controller.show()
            if controller.state.selection == nil, controller.viewSelection == nil {
                controller.showView(WorkspaceUtilityView.dashboard.id)
            }
        }
    }

    private func workspaceController() -> WorkspaceWindowController {
        if let workspace { return workspace }
        let controller = WorkspaceWindowController(delegate: self)
        workspace = controller
        if !Self.isRunningTests {
            let restoration = WorkspaceRestoration.load()
            controller.setSidebarCollapsed(restoration.sidebarCollapsed)
            controller.setInspectorTab(restoration.inspectorTab)
            controller.setInspectorCollapsed(!restoration.inspectorShown)
            controller.order = WorkspaceOrder.load()
        }
        // The terminals offer their shortcuts to the main menu first, and a
        // regular app needs one; the accessory never shows it.
        if NSApp.mainMenu == nil { NSApp.mainMenu = WorkspaceMenu.make(target: self) }
        return controller
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
        guard let workspace, workspace.isOpen else {
            Self.logger.notice("workspace view request (\(request.kind, privacy: .public)) with no workspace open; ignored")
            return
        }
        workspace.open(request)
    }

    func makeUtilityTerminal(_ view: WorkspaceUtilityView) -> Result<WorkspaceTerminal, WorkspaceTerminalProblem> {
        guard let configuration = utilityConfiguration(for: view) else {
            return .failure(WorkspaceTerminalProblem(message: preferences.binaryProblem ?? "amx is still being resolved."))
        }
        guard let runtime = terminalRuntime() else {
            return .failure(WorkspaceTerminalProblem(message: "The embedded terminal could not start. See the log for why."))
        }
        guard let surface = runtime.makeSurface(configuration) else {
            return .failure(WorkspaceTerminalProblem(message: "The terminal could not be created."))
        }
        surface.delegate = workspace
        return .success(WorkspaceTerminal(view: surface) { surface.close() })
    }

    func makeTerminal(runtimeSessionID: String) -> Result<WorkspaceTerminal, WorkspaceTerminalProblem> {
        guard let configuration = attachmentConfiguration(runtimeSessionID: runtimeSessionID) else {
            return .failure(WorkspaceTerminalProblem(message: preferences.binaryProblem ?? "amx is still being resolved."))
        }
        guard let runtime = terminalRuntime() else {
            return .failure(WorkspaceTerminalProblem(message: "The embedded terminal could not start. See the log for why."))
        }
        guard let surface = runtime.makeSurface(configuration) else {
            return .failure(WorkspaceTerminalProblem(message: "The terminal could not be created."))
        }
        surface.delegate = workspace
        // An attachment only views a session the tmux server holds.
        surface.scrollback = .server
        return .success(WorkspaceTerminal(view: surface) { surface.close() })
    }

    func focusExternally(_ row: WorkspaceRow) {
        guard case let .kitty(socket, windowID) = row.target else { return }
        focus(MenuSessionRow(title: row.project, windowID: windowID, kittySocket: socket, toolTip: ""))
    }

    /// While the workspace is open the app is a regular one, with a Dock icon
    /// and a menu bar, so its window takes the keyboard like any other; with
    /// it closed the app is the menu bar accessory again.
    func workspaceVisibilityChanged(_ visible: Bool) {
        NSApp.setActivationPolicy(visible ? .regular : .accessory)
        guard !Self.isRunningTests, !isTerminating else { return }
        WorkspaceRestoration.saveWindowOpen(visible)
    }

    func workspacePlaceChanged(_ place: WorkspaceRestoration.Place?) {
        guard !Self.isRunningTests, !isTerminating else { return }
        Self.logger.notice("workspace: now on \(place?.encoded ?? "nothing", privacy: .public)")
        WorkspaceRestoration.savePlace(place)
    }

    func workspaceSidebarCollapsedChanged(_ collapsed: Bool) {
        guard !Self.isRunningTests, !isTerminating else { return }
        WorkspaceRestoration.saveSidebarCollapsed(collapsed)
    }

    func workspaceInspectorChanged(collapsed: Bool, tab: WorkspaceInspectorTab) {
        guard !Self.isRunningTests, !isTerminating else { return }
        WorkspaceRestoration.saveInspector(shown: !collapsed, tab: tab)
    }

    /// The inspector's buttons do what ⌘R and the session function keys do.
    func workspaceInspectorAction(_ action: WorkspaceInspectorAction) {
        switch action {
        case .name: nameSession(nil)
        case .color: pickSessionColor(nil)
        case .icon: pickSessionIcon(nil)
        case .mute: toggleSessionMute(nil)
        case .clearState: clearSessionState(nil)
        case .markActive: markSessionActive(nil)
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
            if let problem = preferences.binaryProblem {
                completion(.unknown(problem))
            } else {
                statusQuestions.append((runtimeSessionID, completion))
            }
            return
        }
        focusQueue.async {
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
            completion(preferences.binaryProblem ?? "amx has not been found yet.")
            return
        }
        focusQueue.async {
            let problem = SessionClose.problem(command.json(SessionClose.self, ["close", runtimeSessionID, "--yes"]))
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    Self.logger.notice("close \(runtimeSessionID, privacy: .public): \(problem ?? "closed", privacy: .public)")
                    completion(problem)
                }
            }
        }
    }

    /// Names a session with `amx name-session`; an empty name removes the
    /// one it has. The sidebar shows the change once the daemon has read it.
    func nameSession(runtimeSessionID: String, name: String, completion: @escaping @MainActor (String?) -> Void) {
        guard let command else {
            completion(preferences.binaryProblem ?? "amx has not been found yet.")
            return
        }
        focusQueue.async {
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
        showWorkspaceView(WorkspaceUtilityView(kind: .shell, target: home, title: "Shell"))
    }
    /// ⌘↩: a shell beside the session the workspace is on.
    @objc func newSessionTerminal(_ sender: Any?) { workspace?.openTerminalBesideSession() }
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
    /// ⌘R: names the session the workspace's pane is on.
    @objc func nameSession(_ sender: Any?) {
        guard let workspace, workspace.shownSessionRow != nil else { return refuse(Self.noSessionShown) }
        if !workspace.nameShownSession() { refuse(WorkspaceWindowController.namingNeedsEmbedded) }
    }
    @objc func toggleWorkspaceSidebar(_ sender: Any?) { workspace?.toggleSidebar() }
    @objc func toggleWorkspaceInspector(_ sender: Any?) { workspace?.toggleInspector() }
    @objc func toggleRecentPlace(_ sender: Any?) {
        if workspace?.toggleRecentPlace() != true { NSSound.beep() }
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
        guard let command else { return refuse(preferences.binaryProblem ?? "amx is still being resolved; try again in a moment.") }
        var socket: String?
        if case let .kitty(rowSocket, _) = row.target {
            socket = rowSocket.isEmpty ? store.snapshot?.kitty.socket ?? "" : rowSocket
            guard socket?.isEmpty == false else { return refuse(KittyEndpoint.listenOnMissing) }
        }
        let arguments = ["hook-indicator", event] + Self.hookTarget(row.target) + ["--strict"]
        focusQueue.async { [weak self] in
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
        switch row.target {
        case let .kitty(socket, windowID):
            toggleMute(MenuSessionRow(title: row.project, windowID: windowID, kittySocket: socket, toolTip: "", muted: row.muted))
        case let .embedded(id):
            guard let command else { return refuse(preferences.binaryProblem ?? "amx is still being resolved; try again in a moment.") }
            focusQueue.async { [weak self] in
                let result = command.json(MuteToggleResult.self, MuteToggleResult.arguments(runtimeSessionID: id))
                DispatchQueue.main.async {
                    MainActor.assumeIsolated {
                        switch result {
                        case let .success(value), let .partial(value, _):
                            Self.logger.notice("mute-toggle \(id, privacy: .public): muted=\(value.muted)")
                        case let .failure(error):
                            Self.logger.error("mute-toggle \(id, privacy: .public): \(error.message, privacy: .public)")
                            self?.store.show(notice: "Could not toggle mute: \(error.message)")
                        }
                    }
                }
            }
        }
    }

    @objc func focusSessionList(_ sender: Any?) {
        showWorkspace()
        workspace?.focusSidebar()
    }

    // MARK: Companion feed

    private func startSubscription() {
        let client = SubscriptionClient(request: subscribeRequest.params) { [weak self] event in
            DispatchQueue.main.async {
                MainActor.assumeIsolated { self?.handle(event) }
            }
        }
        subscription = client
        client.start()
    }

    private var lastFeed: FeedState?

    private func handle(_ event: SubscriptionClient.Event) {
        switch event {
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
        if store.feed != lastFeed {
            lastFeed = store.feed
            Self.logger.notice("feed: \(String(describing: self.store.feed), privacy: .public)")
            if store.feed != .live { refreshDaemonStatus() }
        }
    }

    private func storeChanged() {
        reconcileSnooze()
        statusItem?.refresh()
        workspace?.refresh()
        preferences.daemonStatus = store.daemonStatus
        publishPreferences()
    }

    /// Hands Preferences what it reports, without republishing an unchanged
    /// value on every snapshot.
    private func publishPreferences() {
        if preferencesModel.snapshot != preferences { preferencesModel.snapshot = preferences }
        let notification = store.feed == .live ? store.snapshot?.notification : nil
        if preferencesModel.daemonNotification != notification { preferencesModel.daemonNotification = notification }
    }

    // MARK: Daemon health

    /// Polls health while the feed is not live; a live feed carries the
    /// daemon's own status in every snapshot.
    func refreshDaemonStatus() {
        guard store.feed != .live, !healthInFlight else { return }
        healthInFlight = true
        work.async { [weak self] in
            let status = DaemonClient().health()
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self else { return }
                    self.healthInFlight = false
                    if status != self.store.health {
                        Self.logger.notice("daemon: \(DaemonStatusLine.text(for: status), privacy: .public)")
                    }
                    self.store.apply(health: status)
                }
            }
        }
    }

    // MARK: Focus

    /// Focuses a session where it is: an embedded one in the workspace, a
    /// Kitty one in Kitty, which is then brought forward.
    func focus(_ row: MenuSessionRow) {
        if !row.runtimeSessionID.isEmpty {
            showWorkspace(focusing: row.runtimeSessionID)
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
            store.show(notice: "daemon too old to focus sessions — run `amx daemon restart`")
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
        case .openWorkspace:
            showWorkspace()
        case .openDashboard:
            openDashboard()
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
            aslPanel = controller
        }
        Self.logger.notice("answer panel: run \(target.runID, privacy: .public)")
        controller.show(target)
    }

    /// The panel closed itself: answered, or its run moved. The menu is
    /// rebuilt now; the snapshot that drops the ❓ follows from the daemon.
    private func finishAnswer(_ notice: String?) {
        if let notice { store.show(notice: notice) }
        statusItem?.rebuildMenu()
    }

    /// Runs `amx daemon start` detached, only ever because the operator chose
    /// "Start daemon": the app never starts the daemon on its own.
    private func startDaemon() {
        guard let command else {
            store.show(notice: preferences.binaryProblem ?? "amx is still being resolved; try again in a moment.")
            return
        }
        guard let pid = command.spawnDetached(["daemon", "start"]) else {
            store.show(notice: "Could not run \(command.binary.path) daemon start.")
            return
        }
        Self.logger.notice("started amx daemon (pid \(pid))")
        store.show(notice: "Starting the daemon…")
        DispatchQueue.main.asyncAfter(deadline: .now() + 1) { [weak self] in
            MainActor.assumeIsolated { self?.subscription?.reconnectNow() }
        }
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

    /// Reads the login shell's environment, asks the daemon which build it
    /// runs, resolves amx against both, and probes what it can do. The shell
    /// can take seconds, so none of it runs on the main thread; it is read
    /// once, and a later resolve (after an override changes) reuses it.
    private func resolveBinary() {
        let detected = detectedEnvironment
        let overrides = preferencesModel.preferences.environment
        work.async { [weak self] in
            let shell = detected ?? LaunchEnvironment.current()
            let environment = shell.applying(overrides).namingBundledRuntime(resources: Bundle.main.resourceURL)
            let build = DaemonClient().health().build
            let resolution = AMXBinaryResolver.live(searchPath: environment.path, daemonBuild: build).resolve()
            var snapshot = PreferencesSnapshot()
            snapshot.detectedEnvironment = shell
            var command: AMXCommand?
            var report: AMXBuildReport?
            switch resolution {
            case let .success(binary):
                let amx = AMXCommand(binary: binary.url, launchEnvironment: environment)
                command = amx
                snapshot.binary = binary
                snapshot.binaryVersion = AMXVersion.read(binary: binary.url.path)
                report = AMXBuildReport.of(binary: binary.url, version: snapshot.binaryVersion)
                snapshot.capabilities = AMXCapabilities.probe(amx)
                snapshot.backend = BackendSetup(BackendStatus.fetch(amx))
                Self.log(snapshot.backend)
                Self.logger.notice("running \(binary.url.path, privacy: .public) (\(String(describing: binary.source), privacy: .public))")
            case let .failure(notFound):
                snapshot.binaryProblem = notFound.message
                Self.logger.error("\(notFound.message, privacy: .public)")
            }
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self else { return }
                    self.detectedEnvironment = shell
                    if self.command?.binary != command?.binary || self.command?.launchEnvironment != command?.launchEnvironment {
                        self.projects.state.reset()
                        self.projects.missingDirectories = []
                        self.recent.state.reset()
                    }
                    self.command = command
                    self.workspace?.retryTerminal()
                    let waiting = self.statusQuestions
                    self.statusQuestions = []
                    for (id, completion) in waiting {
                        if command == nil {
                            completion(.unknown(self.preferences.binaryProblem ?? "amx was not found."))
                        } else {
                            self.sessionStatus(runtimeSessionID: id, completion: completion)
                        }
                    }
                    // Doctor compares the build subscribe reported with the
                    // daemon's, so a new selection needs a new subscribe.
                    if self.subscribeRequest.report(build: report) {
                        self.subscription?.reconnectNow()
                    }
                    snapshot.daemonStatus = self.store.daemonStatus
                    self.preferences = snapshot
                    self.publishPreferences()
                    // Listed and surveyed at launch, once the gate knows what
                    // this amx can do, so the first menu opening already knows
                    // which projects can be launched.
                    self.refreshProjects()
                }
            }
        }
    }

    /// Resolves again once typing in Preferences pauses, so a binary path or
    /// environment value typed one key at a time does not run amx per key.
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
        model.onRecordingChange = { [weak self] recording in
            guard let self else { return }
            if recording {
                self.dashboardHotKey?.unregister()
                self.attentionHotKey?.unregister()
            } else {
                self.registerHotKeys()
            }
        }
    }

    private func preferencesChanged(from old: AppPreferences, to new: AppPreferences) {
        if old.defaultFontSize != new.defaultFontSize {
            applyDefaultFontSize()
        }
        if old.hotKeyDashboard != new.hotKeyDashboard || old.hotKeyNextAttention != new.hotKeyNextAttention {
            registerHotKeys()
        }
        applyMenuBarPreferences(new)
        if old.binaryPath != new.binaryPath || old.environment != new.environment {
            scheduleResolve()
        }
        notifications?.enabled = new.useAppNotifications
    }

    /// Reload first so ⌘⇧- uses the new default, then reset even manually
    /// zoomed terminals, including the workspace's hidden session views.
    private func applyDefaultFontSize() {
        terminals?.reloadConfig(resetFontSize: true)
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
        subscribeRequest.claim = presenter.claim
        presenter.onClaimChange = { [weak self, unowned presenter] claim in
            guard let self else { return }
            Self.logger.notice("notifications: backend claim \(claim.backend ? "on" : "off", privacy: .public) (\(String(describing: presenter.authorization), privacy: .public))")
            self.subscribeRequest.claim = claim
            // The role is claimed in subscribe, so a change needs a new one.
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
        notifications = presenter
        presenter.refreshAuthorization()
    }

    /// What the app does after reporting a banner response. The daemon
    /// focuses the pane on `clicked`; the app brings that Kitty forward, and
    /// carries out the actions the daemon only names.
    private func respond(to response: NotificationPresenter.Response) {
        let origin = Self.origin(of: response, in: store.sessions, snapshotKitty: store.snapshot?.kitty ?? CompanionKitty())
        switch response.disposition {
        case .clicked:
            if !response.embeddedRuntimeSessionID.isEmpty {
                return showWorkspace(focusing: response.embeddedRuntimeSessionID)
            }
            guard response.navigable, let socket = origin.socket else { return }
            _ = KittyActivator.activate(socket: socket)
        case .actionMute:
            guard let session = origin.session else { return report("Could not mute: the session is no longer listed.") }
            // mute-toggle toggles; a banner's Mute never unmutes.
            guard !session.muted else { return }
            toggleMute(.focusing(session))
        case .actionSnooze:
            snooze(.oneHour)
        case .actionAnswer:
            guard !response.runID.isEmpty else { return report("Could not answer: the notification names no run.") }
            answer(ASLAnswerTarget(runID: response.runID, windowID: response.windowID))
        case .actionRespond:
            guard !response.embeddedRuntimeSessionID.isEmpty else { return report("Could not answer: the notification names no session.") }
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
            problem: { [weak self] in self?.preferences.binaryProblem ?? "amx has not been found yet." }
        )))
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
            refuse(preferences.binaryProblem ?? "amx is still being resolved; try again in a moment.")
            return
        }
        if let capabilities = preferences.capabilities, !capabilities.openDashboard {
            refuse("\(command.binary.path) has no open-dashboard command; run `just install`.")
            return
        }
        let candidates = DashboardOpener.candidates(
            override: preferencesModel.preferences.kittySocketOverride,
            snapshot: store.snapshot,
            kittyPIDs: KittyEndpoint.runningKittyPIDs()
        )
        focusQueue.async { [weak self] in
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
            refuse("The session feed is not live.")
            return
        }
        let order = workspace?.order ?? (Self.isRunningTests ? WorkspaceOrder() : .load())
        guard let session = cycler.advance(in: store.sessions, snapshotSocket: snapshot.kitty.socket, order: order) else {
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
            problem: command == nil ? preferences.binaryProblem : nil,
            binaryPath: command?.binary.path ?? "amx"
        )
    }

    var actionsInput: MenuBuilder.ActionsInput {
        let live = store.feed == .live
        let prefs = preferencesModel.preferences
        return MenuBuilder.ActionsInput(
            gate: gate,
            feedLive: live,
            projects: projects.state,
            missingProjectDirectories: projects.missingDirectories,
            recent: recent.state,
            recentDays: prefs.recentDays,
            recentLimit: prefs.recentLimit,
            liveConversations: store.sessions.compactMap(\.conversation).filter { !$0.sessionID.isEmpty },
            notification: live ? store.snapshot?.notification : nil,
            snoozeSupported: snoozeSupported,
            configPath: AMXConfigPath.resolve(command?.launchEnvironment ?? detectedEnvironment),
            now: Date()
        )
    }

    func menuWillOpen() {
        notifications?.refreshAuthorization()
        refreshProjects()
        surveyProjectDirectories()
        refreshRecent()
    }

    private func refreshProjects() {
        guard let command, gate.unavailableReason(.project) == nil,
              projects.needsRefresh(now: Date()), projects.state.begin() else { return }
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
        guard let command, gate.unavailableReason(.sessions) == nil,
              recent.needsRefresh(now: Date()), recent.state.begin() else { return }
        let days = preferencesModel.preferences.recentDays
        statusItem?.refreshSubmenus()
        listQueue.async { [weak self] in
            let result = RecentSessions.fetch(command, days: days)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self, self.command?.binary == command.binary else { return }
                    if case let .failure(error) = result {
                        Self.logger.error("session list: \(error.message, privacy: .public)")
                    }
                    self.recent.state.finish(result, at: Date())
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

    /// Launch Project: `amx project launch` in the Kitty used last, then that
    /// Kitty is brought forward on the new tab. With no Kitty to open a tab
    /// in, or when Preferences says so, the project launches on tmux and the
    /// workspace shows it. A `directory` replaces the project's configured
    /// one for this launch.
    private func launch(_ project: ProjectEntry, directory: String? = nil) {
        guard let command else { return report(preferences.binaryProblem ?? "amx is still being resolved; try again in a moment.") }
        if let reason = gate.unavailableReason(.project) { return report(reason) }
        let candidates = newTabCandidates()
        if preferencesModel.preferences.launchBackend.launchesInWorkspace(kittyAvailable: !candidates.isEmpty) {
            return launchInWorkspace(project, directory: directory, command: command)
        }
        let arguments = ProjectCatalog.launchArguments(for: project, directory: directory)
        focusQueue.async { [weak self] in
            let outcome = KittyTargetedCommand.run(candidates: candidates) { socket in
                command.json(ProjectLaunchResult.self, arguments, kittyListenOn: socket)
            }
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    self?.finishOpening(outcome, label: "launch \(project.profile)/\(project.title)") { $0.windowID }
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
            panel.directoryURL = URL(fileURLWithPath: project.directory, isDirectory: true)
        }
        NSApp.activate(ignoringOtherApps: true)
        guard panel.runModal() == .OK, let url = panel.url else { return }
        launch(project, directory: url.path)
    }

    /// Launches a project on the private tmux server and shows the workspace
    /// on it: the session's own view as soon as the daemon lists it, and the
    /// window meanwhile.
    private func launchInWorkspace(_ project: ProjectEntry, directory: String?, command: AMXCommand) {
        let arguments = ProjectCatalog.workspaceLaunchArguments(for: project, directory: directory)
        let label = "launch \(project.profile)/\(project.title)"
        focusQueue.async { [weak self] in
            let result = command.json(ProjectLaunchResult.self, arguments)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self else { return }
                    switch result {
                    case let .failure(error):
                        Self.logger.error("\(label, privacy: .public) on tmux: \(error.message, privacy: .public)")
                        self.report("Could not \(label): \(error.message)")
                    case let .success(value), let .partial(value, _):
                        Self.logger.notice("\(label, privacy: .public) on tmux as \(value.runtimeSessionID, privacy: .public)")
                        let controller = self.workspaceController()
                        controller.show()
                        controller.open(WorkspaceViewRequest(
                            kind: WorkspaceViewRequest.sessionKind, runtimeSessionID: value.runtimeSessionID, awaited: true
                        ))
                    }
                }
            }
        }
    }

    /// Recent: a conversation a live session runs is focused where it runs,
    /// in whichever Kitty that is; any other goes to `amx session continue`,
    /// which resumes it in the Kitty used last. amx sees only the Kitty it is
    /// pointed at, so asking it to continue a conversation live in another
    /// instance would resume a second copy instead of focusing the first.
    private func continueConversation(_ conversation: RecentConversation) {
        if let live = store.sessions.first(where: { $0.conversation == conversation.identity }) {
            focus(.focusing(live, title: conversation.selector))
            return
        }
        guard let command else { return report(preferences.binaryProblem ?? "amx is still being resolved; try again in a moment.") }
        let candidates = newTabCandidates()
        let arguments = RecentSessions.continueArguments(for: conversation)
        focusQueue.async { [weak self] in
            let outcome = KittyTargetedCommand.run(candidates: candidates) { socket in
                command.json(SessionContinueResult.self, arguments, kittyListenOn: socket)
            }
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    self?.finishOpening(outcome, label: "continue \(conversation.selector)") { $0.windowID }
                }
            }
        }
    }

    /// Brings Kitty forward after a launch or continue, on the reported window
    /// when amx named one and the daemon knows that Kitty.
    private func finishOpening<T>(_ outcome: KittyTargetedCommand.Outcome<T>, label: String, windowID: @escaping (T) -> Int) {
        switch outcome {
        case .noSocket:
            report(KittyEndpoint.listenOnMissing)
        case let .unreachable(message):
            report("Could not reach Kitty: \(message)")
        case let .done(result, socket):
            switch result {
            case let .failure(error):
                Self.logger.error("\(label, privacy: .public): \(error.message, privacy: .public)")
                report("Could not \(label): \(error.message)")
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

    /// ⌥ on a session row: `amx mute-toggle` against that row's own Kitty.
    /// Window IDs mean something only inside the instance that issued them,
    /// so no other socket is tried.
    func toggleMute(_ row: MenuSessionRow) {
        guard row.runtimeSessionID.isEmpty else { return report(MenuBuilder.muteNeedsKitty) }
        guard let command else { return report(preferences.binaryProblem ?? "amx is still being resolved; try again in a moment.") }
        if let reason = gate.unavailableReason(.muteToggle) { return report(reason) }
        let socket = row.kittySocket.isEmpty ? store.snapshot?.kitty.socket ?? "" : row.kittySocket
        guard !socket.isEmpty else { return report(KittyEndpoint.listenOnMissing) }
        // Shown at once, so a menu reopened before the daemon publishes the
        // change does not offer the toggle the click already made.
        store.expectMute(socket: socket, windowID: row.windowID, muted: !row.muted)
        focusQueue.async { [weak self] in
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
                        self.store.show(notice: "Could not toggle mute: \(error.message)")
                    }
                }
            }
        }
    }

    /// Replaces the guess `toggleMute` showed with what `amx` reports it did;
    /// a toggle that changed nothing (the window was gone) shows nothing.
    private func settleExpectedMute(_ value: MuteToggleResult, socket: String, windowID: Int) {
        if value.changed {
            store.expectMute(socket: socket, windowID: windowID, muted: value.muted)
        } else {
            store.dropExpectedMute(socket: socket, windowID: windowID)
        }
    }

    // MARK: Snooze

    /// Notifications ▸, and Preferences' Resume Now: sets or clears the
    /// daemon's snooze and mirrors what the daemon answers into
    /// `amxSnoozeUntil`.
    private func snooze(_ choice: SnoozeChoice) {
        guard let daemon = store.snapshot?.daemon, store.feed == .live else {
            return report("The daemon is not connected.")
        }
        let request = choice.request(now: Date())
        focusQueue.async { [weak self] in
            let outcome = DaemonClient().notificationSnooze(request)
            DispatchQueue.main.async {
                MainActor.assumeIsolated { self?.finishSnooze(outcome, daemon: SnoozeMirror.identity(of: daemon)) }
            }
        }
    }

    private func finishSnooze(_ outcome: SnoozeOutcome, daemon: String) {
        switch outcome {
        case let .state(state):
            Self.logger.notice("snooze: \(state.description(now: Date()), privacy: .public)")
            recordSnooze(state.mirrored(now: Date()), daemon: daemon)
        case .unsupported:
            snoozeSupported = false
            report(MenuBuilder.snoozeUnsupported)
        case .notRunning:
            store.apply(health: .notRunning)
        case let .failed(message):
            Self.logger.error("snooze: \(message, privacy: .public)")
            report("Could not change the snooze: \(message)")
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
        let decision = SnoozeMirror.reconcile(
            daemon: daemon,
            live: SnoozeState(snapshot.notification),
            stored: preferencesModel.preferences.snoozeUntil,
            storedDaemon: preferencesModel.defaults.string(forKey: SnoozeMirror.daemonKey),
            now: now
        )
        switch decision {
        case .keep:
            if preferencesModel.defaults.string(forKey: SnoozeMirror.daemonKey) != daemon {
                preferencesModel.defaults.set(daemon, forKey: SnoozeMirror.daemonKey)
            }
        case let .adopt(until):
            recordSnooze(until, daemon: daemon)
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

    // MARK: StatusItemDelegate

    @objc func showPreferences(_ sender: Any?) {
        let controller = preferencesController ?? PreferencesWindowController(model: preferencesModel)
        preferencesController = controller
        publishPreferences()
        controller.show()
        refreshConfigFontSize()
    }
}

extension AppDelegate: GhosttyAppDelegate {
    /// A `quit` binding typed into a terminal closes nothing: the companion
    /// and its other terminals outlive any one of them.
    nonisolated func ghosttyAppDidRequestQuit(_ app: Ghostty.App) {
        Self.logger.debug("ignored a quit request from a terminal")
    }
}
