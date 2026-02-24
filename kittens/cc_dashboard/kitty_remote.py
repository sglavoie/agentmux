"""Kitty remote control integration for the Claude Code Dashboard."""

import glob
import json
import os
import re
import subprocess
import time

from cc_dashboard import config as _cfg


def get_kitty_socket():
    """Discover the Kitty remote control socket."""
    sock = os.environ.get("KITTY_LISTEN_ON", "")
    if sock:
        return sock
    candidates = sorted(glob.glob("/tmp/kitty-*"), key=os.path.getmtime, reverse=True)
    return f"unix:{candidates[0]}" if candidates else ""


def kitty_cmd(socket, *args):
    """Run a kitty @ remote control command. Returns the CompletedProcess or None."""
    try:
        return subprocess.run(
            ["kitty", "@", "--to", socket] + list(args),
            capture_output=True,
            text=True,
            timeout=5,
        )
    except (subprocess.SubprocessError, OSError):
        return None


def _strip_icons(title):
    """Remove leading star and state icon prefixes from a tab title."""
    if title.startswith("★"):
        title = title[2:]
    for icon in ("⚡", "🛑", "🔑", "⚠️"):
        if title.startswith(icon):
            title = title[len(icon) :]
            break
    return title


def _detect_state(title):
    """Detect the state icon in a tab title, returning a status string.

    Returns one of: "active", "stopped", "permission", or "".
    The ★prefix is skipped before checking.
    """
    if title.startswith("★"):
        title = title[2:]
    if title.startswith("⚡"):
        return "active"
    if title.startswith("🛑"):
        return "stopped"
    if title.startswith("🔑"):
        return "permission"
    if title.startswith("⚠️"):
        return "error"
    return ""


_MANAGED_TITLE_RE = re.compile(r"^(★)?(⚡|🛑|🔑|⚠️)?[A-Z]:")


def query_sessions(socket, managed_only=True):
    """Query Kitty for tabs running Claude Code.

    When managed_only is True (the default), only tabs launched by ccm are
    returned — identified by the cc_account user variable or the standard
    X:title naming convention.
    """
    result = kitty_cmd(socket, "ls")
    if not result or result.returncode != 0:
        return []
    try:
        data = json.loads(result.stdout)
    except json.JSONDecodeError:
        return []

    home = os.path.expanduser("~")
    default_dir = os.path.join(home, ".claude")

    sessions = []
    for os_win in data:
        os_focused = os_win.get("is_focused", False)
        for idx, tab in enumerate(os_win.get("tabs", [])):
            has_claude = False
            detected_config_dir = ""
            windows = tab.get("windows", [])
            cwd = ""
            user_vars = {}

            pids = []
            cmdlines = []

            for win in windows:
                uv = win.get("user_vars", {})
                if not user_vars and ("cc_account" in uv or "cc_cwd" in uv):
                    user_vars = uv
                for proc in win.get("foreground_processes", []):
                    cmdline = proc.get("cmdline", [])
                    cmdline_str = " ".join(cmdline)
                    pid = proc.get("pid")
                    if pid:
                        pids.append(pid)
                    if cmdline:
                        cmdlines.append(cmdline_str)
                    if "claude" in cmdline_str.lower():
                        has_claude = True
                        if not cwd:
                            cwd = win.get("cwd", "")
                    for arg in cmdline:
                        if "CLAUDE_CONFIG_DIR=" in arg:
                            detected_config_dir = arg.split("=", 1)[1]

            # Prefer cc_cwd user_var (set at launch) over process cwd
            if "cc_cwd" in user_vars:
                cwd = user_vars["cc_cwd"]
            elif not cwd and windows:
                cwd = windows[0].get("cwd", "")

            has_cc_var = bool(user_vars.get("cc_account"))

            # Require either a running Claude process or ccm user vars.
            # Tabs launched by ccm have cc_account set immediately, but
            # Claude may not yet appear as a foreground process if the tab
            # was just created (e.g. after a restart).
            if not has_claude and not has_cc_var:
                continue

            # Filter to ccm-managed tabs when managed_only is set
            tab_title = tab.get("title", "")
            if (
                managed_only
                and not has_cc_var
                and not _MANAGED_TITLE_RE.match(tab_title)
            ):
                continue

            if "cc_account" in user_vars:
                account = user_vars["cc_account"]
            else:
                account = _cfg._resolve_account_by_config_dir(detected_config_dir)

            title = _strip_icons(tab.get("title", ""))
            icon_state = _detect_state(tab_title)
            sessions.append(
                {
                    "index": idx + 1,
                    "title": title,
                    "account": account,
                    "cwd": cwd,
                    "status": (
                        "focused"
                        if (os_focused and tab.get("is_focused", False))
                        else icon_state
                        if icon_state
                        else "idle"
                    ),
                    "tab_id": tab.get("id", 0),
                    "window_count": len(windows),
                    "pids": pids,
                    "cmdlines": cmdlines,
                    "config_dir": detected_config_dir,
                }
            )
    return sessions


def do_focus(socket, tab_id):
    """Focus a tab by its Kitty tab ID."""
    kitty_cmd(socket, "focus-tab", "--match", f"id:{tab_id}")


def do_close(socket, tab_id):
    """Close a tab by its Kitty tab ID."""
    kitty_cmd(socket, "close-tab", "--match", f"id:{tab_id}")


def do_launch(socket, title, directory, account, flags="", pw_override=""):
    """Create a new Claude session tab with the given title, directory, and account."""
    directory = os.path.expanduser(directory)
    cmd = [
        "launch",
        "--type=tab",
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
    ]

    home = os.path.expanduser("~")
    default_dir = os.path.join(home, ".claude")
    config_dir = _cfg._accounts.get(account, {}).get("config_dir", "")

    if config_dir and config_dir != default_dir:
        cmd += ["--env", f"CLAUDE_CONFIG_DIR={config_dir}"]

    shell = os.environ.get("SHELL", "/bin/zsh")
    effective_flags = _cfg._resolve_claude_flags(flags)
    shell_cmd = f"exec claude {effective_flags}" if effective_flags else "exec claude"
    cmd += [shell, "-lic", shell_cmd]

    kitty_cmd(socket, *cmd)

    # Retry set-tab-color a few times since Claude may briefly override
    # the tab title on startup, causing the title match to fail.
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


def do_restart(socket, session, flags="", pw_override=""):
    """Close and relaunch a session with the same title, directory, and account."""
    do_close(socket, session["tab_id"])
    time.sleep(0.3)
    do_launch(
        socket, session["title"], session["cwd"], session["account"], flags, pw_override
    )
