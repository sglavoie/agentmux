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
        case pageUp, pageDown, top, bottom, find, findNext, findPrevious

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
                // Ghostty's search needs a field of the host's own, which
                // the utility views do not have.
                case .find, .findNext, .findPrevious: return nil
                }
            }
        }
    }
}

// The View and Edit menus' history items. A session's terminal passes them
// to the tmux server, which pages its own history or the full-screen
// program's; a utility view scrolls Ghostty's buffer.
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
}
