"""Kitty remote control integration for the Claude Code Dashboard."""

import contextlib
import glob
import json
import os
import re
import shlex
import shutil
import subprocess
import time

from cc_dashboard import config as _cfg
from cc_dashboard.config import _resolve_data_path

# ── Icon loading from data/icons.json ────────────────────────────────────────


def _load_icons():
    """Load icon definitions from data/icons.json."""
    path = _resolve_data_path("icons.json")
    if not path:
        return {
            "star": "★",
            "states": {"bolt": "⚡", "stop": "🛑", "key": "🔑", "error": "⚠️"},
            "state_order": ["bolt", "stop", "key", "error"],
        }
    with open(path) as f:
        return json.load(f)


_ICONS = _load_icons()
_ICON_STAR = _ICONS["star"]
_ICON_FLAG = _ICONS.get("flag", "❗")
_ICON_STATES = _ICONS["states"]
_STATE_NAMES = {
    "bolt": "active",
    "stop": "stopped",
    "key": "permission",
    "error": "error",
}

# Build the managed-title regex dynamically from loaded icons
_state_icons_alt = "|".join(re.escape(_ICON_STATES[k]) for k in _ICONS["state_order"])
_MANAGED_TITLE_RE = re.compile(
    r"^("
    + re.escape(_ICON_STAR)
    + r")?("
    + re.escape(_ICON_FLAG)
    + r")?("
    + _state_icons_alt
    + r")?[A-Z]:"
)


def get_kitty_socket():
    """Discover the Kitty remote control socket."""
    sock = os.environ.get("KITTY_LISTEN_ON", "")
    if sock:
        return sock
    candidates = sorted(glob.glob("/tmp/kitty-*"), key=os.path.getmtime, reverse=True)
    return f"unix:{candidates[0]}" if candidates else ""


def _socket_label(socket_path):
    """Derive a short instance label from a socket path.

    Extracts the PID from filenames like /tmp/kitty-12345-... and returns
    "K:12345".  Falls back to the full socket path when the pattern doesn't
    match.
    """
    # socket_path is typically "unix:/tmp/kitty-PID-..." or just "/tmp/kitty-PID-..."
    bare = socket_path.removeprefix("unix:")
    base = os.path.basename(bare)
    m = re.match(r"kitty-(\d+)", base)
    if m:
        return f"K:{m.group(1)}"
    return socket_path


def get_all_kitty_sockets():
    """Discover all active Kitty remote control sockets.

    Collects all /tmp/kitty-* sockets plus KITTY_LISTEN_ON, probes each
    with ``kitty @ ls`` (2-second timeout), and returns a list of
    ``(socket, label)`` tuples for the ones that respond.
    """
    seen = set()
    candidates = []

    # KITTY_LISTEN_ON first (the "home" instance)
    env_sock = os.environ.get("KITTY_LISTEN_ON", "")
    if env_sock:
        candidates.append(env_sock)
        seen.add(env_sock)

    # All /tmp/kitty-* sockets, most recent first
    for path in sorted(glob.glob("/tmp/kitty-*"), key=os.path.getmtime, reverse=True):
        sock = f"unix:{path}"
        if sock not in seen:
            candidates.append(sock)
            seen.add(sock)

    active = []
    for sock in candidates:
        try:
            result = subprocess.run(
                ["kitty", "@", "--to", sock, "ls"],
                capture_output=True,
                text=True,
                timeout=2,
            )
            if result.returncode == 0:
                active.append((sock, _socket_label(sock)))
        except (subprocess.SubprocessError, OSError):
            pass

    return active


def get_host_tab_id(socket):
    """Return the tab ID of the tab hosting the current dashboard process.

    Reads KITTY_WINDOW_ID (set by Kitty for all child processes) and maps it
    to a tab ID by walking the kitty @ ls output.  Returns None when the
    window ID is unavailable or the lookup fails.
    """
    wid_str = os.environ.get("KITTY_WINDOW_ID")
    if not wid_str:
        return None
    try:
        wid = int(wid_str)
    except ValueError:
        return None
    result = kitty_cmd(socket, "ls")
    if not result or result.returncode != 0:
        return None
    try:
        data = json.loads(result.stdout)
    except json.JSONDecodeError:
        return None
    for os_win in data:
        for tab in os_win.get("tabs", []):
            for win in tab.get("windows", []):
                if win.get("id") == wid:
                    return tab.get("id")
    return None


