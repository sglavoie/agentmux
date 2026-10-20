import AppKit

// What the workspace says when something asked of it did not happen: a
// notice at the top of the pane for a refusal, and an alert, with amx's own
// words and a way on, for a failure.
extension WorkspaceWindowController {
    /// Shows `message` at the top of the pane for `WorkspaceNoticeBanner.duration`,
    /// and says it to VoiceOver. A newer notice replaces it.
    func showNotice(_ message: String) {
        guard isOpen else { return }
        if noticeBanner.superview == nil {
            pane.addSubview(noticeBanner, positioned: .above, relativeTo: nil)
            let width = noticeBanner.widthAnchor.constraint(lessThanOrEqualToConstant: 520)
            NSLayoutConstraint.activate([
                noticeBanner.topAnchor.constraint(equalTo: pane.topAnchor, constant: 8),
                noticeBanner.centerXAnchor.constraint(equalTo: pane.centerXAnchor),
                noticeBanner.leadingAnchor.constraint(greaterThanOrEqualTo: pane.leadingAnchor, constant: 16),
                width,
            ])
        } else {
            // Keep it above whatever the pane added since.
            pane.addSubview(noticeBanner, positioned: .above, relativeTo: nil)
        }
        noticeBanner.text = message
        noticeBanner.isHidden = false
        NSAccessibility.post(element: window, notification: .announcementRequested,
                             userInfo: [.announcement: message, .priority: NSAccessibilityPriorityLevel.high.rawValue])
        noticeEnd?.cancel()
        let end = DispatchWorkItem { [weak self] in
            MainActor.assumeIsolated { self?.endNotice() }
        }
        noticeEnd = end
        DispatchQueue.main.asyncAfter(deadline: .now() + WorkspaceNoticeBanner.duration, execute: end)
    }

    func endNotice() {
        noticeEnd?.cancel()
        noticeEnd = nil
        noticeBanner.isHidden = true
    }

    /// The notice the pane shows now, or nil.
    var shownNotice: String? {
        noticeBanner.isHidden ? nil : noticeBanner.text
    }

    /// Reports a failure in an alert on the window: `message` is amx's own
    /// error, offered whole by Copy Details, with Open Settings when it says
    /// amx was not found or Start Daemon while the daemon is down.
    func showError(title: String, message: String, details: String = "") {
        let error = WorkspaceError(
            title: title, message: message, details: details,
            recovery: WorkspaceError.recovery(for: message, daemonIsDown: delegate?.workspaceDaemonIsDown ?? false)
        )
        if let reportError {
            reportError(error)
            return
        }
        guard !AppDelegate.isRunningTests else { return }
        WorkspaceErrorAlert.present(error, on: isOpen ? window : nil) { [weak self] recovery in
            switch recovery {
            case .startDaemon: self?.delegate?.workspaceStartDaemon()
            case .openSettings: self?.delegate?.workspaceOpenAMXSettings()
            }
        }
    }
}
