import AppKit

// F1 to F11, and the other keys about the session the workspace shows.
extension AppDelegate {
    // MARK: Function keys

    // F1 to F11 act on the session the workspace pane shows.

    @objc func showKeybindings(_ sender: Any?) { showWorkspaceView(.keybindings) }
    @objc func focusNextSession(_ sender: Any?) { followingFocus { $0.selectNeighbor(1, sessionsOnly: true) } }
    @objc func focusPreviousSession(_ sender: Any?) { followingFocus { $0.selectNeighbor(-1, sessionsOnly: true) } }
    @objc func pickSessionIcon(_ sender: Any?) { openPicker(.iconPicker) }
    @objc func pickSessionColor(_ sender: Any?) { openPicker(.colorPicker) }
    @objc func clearSessionState(_ sender: Any?) { sendOperatorEvent("user_clear") }
    @objc func markSessionActive(_ sender: Any?) { sendOperatorEvent("user_active") }

    static let noSessionShown = "No session is shown in the workspace."
    static let pickerNeedsEmbedded = "The icon and color pickers need a running tmux session."

    func openPicker(_ kind: WorkspaceUtilityView.Kind) {
        guard let workspace, workspace.shownSessionRow != nil else { return refuse(Self.noSessionShown) }
        if !workspace.openPicker(kind) { refuse(Self.pickerNeedsEmbedded) }
    }

    /// What `amx hook-indicator` is given to name a runtime session.
    nonisolated static func hookTarget(_ target: WorkspaceTarget) -> [String] {
        switch target {
        case let .embedded(id): return ["--runtime-session", id]
        }
    }

    /// F8 and F11: tells the daemon the operator cleared the shown session's
    /// state, or marked it active.
    func sendOperatorEvent(_ event: String) {
        guard let row = workspace?.shownSessionRow else { return refuse(Self.noSessionShown) }
        guard let command else { return refuse(AMXWording.unavailable(notFound: preferences.binaryMenuProblem)) }
        guard case .embedded = row.target else { return refuse(Self.noSessionShown) }
        let arguments = ["hook-indicator", event] + Self.hookTarget(row.target) + ["--strict"]
        let title = Self.operatorEventFailedTitle(event, name: row.displayName)
        amxRunner.run({ command.run(arguments) }, onMain: { [weak self] result in
            guard let self else { return }
            switch result {
            case let .success(output) where output.status == 0:
                Self.logger.notice("\(event, privacy: .public) sent for \(row.project, privacy: .public)")
            case let .success(output):
                let stderr = DaemonStart.tail(output.stderr)
                Self.logger.error("\(event, privacy: .public): amx exited \(output.status): \(stderr, privacy: .public)")
                self.reportError(title: title, message: Self.operatorEventFailure(status: output.status, stderr: stderr), details: stderr)
            case let .failure(error):
                self.reportError(title: title, message: error.message)
            }
        })
    }

    /// The alert's title when F8 or F11 did not reach the daemon.
    nonisolated static func operatorEventFailedTitle(_ event: String, name: String) -> String {
        let what = event == "user_active" ? "marked active" : "cleared"
        return name.isEmpty ? "The session could not be \(what)" : "\(name) could not be \(what)"
    }

    /// What a failed `amx hook-indicator --strict` says: amx's own last line
    /// when it wrote one, else its exit status.
    nonisolated static func operatorEventFailure(status: Int32, stderr: String) -> String {
        let last = stderr.split(whereSeparator: \.isNewline).last.map(String.init)?
            .trimmingCharacters(in: .whitespaces) ?? ""
        return last.isEmpty ? "amx hook-indicator exited \(status) without saying why." : last
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
        case let .embedded(id):
            guard command != nil else { return refuse(AMXWording.unavailable(notFound: preferences.binaryMenuProblem)) }
            toggleMute(runtimeSessionID: id)
        }
    }

    /// `amx mute-toggle --runtime-session` shows the change at
    /// once, so a second click (or a banner's Mute) before the daemon
    /// publishes it does not toggle the session back.
    func toggleMute(runtimeSessionID id: String, banner: Bool = false) {
        guard let command else { return }
        let key = AttentionCycler.Key.embedded(id)
        if let listed = store.managedSessions.first(where: { $0.embeddedRuntimeSessionID == id }) {
            store.expectMute(key, muted: !listed.muted)
        }
        amxRunner.run({ command.json(MuteToggleResult.self, MuteToggleResult.arguments(runtimeSessionID: id)) },
                      onMain: { [weak self] result in
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
        })
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
}
