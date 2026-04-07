"""Mode dispatch for the dashboard main event loop."""

from typing import Any

from cc_dashboard.loop_dashboard_dispatch_forms import dispatch_form_modes
from cc_dashboard.loop_dashboard_dispatch_listing import dispatch_listing_modes
from cc_dashboard.loop_dashboard_dispatch_viewers import dispatch_viewer_modes


def dispatch_mode(
    stdscr,
    key: int,
    state: dict[str, Any],
    build_session_actions,
) -> tuple[bool, bool]:
    """Dispatch one key event based on the active mode.

    Returns `(should_exit, should_continue)` for the caller loop.
    """
    for dispatcher in (
        lambda: dispatch_listing_modes(stdscr, key, state, build_session_actions),
        lambda: dispatch_viewer_modes(stdscr, key, state),
        lambda: dispatch_form_modes(stdscr, key, state),
    ):
        result = dispatcher()
        if result is not None:
            return result

    return False, False
