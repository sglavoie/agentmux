"""Tab action helpers: focus, launch, restart, move, sort, and hooks sync."""

import contextlib
import json
import os
import re
import shlex
import shutil
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


def _spawn_detached_close(socket, tab_id):
    """Spawn a detached subprocess to close a tab after the dashboard exits."""
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
    env_vars="",
):
    """Spawn a fully detached subprocess to close+relaunch a session."""
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

    location = compute_insert_location(socket, account, title, exclude_tab_id=tab_id)
    loc_type, loc_id = location
    loc_args = []

    steps = []
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
        "--var",
        f"cc_flags={flags}",
        "--var",
        f"cc_pw={pw_override}",
        "--var",
        f"cc_env={env_vars}",
    ]
    if config_dir and config_dir != default_dir:
        launch_cmd += ["--env", f"CLAUDE_CONFIG_DIR={config_dir}"]
    if env_vars and env_vars != "-":
        for token in shlex.split(env_vars):
            if "=" in token:
                launch_cmd += ["--env", token]
    launch_cmd += [shell, "-lic", shell_cmd]
    steps.append({"cmd": launch_cmd})

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


def do_restart(
    socket,
    session,
    flags="",
    pw_override="",
    config_dir_override=None,
    host_tab_id=None,
    env_vars="",
):
    """Close and relaunch a session with the same title, directory, and account."""
    account = session["account"]
    title = session["title"]

    if config_dir_override:
        from cc_dashboard.remote.shared import _append_override_marker

        title = _append_override_marker(title)

    tab_id = session["tab_id"]
    cwd = session["cwd"]

    if host_tab_id is not None and tab_id == host_tab_id:
        _spawn_detached_restart(
            socket,
            tab_id,
            title,
            cwd,
            account,
            flags,
            pw_override,
            config_dir_override,
            env_vars,
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
        env_vars=env_vars,
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
    env_vars="",
):
    """Spawn a detached subprocess to move a session across kitty instances."""
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

    location = compute_insert_location(target_socket, account, title)
    loc_type, loc_id = location
    loc_args = []

    steps = []
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
        "--var",
        f"cc_flags={flags}",
        "--var",
        f"cc_pw={pw_override}",
        "--var",
        f"cc_env={env_vars}",
    ]
    if config_dir and config_dir != default_dir:
        launch_cmd += ["--env", f"CLAUDE_CONFIG_DIR={config_dir}"]
    if env_vars and env_vars != "-":
        for token in shlex.split(env_vars):
            if "=" in token:
                launch_cmd += ["--env", token]
    launch_cmd += [shell, "-lic", shell_cmd]
    steps.append({"cmd": launch_cmd})

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
    env_vars="",
):
    """Move a session from one Kitty instance to another (close + relaunch)."""
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
            env_vars,
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
        env_vars=env_vars,
    )


def do_sort_tabs(socket):
    """Sort managed tabs by account then title, pushing non-managed tabs to the end."""
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

    managed = []
    unmanaged = []
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

    if _cfg._default_sort_sessions != "alpha" and not has_pinned:
        return

    if _cfg._default_sort_sessions == "alpha":
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
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=5)
    if result.returncode != 0:
        import sys

        print(f"reorder_tabs error: {result.stderr}", file=sys.stderr)


def run_hooks_sync(account_name=None):
    """Run install-hooks.sh to sync hooks into settings.json files."""
    this_dir = os.path.dirname(os.path.realpath(__file__))
    repo_lib = os.path.join(this_dir, "..", "..", "..", "lib", "install-hooks.sh")
    installed_lib = os.path.join(
        os.path.expanduser("~"), ".local", "lib", "cc-manager", "install-hooks.sh"
    )
    if os.path.isfile(repo_lib):
        script_path = os.path.realpath(repo_lib)
    elif os.path.isfile(installed_lib):
        script_path = installed_lib
    else:
        return False, "install-hooks.sh not found"

    repo_example = os.path.join(
        this_dir, "..", "..", "..", "config", "hooks.example.json"
    )
    installed_example = os.path.join(
        os.path.expanduser("~"), ".local", "lib", "cc-manager", "hooks.example.json"
    )
    if os.path.isfile(repo_example):
        example_path = os.path.realpath(repo_example)
    elif os.path.isfile(installed_example):
        example_path = installed_example
    else:
        return False, "hooks.example.json not found"

    cmd = ["bash", script_path]
    if account_name:
        cmd += ["--account", account_name]
    cmd.append(example_path)

    result = subprocess.run(cmd, capture_output=True, text=True, timeout=10)
    output = (result.stdout + result.stderr).strip()
    return result.returncode == 0, output


def do_open_editor(socket, filepath):
    """Open a file in the user's editor in a new Kitty OS window."""
    editor = os.environ.get("VISUAL") or os.environ.get("EDITOR") or "vi"
    editor_parts = shlex.split(editor)
    subprocess.Popen(
        [
            "kitty",
            "@",
            "--to",
            socket,
            "launch",
            "--type=os-window",
            *editor_parts,
            filepath,
        ],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
