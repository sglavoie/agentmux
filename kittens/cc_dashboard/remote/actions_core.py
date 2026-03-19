"""Core tab action helpers: focus/close/rename, clone naming, launch, placement."""

import contextlib
import json
import os
import re
import shlex
import subprocess
import time
from pathlib import Path

from cc_dashboard import config as _cfg
from cc_dashboard.config_core import _resolve_notes_editor, notes_file_for_title
from cc_dashboard.remote.command import kitty_cmd
from cc_dashboard.remote.shared import (
    _ICON_FLAG,
    _ICON_MUTE,
    _ICON_STAR,
    _ICON_STATES,
    _ICONS,
    _MANAGED_TITLE_RE,
    _strip_account_prefix,
    _strip_icons,
    _strip_override_marker,
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

    # Clear dim user vars when color is explicitly changed
    if win_id:
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
        # Intentionally fire-and-forget: Kitty manages the overlay process
        # lifecycle, so the subprocess exits promptly after launching the
        # overlay and does not need reaping by the dashboard.
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


def do_focus(socket, tab_id):
    """Focus a tab by its Kitty tab ID. Returns True on success."""
    result = kitty_cmd(socket, "focus-tab", "--match", f"id:{tab_id}")
    return result is not None and result.returncode == 0


def do_cross_instance_focus(socket, tab_id, home_socket):
    """Focus a tab and raise the OS window if it's in a different Kitty instance.

    When the target socket matches home_socket, this delegates to do_focus.
    When the target is a different instance, it additionally uses osascript
    to bring that instance's OS window to the foreground.
    """
    do_focus(socket, tab_id)
    if socket == home_socket:
        return
    bare = socket.removeprefix("unix:")
    base = os.path.basename(bare)
    m = re.match(r"kitty-(\d+)", base)
    if not m:
        return
    pid = m.group(1)
    with contextlib.suppress(subprocess.SubprocessError, OSError):
        subprocess.run(
            [
                "osascript",
                "-e",
                f'tell application "System Events" to set frontmost of '
                f"(first process whose unix id is {pid}) to true",
            ],
            capture_output=True,
            timeout=3,
        )


def do_close(socket, tab_id):
    """Close a tab by its Kitty tab ID. Returns True on success."""
    result = kitty_cmd(socket, "close-tab", "--match", f"id:{tab_id}")
    return result is not None and result.returncode == 0


def do_rename(socket, tab_id, new_title):
    """Rename a tab by its Kitty tab ID. Returns True on success."""
    result = kitty_cmd(socket, "set-tab-title", "--match", f"id:{tab_id}", new_title)
    return result is not None and result.returncode == 0


def generate_clone_title(sessions, original_title):
    """Generate a unique title for a cloned session.

    Strips the override marker and any trailing -N suffix to find the base
    title, then increments N until a unique title is found among existing
    sessions.
    """
    existing_titles = {s["title"] for s in sessions}
    stripped = _strip_override_marker(original_title)
    base = re.sub(r"-(\d+)$", "", stripped)
    n = 2
    max_clones = 100
    while n <= max_clones:
        candidate = f"{base}-{n}"
        if candidate not in existing_titles:
            return candidate
        n += 1
    return f"{base}-{n}"


def _resolve_tab_account(tab_title, stripped_title, cc_account):
    """Resolve an account name for a managed tab, using title fallback."""
    account = cc_account
    if not account and _MANAGED_TITLE_RE.match(tab_title):
        initial = stripped_title[0].upper() if stripped_title else ""
        for name in _cfg._account_names:
            if name and name[0].upper() == initial:
                account = name
                break
        if not account:
            account = "unknown"
    return account


def compute_insert_location(socket, account, title, exclude_tab_id=None):
    """Compute where to insert a new tab to maintain sorted order.

    Returns ("first", None) if the tab should be first,
    ("after", tab_id) if it should be placed after that tab,
    or (None, None) if sorting is disabled / no reordering needed.
    """
    result = kitty_cmd(socket, "ls")
    if not result or result.returncode != 0:
        return (None, None)
    try:
        data = json.loads(result.stdout)
    except json.JSONDecodeError:
        return (None, None)

    if not data:
        return (None, None)
    tabs = data[0].get("tabs", [])

    existing = []
    for tab in tabs:
        tab_id = tab.get("id", 0)
        if exclude_tab_id is not None and tab_id == exclude_tab_id:
            continue
        tab_title = tab.get("title", "")
        stripped = _strip_icons(tab_title)
        pinned = tab_title.startswith(_ICON_STAR)

        cc_account = ""
        for win in tab.get("windows", []):
            uv = win.get("user_vars", {})
            acc = uv.get("cc_account", "")
            if acc:
                cc_account = acc
                break

        is_managed = bool(cc_account) or bool(_MANAGED_TITLE_RE.match(tab_title))
        if not is_managed:
            continue

        acct = _resolve_tab_account(tab_title, stripped, cc_account)
        project_name = _strip_account_prefix(stripped)
        pin_prefix = 0 if pinned else 1
        sort_key = (pin_prefix, acct.lower(), project_name.lower())
        existing.append((sort_key, tab_id))

    existing.sort(key=lambda x: x[0])
    new_project = _strip_account_prefix(title)
    new_key = (1, account.lower(), new_project.lower())

    prev_id = None
    for sort_key, tab_id in existing:
        if sort_key <= new_key:
            prev_id = tab_id
        else:
            break

    if prev_id is None:
        return ("first", None)
    return ("after", prev_id)


def do_launch(
    socket,
    title,
    directory,
    account,
    flags="",
    pw_override="",
    location=None,
    config_dir_override=None,
    env_vars="",
    color_override="",
    group="",
    mute="",
):
    """Create a new Claude session tab with the given title, directory, and account."""
    directory = os.path.expanduser(directory)

    if not os.path.isdir(directory):
        return False

    # Prepend mute icon to title when launching a muted session
    if mute == "1" and _ICON_MUTE not in title:
        title = _ICON_MUTE + title

    loc_args = []
    if location:
        loc_type, loc_id = location
        if loc_type == "first":
            loc_args = ["--location", "first"]
        elif loc_type == "after" and loc_id is not None:
            kitty_cmd(socket, "focus-tab", "--match", f"id:{loc_id}")
            time.sleep(0.1)
            loc_args = ["--location", "after"]

    cmd = [
        "launch",
        "--type=tab",
        *loc_args,
        "--allow-remote-control",
        "--tab-title",
        title,
        "--cwd",
        directory,
        "--env",
        f"KITTY_LISTEN_ON={socket}",
        "--var",
        f"cc_account={account}",
        "--var",
        f"cc_cwd={directory}",
        "--var",
        f"cc_flags={flags}",
        "--var",
        f"cc_pw={pw_override}",
        "--var",
        f"cc_env={env_vars}",
        "--var",
        f"cc_group={group}",
    ]

    if color_override:
        cmd += ["--var", f"cc_color={color_override}"]

    if mute == "1":
        cmd += ["--var", "cc_mute=1"]

    home = os.path.expanduser("~")
    default_dir = os.path.join(home, ".claude")
    config_dir = (
        config_dir_override
        if config_dir_override
        else _cfg._accounts.get(account, {}).get("config_dir", "")
    )

    cmd += ["--var", f"cc_config_dir={config_dir}"]

    if config_dir and config_dir != default_dir:
        cmd += ["--env", f"CLAUDE_CONFIG_DIR={config_dir}"]

    if env_vars and env_vars != "-":
        try:
            env_tokens = shlex.split(env_vars)
        except ValueError:
            env_tokens = []
        for token in env_tokens:
            if "=" in token:
                cmd += ["--env", token]

    shell = os.environ.get("SHELL", "/bin/zsh")
    effective_flags = _cfg._resolve_claude_flags(flags)
    shell_cmd = f"exec claude {effective_flags}" if effective_flags else "exec claude"
    cmd += [shell, "-lic", shell_cmd]

    result = kitty_cmd(socket, *cmd)
    if not result or result.returncode != 0:
        return False

    for _ in range(5):
        result = kitty_cmd(
            socket,
            "set-tab-color",
            "--match",
            f"title:^{re.escape(title)}$",
            *_cfg._palette_tab_colors(
                account, color_override=color_override, config_dir_override=config_dir
            ),
        )
        if result and result.returncode == 0:
            break
        time.sleep(0.1)

    pw_mode = _cfg._resolve_project_window(pw_override)
    if pw_mode:
        kitty_cmd(
            socket,
            "launch",
            "--type=window",
            "--match",
            f"title:^{re.escape(title)}$",
            "--cwd",
            directory,
            "--keep-focus",
        )
        kitty_cmd(
            socket,
            "goto-layout",
            "--match",
            f"title:^{re.escape(title)}$",
            pw_mode,
        )

    return True


def do_toggle_mute(socket, session):
    """Toggle the mute icon on a session's tab title.

    Returns True on success.
    """
    tab_id = session["tab_id"]
    win_id = session.get("claude_window_id", 0)

    # Get current tab title from kitty
    result = kitty_cmd(socket, "ls")
    if not result or result.returncode != 0:
        return False
    try:
        data = json.loads(result.stdout)
    except json.JSONDecodeError:
        return False

    tab_title = ""
    for os_win in data:
        for tab in os_win.get("tabs", []):
            if tab.get("id") == tab_id:
                tab_title = tab.get("title", "")
                break

    if not tab_title:
        return False

    # Parse current icon state
    has_star = tab_title.startswith(_ICON_STAR)
    rest = tab_title
    if has_star:
        rest = rest[len(_ICON_STAR) :]

    has_flag = rest.startswith(_ICON_FLAG)
    if has_flag:
        rest = rest[len(_ICON_FLAG) :]

    has_mute = rest.startswith(_ICON_MUTE)
    if has_mute:
        rest = rest[len(_ICON_MUTE) :]

    # Detect and strip state icon
    state_icon = ""
    for key in _ICONS["state_order"]:
        icon = _ICON_STATES[key]
        if rest.startswith(icon):
            state_icon = icon
            rest = rest[len(icon) :]
            break

    # Toggle mute
    has_mute = not has_mute

    # Rebuild title: star? flag? mute? state? base
    new_title = ""
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

    # Persist mute state as user var
    if win_id:
        mute_val = "1" if has_mute else ""
        kitty_cmd(
            socket,
            "set-user-vars",
            "--match",
            f"id:{win_id}",
            f"cc_mute={mute_val}",
        )

    # Re-apply tab colors (title changes can lose color overrides)
    do_set_color(socket, session, session.get("cc_color", ""))

    return True
