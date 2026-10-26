import AppKit

// The companion feed as the app sees it: what each frame does, what a store
// change redraws, and the daemon starts and restarts the operator asks for,
// all through `DaemonConnection`.
extension AppDelegate: DaemonConnectionDelegate {
    /// How long the first subscribe waits for amx to be resolved.
    static let firstSubscribeWait: TimeInterval = 3

    /// Whether a health answer means the feed should dial now
    /// (`DaemonConnection.shouldReconnect`).
    nonisolated static func shouldReconnect(after status: DaemonStatus, feed: FeedState) -> Bool {
        DaemonConnection.shouldReconnect(after: status, feed: feed)
    }

    /// Opens the companion feed, once.
    func startSubscription() {
        guard !isTerminating else { return }
        connection.start()
    }

    /// Asks the daemon for its health while the feed is not live.
    func refreshDaemonStatus() {
        connection.refreshStatus()
    }

    func connection(_ connection: DaemonConnection, received event: SubscriptionClient.Event) {
        switch event {
        case .disconnected:
            notifications?.connectionEnded()
        case let .frame(.notification(_, data)):
            notifications?.present(frame: data)
        case let .frame(.notificationRetract(_, frame)):
            notifications?.retract(frame)
        case let .frame(.workspaceView(_, request)):
            handle(request)
        default:
            break
        }
    }

    func connectionApplied(_ connection: DaemonConnection) {
        followDaemonBuild()
    }

    func connection(_ connection: DaemonConnection, report message: String) {
        report(message)
    }

    func connection(_ connection: DaemonConnection, failed title: String, message: String) {
        reportFailure(title: title, message)
    }

    func connectionBusyChanged(_ connection: DaemonConnection) {
        statusItem?.refresh()
    }

    func connection(_ connection: DaemonConnection, beginChange label: String) -> Int {
        beginChange(label)
    }

    func connection(_ connection: DaemonConnection, endChange token: Int) {
        endChange(token)
    }

    func storeChanged() {
        historyWindow?.model.liveConversations = Set(store.feed == .live ? store.sessions.compactMap { $0.conversation?.encoded } : [])
        snoozes.reconcile()
        statusItem?.refresh()
        workspace?.refresh()
        sharePinsOnce()
        // Only a live snapshot can say a question is no longer open.
        if store.feed == .live, let snapshot = store.snapshot {
            aslPanel?.flow.observe(waiting: snapshot.asl.waiting)
        }
        let kitty = Self.kittyInUse(snapshotSocket: store.snapshot?.kitty.socket ?? "", sessions: store.sessions)
        if preferencesModel.kittyInUse != kitty { preferencesModel.kittyInUse = kitty }
        preferences.daemonStatus = store.daemonStatus
        publishPreferences()
    }

    /// Pins the sessions this app pinned by itself before pins were the
    /// sessions' own (the ★ in the title, which the dashboard reads too),
    /// once, at the first live snapshot. A session not listed then keeps
    /// only its place among the pinned.
    func sharePinsOnce() {
        let defaults = UserDefaults.standard
        guard !Self.isRunningTests, !defaults.bool(forKey: WorkspaceOrder.pinsSharedDefaultsKey),
              store.feed == .live, let snapshot = store.snapshot, command != nil else { return }
        defaults.set(true, forKey: WorkspaceOrder.pinsSharedDefaultsKey)
        let rows = WorkspaceSidebar.rows(store.sessions, snapshotSocket: snapshot.kitty.socket)
        for row in attentionOrder.unsharedPins(rows) {
            Self.logger.notice("workspace: sharing the pin of \(WorkspaceOrder.token(row.key), privacy: .public)")
            workspaceSetPinned(row, true)
        }
    }

    /// Whether any Kitty is in use, for Settings' Kitty-only fields: the
    /// daemon knows a Kitty socket, or a listed session lives in Kitty.
    nonisolated static func kittyInUse(snapshotSocket: String, sessions: [CompanionSession]) -> Bool {
        !snapshotSocket.isEmpty || sessions.contains { $0.embeddedRuntimeSessionID == nil && $0.windowID > 0 }
    }

    /// Hands Preferences what it reports, without republishing an unchanged
    /// value on every snapshot.
    func publishPreferences() {
        AMXWording.notFoundProblem = command == nil ? preferences.binaryMenuProblem : nil
        if preferencesModel.snapshot != preferences { preferencesModel.snapshot = preferences }
        let notification = store.feed == .live ? store.snapshot?.notification : nil
        if preferencesModel.daemonNotification != notification { preferencesModel.daemonNotification = notification }
        workspace?.refreshCommands()
    }

    // MARK: Start and restart

    /// What the daemon start or restart under way is doing, or nil.
    var daemonBusy: String? { connection.busy }

    /// Start Daemon, from the menu, the workspace, or an error's button: the
    /// app never starts the daemon on its own.
    func startDaemon() {
        guard let command else {
            lookAgainForAmx()
            return report(AMXWording.unavailable(notFound: preferences.binaryMenuProblem))
        }
        connection.startDaemon(command)
    }

    /// Restart Daemon, offered when the daemon is too old for this app.
    func restartDaemon(completion: ((Bool) -> Void)? = nil) {
        guard let command else {
            completion?(false)
            return report(AMXWording.unavailable(notFound: preferences.binaryMenuProblem))
        }
        connection.restartDaemon(command, completion: completion)
    }

    /// Whether the daemon is known to be down, so an error can offer to
    /// start it.
    var daemonIsDown: Bool {
        guard store.feed != .live else { return false }
        if store.feed == .notRunning { return true }
        if case .notRunning? = store.health { return true }
        return false
    }

    // MARK: Snooze

    /// Notifications ▸, and Preferences' Resume Now.
    func snooze(_ choice: SnoozeChoice, banner: Bool = false) {
        snoozes.snooze(choice, banner: banner)
    }
}
