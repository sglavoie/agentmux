// Ported and trimmed from Ghostty's macOS app (MIT); see macos/NOTICE-ghostty.
import GhosttyKit

extension Ghostty {
    /// Who answers a runtime action. tt.app is one window over one surface,
    /// so everything built on tabs, splits, extra windows, or Ghostty's own
    /// chrome is refused here rather than half-implemented.
    enum ActionDisposition: Equatable {
        /// Answered by `Ghostty.App` itself.
        case app
        /// Forwarded to the app delegate, which owns the window and surface.
        case surface
        /// Refused: the action is reported as not performed.
        case ignored
    }

    static func disposition(of tag: ghostty_action_tag_e) -> ActionDisposition {
        switch tag {
        case GHOSTTY_ACTION_QUIT,
             GHOSTTY_ACTION_OPEN_URL,
             GHOSTTY_ACTION_OPEN_CONFIG,
             GHOSTTY_ACTION_RELOAD_CONFIG,
             GHOSTTY_ACTION_CONFIG_CHANGE,
             GHOSTTY_ACTION_RING_BELL:
            return .app

        case GHOSTTY_ACTION_SET_TITLE,
             GHOSTTY_ACTION_PWD,
             GHOSTTY_ACTION_MOUSE_SHAPE,
             GHOSTTY_ACTION_MOUSE_VISIBILITY,
             GHOSTTY_ACTION_MOUSE_OVER_LINK,
             GHOSTTY_ACTION_COLOR_CHANGE,
             GHOSTTY_ACTION_RENDERER_HEALTH,
             GHOSTTY_ACTION_CELL_SIZE,
             GHOSTTY_ACTION_INITIAL_SIZE,
             GHOSTTY_ACTION_SIZE_LIMIT,
             GHOSTTY_ACTION_SECURE_INPUT,
             GHOSTTY_ACTION_SHOW_CHILD_EXITED,
             GHOSTTY_ACTION_CLOSE_WINDOW,
             GHOSTTY_ACTION_TOGGLE_FULLSCREEN,
             GHOSTTY_ACTION_DESKTOP_NOTIFICATION:
            return .surface

        // Tabs, splits, the quick terminal, and new windows: explicitly ignored.
        case GHOSTTY_ACTION_NEW_WINDOW,
             GHOSTTY_ACTION_NEW_TAB,
             GHOSTTY_ACTION_CLOSE_TAB,
             GHOSTTY_ACTION_MOVE_TAB,
             GHOSTTY_ACTION_GOTO_TAB,
             GHOSTTY_ACTION_TOGGLE_TAB_OVERVIEW,
             GHOSTTY_ACTION_SET_TAB_TITLE,
             GHOSTTY_ACTION_NEW_SPLIT,
             GHOSTTY_ACTION_GOTO_SPLIT,
             GHOSTTY_ACTION_RESIZE_SPLIT,
             GHOSTTY_ACTION_EQUALIZE_SPLITS,
             GHOSTTY_ACTION_TOGGLE_SPLIT_ZOOM,
             GHOSTTY_ACTION_TOGGLE_QUICK_TERMINAL,
             GHOSTTY_ACTION_GOTO_WINDOW,
             GHOSTTY_ACTION_CLOSE_ALL_WINDOWS,
             GHOSTTY_ACTION_PRESENT_TERMINAL,
             GHOSTTY_ACTION_TOGGLE_VISIBILITY,
             GHOSTTY_ACTION_FLOAT_WINDOW,
             GHOSTTY_ACTION_TOGGLE_MAXIMIZE,
             GHOSTTY_ACTION_TOGGLE_WINDOW_DECORATIONS,
             GHOSTTY_ACTION_TOGGLE_BACKGROUND_OPACITY,
             GHOSTTY_ACTION_RESET_WINDOW_SIZE,
             GHOSTTY_ACTION_TOGGLE_COMMAND_PALETTE,
             GHOSTTY_ACTION_INSPECTOR,
             GHOSTTY_ACTION_RENDER_INSPECTOR,
             GHOSTTY_ACTION_SHOW_GTK_INSPECTOR,
             GHOSTTY_ACTION_PROMPT_TITLE,
             GHOSTTY_ACTION_COPY_TITLE_TO_CLIPBOARD,
             GHOSTTY_ACTION_UNDO,
             GHOSTTY_ACTION_REDO,
             GHOSTTY_ACTION_CHECK_FOR_UPDATES,
             GHOSTTY_ACTION_QUIT_TIMER:
            return .ignored

        default:
            // Search, progress, key tables, scrollbar, render, readonly, and
            // anything a later pin adds: nothing tt.app draws reads them.
            return .ignored
        }
    }
}
