"""Dispatch handlers for activity log viewer modes."""

from cc_dashboard.loop_viewers_actlog_browse import handle_actlog_browse_mode
from cc_dashboard.loop_viewers_actlog_main import (
    handle_actlog_detail_mode,
    handle_actlog_mode_main,
)
from cc_dashboard.loop_viewers_actlog_search import handle_actlog_gsearch_mode


def handle_actlog_mode(stdscr, key, state, sessions):
    """Handle key events for activity log modes.

    Returns True when the caller should continue to the next loop iteration.
    """
    mode = state["mode"]

    if mode == "actlog_detail":
        return handle_actlog_detail_mode(key, state)

    if mode == "actlog":
        return handle_actlog_mode_main(stdscr, key, state, sessions)

    if mode == "actlog_browse":
        return handle_actlog_browse_mode(stdscr, key, state)

    if mode == "actlog_gsearch":
        return handle_actlog_gsearch_mode(stdscr, key, state)

    return False


__all__ = ["handle_actlog_mode"]
