"""Core tab action helpers: focus/close/rename, clone naming, launch, placement."""

import contextlib
import json
import os
import re
import shlex
import subprocess
import time

from cc_dashboard import config as _cfg
from cc_dashboard.remote.command import kitty_cmd
from cc_dashboard.remote.shared import (
    _ICON_STAR,
    _MANAGED_TITLE_RE,
    _strip_account_prefix,
    _strip_icons,
    _strip_override_marker,
)


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
    while True:
        candidate = f"{base}-{n}"
        if candidate not in existing_titles:
            return candidate
        n += 1


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
    has_pinned = False
    for tab in tabs:
        tab_id = tab.get("id", 0)
        if exclude_tab_id is not None and tab_id == exclude_tab_id:
            continue
        tab_title = tab.get("title", "")
        stripped = _strip_icons(tab_title)
        pinned = tab_title.startswith(_ICON_STAR)
        if pinned:
            has_pinned = True

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

    if _cfg._default_sort_sessions != "alpha" and not has_pinned:
        return (None, None)

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
):
    """Create a new Claude session tab with the given title, directory, and account."""
    directory = os.path.expanduser(directory)

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
    ]

    home = os.path.expanduser("~")
    default_dir = os.path.join(home, ".claude")
    config_dir = (
        config_dir_override
        if config_dir_override
        else _cfg._accounts.get(account, {}).get("config_dir", "")
    )

    if config_dir and config_dir != default_dir:
        cmd += ["--env", f"CLAUDE_CONFIG_DIR={config_dir}"]

    if env_vars and env_vars != "-":
        for token in shlex.split(env_vars):
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
            f"title:^{title}$",
            *_cfg._palette_tab_colors(account),
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
            f"title:^{title}$",
            "--cwd",
            directory,
            "--keep-focus",
        )
        kitty_cmd(
            socket,
            "goto-layout",
            "--match",
            f"title:^{title}$",
            pw_mode,
        )

    return True
