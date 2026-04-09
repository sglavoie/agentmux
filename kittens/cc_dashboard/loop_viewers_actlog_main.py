"""Handlers for primary activity log screens."""

import curses

from cc_dashboard.loop_helpers import _compute_peek_matches
from cc_dashboard.remote.activity import list_activity_logs, load_activity_log
from cc_dashboard.views import _format_log_entry


def handle_actlog_detail_mode(key, state):
    """Handle key events for the activity log detail overlay."""
    if key in (27, ord("q"), ord("Q"), 10, 13, curses.KEY_ENTER):
        state["mode"] = "actlog"
    return False


def handle_actlog_mode_main(stdscr, key, state, sessions):
    """Handle key events for the main activity log viewer."""
    content_height = max(1, stdscr.getmaxyx()[0] - 5)
    total = len(state["actlog_lines"])
    max_scroll = max(0, total - content_height)

    def _actlog_clamp_scroll():
        if state["actlog_selected"] >= state["actlog_scroll"] + content_height:
            state["actlog_scroll"] = state["actlog_selected"] - content_height + 1
        elif state["actlog_selected"] < state["actlog_scroll"]:
            state["actlog_scroll"] = state["actlog_selected"]

    if state["actlog_search_active"]:
        if key == 27:
            state["actlog_search_active"] = False
            state["actlog_search_query"] = ""
            state["actlog_search_cursor"] = 0
            state["actlog_match_lines"] = []
            state["actlog_match_index"] = 0
        elif key in (10, 13, curses.KEY_ENTER):
            state["actlog_search_active"] = False
            if state["actlog_match_lines"]:
                state["actlog_scroll"] = min(
                    state["actlog_match_lines"][0],
                    max_scroll,
                )
                state["actlog_match_index"] = 0
        elif key in (127, 8, curses.KEY_BACKSPACE):
            if state["actlog_search_cursor"] > 0:
                query = state["actlog_search_query"]
                cursor = state["actlog_search_cursor"]
                state["actlog_search_query"] = query[: cursor - 1] + query[cursor:]
                state["actlog_search_cursor"] = cursor - 1
                state["actlog_match_lines"] = _compute_peek_matches(
                    state["actlog_lines"],
                    state["actlog_search_query"],
                )
                state["actlog_match_index"] = 0
        elif key == curses.KEY_LEFT:
            state["actlog_search_cursor"] = max(
                0,
                state["actlog_search_cursor"] - 1,
            )
        elif key == curses.KEY_RIGHT:
            state["actlog_search_cursor"] = min(
                len(state["actlog_search_query"]),
                state["actlog_search_cursor"] + 1,
            )
        elif 32 <= key <= 126:
            ch = chr(key)
            query = state["actlog_search_query"]
            cursor = state["actlog_search_cursor"]
            state["actlog_search_query"] = query[:cursor] + ch + query[cursor:]
            state["actlog_search_cursor"] = cursor + 1
            state["actlog_match_lines"] = _compute_peek_matches(
                state["actlog_lines"],
                state["actlog_search_query"],
            )
            state["actlog_match_index"] = 0
        return True

    if key in (27, ord("q"), ord("Q")):
        if state["actlog_search_query"]:
            state["actlog_search_query"] = ""
            state["actlog_match_lines"] = []
            state["actlog_match_index"] = 0
        else:
            state["mode"] = "list"
    elif key == ord("/"):
        state["actlog_search_active"] = True
        state["actlog_search_query"] = ""
        state["actlog_search_cursor"] = 0
        state["actlog_match_lines"] = []
        state["actlog_match_index"] = 0
    elif key == ord("n"):
        if state["actlog_match_lines"]:
            state["actlog_match_index"] = (state["actlog_match_index"] + 1) % len(
                state["actlog_match_lines"]
            )
            state["actlog_scroll"] = min(
                state["actlog_match_lines"][state["actlog_match_index"]],
                max_scroll,
            )
        else:
            state["actlog_scroll"] = min(
                state["actlog_scroll"] + content_height // 2,
                max_scroll,
            )
    elif key == ord("N"):
        if state["actlog_match_lines"]:
            state["actlog_match_index"] = (state["actlog_match_index"] - 1) % len(
                state["actlog_match_lines"]
            )
            state["actlog_scroll"] = min(
                state["actlog_match_lines"][state["actlog_match_index"]],
                max_scroll,
            )
        else:
            state["actlog_scroll"] = max(
                state["actlog_scroll"] - content_height // 2,
                0,
            )
    elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and total:
        state["actlog_selected"] = min(state["actlog_selected"] + 1, total - 1)
        _actlog_clamp_scroll()
    elif key in (curses.KEY_UP, ord("k"), ord("K")) and total:
        state["actlog_selected"] = max(state["actlog_selected"] - 1, 0)
        _actlog_clamp_scroll()
    elif key == ord("G") and total:
        state["actlog_selected"] = total - 1
        _actlog_clamp_scroll()
    elif key == ord("g") and total:
        state["actlog_selected"] = 0
        state["actlog_scroll"] = 0
    elif key == 4 and total:
        state["actlog_selected"] = min(
            state["actlog_selected"] + content_height // 2, total - 1
        )
        _actlog_clamp_scroll()
    elif key == 21 and total:
        state["actlog_selected"] = max(
            state["actlog_selected"] - content_height // 2, 0
        )
        _actlog_clamp_scroll()
    elif key in (6, curses.KEY_NPAGE) and total:
        state["actlog_selected"] = min(
            state["actlog_selected"] + content_height, total - 1
        )
        _actlog_clamp_scroll()
    elif key in (2, curses.KEY_PPAGE) and total:
        state["actlog_selected"] = max(state["actlog_selected"] - content_height, 0)
        _actlog_clamp_scroll()
    elif key in (10, 13, curses.KEY_ENTER) and total:
        entries = state.get("actlog_entries", [])
        if entries and 0 <= state["actlog_selected"] < len(entries):
            state["actlog_detail_entry"] = entries[state["actlog_selected"]]
            state["mode"] = "actlog_detail"
    elif key == ord("r"):
        if state["actlog_session_id"]:
            entries, summary = load_activity_log(state["actlog_session_id"])
            if entries:
                state["actlog_summary"] = summary
                state["actlog_lines"] = [
                    _format_log_entry(entry, stdscr.getmaxyx()[1]) for entry in entries
                ]
                state["actlog_entries"] = entries
                state["actlog_selected"] = min(
                    state["actlog_selected"], len(entries) - 1
                )
                state["actlog_scroll"] = min(
                    state["actlog_scroll"],
                    max(0, len(state["actlog_lines"]) - content_height),
                )
                state["actlog_match_lines"] = _compute_peek_matches(
                    state["actlog_lines"],
                    state["actlog_search_query"],
                )
                state["actlog_match_index"] = 0
    elif key == ord("b"):
        state["actlog_browse_entries"] = list_activity_logs(sessions)
        state["actlog_browse_selected"] = 0
        state["actlog_browse_scroll"] = 0
        state["actlog_browse_search_active"] = False
        state["actlog_browse_search_query"] = ""
        state["actlog_browse_search_cursor"] = 0
        state["actlog_browse_match_lines"] = []
        state["actlog_browse_match_index"] = 0
        state["mode"] = "actlog_browse"
    return False


__all__ = ["handle_actlog_detail_mode", "handle_actlog_mode_main"]
