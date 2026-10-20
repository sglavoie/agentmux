import AppKit

/// A failure the workspace reports in an alert: what failed, why in amx's
/// own words, and the one thing that would fix it when there is one.
struct WorkspaceError: Equatable {
    enum Recovery: Equatable {
        /// The daemon is down: Start Daemon, the menu's own start.
        case startDaemon
        /// amx was not found: Settings › Advanced names one.
        case openSettings

        var title: String {
            switch self {
            case .startDaemon: return "Start Daemon"
            case .openSettings: return AMXWording.openSettings
            }
        }
    }

    var title: String
    var message: String
    /// What amx wrote, whole, for Copy Details; the message may be cut short.
    var details: String = ""
    var recovery: Recovery?

    static let copyDetailsTitle = "Copy Details"

    /// The fix for `message`: Open Settings when it says amx was not found,
    /// Start Daemon while the daemon is down, else none.
    static func recovery(for message: String, daemonIsDown: Bool) -> Recovery? {
        if AMXWording.isNotFound(message) { return .openSettings }
        return daemonIsDown ? .startDaemon : nil
    }

    /// What Copy Details puts on the clipboard: the title, the message, and
    /// amx's own words when they say more.
    var copiedText: String {
        var lines = [title]
        if !message.isEmpty { lines.append(message) }
        if !details.isEmpty, details != message { lines += ["", details] }
        return lines.joined(separator: "\n")
    }

    /// The alert's buttons, in order: OK first (Return), then the fix, then
    /// Copy Details.
    var buttonTitles: [String] {
        ["OK"] + (recovery.map { [$0.title] } ?? []) + [Self.copyDetailsTitle]
    }

    enum Choice: Equatable { case dismiss, recover(Recovery), copy }

    /// What the button at `index` (from the first) does.
    func choice(at index: Int) -> Choice {
        let titles = buttonTitles
        guard titles.indices.contains(index) else { return .dismiss }
        if titles[index] == Self.copyDetailsTitle { return .copy }
        if index > 0, let recovery { return .recover(recovery) }
        return .dismiss
    }
}

/// Builds and answers the alert for a `WorkspaceError`.
@MainActor
enum WorkspaceErrorAlert {
    static func make(_ error: WorkspaceError) -> NSAlert {
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = error.title
        alert.informativeText = error.message
        for title in error.buttonTitles { alert.addButton(withTitle: title) }
        return alert
    }

    /// Carries out the button the operator chose.
    static func answer(_ response: NSApplication.ModalResponse, to error: WorkspaceError,
                       pasteboard: NSPasteboard = .general, recover: (WorkspaceError.Recovery) -> Void) {
        let index = response.rawValue - NSApplication.ModalResponse.alertFirstButtonReturn.rawValue
        switch error.choice(at: index) {
        case .dismiss:
            break
        case .copy:
            pasteboard.clearContents()
            pasteboard.setString(error.copiedText, forType: .string)
        case let .recover(recovery):
            recover(recovery)
        }
    }

    /// Shows `error` on `window` while it is open, else on its own, since a
    /// sheet on a closed window is never seen.
    static func present(_ error: WorkspaceError, on window: NSWindow?, recover: @escaping @MainActor (WorkspaceError.Recovery) -> Void) {
        let alert = make(error)
        if let window, window.isVisible {
            alert.beginSheetModal(for: window) { response in
                MainActor.assumeIsolated { answer(response, to: error, recover: recover) }
            }
        } else {
            NSApp.activate()
            answer(alert.runModal(), to: error, recover: recover)
        }
    }
}
