"""Main event loop coordinator for the Claude Code Dashboard."""

import curses

from cc_dashboard.colors import init_colors
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.loop_dashboard_bootstrap import (
    build_session_actions,
    initialize_dashboard_state,
    preflight_check,
    show_preflight_error,
)
from cc_dashboard.loop_dashboard_dispatch import dispatch_mode
from cc_dashboard.loop_dashboard_draw import build_draw_state
from cc_dashboard.loop_dashboard_idle import handle_idle_tick
from cc_dashboard.loop_render import draw_dashboard_frame


def dashboard(stdscr):
    """Run the interactive dashboard loop."""
    curses.set_escdelay(25)  # ms; reduce Escape key lag (default 1000ms)
    curses.curs_set(0)
    init_colors()
    stdscr.timeout(100)

    preflight_err = preflight_check()
    if preflight_err:
        show_preflight_error(stdscr, preflight_err)
        return

    state = initialize_dashboard_state(stdscr)

    def current_session_actions(sess, scope="selected"):
        return build_session_actions(state, sess, scope)

    min_rows, min_cols = 10, 40  # minimum terminal size for usable layout

    while True:
        rows, cols = stdscr.getmaxyx()
        if rows < min_rows or cols < min_cols:
            stdscr.erase()
            safe_addstr(stdscr, 0, 0, "Terminal too small")
            stdscr.refresh()
            key = stdscr.getch()
            if key == curses.KEY_RESIZE:
                continue
            if key == ord("q"):
                return
            continue

        draw_dashboard_frame(stdscr, build_draw_state(state), current_session_actions)
        stdscr.refresh()

        if state["msg_frames"] > 0:
            state["msg_frames"] -= 1
            if state["msg_frames"] == 0:
                state["msg"] = ""

        key = stdscr.getch()
        if key == curses.KEY_RESIZE:
            continue

        if key == -1:
            handle_idle_tick(stdscr, state)
            continue

        state["refresh_ticks"] = 0
        should_exit, should_continue = dispatch_mode(
            stdscr,
            key,
            state,
            current_session_actions,
        )
        if should_exit:
            return
        if state.pop("force_refresh", False):
            state["refresh_ticks"] = state["refresh_threshold"]
        if should_continue:
            continue
