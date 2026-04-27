"""Tab sorting helpers for managed Claude sessions."""

import json
import subprocess
import sys

from cc_dashboard import config as _cfg
from cc_dashboard.remote.actions_launch import _parse_tab_groups, _sort_key_for_tab
from cc_dashboard.remote.command import kitty_cmd
from cc_dashboard.remote.shared import (
    _ICON_DASHBOARD,
    _ICON_STAR,
    _MANAGED_TITLE_RE,
    _strip_account_prefix,
    _strip_icons,
)


def _pick_os_window(data):
    """Pick the best OS window to sort tabs in.

    Prefers the focused OS window; falls back to the first one.
    Returns the OS window dict, or None if data is empty/malformed.
    """
    if not isinstance(data, list) or not data:
        return None
    for os_win in data:
        if isinstance(os_win, dict) and os_win.get("is_focused", False):
            return os_win
    return data[0] if isinstance(data[0], dict) else None


def do_sort_tabs(socket):
    """Sort managed tabs by account then title, pushing non-managed tabs to the end."""
    result = kitty_cmd(socket, "ls")
    if not result or result.returncode != 0:
        return
    try:
        data = json.loads(result.stdout)
    except json.JSONDecodeError:
        return

    os_win = _pick_os_window(data)
    if os_win is None:
        return
    tabs = os_win.get("tabs", [])
    if len(tabs) <= 1:
        return

    managed = []
    unmanaged = []
    focused_id = None
    has_pinned = False
    use_group = _cfg._default_sort_sessions == "group"

    for pos, tab in enumerate(tabs):
        tab_id = tab.get("id", 0)
        title = tab.get("title", "")
        stripped = _strip_icons(title)
        _pin_title = title
        if _pin_title.startswith(_ICON_DASHBOARD):
            _pin_title = _pin_title[len(_ICON_DASHBOARD) :]
        pinned = _pin_title.startswith(_ICON_STAR)
        if pinned:
            has_pinned = True

        if tab.get("is_focused", False):
            focused_id = tab_id

        cc_account = ""
        for win in tab.get("windows", []):
            uv = win.get("user_vars", {})
            acc = uv.get("cc_account", "")
            if acc:
                cc_account = acc
                break

        is_managed = bool(cc_account) or bool(_MANAGED_TITLE_RE.match(title))

        if is_managed:
            account = cc_account
            if not account and _MANAGED_TITLE_RE.match(title):
                initial = stripped[0].upper() if stripped else ""
                for name in _cfg._account_names:
                    if name and name[0].upper() == initial:
                        account = name
                        break
                if not account:
                    account = "unknown"
            project_name = _strip_account_prefix(stripped)
            pin_prefix = 0 if pinned else 1
            tab_groups = _parse_tab_groups(tab) if use_group else []
            sort_key = _sort_key_for_tab(
                pin_prefix, account, project_name, tab_groups, use_group
            )
            managed.append((sort_key, pos, tab_id, pinned))
        else:
            unmanaged.append((pos, tab_id))

    if not managed:
        return

    if _cfg._default_sort_sessions in ("alpha", "group") or not has_pinned:
        managed.sort(key=lambda x: x[0])
        desired_ids = [m[2] for m in managed] + [u[1] for u in unmanaged]
    else:
        pinned_tabs = [m for m in managed if m[3]]
        unpinned_tabs = [m for m in managed if not m[3]]
        pinned_tabs.sort(key=lambda x: x[0])
        desired_ids = (
            [m[2] for m in pinned_tabs]
            + [m[2] for m in unpinned_tabs]
            + [u[1] for u in unmanaged]
        )

    current_ids = [tab.get("id", 0) for tab in tabs]
    if desired_ids == current_ids:
        return

    cmd = [
        "kitty",
        "@",
        "--to",
        socket,
        "kitten",
        "cc_reorder_tabs.py",
    ]
    if focused_id is not None:
        cmd.append(f"--focus={focused_id}")
    cmd.extend(str(tid) for tid in desired_ids)
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=5)
    except subprocess.TimeoutExpired:
        print("reorder_tabs: timed out", file=sys.stderr)
        return
    if result.returncode != 0:
        print(f"reorder_tabs error: {result.stderr}", file=sys.stderr)
