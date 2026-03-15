"""Mode handlers for config browser screens in the dashboard loop."""

import curses

from cc_dashboard import config as _cfg
from cc_dashboard.remote import do_open_editor
from cc_dashboard.views import _shorten_home


def handle_config_modes(key, state, home_socket, reload_all_config):
    """Handle key events for config-related modes.

    Returns True when the mode was handled.
    """
    mode = state["mode"]

    if mode == "config_pick":
        config_entries = _cfg._all_config_paths()
        if key in (27, ord("q"), ord("Q")):
            state["mode"] = "list"
        elif key in (curses.KEY_UP, ord("k"), ord("K")):
            if config_entries:
                state["config_selected"] = (state["config_selected"] - 1) % len(
                    config_entries
                )
        elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
            if config_entries:
                state["config_selected"] = (state["config_selected"] + 1) % len(
                    config_entries
                )
        elif key in (10, 13, curses.KEY_ENTER) and config_entries:
            (
                state["config_file_label"],
                state["config_file_path"],
            ) = config_entries[state["config_selected"]]
            state["config_action_selected"] = 0
            state["mode"] = "config_action"
        elif key in (ord("r"), ord("R")):
            reload_all_config()
            state["msg"] = "Config reloaded from disk"
            state["msg_frames"] = 20
        return True

    if mode == "config_action":
        if key == 27:
            state["mode"] = "config_pick"
        elif key in (curses.KEY_UP, ord("k"), ord("K")):
            state["config_action_selected"] = (state["config_action_selected"] - 1) % 3
        elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
            state["config_action_selected"] = (state["config_action_selected"] + 1) % 3
        elif key in (10, 13, curses.KEY_ENTER):
            if state["config_action_selected"] == 0:
                try:
                    with open(state["config_file_path"], errors="replace") as f:
                        state["config_show_lines"] = f.read().splitlines()
                except OSError:
                    state["config_show_lines"] = ["(file not found)"]
                state["config_show_scroll"] = 0
                state["mode"] = "config_show"
            elif state["config_action_selected"] == 1:
                ok = do_open_editor(home_socket, state["config_file_path"])
                if ok:
                    state["msg"] = (
                        f"Opened {state['config_file_label']} in editor — "
                        "press R to reload config"
                    )
                else:
                    state["msg"] = "Failed to open editor"
                state["msg_frames"] = 30
                state["mode"] = "config_pick"
            elif state["config_action_selected"] == 2:
                state["msg"] = _shorten_home(state["config_file_path"])
                state["msg_frames"] = 30
                state["mode"] = "list"
        return True

    if mode == "config_show":
        if key in (27, ord("q"), ord("Q")):
            state["mode"] = "config_pick"
        elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
            content_height = state["screen_height"] - 3
            max_scroll = max(0, len(state["config_show_lines"]) - content_height)
            state["config_show_scroll"] = min(
                state["config_show_scroll"] + 1, max_scroll
            )
        elif key in (curses.KEY_UP, ord("k"), ord("K")):
            state["config_show_scroll"] = max(state["config_show_scroll"] - 1, 0)
        elif key in (ord("r"), ord("R")):
            reload_all_config()
            state["msg"] = "Config reloaded from disk"
            state["msg_frames"] = 20
        return True

    return False
