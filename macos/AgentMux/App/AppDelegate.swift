import AppKit
import os
import UserNotifications

final class AppDelegate: NSObject, NSApplicationDelegate, StatusItemDelegate {
    private nonisolated static let logger = Logger(subsystem: "io.github.sglavoie.agentmux", category: "app")

    /// How often the daemon line is refreshed while the companion feed is
    /// not live: with no daemon, or one too old to stream.
    static let healthInterval: TimeInterval = 10

    private(set) var statusItem: StatusItemController?
    private var preferencesController: PreferencesWindowController?
    /// The ASL answer panel, made the first time a question is answered.
    private var aslPanel: ASLQuestionPanelController?
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
            MainActor.assumeIsolated { self?.subscription?.reconnectNow() }
        }
        trackActiveKitty()
        resolveBinary()
    }

    func applicationWillTerminate(_ notification: Notification) {
        healthTimer?.invalidate()
        subscription?.stop()
        if let wakeObserver { NSWorkspace.shared.notificationCenter.removeObserver(wakeObserver) }
        if let activationObserver { NSWorkspace.shared.notificationCenter.removeObserver(activationObserver) }
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

    func focus(_ row: MenuSessionRow) {
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
        case .openDashboard:
            openDashboard()
        case let .launchProject(project):
            launch(project)
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

    /// Reads the login shell's environment, asks the daemon which build it
    /// runs, resolves amx against both, and probes what it can do. The shell
    /// can take seconds, so none of it runs on the main thread; it is read
    /// once, and a later resolve (after an override changes) reuses it.
    private func resolveBinary() {
        let detected = detectedEnvironment
        let overrides = preferencesModel.preferences.environment
        work.async { [weak self] in
            let shell = detected ?? LaunchEnvironment.current()
            let environment = shell.applying(overrides)
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
                        self.recent.state.reset()
                    }
                    self.command = command
                    // Doctor compares the build subscribe reported with the
                    // daemon's, so a new selection needs a new subscribe.
                    if self.subscribeRequest.report(build: report) {
                        self.subscription?.reconnectNow()
                    }
                    snapshot.daemonStatus = self.store.daemonStatus
                    self.preferences = snapshot
                    self.publishPreferences()
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
        if old.hotKeyDashboard != new.hotKeyDashboard || old.hotKeyNextAttention != new.hotKeyNextAttention {
            registerHotKeys()
        }
        applyMenuBarPreferences(new)
        if old.binaryPath != new.binaryPath || old.environment != new.environment {
            scheduleResolve()
        }
        notifications?.enabled = new.useAppNotifications
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
            guard response.navigable, let socket = origin.socket else { return }
            _ = KittyActivator.activate(socket: socket)
        case .actionMute:
            guard let session = origin.session else { return report("Could not mute: the session is no longer listed.") }
            // mute-toggle toggles; a banner's Mute never unmutes.
            guard !session.muted else { return }
            toggleMute(MenuSessionRow(title: session.project, windowID: session.windowID, kittySocket: session.kittySocket, toolTip: ""))
        case .actionSnooze:
            snooze(.oneHour)
        case .actionAnswer:
            guard !response.runID.isEmpty else { return report("Could not answer: the notification names no run.") }
            answer(ASLAnswerTarget(runID: response.runID, windowID: response.windowID))
        case .delivered, .dismissed, .failed:
            break
        }
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

    /// ⌃⌥⌘A: focuses the running dashboard, or opens one, then brings its
    /// Kitty forward.
    func openDashboard() {
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
        switch outcome {
        case let .opened(result, socket):
            Self.logger.notice("dashboard \(result.opened ? "opened" : "focused", privacy: .public) (window \(result.windowID))")
            if !KittyActivator.activate(socket: socket) {
                refuse("Kitty is not running.")
            }
        case .noSocket:
            refuse(KittyEndpoint.listenOnMissing)
        case let .failed(message):
            refuse("Could not open the dashboard: \(message)")
        }
    }

    /// ⌃⌥⌘N: focuses the session that has waited longest, then the next on
    /// each press, across every Kitty instance.
    func focusNextAttention() {
        guard store.feed == .live, let snapshot = store.snapshot else {
            refuse("The session feed is not live.")
            return
        }
        guard let session = cycler.advance(in: store.sessions, snapshotSocket: snapshot.kitty.socket) else {
            Self.logger.notice("next attention: nothing is waiting")
            NSSound.beep()
            return
        }
        Self.logger.notice("next attention: window \(session.windowID) (\(session.iconState, privacy: .public))")
        focus(MenuSessionRow(title: session.project, windowID: session.windowID, kittySocket: session.kittySocket, toolTip: ""))
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
        refreshRecent()
    }

    private func refreshProjects() {
        guard let command, gate.unavailableReason(.project) == nil,
              projects.needsRefresh(now: Date()), projects.state.begin() else { return }
        listQueue.async { [weak self] in
            let result = ProjectCatalog.fetch(command)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self, self.command?.binary == command.binary else { return }
                    if case let .failure(error) = result {
                        Self.logger.error("project list: \(error.message, privacy: .public)")
                    }
                    self.projects.state.finish(result, at: Date())
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
    /// Kitty is brought forward on the new tab.
    private func launch(_ project: ProjectEntry) {
        guard let command else { return report(preferences.binaryProblem ?? "amx is still being resolved; try again in a moment.") }
        if let reason = gate.unavailableReason(.project) { return report(reason) }
        let candidates = newTabCandidates()
        let arguments = ProjectCatalog.launchArguments(for: project)
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

    /// Recent: a conversation a live session runs is focused where it runs,
    /// in whichever Kitty that is; any other goes to `amx session continue`,
    /// which resumes it in the Kitty used last. amx sees only the Kitty it is
    /// pointed at, so asking it to continue a conversation live in another
    /// instance would resume a second copy instead of focusing the first.
    private func continueConversation(_ conversation: RecentConversation) {
        if let live = store.sessions.first(where: { $0.conversation == conversation.identity }) {
            focus(MenuSessionRow(title: conversation.selector, windowID: live.windowID, kittySocket: live.kittySocket, toolTip: ""))
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
        guard let command else { return report(preferences.binaryProblem ?? "amx is still being resolved; try again in a moment.") }
        if let reason = gate.unavailableReason(.muteToggle) { return report(reason) }
        let socket = row.kittySocket.isEmpty ? store.snapshot?.kitty.socket ?? "" : row.kittySocket
        guard !socket.isEmpty else { return report(KittyEndpoint.listenOnMissing) }
        focusQueue.async { [weak self] in
            let result = command.json(MuteToggleResult.self, MuteToggleResult.arguments(windowID: row.windowID), kittyListenOn: socket)
            DispatchQueue.main.async {
                MainActor.assumeIsolated {
                    guard let self else { return }
                    switch result {
                    case let .success(value):
                        Self.logger.notice("mute-toggle window \(row.windowID): muted=\(value.muted)")
                    case let .partial(value, _):
                        self.store.show(notice: value.muted ? "Muted, but the tab title was not updated." : "Unmuted, but the tab title was not updated.")
                    case let .failure(error):
                        Self.logger.error("mute-toggle window \(row.windowID): \(error.message, privacy: .public)")
                        self.store.show(notice: "Could not toggle mute: \(error.message)")
                    }
                }
            }
        }
    }

    // MARK: Snooze

    /// Notifications ▸: sets or clears the daemon's snooze and mirrors what
    /// the daemon answers into `amxSnoozeUntil`.
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
    }
}
