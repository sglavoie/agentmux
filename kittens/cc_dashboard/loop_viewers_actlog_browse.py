"""Handlers for activity log browsing screens."""

import curses

from cc_dashboard.loop_helpers import _compute_browse_matches
from cc_dashboard.remote.activity import load_activity_log
from cc_dashboard.views import _format_log_entry


def handle_actlog_browse_mode(stdscr, key, state):
    """Handle key events for the activity log browser."""
    entries = state["actlog_browse_entries"]
    total = len(entries)
    browse_content_h = max(1, stdscr.getmaxyx()[0] - 5)

    def _browse_clamp_scroll():
        if (
            state["actlog_browse_selected"]
            >= state["actlog_browse_scroll"] + browse_content_h
        ):
            state["actlog_browse_scroll"] = (
                state["actlog_browse_selected"] - browse_content_h + 1
            )
        elif state["actlog_browse_selected"] < state["actlog_browse_scroll"]:
            state["actlog_browse_scroll"] = state["actlog_browse_selected"]

    if state["actlog_browse_search_active"]:
        if key == 27:
            state["actlog_browse_search_active"] = False
            state["actlog_browse_search_query"] = ""
            state["actlog_browse_search_cursor"] = 0
            state["actlog_browse_match_lines"] = []
            state["actlog_browse_match_index"] = 0
        elif key in (10, 13, curses.KEY_ENTER):
            state["actlog_browse_search_active"] = False
            if state["actlog_browse_match_lines"]:
                state["actlog_browse_selected"] = state["actlog_browse_match_lines"][0]
                state["actlog_browse_match_index"] = 0
                _browse_clamp_scroll()
        elif key in (127, 8, curses.KEY_BACKSPACE):
            if state["actlog_browse_search_cursor"] > 0:
                query = state["actlog_browse_search_query"]
                cursor = state["actlog_browse_search_cursor"]
                state["actlog_browse_search_query"] = (
                    query[: cursor - 1] + query[cursor:]
                )
                state["actlog_browse_search_cursor"] = cursor - 1
                state["actlog_browse_match_lines"] = _compute_browse_matches(
                    entries, state["actlog_browse_search_query"]
                )
                state["actlog_browse_match_index"] = 0
        elif key == curses.KEY_LEFT:
            state["actlog_browse_search_cursor"] = max(
                0, state["actlog_browse_search_cursor"] - 1
            )
        elif key == curses.KEY_RIGHT:
            state["actlog_browse_search_cursor"] = min(
                len(state["actlog_browse_search_query"]),
                state["actlog_browse_search_cursor"] + 1,
            )
        elif 32 <= key <= 126:
            ch = chr(key)
            query = state["actlog_browse_search_query"]
            cursor = state["actlog_browse_search_cursor"]
            state["actlog_browse_search_query"] = query[:cursor] + ch + query[cursor:]
            state["actlog_browse_search_cursor"] = cursor + 1
            state["actlog_browse_match_lines"] = _compute_browse_matches(
                entries, state["actlog_browse_search_query"]
            )
            state["actlog_browse_match_index"] = 0
        return True

    if key in (27, ord("q"), ord("Q")):
        if state["actlog_browse_search_query"]:
            state["actlog_browse_search_query"] = ""
            state["actlog_browse_match_lines"] = []
            state["actlog_browse_match_index"] = 0
        else:
            state["mode"] = "list"
    elif key == ord("/"):
        state["actlog_browse_search_active"] = True
        state["actlog_browse_search_query"] = ""
        state["actlog_browse_search_cursor"] = 0
        state["actlog_browse_match_lines"] = []
        state["actlog_browse_match_index"] = 0
    elif key == ord("n"):
        if state["actlog_browse_match_lines"]:
            state["actlog_browse_match_index"] = (
                state["actlog_browse_match_index"] + 1
            ) % len(state["actlog_browse_match_lines"])
            state["actlog_browse_selected"] = state["actlog_browse_match_lines"][
                state["actlog_browse_match_index"]
            ]
            _browse_clamp_scroll()
    elif key == ord("N"):
        if state["actlog_browse_match_lines"]:
            state["actlog_browse_match_index"] = (
                state["actlog_browse_match_index"] - 1
            ) % len(state["actlog_browse_match_lines"])
            state["actlog_browse_selected"] = state["actlog_browse_match_lines"][
                state["actlog_browse_match_index"]
            ]
            _browse_clamp_scroll()
    elif key in (ord("j"), curses.KEY_DOWN) and total:
        state["actlog_browse_selected"] = (state["actlog_browse_selected"] + 1) % total
        _browse_clamp_scroll()
    elif key in (ord("k"), curses.KEY_UP) and total:
        state["actlog_browse_selected"] = (state["actlog_browse_selected"] - 1) % total
        _browse_clamp_scroll()
    elif key == ord("G") and total:
        state["actlog_browse_selected"] = total - 1
        _browse_clamp_scroll()
    elif key == ord("g") and total:
        state["actlog_browse_selected"] = 0
        state["actlog_browse_scroll"] = 0
    elif key == 4 and total:
        state["actlog_browse_selected"] = min(
            state["actlog_browse_selected"] + browse_content_h // 2, total - 1
        )
        _browse_clamp_scroll()
    elif key == 21 and total:
        state["actlog_browse_selected"] = max(
            state["actlog_browse_selected"] - browse_content_h // 2, 0
        )
        _browse_clamp_scroll()
    elif key in (6, curses.KEY_NPAGE) and total:
        state["actlog_browse_selected"] = min(
            state["actlog_browse_selected"] + browse_content_h, total - 1
        )
        _browse_clamp_scroll()
    elif key in (2, curses.KEY_PPAGE) and total:
        state["actlog_browse_selected"] = max(
            state["actlog_browse_selected"] - browse_content_h, 0
        )
        _browse_clamp_scroll()
    elif key == ord("s"):
        state["actlog_gsearch_query"] = ""
        state["actlog_gsearch_cursor"] = 0
        state["actlog_gsearch_results"] = []
        state["actlog_gsearch_selected"] = 0
        state["actlog_gsearch_scroll"] = 0
        state["actlog_gsearch_last_query"] = ""
        state["actlog_gsearch_debounce_ticks"] = 0
        state["actlog_gsearch_loading"] = False
        state["actlog_gsearch_total_scanned"] = 0
        state["actlog_gsearch_log_index"] = entries
        state["actlog_gsearch_search_active"] = True
        state["actlog_gsearch_parent_mode"] = "actlog_browse"
        state["mode"] = "actlog_gsearch"
    elif key in (10, 13, curses.KEY_ENTER) and total:
        chosen = entries[state["actlog_browse_selected"]]
        session_id = chosen["session_id"]
        log_entries, summary = load_activity_log(session_id)
        if log_entries:
            state["actlog_session_title"] = chosen.get("title", "") or session_id[:12]
            state["actlog_session_id"] = session_id
            state["actlog_summary"] = summary
            state["actlog_lines"] = [
                _format_log_entry(entry, stdscr.getmaxyx()[1]) for entry in log_entries
            ]
            state["actlog_entries"] = log_entries
            state["actlog_selected"] = max(0, len(log_entries) - 1)
            state["actlog_scroll"] = max(
                0,
                len(state["actlog_lines"]) - max(1, stdscr.getmaxyx()[0] - 5),
            )
            state["actlog_search_query"] = ""
            state["actlog_search_active"] = False
            state["actlog_search_cursor"] = 0
            state["actlog_match_lines"] = []
            state["actlog_match_index"] = 0
            state["mode"] = "actlog"
        else:
            state["msg"] = "Log file is empty"
            state["msg_frames"] = 20
    return False


__all__ = ["handle_actlog_browse_mode"]
