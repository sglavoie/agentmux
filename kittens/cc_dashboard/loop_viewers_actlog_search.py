"""Handlers for activity log global search screens."""

import curses

from cc_dashboard.loop_helpers import _compute_peek_matches
from cc_dashboard.remote.activity import load_activity_log
from cc_dashboard.views import _format_log_entry


def handle_actlog_gsearch_mode(stdscr, key, state):
    """Handle key events for the activity log global search view."""
    results = state["actlog_gsearch_results"]
    total = len(results)
    content_height = max(1, stdscr.getmaxyx()[0] - 6)

    def _gsearch_clamp_scroll():
        if (
            state["actlog_gsearch_selected"]
            >= state["actlog_gsearch_scroll"] + content_height
        ):
            state["actlog_gsearch_scroll"] = (
                state["actlog_gsearch_selected"] - content_height + 1
            )
        elif state["actlog_gsearch_selected"] < state["actlog_gsearch_scroll"]:
            state["actlog_gsearch_scroll"] = state["actlog_gsearch_selected"]

    if state["actlog_gsearch_search_active"]:
        if key == 27:
            state["actlog_gsearch_query"] = ""
            state["actlog_gsearch_cursor"] = 0
            state["actlog_gsearch_results"] = []
            state["actlog_gsearch_selected"] = 0
            state["actlog_gsearch_scroll"] = 0
            state["actlog_gsearch_last_query"] = ""
            state["actlog_gsearch_search_active"] = False
        elif key in (10, 13, curses.KEY_ENTER):
            state["actlog_gsearch_search_active"] = False
        elif key in (127, 8, curses.KEY_BACKSPACE):
            if state["actlog_gsearch_cursor"] > 0:
                query = state["actlog_gsearch_query"]
                cursor = state["actlog_gsearch_cursor"]
                state["actlog_gsearch_query"] = query[: cursor - 1] + query[cursor:]
                state["actlog_gsearch_cursor"] = cursor - 1
                state["actlog_gsearch_debounce_ticks"] = 3
        elif key == curses.KEY_LEFT:
            state["actlog_gsearch_cursor"] = max(0, state["actlog_gsearch_cursor"] - 1)
        elif key == curses.KEY_RIGHT:
            state["actlog_gsearch_cursor"] = min(
                len(state["actlog_gsearch_query"]),
                state["actlog_gsearch_cursor"] + 1,
            )
        elif key in (curses.KEY_DOWN,) and total:
            state["actlog_gsearch_selected"] = min(
                state["actlog_gsearch_selected"] + 1, total - 1
            )
            _gsearch_clamp_scroll()
        elif key in (curses.KEY_UP,) and total:
            state["actlog_gsearch_selected"] = max(
                state["actlog_gsearch_selected"] - 1, 0
            )
            _gsearch_clamp_scroll()
        elif 32 <= key <= 126:
            ch = chr(key)
            query = state["actlog_gsearch_query"]
            cursor = state["actlog_gsearch_cursor"]
            state["actlog_gsearch_query"] = query[:cursor] + ch + query[cursor:]
            state["actlog_gsearch_cursor"] = cursor + 1
            state["actlog_gsearch_debounce_ticks"] = 3
        return True

    if key == 27:
        state["mode"] = state.get("actlog_gsearch_parent_mode", "list")
    elif key == ord("/"):
        state["actlog_gsearch_search_active"] = True
        state["actlog_gsearch_cursor"] = len(state["actlog_gsearch_query"])
    elif key in (10, 13, curses.KEY_ENTER) and total:
        result = results[state["actlog_gsearch_selected"]]
        session_id = result["session_id"]
        log_entries, summary = load_activity_log(session_id)
        if log_entries:
            cols = stdscr.getmaxyx()[1]
            state["actlog_session_title"] = result.get("title", "") or session_id[:12]
            state["actlog_session_id"] = session_id
            state["actlog_summary"] = summary
            state["actlog_lines"] = [
                _format_log_entry(entry, cols) for entry in log_entries
            ]
            state["actlog_entries"] = log_entries
            state["actlog_selected"] = result.get("entry_index", 0)
            state["actlog_search_query"] = state["actlog_gsearch_query"]
            state["actlog_search_active"] = False
            state["actlog_search_cursor"] = 0
            state["actlog_match_lines"] = _compute_peek_matches(
                state["actlog_lines"],
                state["actlog_search_query"],
            )
            state["actlog_match_index"] = 0
            entry_idx = result.get("entry_index", 0)
            max_scroll = max(
                0, len(state["actlog_lines"]) - max(1, stdscr.getmaxyx()[0] - 5)
            )
            state["actlog_scroll"] = min(entry_idx, max_scroll)
            state["mode"] = "actlog"
        else:
            state["msg"] = "Log file is empty"
            state["msg_frames"] = 20
    elif key in (curses.KEY_DOWN, ord("j")) and total:
        state["actlog_gsearch_selected"] = min(
            state["actlog_gsearch_selected"] + 1, total - 1
        )
        _gsearch_clamp_scroll()
    elif key in (curses.KEY_UP, ord("k")) and total:
        state["actlog_gsearch_selected"] = max(state["actlog_gsearch_selected"] - 1, 0)
        _gsearch_clamp_scroll()
    elif key == 4 and total:
        state["actlog_gsearch_selected"] = min(
            state["actlog_gsearch_selected"] + content_height // 2, total - 1
        )
        _gsearch_clamp_scroll()
    elif key == 21 and total:
        state["actlog_gsearch_selected"] = max(
            state["actlog_gsearch_selected"] - content_height // 2, 0
        )
        _gsearch_clamp_scroll()
    elif key in (6, curses.KEY_NPAGE) and total:
        state["actlog_gsearch_selected"] = min(
            state["actlog_gsearch_selected"] + content_height, total - 1
        )
        _gsearch_clamp_scroll()
    elif key in (2, curses.KEY_PPAGE) and total:
        state["actlog_gsearch_selected"] = max(
            state["actlog_gsearch_selected"] - content_height, 0
        )
        _gsearch_clamp_scroll()
    elif key == ord("G") and total:
        state["actlog_gsearch_selected"] = total - 1
        _gsearch_clamp_scroll()
    elif key == ord("g") and total:
        state["actlog_gsearch_selected"] = 0
        state["actlog_gsearch_scroll"] = 0
    return False


__all__ = ["handle_actlog_gsearch_mode"]
