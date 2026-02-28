"""Mode handlers for dashboard viewer-style screens."""

import curses

from cc_dashboard.loop_helpers import _compute_peek_matches, _rstrip_blank_lines
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
            state["mode"] = "actlog_browse"
        return False

    if mode == "actlog_browse":
        if key in (ord("q"), ord("Q"), 27):
            state["mode"] = "list"
        elif key in (ord("j"), curses.KEY_DOWN) and state["actlog_browse_entries"]:
            state["actlog_browse_selected"] = (
                state["actlog_browse_selected"] + 1
            ) % len(state["actlog_browse_entries"])
            browse_content_h = stdscr.getmaxyx()[0] - 5
            if (
                state["actlog_browse_selected"]
                >= state["actlog_browse_scroll"] + browse_content_h
            ):
                state["actlog_browse_scroll"] = (
                    state["actlog_browse_selected"] - browse_content_h + 1
                )
            elif state["actlog_browse_selected"] < state["actlog_browse_scroll"]:
                state["actlog_browse_scroll"] = state["actlog_browse_selected"]
        elif key in (ord("k"), curses.KEY_UP) and state["actlog_browse_entries"]:
            state["actlog_browse_selected"] = (
                state["actlog_browse_selected"] - 1
            ) % len(state["actlog_browse_entries"])
            browse_content_h = stdscr.getmaxyx()[0] - 5
            if (
                state["actlog_browse_selected"]
                >= state["actlog_browse_scroll"] + browse_content_h
            ):
                state["actlog_browse_scroll"] = (
                    state["actlog_browse_selected"] - browse_content_h + 1
                )
            elif state["actlog_browse_selected"] < state["actlog_browse_scroll"]:
                state["actlog_browse_scroll"] = state["actlog_browse_selected"]
        elif key in (10, 13, curses.KEY_ENTER) and state["actlog_browse_entries"]:
            chosen = state["actlog_browse_entries"][state["actlog_browse_selected"]]
            sid = chosen["session_id"]
            entries, summary = load_activity_log(sid)
            if entries:
                state["actlog_session_title"] = chosen.get("title", "") or sid[:12]
                state["actlog_session_id"] = sid
                state["actlog_summary"] = summary
                state["actlog_lines"] = [
                    _format_log_entry(e, stdscr.getmaxyx()[1]) for e in entries
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