_DEBUG_LOG = os.environ.get("CCM_DEBUG_LOG", "")


def kitty_cmd(socket, *args):
    """Run a kitty @ remote control command. Returns the CompletedProcess or None."""
    try:
        result = subprocess.run(
            ["kitty", "@", "--to", socket, *list(args)],
            capture_output=True,
            text=True,
            timeout=5,
        )
        if _DEBUG_LOG and result.returncode != 0:
            with open(_DEBUG_LOG, "a") as f:
                f.write(
                    f"FAIL rc={result.returncode} cmd={list(args)}\n"
                    f"  stdout={result.stdout!r}\n"
                    f"  stderr={result.stderr!r}\n"
                )
        return result
    except (subprocess.SubprocessError, OSError) as exc:
        if _DEBUG_LOG:
            with open(_DEBUG_LOG, "a") as f:
                f.write(f"EXCEPTION cmd={list(args)} exc={exc}\n")
        return None


def get_window_text(socket, window_id, extent="screen"):
    """Capture text from a Kitty window. Returns text string or None."""
    result = kitty_cmd(socket, "get-text", "--extent", extent,
                       "--match", f"id:{window_id}")
    if result and result.returncode == 0:
        return result.stdout
    return None


def _strip_icons(title):
    """Remove leading star, flag, and state icon prefixes from a tab title."""
    if title.startswith(_ICON_STAR):
        title = title[len(_ICON_STAR) :]
    if title.startswith(_ICON_FLAG):
        title = title[len(_ICON_FLAG) :]
    for key in _ICONS["state_order"]:
        icon = _ICON_STATES[key]
        if title.startswith(icon):
            title = title[len(icon) :]
            break
    return title


def _strip_account_prefix(title):
    """Remove the leading 'X:' account initial prefix from a stripped title.

    E.g. 'P:ccm' -> 'ccm', 'T:Excel' -> 'Excel'. Titles without the prefix
    are returned as-is.
    """
    if len(title) >= 2 and title[0].isupper() and title[1] == ":":
        return title[2:]
    return title


OVERRIDE_MARKER = "\u203e"  # U+203E OVERLINE


def _strip_override_marker(title):
    """Remove trailing override marker(s) from a title."""
    return title.rstrip(OVERRIDE_MARKER)


def _append_override_marker(title):
    """Append a single override marker to a title (idempotent)."""
    return _strip_override_marker(title) + OVERRIDE_MARKER


def _detect_state(title):
    """Detect the state icon in a tab title, returning a status string.

    Returns one of: "active", "stopped", "permission", "error", or "".
    The star and flag prefixes are skipped before checking.
    """
    if title.startswith(_ICON_STAR):
        title = title[len(_ICON_STAR) :]
    if title.startswith(_ICON_FLAG):
        title = title[len(_ICON_FLAG) :]
    for key in _ICONS["state_order"]:
        if title.startswith(_ICON_STATES[key]):
            return _STATE_NAMES[key]
    return ""


def _get_git_info(cwd):
    """Return git branch and dirty state for a directory, or None if not a git repo.

    Returns {"branch": str, "dirty": bool} on success, None otherwise.
    Each git command has a 2-second timeout to avoid stalling on slow filesystems.
    """
    try:
        result = subprocess.run(
            ["git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD"],
            capture_output=True,
            text=True,
            timeout=2,
        )
        if result.returncode != 0:
            return None
        branch = result.stdout.strip()
    except (subprocess.SubprocessError, OSError):
        return None

    dirty = False
    try:
        result = subprocess.run(
            ["git", "-C", cwd, "status", "--porcelain"],
            capture_output=True,
            text=True,
            timeout=2,
        )
        if result.returncode == 0 and result.stdout.strip():
            dirty = True
    except (subprocess.SubprocessError, OSError):
        pass

    return {"branch": branch, "dirty": dirty}


