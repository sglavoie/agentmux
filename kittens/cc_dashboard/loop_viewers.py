"""Mode handlers for dashboard viewer-style screens."""

import curses

from cc_dashboard.loop_helpers import (
    _compute_browse_matches,
    _compute_peek_matches,
    _rstrip_blank_lines,
)
from cc_dashboard.remote.activity import list_activity_logs, load_activity_log
from cc_dashboard.remote.command import get_window_text
from cc_dashboard.views import _format_log_entry


def handle_actlog_mode(stdscr, key, state, sessions):
    """Handle key events for activity log modes.

    Returns True when the caller should continue to the next loop iteration.
    """
    mode = state["mode"]
    if mode == "actlog":
        content_height = stdscr.getmaxyx()[0] - 5
        max_scroll = max(0, len(state["actlog_lines"]) - content_height)

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
        elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
            state["actlog_scroll"] = min(state["actlog_scroll"] + 1, max_scroll)
        elif key in (curses.KEY_UP, ord("k"), ord("K")):
            state["actlog_scroll"] = max(state["actlog_scroll"] - 1, 0)
        elif key == ord("G"):
            state["actlog_scroll"] = max_scroll
        elif key == ord("g"):
            state["actlog_scroll"] = 0
        elif key == 4:
            state["actlog_scroll"] = min(
                state["actlog_scroll"] + content_height // 2,
                max_scroll,
            )
        elif key == 21:
            state["actlog_scroll"] = max(
                state["actlog_scroll"] - content_height // 2,
                0,
            )
        elif key in (6, curses.KEY_NPAGE):
            state["actlog_scroll"] = min(
                state["actlog_scroll"] + content_height,
                max_scroll,
            )
        elif key in (2, curses.KEY_PPAGE):
            state["actlog_scroll"] = max(state["actlog_scroll"] - content_height, 0)
        elif key == ord("r"):
            if state["actlog_session_id"]:
                entries, summary = load_activity_log(state["actlog_session_id"])
                if entries:
                    state["actlog_summary"] = summary
                    state["actlog_lines"] = [
                        _format_log_entry(e, stdscr.getmaxyx()[1]) for e in entries
                    ]
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

    if mode == "actlog_browse":
        entries = state["actlog_browse_entries"]
        total = len(entries)
        browse_content_h = stdscr.getmaxyx()[0] - 5

        def _browse_clamp_scroll():
            if state["actlog_browse_selected"] >= state["actlog_browse_scroll"] + browse_content_h:
                state["actlog_browse_scroll"] = state["actlog_browse_selected"] - browse_content_h + 1
            elif state["actlog_browse_selected"] < state["actlog_browse_scroll"]:
                state["actlog_browse_scroll"] = state["actlog_browse_selected"]

        # --- Search input mode ---
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
                    state["actlog_browse_search_query"] = query[: cursor - 1] + query[cursor:]
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

        # --- Normal mode ---
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
        elif key == 4 and total:  # Ctrl+d
            state["actlog_browse_selected"] = min(
                state["actlog_browse_selected"] + browse_content_h // 2, total - 1
            )
            _browse_clamp_scroll()
        elif key == 21 and total:  # Ctrl+u
            state["actlog_browse_selected"] = max(
                state["actlog_browse_selected"] - browse_content_h // 2, 0
            )
            _browse_clamp_scroll()
        elif key in (6, curses.KEY_NPAGE) and total:  # Ctrl+f / Page Down
            state["actlog_browse_selected"] = min(
                state["actlog_browse_selected"] + browse_content_h, total - 1
            )
            _browse_clamp_scroll()
        elif key in (2, curses.KEY_PPAGE) and total:  # Ctrl+b / Page Up
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
            state["mode"] = "actlog_gsearch"
        elif key in (10, 13, curses.KEY_ENTER) and total:
            chosen = entries[state["actlog_browse_selected"]]
            sid = chosen["session_id"]
            log_entries, summary = load_activity_log(sid)
            if log_entries:
                state["actlog_session_title"] = chosen.get("title", "") or sid[:12]
                state["actlog_session_id"] = sid
                state["actlog_summary"] = summary
                state["actlog_lines"] = [
                    _format_log_entry(e, stdscr.getmaxyx()[1]) for e in log_entries
                ]
                state["actlog_scroll"] = max(
                    0,
                    len(state["actlog_lines"]) - (stdscr.getmaxyx()[0] - 5),
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

    if mode == "actlog_gsearch":
        results = state["actlog_gsearch_results"]
        total = len(results)
        content_height = stdscr.getmaxyx()[0] - 6

        def _gsearch_clamp_scroll():
            if state["actlog_gsearch_selected"] >= state["actlog_gsearch_scroll"] + content_height:
                state["actlog_gsearch_scroll"] = state["actlog_gsearch_selected"] - content_height + 1
            elif state["actlog_gsearch_selected"] < state["actlog_gsearch_scroll"]:
                state["actlog_gsearch_scroll"] = state["actlog_gsearch_selected"]

        # --- Search input mode (j/k/g/G are text, not navigation) ---
        if state["actlog_gsearch_search_active"]:
            if key == 27:
                # Esc clears query in search mode
                state["actlog_gsearch_query"] = ""
                state["actlog_gsearch_cursor"] = 0
                state["actlog_gsearch_results"] = []
                state["actlog_gsearch_selected"] = 0
                state["actlog_gsearch_scroll"] = 0
                state["actlog_gsearch_last_query"] = ""
                state["actlog_gsearch_search_active"] = False
            elif key in (10, 13, curses.KEY_ENTER):
                # Enter confirms search, switch to navigation mode
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
                state["actlog_gsearch_selected"] = min(state["actlog_gsearch_selected"] + 1, total - 1)
                _gsearch_clamp_scroll()
            elif key in (curses.KEY_UP,) and total:
                state["actlog_gsearch_selected"] = max(state["actlog_gsearch_selected"] - 1, 0)
                _gsearch_clamp_scroll()
            elif 32 <= key <= 126:
                ch = chr(key)
                query = state["actlog_gsearch_query"]
                cursor = state["actlog_gsearch_cursor"]
                state["actlog_gsearch_query"] = query[:cursor] + ch + query[cursor:]
                state["actlog_gsearch_cursor"] = cursor + 1
                state["actlog_gsearch_debounce_ticks"] = 3
            return True

        # --- Normal mode (navigate results) ---
        if key == 27:
            state["mode"] = "list"
        elif key == ord("/"):
            state["actlog_gsearch_search_active"] = True
            state["actlog_gsearch_cursor"] = len(state["actlog_gsearch_query"])
        elif key in (10, 13, curses.KEY_ENTER) and total:
            # Jump to the selected result's session log
            result = results[state["actlog_gsearch_selected"]]
            sid = result["session_id"]
            log_entries, summary = load_activity_log(sid)
            if log_entries:
                cols = stdscr.getmaxyx()[1]
                state["actlog_session_title"] = result.get("title", "") or sid[:12]
                state["actlog_session_id"] = sid
                state["actlog_summary"] = summary
                state["actlog_lines"] = [
                    _format_log_entry(e, cols) for e in log_entries
                ]
                # Carry over search query for highlighting
                state["actlog_search_query"] = state["actlog_gsearch_query"]
                state["actlog_search_active"] = False
                state["actlog_search_cursor"] = 0
                state["actlog_match_lines"] = _compute_peek_matches(
                    state["actlog_lines"],
                    state["actlog_search_query"],
                )
                state["actlog_match_index"] = 0
                # Scroll to the entry_index position
                entry_idx = result.get("entry_index", 0)
                max_scroll = max(0, len(state["actlog_lines"]) - (stdscr.getmaxyx()[0] - 5))
                state["actlog_scroll"] = min(entry_idx, max_scroll)
                state["mode"] = "actlog"
            else:
                state["msg"] = "Log file is empty"
                state["msg_frames"] = 20
        elif key in (curses.KEY_DOWN, ord("j")) and total:
            state["actlog_gsearch_selected"] = min(state["actlog_gsearch_selected"] + 1, total - 1)
            _gsearch_clamp_scroll()
        elif key in (curses.KEY_UP, ord("k")) and total:
            state["actlog_gsearch_selected"] = max(state["actlog_gsearch_selected"] - 1, 0)
            _gsearch_clamp_scroll()
        elif key == 4 and total:  # Ctrl+D
            state["actlog_gsearch_selected"] = min(
                state["actlog_gsearch_selected"] + content_height // 2, total - 1
            )
            _gsearch_clamp_scroll()
        elif key == 21 and total:  # Ctrl+U
            state["actlog_gsearch_selected"] = max(
                state["actlog_gsearch_selected"] - content_height // 2, 0
            )
            _gsearch_clamp_scroll()
        elif key in (6, curses.KEY_NPAGE) and total:  # Ctrl+F / PgDn
            state["actlog_gsearch_selected"] = min(
                state["actlog_gsearch_selected"] + content_height, total - 1
            )
            _gsearch_clamp_scroll()
        elif key in (2, curses.KEY_PPAGE) and total:  # Ctrl+B / PgUp
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

    return False


def handle_peek_mode(stdscr, key, state, filtered, selected, home_socket):
    """Handle key events for peek mode.

    Returns True when the caller should continue to the next loop iteration.
    """
    if state["mode"] != "peek":
        return False

    content_height = stdscr.getmaxyx()[0] - 3
    max_scroll = max(0, len(state["peek_lines"]) - content_height)

    if state["peek_search_active"]:
        if key == 27:
            state["peek_search_active"] = False
            state["peek_search_query"] = ""
            state["peek_search_cursor"] = 0
            state["peek_match_lines"] = []
            state["peek_match_index"] = 0
        elif key in (10, 13, curses.KEY_ENTER):
            state["peek_search_active"] = False
            if state["peek_match_lines"]:
                state["peek_scroll"] = min(state["peek_match_lines"][0], max_scroll)
                state["peek_match_index"] = 0
        elif key in (127, 8, curses.KEY_BACKSPACE):
            if state["peek_search_cursor"] > 0:
                query = state["peek_search_query"]
                cursor = state["peek_search_cursor"]
                state["peek_search_query"] = query[: cursor - 1] + query[cursor:]
                state["peek_search_cursor"] = cursor - 1
                state["peek_match_lines"] = _compute_peek_matches(
                    state["peek_lines"],
                    state["peek_search_query"],
                )
                state["peek_match_index"] = 0
        elif key == curses.KEY_LEFT:
            state["peek_search_cursor"] = max(0, state["peek_search_cursor"] - 1)
        elif key == curses.KEY_RIGHT:
            state["peek_search_cursor"] = min(
                len(state["peek_search_query"]),
                state["peek_search_cursor"] + 1,
            )
        elif 32 <= key <= 126:
            ch = chr(key)
            query = state["peek_search_query"]
            cursor = state["peek_search_cursor"]
            state["peek_search_query"] = query[:cursor] + ch + query[cursor:]
            state["peek_search_cursor"] = cursor + 1
            state["peek_match_lines"] = _compute_peek_matches(
                state["peek_lines"],
                state["peek_search_query"],
            )
            state["peek_match_index"] = 0
        return True

    if key in (27, ord("q"), ord("Q")):
        if state["peek_search_query"]:
            state["peek_search_query"] = ""
            state["peek_match_lines"] = []
            state["peek_match_index"] = 0
        else:
            state["mode"] = "list"
    elif key == ord("/"):
        state["peek_search_active"] = True
        state["peek_search_query"] = ""
        state["peek_search_cursor"] = 0
        state["peek_match_lines"] = []
        state["peek_match_index"] = 0
    elif key == ord("n"):
        if state["peek_match_lines"]:
            state["peek_match_index"] = (state["peek_match_index"] + 1) % len(
                state["peek_match_lines"]
            )
            state["peek_scroll"] = min(
                state["peek_match_lines"][state["peek_match_index"]],
                max_scroll,
            )
    elif key == ord("N"):
        if state["peek_match_lines"]:
            state["peek_match_index"] = (state["peek_match_index"] - 1) % len(
                state["peek_match_lines"]
            )
            state["peek_scroll"] = min(
                state["peek_match_lines"][state["peek_match_index"]],
                max_scroll,
            )
    elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
        state["peek_scroll"] = min(state["peek_scroll"] + 1, max_scroll)
    elif key in (curses.KEY_UP, ord("k"), ord("K")):
        state["peek_scroll"] = max(state["peek_scroll"] - 1, 0)
    elif key == ord("G"):
        state["peek_scroll"] = max_scroll
    elif key == ord("g"):
        state["peek_scroll"] = 0
    elif key == 4:
        state["peek_scroll"] = min(
            state["peek_scroll"] + content_height // 2,
            max_scroll,
        )
    elif key == 21:
        state["peek_scroll"] = max(
            state["peek_scroll"] - content_height // 2,
            0,
        )
    elif key in (6, curses.KEY_NPAGE):
        state["peek_scroll"] = min(
            state["peek_scroll"] + content_height,
            max_scroll,
        )
    elif key in (2, curses.KEY_PPAGE):
        state["peek_scroll"] = max(state["peek_scroll"] - content_height, 0)
    elif key == curses.KEY_HOME:
        state["peek_scroll"] = 0
    elif key == curses.KEY_END:
        state["peek_scroll"] = max_scroll
    elif key == ord("r"):
        sess = filtered[selected] if filtered else None
        if sess:
            win_id = sess.get("claude_window_id", 0)
            sess_socket = sess.get("socket", home_socket)
            if win_id:
                text = get_window_text(sess_socket, win_id, state["peek_extent"])
                if text is not None:
                    state["peek_lines"] = _rstrip_blank_lines(text.splitlines())
                    state["peek_error"] = ""
                    state["peek_scroll"] = min(
                        state["peek_scroll"],
                        max(0, len(state["peek_lines"]) - content_height),
                    )
                else:
                    state["peek_error"] = "Failed to capture window text"
                    state["peek_lines"] = []
                    state["peek_scroll"] = 0
                state["peek_match_lines"] = _compute_peek_matches(
                    state["peek_lines"],
                    state["peek_search_query"],
                )
                state["peek_match_index"] = 0
    elif key == ord("s"):
        state["peek_extent"] = "all" if state["peek_extent"] == "screen" else "screen"
        state["peek_scroll"] = 0
        sess = filtered[selected] if filtered else None
        if sess:
            win_id = sess.get("claude_window_id", 0)
            sess_socket = sess.get("socket", home_socket)
            if win_id:
                text = get_window_text(sess_socket, win_id, state["peek_extent"])
                if text is not None:
                    state["peek_lines"] = _rstrip_blank_lines(text.splitlines())
                    state["peek_error"] = ""
                else:
                    state["peek_error"] = "Failed to capture window text"
                    state["peek_lines"] = []
            state["peek_match_lines"] = _compute_peek_matches(
                state["peek_lines"],
                state["peek_search_query"],
            )
            state["peek_match_index"] = 0

    return False
