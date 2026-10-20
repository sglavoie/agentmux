import AppKit
import UserNotifications

// The app's banners: the presenter, and what a click or a button on one does.
extension AppDelegate {
    // MARK: Notifications

    func setUpNotifications() {
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
            self.connection.reconnectNow()
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
    func respond(to response: NotificationPresenter.Response) {
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
            problem: { [weak self] in AMXWording.unavailable(notFound: self?.preferences.binaryMenuProblem) }
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

    func applyMenuBarPreferences(_ preferences: AppPreferences) {
        guard let statusItem else { return }
        if statusItem.titleStyle != preferences.statusStyle { statusItem.titleStyle = preferences.statusStyle }
        if statusItem.showWorking != preferences.showWorkingCount { statusItem.showWorking = preferences.showWorkingCount }
        statusItem.kittySocketOverride = preferences.kittySocketOverride
    }

}