def do_create_worktree(cwd, branch_name):
    """Create a git worktree for the given branch.

    Returns a dict on success: {"worktree_path": str, "project_name": str}
    Returns a string error message on failure.
    """
    try:
        result = subprocess.run(
            ["git", "-C", cwd, "rev-parse", "--show-toplevel"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        if result.returncode != 0:
            return "Could not determine git root"
        repo_root = result.stdout.strip()
    except (subprocess.SubprocessError, OSError):
        return "Could not determine git root"

    project_name = os.path.basename(repo_root)
    worktree_path = os.path.normpath(
        os.path.join(repo_root, "..", f"{project_name}-{branch_name}")
    )

    branch_exists = False
    try:
        result = subprocess.run(
            ["git", "-C", repo_root, "rev-parse", "--verify", branch_name],
            capture_output=True,
            text=True,
            timeout=30,
        )
        if result.returncode == 0:
            branch_exists = True
    except (subprocess.SubprocessError, OSError):
        pass

    if not branch_exists:
        try:
            result = subprocess.run(
                [
                    "git",
                    "-C",
                    repo_root,
                    "rev-parse",
                    "--verify",
                    f"origin/{branch_name}",
                ],
                capture_output=True,
                text=True,
                timeout=30,
            )
            if result.returncode == 0:
                branch_exists = True
        except (subprocess.SubprocessError, OSError):
            pass

    if branch_exists:
        cmd = ["git", "-C", repo_root, "worktree", "add", worktree_path, branch_name]
    else:
        cmd = [
            "git",
            "-C",
            repo_root,
            "worktree",
            "add",
            "-b",
            branch_name,
            worktree_path,
        ]

    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
    except (subprocess.SubprocessError, OSError) as exc:
        return str(exc)

    if result.returncode != 0:
        stderr_lines = result.stderr.strip().splitlines()
        # Prefer the last "fatal:" line over progress messages like "Preparing worktree"
        for line in reversed(stderr_lines):
            if line.startswith("fatal:"):
                return line
        return stderr_lines[-1] if stderr_lines else "git worktree add failed"

    return {"worktree_path": worktree_path, "project_name": project_name}


def detect_worktree(cwd):
    """Check if a directory is a git worktree (not a normal repo or non-git dir).

    Returns {"is_worktree": bool, "main_repo": str} where main_repo is the
    git common dir path (needed for `git worktree remove`).
    """
    try:
        common = subprocess.run(
            ["git", "-C", cwd, "rev-parse", "--git-common-dir"],
            capture_output=True, text=True, timeout=10,
        )
        gitdir = subprocess.run(
            ["git", "-C", cwd, "rev-parse", "--git-dir"],
            capture_output=True, text=True, timeout=10,
        )
    except (subprocess.SubprocessError, OSError):
        return {"is_worktree": False, "main_repo": ""}

    if common.returncode != 0 or gitdir.returncode != 0:
        return {"is_worktree": False, "main_repo": ""}

    common_dir = os.path.realpath(os.path.join(cwd, common.stdout.strip()))
    git_dir = os.path.realpath(os.path.join(cwd, gitdir.stdout.strip()))

    if common_dir != git_dir:
        # main_repo is the working tree that contains the common .git dir
        main_repo = os.path.dirname(common_dir)
        return {"is_worktree": True, "main_repo": main_repo}

    return {"is_worktree": False, "main_repo": ""}


def do_remove_worktree(main_repo, worktree_path):
    """Remove a git worktree cleanly. Returns None on success or error string."""
    try:
        result = subprocess.run(
            ["git", "-C", main_repo, "worktree", "remove", worktree_path],
            capture_output=True, text=True, timeout=30,
        )
    except (subprocess.SubprocessError, OSError) as exc:
        return str(exc)

    if result.returncode != 0:
        stderr = result.stderr.strip()
        return stderr if stderr else "git worktree remove failed"
    return None


def format_duration(seconds):
    """Format a duration in seconds into a compact human-readable string."""
    seconds = max(0, int(seconds))
    if seconds < 60:
        return f"{seconds}s"
    minutes = seconds // 60
    if minutes < 60:
        return f"{minutes}m"
    hours = minutes // 60
    remaining_min = minutes % 60
    if remaining_min:
        return f"{hours}h {remaining_min}m"
    return f"{hours}h"


def load_activity(tab_id):
    """Read and parse the activity counts file for a tab, returning a dict or None."""
    path = f"/tmp/ccm-activity-{tab_id}.counts"
    try:
        with open(path) as f:
            lines = f.read().splitlines()
    except OSError:
        return None
    if len(lines) < 9:
        return None
    try:
        return {
            "first_event_ts": int(lines[0]),
            "last_event_ts": int(lines[1]),
            "last_event_type": lines[2],
            "prompt_count": int(lines[3]),
            "tool_use_count": int(lines[4]),
            "tool_failure_count": int(lines[5]),
            "permission_count": int(lines[6]),
            "stop_count": int(lines[7]),
            "last_attention_ts": int(lines[8]),
        }
    except (ValueError, IndexError):
        return None


def cleanup_stale_activity(live_tab_ids):
    """Remove orphaned activity files whose tab_id is not in the live set."""
    live = set(str(tid) for tid in live_tab_ids)
    for path in glob.glob("/tmp/ccm-activity-*.counts"):
        fname = os.path.basename(path)
        # Extract tab_id from "ccm-activity-<tab_id>.counts"
        tab_id_str = fname.replace("ccm-activity-", "").replace(".counts", "")
        if tab_id_str not in live:
            with contextlib.suppress(OSError):
                os.remove(path)


def _is_claude_process(cmdline):
    """Check if a process cmdline represents the Claude Code CLI.

    Returns True for the actual Claude binary (e.g. ["claude", ...] or
    ["/path/to/claude", ...]) but False for kittens or scripts that
    merely contain 'claude' in their filename (e.g. claude_dashboard.py,
    cc_quick_switch.py).
    """
    for arg in cmdline:
        arg_lower = arg.lower()
        if "claude" not in arg_lower:
            continue
        # Skip Python/kitten scripts that happen to contain "claude"
        if arg_lower.endswith(".py"):
            continue
        # Skip kitty kitten invocations (e.g. "kitten claude_dashboard.py")
        if arg_lower == "kitten" or arg_lower.endswith("/kitten"):
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

    home = os.path.expanduser("~")
    os.path.join(home, ".claude")

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
                    if _is_claude_process(cmdline):
                        has_claude = True
                        claude_win_id = win.get("id", 0)
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

            if claude_win_id == 0 and windows:
                claude_win_id = windows[0].get("id", 0)

            if "cc_account" in user_vars:
                account = user_vars["cc_account"]
            else:
                account = _cfg._resolve_account_by_config_dir(detected_config_dir)

            title = _strip_icons(tab.get("title", ""))
            icon_state = _detect_state(tab_title)
            tab_id = tab.get("id", 0)
            activity = load_activity(tab_id)
            git_info = _get_git_info(cwd) if cwd else None
            sessions.append(
                {
                    "index": idx + 1,
                    "title": title,
                    "account": account,
                    "cwd": cwd,
                    "git": git_info,
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
                    "config_dir": detected_config_dir,
                    "activity": activity,
                    "claude_window_id": claude_win_id,
                }
            )

    cleanup_stale_activity(s["tab_id"] for s in sessions)
    return sessions


def query_all_sessions(managed_only=True):
    """Query all Kitty instances for Claude Code sessions.

    Calls ``query_sessions`` for each active socket discovered by
    ``get_all_kitty_sockets`` and injects ``"socket"`` and
    ``"instance_label"`` keys into every session dict.  The ``(socket,
    tab_id)`` pair serves as the globally unique identifier since tab IDs
    alone can collide across Kitty processes.
    """
    sockets = get_all_kitty_sockets()
    all_sessions = []
    for sock, label in sockets:
        sessions = query_sessions(sock, managed_only=managed_only)
        for s in sessions:
            s["socket"] = sock
            s["instance_label"] = label
        all_sessions.extend(sessions)
    return all_sessions


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
    # Extract PID from the socket path (e.g. "unix:/tmp/kitty-12345-...")
    bare = socket.removeprefix("unix:")
    base = os.path.basename(bare)
    m = re.match(r"kitty-(\d+)", base)
    if not m:
        return
    pid = m.group(1)
    try:
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
    except (subprocess.SubprocessError, OSError):
        pass


def do_close(socket, tab_id):
    """Close a tab by its Kitty tab ID. Returns True on success."""
    result = kitty_cmd(socket, "close-tab", "--match", f"id:{tab_id}")
    return result is not None and result.returncode == 0


def do_rename(socket, tab_id, new_title):
    """Rename a tab by its Kitty tab ID. Returns True on success."""
    result = kitty_cmd(socket, "set-tab-title", "--match", f"id:{tab_id}", new_title)
    return result is not None and result.returncode == 0


def compute_insert_location(socket, account, title, exclude_tab_id=None):
    """Compute where to insert a new tab to maintain sorted order.

    Returns ("first", None) if the tab should be first,
    ("after", tab_id) if it should be placed after that tab,
    or (None, None) if sorting is disabled / no reordering needed.

    exclude_tab_id: if provided, that tab is ignored when building the sorted
    list.  Used when pre-computing the location before a tab has been closed
    (e.g. the host tab restart path).
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

    # Collect existing managed tabs with their sort keys
    existing = []  # (sort_key, tab_id)
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

        acct = cc_account
        if not acct and _MANAGED_TITLE_RE.match(tab_title):
            initial = stripped[0].upper() if stripped else ""
            for name in _cfg._account_names:
                if name and name[0].upper() == initial:
                    acct = name
                    break
            if not acct:
                acct = "unknown"

        project_name = _strip_account_prefix(stripped)
        pin_prefix = 0 if pinned else 1
        sort_key = (pin_prefix, acct.lower(), project_name.lower())
        existing.append((sort_key, tab_id))

    # When sort is not alpha and no pinned tabs exist, no insertion guidance
    if _cfg._default_sort_sessions != "alpha" and not has_pinned:
        return (None, None)

    # Sort existing and find where the new tab fits
    existing.sort(key=lambda x: x[0])
    new_project = _strip_account_prefix(title)
    # New tabs are never pinned, so pin_prefix is always 1
    new_key = (1, account.lower(), new_project.lower())

    # Find the last existing entry whose key is <= new_key
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
):
    """Create a new Claude session tab with the given title, directory, and account."""
    directory = os.path.expanduser(directory)

    # Handle positional insertion
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

    shell = os.environ.get("SHELL", "/bin/zsh")
    effective_flags = _cfg._resolve_claude_flags(flags)
    shell_cmd = f"exec claude {effective_flags}" if effective_flags else "exec claude"
    cmd += [shell, "-lic", shell_cmd]

    result = kitty_cmd(socket, *cmd)
    if not result or result.returncode != 0:
        return False

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

    return True


def _spawn_detached_close(socket, tab_id):
    """Spawn a detached subprocess to close a tab after the dashboard exits.

    Used when the tab being closed is the dashboard's own host tab.
    A short delay lets the dashboard's return clean up curses before the tab
    is destroyed.
    """
    steps = [
        {
            "cmd": [
                "kitty",
                "@",
                "--to",
                socket,
                "close-tab",
                "--match",
                f"id:{tab_id}",
            ],
            "sleep": 0.3,
        }
    ]

    script = """
import json, os, subprocess, time
steps = json.loads(os.environ["_CCM_STEPS"])
for step in steps:
    cmd = step["cmd"]
    sleep_after = step.get("sleep", 0)
    if sleep_after:
        time.sleep(sleep_after)
    subprocess.run(cmd, capture_output=True)
"""
    env = os.environ.copy()
    env["_CCM_STEPS"] = json.dumps(steps)

    python = shutil.which("python3") or shutil.which("python")
    if not python:
        return
    subprocess.Popen(
        [python, "-c", script],
        start_new_session=True,
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )


def _spawn_detached_restart(
    socket,
    tab_id,
    title,
    cwd,
    account,
    flags="",
    pw_override="",
    config_dir_override=None,
):
    """Spawn a fully detached subprocess to close+relaunch a session.

    Used when the session being restarted is the dashboard's own host tab.
    Because closing that tab destroys the dashboard overlay, the close+launch
    sequence must run in a separate process that outlives the dashboard.

    All kitty @ command arguments are built here in the main process (mirroring
    do_launch exactly) so the detached script needs only the stdlib.
    Each step is {"cmd": [...], "sleep": float, "retry": int} where retry > 0
    means retry up to that many extra times (sleeping 0.1s between) until the
    command returns rc 0.
    """
    directory = os.path.expanduser(cwd)
    home = os.path.expanduser("~")
    default_dir = os.path.join(home, ".claude")
    config_dir = (
        config_dir_override
        if config_dir_override
        else _cfg._accounts.get(account, {}).get("config_dir", "")
    )
    shell = os.environ.get("SHELL", "/bin/zsh")
    effective_flags = _cfg._resolve_claude_flags(flags)
    shell_cmd = f"exec claude {effective_flags}" if effective_flags else "exec claude"

    # Pre-compute sort location excluding the closing tab
    location = compute_insert_location(socket, account, title, exclude_tab_id=tab_id)
    loc_type, loc_id = location
    loc_args = []

    steps = []

    # 1. Close the host tab
    steps.append(
        {
            "cmd": [
                "kitty",
                "@",
                "--to",
                socket,
                "close-tab",
                "--match",
                f"id:{tab_id}",
            ],
            "sleep": 0.5,
        }
    )

    # 2. Focus neighbour tab for sorted insertion (mirrors do_launch)
    if loc_type == "first":
        loc_args = ["--location", "first"]
    elif loc_type == "after" and loc_id is not None:
        steps.append(
            {
                "cmd": [
                    "kitty",
                    "@",
                    "--to",
                    socket,
                    "focus-tab",
                    "--match",
                    f"id:{loc_id}",
                ],
                "sleep": 0.1,
            }
        )
        loc_args = ["--location", "after"]

    # 3. Launch the tab
    launch_cmd = [
        "kitty",
        "@",
        "--to",
        socket,
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
    ]
    if config_dir and config_dir != default_dir:
        launch_cmd += ["--env", f"CLAUDE_CONFIG_DIR={config_dir}"]
    launch_cmd += [shell, "-lic", shell_cmd]
    steps.append({"cmd": launch_cmd})

    # 4. Set tab colour (with retries, mirrors do_launch)
    steps.append(
        {
            "cmd": [
                "kitty",
                "@",
                "--to",
                socket,
                "set-tab-color",
                "--match",
                f"title:^{title}$",
                *_cfg._palette_tab_colors(account),
            ],
            "retry": 5,
            "retry_sleep": 0.1,
        }
    )

    # 5. Project window (mirrors do_launch pw_mode block)
    pw_mode = _cfg._resolve_project_window(pw_override)
    if pw_mode:
        steps.append(
            {
                "cmd": [
                    "kitty",
                    "@",
                    "--to",
                    socket,
                    "launch",
                    "--type=window",
                    "--match",
                    f"title:^{title}$",
                    "--cwd",
                    directory,
                    "--keep-focus",
                ]
            }
        )
        steps.append(
            {
                "cmd": [
                    "kitty",
                    "@",
                    "--to",
                    socket,
                    "goto-layout",
                    "--match",
                    f"title:^{title}$",
                    pw_mode,
                ]
            }
        )

    # ── Minimal detached script — stdlib only ────────────────────────────────
    script = """
import json, os, subprocess, time
steps = json.loads(os.environ["_CCM_STEPS"])
for step in steps:
    cmd = step["cmd"]
    retry = step.get("retry", 0)
    retry_sleep = step.get("retry_sleep", 0.1)
    sleep_after = step.get("sleep", 0)
    if retry:
        for _ in range(retry + 1):
            r = subprocess.run(cmd, capture_output=True)
            if r.returncode == 0:
                break
            time.sleep(retry_sleep)
    else:
        subprocess.run(cmd, capture_output=True)
    if sleep_after:
        time.sleep(sleep_after)
"""
    env = os.environ.copy()
    env["_CCM_STEPS"] = json.dumps(steps)

    # sys.executable inside a Kitty kitten is the kitty binary itself, not
    # Python.  Locate the real python3 on PATH instead.
    python = shutil.which("python3") or shutil.which("python")
    if not python:
        return  # no Python found — cannot spawn detached process
    subprocess.Popen(
        [python, "-c", script],
        start_new_session=True,
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )


def do_restart(
    socket,
    session,
    flags="",
    pw_override="",
    config_dir_override=None,
    host_tab_id=None,
):
    """Close and relaunch a session with the same title, directory, and account.

    When config_dir_override is provided, the session is relaunched with a
    different CLAUDE_CONFIG_DIR, but the tab keeps its original account,
    color, and title prefix so it stays in its visual group.  An overline
    marker is appended to signal the override.

    When host_tab_id matches the target tab_id, a detached subprocess is used
    so the relaunch survives the dashboard overlay being destroyed along with
    the host tab.
    """
    account = session["account"]
    title = session["title"]

    if config_dir_override:
        title = _append_override_marker(title)

    tab_id = session["tab_id"]
    cwd = session["cwd"]

    if host_tab_id is not None and tab_id == host_tab_id:
        _spawn_detached_restart(
            socket, tab_id, title, cwd, account, flags, pw_override, config_dir_override
        )
        return True

    ok = do_close(socket, tab_id)
    if not ok:
        return False
    time.sleep(0.3)
    location = compute_insert_location(socket, account, title)
    return do_launch(
        socket,
        title,
        cwd,
        account,
        flags,
        pw_override,
        location=location,
        config_dir_override=config_dir_override,
    )


def _spawn_detached_move(
    source_socket,
    target_socket,
    tab_id,
    title,
    cwd,
    account,
    flags="",
    pw_override="",
    config_dir_override=None,
):
    """Spawn a detached subprocess to close a session on one instance and relaunch on another.

    Used when the session being moved is the dashboard's own host tab.
    Mirrors _spawn_detached_restart but targets a different socket for the
    relaunch step.
    """
    directory = os.path.expanduser(cwd)
    home = os.path.expanduser("~")
    default_dir = os.path.join(home, ".claude")
    config_dir = (
        config_dir_override
        if config_dir_override
        else _cfg._accounts.get(account, {}).get("config_dir", "")
    )
    shell = os.environ.get("SHELL", "/bin/zsh")
    effective_flags = _cfg._resolve_claude_flags(flags)
    shell_cmd = f"exec claude {effective_flags}" if effective_flags else "exec claude"

    # Compute insert location on the target instance
    location = compute_insert_location(target_socket, account, title)
    loc_type, loc_id = location
    loc_args = []

    steps = []

    # 1. Close the tab on the source instance
    steps.append(
        {
            "cmd": [
                "kitty",
                "@",
                "--to",
                source_socket,
                "close-tab",
                "--match",
                f"id:{tab_id}",
            ],
            "sleep": 0.5,
        }
    )

    # 2. Focus neighbour tab for sorted insertion on the target
    if loc_type == "first":
        loc_args = ["--location", "first"]
    elif loc_type == "after" and loc_id is not None:
        steps.append(
            {
                "cmd": [
                    "kitty",
                    "@",
                    "--to",
                    target_socket,
                    "focus-tab",
                    "--match",
                    f"id:{loc_id}",
                ],
                "sleep": 0.1,
            }
        )
        loc_args = ["--location", "after"]

    # 3. Launch the tab on the target instance
    launch_cmd = [
        "kitty",
        "@",
        "--to",
        target_socket,
        "launch",
        "--type=tab",
        *loc_args,
        "--allow-remote-control",
        "--tab-title",
        title,
        "--cwd",
        directory,
        "--env",
        f"KITTY_LISTEN_ON={target_socket}",
        "--var",
        f"cc_account={account}",
        "--var",
        f"cc_cwd={directory}",
    ]
    if config_dir and config_dir != default_dir:
        launch_cmd += ["--env", f"CLAUDE_CONFIG_DIR={config_dir}"]
    launch_cmd += [shell, "-lic", shell_cmd]
    steps.append({"cmd": launch_cmd})

    # 4. Set tab colour (with retries)
    steps.append(
        {
            "cmd": [
                "kitty",
                "@",
                "--to",
                target_socket,
                "set-tab-color",
                "--match",
                f"title:^{title}$",
                *_cfg._palette_tab_colors(account),
            ],
            "retry": 5,
            "retry_sleep": 0.1,
        }
    )

    # 5. Project window (if configured)
    pw_mode = _cfg._resolve_project_window(pw_override)
    if pw_mode:
        steps.append(
            {
                "cmd": [
                    "kitty",
                    "@",
                    "--to",
                    target_socket,
                    "launch",
                    "--type=window",
                    "--match",
                    f"title:^{title}$",
                    "--cwd",
                    directory,
                    "--keep-focus",
                ]
            }
        )
        steps.append(
            {
                "cmd": [
                    "kitty",
                    "@",
                    "--to",
                    target_socket,
                    "goto-layout",
                    "--match",
                    f"title:^{title}$",
                    pw_mode,
                ]
            }
        )

    script = """
import json, os, subprocess, time
steps = json.loads(os.environ["_CCM_STEPS"])
for step in steps:
    cmd = step["cmd"]
    retry = step.get("retry", 0)
    retry_sleep = step.get("retry_sleep", 0.1)
    sleep_after = step.get("sleep", 0)
    if retry:
        for _ in range(retry + 1):
            r = subprocess.run(cmd, capture_output=True)
            if r.returncode == 0:
                break
            time.sleep(retry_sleep)
    else:
        subprocess.run(cmd, capture_output=True)
    if sleep_after:
        time.sleep(sleep_after)
"""
    env = os.environ.copy()
    env["_CCM_STEPS"] = json.dumps(steps)

    python = shutil.which("python3") or shutil.which("python")
    if not python:
        return
    subprocess.Popen(
        [python, "-c", script],
        start_new_session=True,
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )


def do_move(
    source_socket,
    target_socket,
    session,
    flags="",
    pw_override="",
    config_dir_override=None,
    host_tab_id=None,
):
    """Move a session from one Kitty instance to another (close + relaunch).

    Preserves the session's title, cwd, account, and metadata.  When
    host_tab_id matches the session's tab_id, a detached subprocess is
    used so the move survives the dashboard overlay being destroyed.
    """
    account = session["account"]
    title = session["title"]
    tab_id = session["tab_id"]
    cwd = session["cwd"]

    if host_tab_id is not None and tab_id == host_tab_id:
        _spawn_detached_move(
            source_socket,
            target_socket,
            tab_id,
            title,
            cwd,
            account,
            flags,
            pw_override,
            config_dir_override,
        )
        return True

    ok = do_close(source_socket, tab_id)
    if not ok:
        return False
    time.sleep(0.3)
    location = compute_insert_location(target_socket, account, title)
    return do_launch(
        target_socket,
        title,
        cwd,
        account,
        flags,
        pw_override,
        location=location,
        config_dir_override=config_dir_override,
    )


def do_sort_tabs(socket):
    """Sort managed tabs by account then title, pushing non-managed tabs to the end.

    Pinned (starred) tabs always sort to the front, alphabetically among
    themselves.  When sort_sessions is "alpha", unpinned managed tabs are also
    sorted alphabetically after the pinned group.  When sort_sessions is not
    "alpha", unpinned managed tabs retain their current relative order, and the
    function returns early if no pinned tabs exist.
    Uses kitty @ focus-tab + action move_tab_forward/backward to reorder.
    """
    result = kitty_cmd(socket, "ls")
    if not result or result.returncode != 0:
        return
    try:
        data = json.loads(result.stdout)
    except json.JSONDecodeError:
        return

    if not data:
        return
    tabs = data[0].get("tabs", [])
    if len(tabs) <= 1:
        return

    # Build tab info list with classification
    managed = []  # (sort_key, position, tab_id, is_pinned)
    unmanaged = []  # (position, tab_id)
    focused_id = None
    has_pinned = False

    for pos, tab in enumerate(tabs):
        tab_id = tab.get("id", 0)
        title = tab.get("title", "")
        stripped = _strip_icons(title)
        pinned = title.startswith(_ICON_STAR)
        if pinned:
            has_pinned = True

        if tab.get("is_focused", False):
            focused_id = tab_id

        # Detect account from user vars
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
            sort_key = (pin_prefix, account.lower(), project_name.lower())
            managed.append((sort_key, pos, tab_id, pinned))
        else:
            unmanaged.append((pos, tab_id))

    if not managed:
        return

    # When sort_sessions is not alpha and no pinned tabs exist, nothing to do
    if _cfg._default_sort_sessions != "alpha" and not has_pinned:
        return

    # Build desired order depending on sort mode
    if _cfg._default_sort_sessions == "alpha":
        # Full sort: all managed tabs sorted by (pin, account, project)
        managed.sort(key=lambda x: x[0])
        desired_ids = [m[2] for m in managed] + [u[1] for u in unmanaged]
    else:
        # Non-alpha mode: pinned tabs sorted alphabetically at front,
        # unpinned managed tabs retain their current relative order
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

    # Atomically reorder tabs via headless kitten (kitty @ kitten dispatches
    # directly into the Kitty process, triggering handle_result with boss)
    cmd = [
        "kitty", "@", "--to", socket, "kitten",
        "cc_reorder_tabs.py",
    ]
    if focused_id is not None:
        cmd.append(f"--focus={focused_id}")
    cmd.extend(str(tid) for tid in desired_ids)
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=5)
    if result.returncode != 0:
        import sys
        print(f"reorder_tabs error: {result.stderr}", file=sys.stderr)


def do_open_editor(socket, filepath):
    """Open a file in the user's editor in a new Kitty OS window.

    Resolves the editor from $VISUAL / $EDITOR / vi, then launches it
    via kitty @ launch --type=os-window.  Uses shlex.split to handle
    compound editors like "code --wait".
    """
    editor = os.environ.get("VISUAL") or os.environ.get("EDITOR") or "vi"
    editor_parts = shlex.split(editor)
    subprocess.Popen(
        [
            "kitty", "@", "--to", socket,
            "launch", "--type=os-window",
            *editor_parts, filepath,
        ],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
