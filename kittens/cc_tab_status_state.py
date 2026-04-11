import json
from typing import TypedDict

from cc_dashboard.remote.command import kitty_cmd
from cc_dashboard.remote.shared import _strip_icons, find_current_tab


class SessionInfo(TypedDict):
    tab_id: int
    title: str
    raw_title: str
    account: str
    cc_color: str
    claude_window_id: int
    cc_config_dir: str


DEFAULT_STATUSES = [
    "reviewing",
    "WIP",
    "blocked",
    "testing",
    "done",
    "paused",
    "debugging",
]


def load_statuses() -> list[str]:
    """Load status presets from config state. Falls back to defaults."""
    from cc_dashboard import config_state as state

    statuses = state._default_tab_statuses
    if not statuses:
        return list(DEFAULT_STATUSES)

    seen: set[str] = set()
    deduped: list[str] = []
    for status in statuses:
        if status not in seen:
            seen.add(status)
            deduped.append(status)
    return deduped


def extract_status(title: str) -> tuple[str, str | None]:
    """Split icon-stripped title into (base, status_or_none)."""
    if " - " in title:
        base, status = title.split(" - ", 1)
        return base, status
    return title, None


def set_status(title: str, status: str | None) -> str:
    """Return title with status set or cleared."""
    base, _ = extract_status(title)
    return f"{base} - {status}" if status else base


def get_current_session(socket: str) -> SessionInfo | None:
    """Look up the current tab's session info via KITTY_WINDOW_ID."""
    result = kitty_cmd(socket, "ls")
    if not result or result.returncode != 0:
        return None
    try:
        data = json.loads(result.stdout)
    except (json.JSONDecodeError, TypeError):
        return None

    found = find_current_tab(data)
    if found is None:
        return None
    tab, cc_win = found
    if cc_win is None:
        return None

    raw_title = tab.get("title", "")
    title = _strip_icons(str(raw_title))
    user_vars = cc_win.get("user_vars", {})
    return {
        "tab_id": tab.get("id", 0),
        "title": title,
        "raw_title": str(raw_title),
        "account": user_vars.get("cc_account", ""),
        "cc_color": user_vars.get("cc_color", ""),
        "claude_window_id": cc_win.get("id", 0),
        "cc_config_dir": user_vars.get("cc_config_dir", ""),
    }
