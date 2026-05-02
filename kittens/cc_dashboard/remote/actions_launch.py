"""Launch and placement helpers for managed Kitty tabs."""

import json
import os
import shlex
import time

from cc_dashboard import config as _cfg
from cc_dashboard.remote.command import kitty_cmd
from cc_dashboard.remote.shared import (
    _ICON_MUTE,
    _ICON_STAR,
    _MANAGED_TITLE_RE,
    _strip_account_prefix,
    _strip_icons,
    _strip_override_marker,
)


def _normalize_commands(raw_commands):
    """Return a sanitized list of command strings for launch operations."""
    if raw_commands is None:
        return []
    if isinstance(raw_commands, str):
        return raw_commands.split("\x1f") if raw_commands else []
    if not isinstance(raw_commands, list):
        return []
    result = []
    for command in raw_commands:
        if (
            isinstance(command, str)
            and "\t" not in command
            and "\n" not in command
            and "\r" not in command
        ):
            result.append(command)
    return result


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


def _sort_key_for_tab(pin_prefix, account, project_name, groups, use_group):
    """Build a sort key tuple, using group or account as the secondary key."""
    if use_group:
        first_group = sorted(groups, key=str.lower)[0].lower() if groups else "zzz"
        return (pin_prefix, first_group, project_name.lower())
    return (pin_prefix, account.lower(), project_name.lower())


def _parse_tab_groups(tab):
    """Extract parsed groups list from a tab's user vars."""
    for win in tab.get("windows", []):
        raw = win.get("user_vars", {}).get("cc_groups", "")
        if raw:
            return raw.split("\x1f")
    return []


def compute_insert_location(socket, account, title, exclude_tab_id=None, groups=None):
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

    use_group = _cfg._default_sort_sessions == "group"

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
            user_vars = win.get("user_vars", {})
            account_name = user_vars.get("cc_account", "")
            if account_name:
                cc_account = account_name
                break

        is_managed = bool(cc_account) or bool(_MANAGED_TITLE_RE.match(tab_title))
        if not is_managed:
            continue

        acct = _resolve_tab_account(tab_title, stripped, cc_account)
        project_name = _strip_account_prefix(_strip_override_marker(stripped))
        pin_prefix = 0 if pinned else 1
        tab_groups = _parse_tab_groups(tab) if use_group else []
        sort_key = _sort_key_for_tab(
            pin_prefix, acct, project_name, tab_groups, use_group
        )
        existing.append((sort_key, tab_id))

    existing.sort(key=lambda item: item[0])
    new_project = _strip_account_prefix(_strip_override_marker(title))
    new_key = _sort_key_for_tab(1, account, new_project, groups or [], use_group)

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
    groups=None,
    mute="",
    commands=None,
):
    """Create a new Claude session tab with the given title, directory, and account."""
    directory = os.path.expanduser(directory)
    commands = _normalize_commands(commands)

    if not os.path.isdir(directory):
        return False

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
        f"cc_groups={chr(0x1F).join(groups or [])}",
        "--var",
        f"cc_commands={chr(0x1F).join(commands)}",
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
    window_id = result.stdout.strip()

    for _ in range(5):
        result = kitty_cmd(
            socket,
            "set-tab-color",
            "--match",
            f"window_id:{window_id}",
            *_cfg._palette_tab_colors(
                account,
                color_override=color_override,
                config_dir_override=config_dir,
            ),
        )
        if result and result.returncode == 0:
            break
        time.sleep(0.1)

    if commands and commands != ["-"] and not pw_override:
        pw_override = "stack"

    pw_mode = _cfg._resolve_project_window(pw_override)
    if pw_mode:
        shell = os.environ.get("SHELL", "/bin/zsh")
        if commands == ["-"]:
            pass
        elif commands:
            for cmd_entry in commands:
                if cmd_entry == "shell":
                    kitty_cmd(
                        socket,
                        "launch",
                        "--type=window",
                        "--match",
                        f"window_id:{window_id}",
                        "--title",
                        "shell",
                        "--cwd",
                        directory,
                        "--keep-focus",
                    )
                elif cmd_entry:
                    kitty_cmd(
                        socket,
                        "launch",
                        "--type=window",
                        "--match",
                        f"window_id:{window_id}",
                        "--title",
                        cmd_entry,
                        "--cwd",
                        directory,
                        "--keep-focus",
                        "--hold",
                        "--",
                        shell,
                        "-lic",
                        cmd_entry,
                    )
        else:
            kitty_cmd(
                socket,
                "launch",
                "--type=window",
                "--match",
                f"window_id:{window_id}",
                "--cwd",
                directory,
                "--keep-focus",
            )
        kitty_cmd(
            socket,
            "goto-layout",
            "--match",
            f"window_id:{window_id}",
            pw_mode,
        )

    return True


def open_terminal(socket, directory, account, location=None, color_override=""):
    """Open a bare terminal tab in the given directory with account colors."""
    directory = os.path.expanduser(directory)
    if not os.path.isdir(directory):
        return False

    dir_name = os.path.basename(directory.rstrip("/"))
    initial = account[0].upper() if account else "?"
    title = f"{initial}:{dir_name}"

    loc_args = []
    if location:
        loc_type, loc_id = location
        if loc_type == "first":
            loc_args = ["--location", "first"]
        elif loc_type == "after" and loc_id is not None:
            kitty_cmd(socket, "focus-tab", "--match", f"id:{loc_id}")
            time.sleep(0.1)
            loc_args = ["--location", "after"]

    cmd = ["launch", "--type=tab", *loc_args, "--tab-title", title, "--cwd", directory]

    result = kitty_cmd(socket, *cmd)
    if not result or result.returncode != 0:
        return False
    window_id = result.stdout.strip()

    for _ in range(5):
        result = kitty_cmd(
            socket,
            "set-tab-color",
            "--match",
            f"window_id:{window_id}",
            *_cfg._palette_tab_colors(account, color_override=color_override),
        )
        if result and result.returncode == 0:
            break
        time.sleep(0.1)

    return True


__all__ = [
    "_normalize_commands",
    "_parse_tab_groups",
    "_sort_key_for_tab",
    "compute_insert_location",
    "do_launch",
    "open_terminal",
]
