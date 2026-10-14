import AppKit
import GhosttyKit

extension Ghostty {
    /// Who holds what scrolled off a terminal.
    enum Scrollback: Equatable {
        /// Ghostty's own buffer: the program runs directly in the surface, and
        /// its history goes when the surface does.
        case terminal
        /// AgentMux's private tmux server. The surface is only a viewer of a
        /// session there, so its own buffer is empty and history is asked of
        /// the server, where it outlives this viewer and the app.
        case server
    }

    /// Something to do with a terminal's history. The raw values are the
    /// order of the private tmux server's user keys (`internal/tmux`,
    /// `ViewerKey`), which `TestViewerKeysMatchTheApp` holds this file to.
    enum HistoryRequest: Int, CaseIterable {
        case pageUp, pageDown, top, bottom, find, findNext, findPrevious, markers

        /// What follows CSI in the escape sequence the server binds for this
        /// request. No keyboard produces it.
        var serverKey: String { "\(5001 + rawValue)~" }

        /// The Ghostty binding action that carries out the request, or nil
        /// when that holder of history cannot.
        func bindingAction(for scrollback: Scrollback) -> String? {
            switch scrollback {
            case .server:
                return "csi:\(serverKey)"
            case .terminal:
                switch self {
                case .pageUp: return "scroll_page_up"
                case .pageDown: return "scroll_page_down"
                case .top: return "scroll_to_top"
                case .bottom: return "scroll_to_bottom"
                // The search opens the host's find bar (`start_search`),
                // which then sends the needle itself.
                case .find: return "start_search"
                case .findNext: return "navigate_search:next"
                case .findPrevious: return "navigate_search:previous"
                // The markers are the server's.
                case .markers: return nil
                }
            }
        }
    }
}

// The View and Edit menus' history items. A session's terminal passes them
// to the tmux server, which pages its own history or the full-screen
// program's; a utility view scrolls and searches Ghostty's buffer, the search
// shown in the workspace's find bar.
extension Ghostty.SurfaceView {
    @discardableResult
    func perform(_ request: Ghostty.HistoryRequest) -> Bool {
        guard let action = request.bindingAction(for: scrollback) else { return false }
        return performBindingAction(action)
    }

    override func scrollPageUp(_ sender: Any?) { perform(.pageUp) }

    override func scrollPageDown(_ sender: Any?) { perform(.pageDown) }

    override func scrollToBeginningOfDocument(_ sender: Any?) { perform(.top) }

    override func scrollToEndOfDocument(_ sender: Any?) { perform(.bottom) }

    @IBAction func find(_ sender: Any?) { perform(.find) }

    @IBAction func findNext(_ sender: Any?) { perform(.findNext) }

    @IBAction func findPrevious(_ sender: Any?) { perform(.findPrevious) }

    /// F3: highlights the errors, failures, warnings, and deprecations in a
    /// session's history, or goes back to the live screen. A full-screen
    /// agent gets its own F3 from the server instead.
    @IBAction func toggleMarkers(_ sender: Any?) { perform(.markers) }
}
