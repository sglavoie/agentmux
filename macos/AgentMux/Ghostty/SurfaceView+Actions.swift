// Ported and trimmed from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import AppKit
import GhosttyKit

extension Ghostty.SurfaceView {
    // MARK: Runtime actions

    /// Answers a surface-targeted action, returning whether it was
    /// performed. Ghostty may call from any thread and is still inside its
    /// own callback, so state changes hop to the main thread and delegate
    /// calls are always deferred: a delegate may then free the surface.
    func perform(_ action: ghostty_action_s) -> Bool {
        switch action.tag {
        case GHOSTTY_ACTION_SET_TITLE:
            guard let ptr = action.action.set_title.title else { return false }
            let title = String(cString: ptr)
            later { $0.setTitle(title) }
            return true

        case GHOSTTY_ACTION_PWD:
            guard let ptr = action.action.pwd.pwd else { return false }
            let pwd = String(cString: ptr)
            later { $0.setWorkingDirectory(pwd) }
            return true

        case GHOSTTY_ACTION_MOUSE_SHAPE:
            let shape = action.action.mouse_shape
            later { $0.setCursorShape(shape) }
            return true

        case GHOSTTY_ACTION_MOUSE_VISIBILITY:
            let visible = action.action.mouse_visibility == GHOSTTY_MOUSE_VISIBLE
            later { $0.setCursorVisibility(visible) }
            return true

        case GHOSTTY_ACTION_COLOR_CHANGE:
            let change = action.action.color_change
            guard change.kind == GHOSTTY_ACTION_COLOR_KIND_BACKGROUND else { return false }
            let color = Ghostty.color(ghostty_config_color_s(r: change.r, g: change.g, b: change.b))
            later { $0.setBackgroundColor(color) }
            return true

        case GHOSTTY_ACTION_RENDERER_HEALTH:
            let healthy = action.action.renderer_health == GHOSTTY_RENDERER_HEALTH_HEALTHY
            if !healthy {
                Ghostty.logger.error("renderer reported unhealthy")
            }
            later { $0.setRendererHealthy(healthy) }
            return true

        case GHOSTTY_ACTION_DESKTOP_NOTIFICATION:
            // Nobody to show it: Ghostty is told it was not.
            guard delegate != nil else { return false }
            let note = action.action.desktop_notification
            let title = note.title.map { String(cString: $0) } ?? ""
            let body = note.body.map { String(cString: $0) } ?? ""
            later { $0.delegate?.surface($0, didRequestNotificationTitled: title, body: body) }
            return true

        case GHOSTTY_ACTION_RING_BELL:
            later { $0.delegate?.surfaceDidRingBell($0) }
            return true

        case GHOSTTY_ACTION_MOUSE_OVER_LINK:
            let link = action.action.mouse_over_link
            let url = link.url.flatMap { ptr in
                link.len == 0 ? nil : String(data: Data(bytes: ptr, count: Int(link.len)), encoding: .utf8)
            }
            later { $0.setHoveredLink(url) }
            return true

        case GHOSTTY_ACTION_SECURE_INPUT:
            let change = action.action.secure_input
            now { $0.setSecureInput(change) }
            return true

        // The next key may already be on its way: these take effect
        // before this callback returns whenever it runs on the main thread.
        case GHOSTTY_ACTION_KEY_SEQUENCE:
            let active = action.action.key_sequence.active
            now { $0.setKeySequenceActive(active) }
            return true

        case GHOSTTY_ACTION_KEY_TABLE:
            let table = action.action.key_table
            // The name is Ghostty's only for the length of the callback.
            if table.tag == GHOSTTY_KEY_TABLE_ACTIVATE, !Thread.isMainThread {
                let value = table.value.activate
                let name = value.name.map {
                    String(data: Data(bytes: $0, count: Int(value.len)), encoding: .utf8) ?? ""
                } ?? ""
                later { $0.keyTables.append(name) }
                return true
            }
            now { $0.setKeyTable(table) }
            return true

        // A session's terminal is a viewer of the tmux server, whose
        // history and search are the server's (Ghostty.SurfaceView+History.swift).
        case GHOSTTY_ACTION_START_SEARCH:
            guard scrollback == .terminal else { return false }
            let needle = action.action.start_search.needle.map { String(cString: $0) } ?? ""
            later { $0.searchDidChange(.started(needle: needle)) }
            return true

        case GHOSTTY_ACTION_END_SEARCH:
            guard scrollback == .terminal else { return false }
            later { $0.searchDidChange(.ended) }
            return true

        case GHOSTTY_ACTION_SEARCH_TOTAL:
            guard scrollback == .terminal else { return false }
            let total = action.action.search_total.total
            later { $0.searchDidChange(.total(total < 0 ? nil : Int(total))) }
            return true

        case GHOSTTY_ACTION_SEARCH_SELECTED:
            guard scrollback == .terminal else { return false }
            let selected = action.action.search_selected.selected
            later { $0.searchDidChange(.selected(selected < 0 ? nil : Int(selected))) }
            return true

        case GHOSTTY_ACTION_SCROLLBAR:
            guard scrollback == .terminal else { return false }
            let bar = action.action.scrollbar
            let scrollbar = Ghostty.Scrollbar(total: bar.total, offset: bar.offset, len: bar.len)
            later { $0.setScrollbar(scrollbar) }
            return true

        case GHOSTTY_ACTION_PROGRESS_REPORT:
            let progress = Ghostty.Progress(action.action.progress_report)
            later { $0.setProgress(progress) }
            return true

        case GHOSTTY_ACTION_SHOW_CHILD_EXITED:
            // Returning true tells Ghostty the host shows the exit, so it
            // writes nothing of its own into the terminal.
            later { $0.childDidExit() }
            return true

        case GHOSTTY_ACTION_CLOSE_WINDOW:
            // An embedded terminal's "window" is its view.
            closeRequested(needsConfirm: false)
            return true

        default:
            return false
        }
    }

    /// Ghostty asked for this surface to be closed.
    func closeRequested(needsConfirm: Bool) {
        later { $0.delegate?.surfaceDidRequestClose($0, needsConfirm: needsConfirm) }
    }

    /// Runs `work` on the main thread after the current Ghostty callback
    /// has returned, and only if the surface is still open then.
    private func later(_ work: @escaping (Ghostty.SurfaceView) -> Void) {
        DispatchQueue.main.async { [weak self] in
            guard let self, self.surface != nil else { return }
            work(self)
        }
    }

    /// Runs `work` at once when on the main thread, else as `later`. Only
    /// for state changes: it calls no delegate.
    private func now(_ work: @escaping (Ghostty.SurfaceView) -> Void) {
        guard Thread.isMainThread else { return later(work) }
        guard surface != nil else { return }
        work(self)
    }
}
