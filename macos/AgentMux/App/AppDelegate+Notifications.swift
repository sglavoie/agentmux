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
            self?.refreshDockBadge(force: true)
        }
        notifications = presenter
        presenter.refreshAuthorization()
    }

    /// What the app does after reporting a banner response. The daemon
    /// focuses a runtime session on `clicked` through `focus_session`.
    func respond(to response: NotificationPresenter.Response) {
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
            return
        case .actionMute:
            // A session the workspace embeds has no window to match by.
            guard let session = Self.session(runtimeSessionID: response.embeddedRuntimeSessionID, in: store.managedSessions) else {
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
			answer(ASLAnswerTarget(runID: response.runID))
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
        controller.flow.onAnswerSent = { [weak self] target in self?.workspaceAnswerSent(runtimeSessionID: target.runtimeSessionID) }
        promptPanel = controller
        Self.logger.notice("prompt panel: session \(target.runtimeSessionID, privacy: .public)")
        controller.show(target)
    }

    func applyMenuBarPreferences(_ preferences: AppPreferences) {
        guard let statusItem else { return }
        if statusItem.titleStyle != preferences.statusStyle { statusItem.titleStyle = preferences.statusStyle }
        if statusItem.showWorking != preferences.showWorkingCount { statusItem.showWorking = preferences.showWorkingCount }
    }

}
