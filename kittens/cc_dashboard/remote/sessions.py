"""Session discovery across kitty instances."""

import json
import os

from cc_dashboard import config as _cfg
from cc_dashboard.config_core import read_notes
from cc_dashboard.remote.activity import (
    cleanup_stale_activity,
    invalidate_tabid_cache,
    load_activity,
    load_session_meta,
    resolve_session_id,
)
from cc_dashboard.remote.command import get_all_kitty_sockets, kitty_cmd
from cc_dashboard.remote.git_ops import get_git_info_cached
from cc_dashboard.remote.shared import (
    _ICON_MUTE,
    _MANAGED_TITLE_RE,
    _detect_state,
    _strip_icons,
    _strip_override_marker,
)


def _is_claude_process(cmdline):
    """Check if a process cmdline represents the Claude Code CLI.

    Returns True for the actual Claude binary (e.g. ["claude", ...] or
    ["/path/to/claude", ...]) but False for kittens, scripts, or
    arguments that merely contain 'claude' as a substring (e.g.
    env vars like CLAUDE_CONFIG_DIR, paths containing 'claude', etc.).
    """
    for arg in cmdline:
        # Skip env-var-style arguments (e.g. CLAUDE_CONFIG_DIR=...)
        if "=" in arg:
            continue
        arg_lower = arg.lower()
        # Check the basename of the arg to match the actual binary
        basename = os.path.basename(arg_lower)
        if basename != "claude":
            continue
        if arg_lower.endswith(".py"):
            continue
        return True
    return False


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
    if not isinstance(data, list):
        return []

    sessions = []
    for os_win in data:
        os_focused = os_win.get("is_focused", False)
        for idx, tab in enumerate(os_win.get("tabs", [])):
            has_claude = False
            detected_config_dir = ""
            windows = tab.get("windows", [])
            cwd = ""
            user_vars: dict[str, str] = {}
            claude_win_id = 0

            pids = []
            cmdlines = []

            for win in windows:
                uv_raw = win.get("user_vars", {})
                uv = uv_raw if isinstance(uv_raw, dict) else {}
                if not user_vars and ("cc_account" in uv or "cc_cwd" in uv):
                    user_vars = {str(k): str(v) for k, v in uv.items()}
                for proc in win.get("foreground_processes", []):
                    cmdline = proc.get("cmdline", [])
                    cmdline_str = " ".join(cmdline)
                    pid = proc.get("pid")
                    if pid:
                        pids.append(pid)
                    if cmdline:
                        cmdlines.append(cmdline_str)
                    if _is_claude_process(cmdline):
                        has_claude = True
                        claude_win_id = win.get("id", 0)
                        if not cwd:
                            cwd = win.get("cwd", "")
                    for arg in cmdline:
                        if "CLAUDE_CONFIG_DIR=" in arg:
                            detected_config_dir = arg.split("=", 1)[1]

            if "cc_cwd" in user_vars:
                cwd = user_vars["cc_cwd"]
            elif not cwd and windows:
                cwd = windows[0].get("cwd", "")

            has_cc_var = bool(user_vars.get("cc_account"))

            if not has_claude and not has_cc_var:
                continue

            tab_title = tab.get("title", "")
            cc_mute = user_vars.get("cc_mute", "")
            muted = _ICON_MUTE in tab_title or bool(cc_mute)
            if (
                managed_only
                and not has_cc_var
                and not _MANAGED_TITLE_RE.match(tab_title)
            ):
                continue

            if claude_win_id == 0 and windows:
                claude_win_id = windows[0].get("id", 0)

            if "cc_account" in user_vars:
                account = user_vars["cc_account"]
            else:
                account = _cfg._resolve_account_by_config_dir(detected_config_dir)

            cc_flags = user_vars.get("cc_flags", "")
            cc_pw = user_vars.get("cc_pw", "")
            cc_env = user_vars.get("cc_env", "")
            cc_config_dir = user_vars.get("cc_config_dir", "")
            cc_color = user_vars.get("cc_color", "")
            cc_group = user_vars.get("cc_group", "")
            raw_cmds = user_vars.get("cc_commands", "")
            cc_commands = raw_cmds.split("\x1f") if raw_cmds else []

            title = _strip_override_marker(_strip_icons(tab.get("title", "")))

            # Fallback: enrich group from config when user var is missing
            if not cc_group:
                cfg_entry = _cfg._find_config_session(title)
                if cfg_entry:
                    cc_group = cfg_entry.get("group", "")
            try:
                cc_notes = read_notes(title)
            except (ValueError, OSError):
                cc_notes = ""
            icon_state = _detect_state(tab_title)
            tab_id = tab.get("id", 0)

            dimmed = user_vars.get("cc_dim_type") or None

            activity = load_activity(tab_id)
            session_id = resolve_session_id(tab_id)
            session_meta = load_session_meta(session_id)
            git_info = get_git_info_cached(cwd) if cwd else None
            sessions.append(
                {
                    "index": idx + 1,
                    "title": title,
                    "account": account,
                    "cwd": cwd,
                    "cc_flags": cc_flags,
                    "cc_pw": cc_pw,
                    "cc_env": cc_env,
                    "group": cc_group,
                    "cc_notes": cc_notes,
                    "cc_color": cc_color,
                    "git": git_info,
                    "model": session_meta.get("model", ""),
                    "agent_type": session_meta.get("agent_type", ""),
                    "status": (
                        "focused"
                        if (os_focused and tab.get("is_focused", False))
                        else icon_state
                        if icon_state
                        else "idle"
                    ),
                    "tab_id": tab_id,
                    "window_count": len(windows),
                    "pids": pids,
                    "cmdlines": cmdlines,
                    "config_dir": cc_config_dir or detected_config_dir,
                    "activity": activity,
                    "dimmed": dimmed,
                    "muted": muted,
                    "claude_window_id": claude_win_id,
                    "cc_commands": cc_commands,
                }
            )

    cleanup_stale_activity(s["tab_id"] for s in sessions)
    return sessions


def query_all_sessions(managed_only=True):
    """Query all Kitty instances for Claude Code sessions.

    Calls ``query_sessions`` for each active socket discovered by
    ``get_all_kitty_sockets`` and injects ``"socket"`` and
    ``"instance_label"`` keys into every session dict.
    """
    invalidate_tabid_cache()
    sockets = get_all_kitty_sockets()
    all_sessions = []
    for sock, label in sockets:
        sessions = query_sessions(sock, managed_only=managed_only)
        for s in sessions:
            s["socket"] = sock
            s["instance_label"] = label
        all_sessions.extend(sessions)
    return all_sessions
