"""Handlers for the dashboard peek viewer."""

import curses

from cc_dashboard.loop_helpers import _compute_peek_matches, _rstrip_blank_lines
from cc_dashboard.remote.command import get_window_text


def handle_peek_mode(stdscr, key, state, filtered, selected, home_socket):
    """Handle key events for peek mode.

    Returns True when the caller should continue to the next loop iteration.
    """
    if state["mode"] != "peek":
        return False

    content_height = max(1, stdscr.getmaxyx()[0] - 3)
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


__all__ = ["handle_peek_mode"]
