import curses
import os

from cc_dashboard.config import load_accounts, load_defaults
from cc_dashboard.config_defaults import _save_tab_statuses
from cc_dashboard.remote.actions_core import do_rename
from cc_dashboard.remote.command import get_kitty_socket
from cc_dashboard.remote.shared import _strip_override_marker
from cc_tab_status_restore import build_restore_payload, schedule_color_restore
from cc_tab_status_state import get_current_session, load_statuses, set_status
from cc_tab_status_ui import run_status_picker


def main(args=None) -> str:
    load_accounts()
    load_defaults()
    socket = os.environ.get("KITTY_LISTEN_ON", "") or get_kitty_socket()
    session = get_current_session(socket)
    if session is None:
        return ""

    result = curses.wrapper(run_status_picker, session)
    if result is None:
        return ""

    kind, value = result
    title, override_emoji = _strip_override_marker(session["title"], return_emoji=True)
    new_title = set_status(title, value if value else None)
    if override_emoji:
        new_title = override_emoji + new_title
    do_rename(socket, session["tab_id"], new_title)

    if kind == "custom" and value:
        statuses = load_statuses()
        if value not in statuses:
            statuses.append(value)
            _save_tab_statuses(statuses)

    schedule_color_restore(
        socket,
        session["tab_id"],
        session.get("cc_color", ""),
        session.get("account", ""),
        session.get("cc_config_dir", ""),
    )
    return build_restore_payload(session)
