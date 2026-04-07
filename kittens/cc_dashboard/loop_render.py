"""Rendering helpers for the dashboard event loop."""

import curses

from cc_dashboard.loop_render_activity import render_activity_modes
from cc_dashboard.loop_render_forms import render_form_modes
from cc_dashboard.loop_render_hooks import render_hooks_modes
from cc_dashboard.loop_render_listing import render_list_modes

MIN_COLS = 40
MIN_ROWS = 10


def draw_dashboard_frame(stdscr, state, build_session_actions):
    """Render one dashboard frame for the current mode/state."""
    rows, cols = stdscr.getmaxyx()
    if cols < MIN_COLS or rows < MIN_ROWS:
        stdscr.erase()
        msg = f"Terminal too small ({cols}x{rows})"
        hint = f"Need at least {MIN_COLS}x{MIN_ROWS}"
        y = rows // 2
        x = max(0, (cols - len(msg)) // 2)
        try:
            stdscr.addstr(y, x, msg[:cols])
            if y + 1 < rows:
                x2 = max(0, (cols - len(hint)) // 2)
                stdscr.addstr(y + 1, x2, hint[:cols])
        except curses.error:
            pass
        return

    if render_list_modes(stdscr, state, build_session_actions):
        return
    if render_form_modes(stdscr, state):
        return
    if render_hooks_modes(stdscr, state):
        return
    render_activity_modes(stdscr, state)
