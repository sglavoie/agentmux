"""Tab UI helpers: colors, notes overlays, and mute state."""

import json
import shlex
import subprocess
from pathlib import Path

from cc_dashboard import config as _cfg
from cc_dashboard.config_notes import _resolve_notes_editor, notes_file_for_title
from cc_dashboard.remote.command import kitty_cmd
from cc_dashboard.remote.shared import (
    _ICON_DASHBOARD,
    _ICON_FLAG,
    _ICON_MUTE,
    _ICON_STAR,
    _ICON_STATES,
    _ICONS,
)


def do_set_color(socket, session, color_name):
    """Apply a color override to a session's tab and persist via user var.

    Pass empty string for color_name to reset to account default.
    Returns True on success.
    """
    tab_id = session["tab_id"]
    win_id = session.get("claude_window_id", 0)
    account = session["account"]
    config_dir = session.get("cc_config_dir", "")

    result = kitty_cmd(
        socket,
        "set-tab-color",
        "--match",
        f"id:{tab_id}",
        *_cfg._palette_tab_colors(
            account, color_override=color_name, config_dir_override=config_dir
        ),
    )
    if not result or result.returncode != 0:
        return False

    if win_id:
        kitty_cmd(
            socket,
            "set-user-vars",
            "--match",
            f"id:{win_id}",
            f"cc_color={color_name}",
        )
        kitty_cmd(
            socket,
            "set-user-vars",
            "--match",
            f"id:{win_id}",
            "cc_dim_type=",
            "cc_dim_orig=",
        )

    return True


def do_edit_notes(socket, tab_id, title):
    """Open the notes editor as an overlay on the target session's tab.

    Returns True on success.
    """
    editor = _resolve_notes_editor()
    filepath = notes_file_for_title(title)
    Path(filepath).touch()
    try:
        subprocess.Popen(
            [
                "kitty",
                "@",
                "--to",
                socket,
                "launch",
                "--type=overlay",
                "--match",
                f"id:{tab_id}",
                *shlex.split(editor),
                filepath,
            ],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
    except OSError:
        return False
    return True


def do_toggle_mute(socket, session):
    """Toggle the mute icon on a session's tab title.

    Returns True on success.
    """
    tab_id = session["tab_id"]
    win_id = session.get("claude_window_id", 0)

    result = kitty_cmd(socket, "ls")
    if not result or result.returncode != 0:
        return False
    try:
        data = json.loads(result.stdout)
    except json.JSONDecodeError:
        return False

    tab_title = ""
    fresh_cc_color = ""
    fresh_account = ""
    fresh_config_dir = ""
    for os_win in data:
        for tab in os_win.get("tabs", []):
            if tab.get("id") == tab_id:
                tab_title = tab.get("title", "")
                for win in tab.get("windows", []):
                    user_vars = win.get("user_vars", {})
                    if user_vars.get("cc_account") or user_vars.get("cc_color"):
                        fresh_cc_color = user_vars.get("cc_color", "")
                        fresh_account = user_vars.get("cc_account", "")
                        fresh_config_dir = user_vars.get("cc_config_dir", "")
                        break
                break

    if not tab_title:
        return False

    had_dashboard = tab_title.startswith(_ICON_DASHBOARD)
    if had_dashboard:
        tab_title = tab_title[len(_ICON_DASHBOARD) :]

    has_star = tab_title.startswith(_ICON_STAR)
    rest = tab_title[len(_ICON_STAR) :] if has_star else tab_title

    has_flag = rest.startswith(_ICON_FLAG)
    if has_flag:
        rest = rest[len(_ICON_FLAG) :]

    has_mute = rest.startswith(_ICON_MUTE)
    if has_mute:
        rest = rest[len(_ICON_MUTE) :]

    state_icon = ""
    for key in _ICONS["state_order"]:
        icon = _ICON_STATES[key]
        if rest.startswith(icon):
            state_icon = icon
            rest = rest[len(icon) :]
            break

    has_mute = not has_mute

    new_title = ""
    if had_dashboard:
        new_title += _ICON_DASHBOARD
    if has_star:
        new_title += _ICON_STAR
    if has_flag:
        new_title += _ICON_FLAG
    if has_mute:
        new_title += _ICON_MUTE
    new_title += state_icon + rest

    result = kitty_cmd(socket, "set-tab-title", "--match", f"id:{tab_id}", new_title)
    if not result or result.returncode != 0:
        return False

    if win_id:
        mute_val = "1" if has_mute else ""
        kitty_cmd(
            socket,
            "set-user-vars",
            "--match",
            f"id:{win_id}",
            f"cc_mute={mute_val}",
        )

    kitty_cmd(
        socket,
        "set-tab-color",
        "--match",
        f"id:{tab_id}",
        *_cfg._palette_tab_colors(
            fresh_account,
            color_override=fresh_cc_color,
            config_dir_override=fresh_config_dir,
        ),
    )

    return True


__all__ = [
    "do_edit_notes",
    "do_set_color",
    "do_toggle_mute",
]
