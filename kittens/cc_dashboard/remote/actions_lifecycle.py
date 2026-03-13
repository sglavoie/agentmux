"""Session lifecycle helpers: detached close/restart/move and in-process wrappers."""

import contextlib
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import time

from cc_dashboard import config as _cfg
from cc_dashboard.remote.actions_core import (
    compute_insert_location,
    do_close,
    do_launch,
)
from cc_dashboard.remote.actions_sorting import do_sort_tabs
from cc_dashboard.remote.shared import (
    _append_override_for_dir,
    _strip_override_marker,
)


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
    try:
        subprocess.run(cmd, capture_output=True, timeout=30)
    except subprocess.TimeoutExpired:
        pass
"""
    env = os.environ.copy()
    env["_CCM_STEPS"] = json.dumps(steps)

    python = shutil.which("python3") or shutil.which("python")
    if not python:
        return
    with contextlib.suppress(OSError):
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
    color_override="",
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
    if color_override:
        launch_cmd += ["--var", f"cc_color={color_override}"]
    launch_cmd += ["--var", f"cc_config_dir={config_dir}"]
    if config_dir and config_dir != default_dir:
        launch_cmd += ["--env", f"CLAUDE_CONFIG_DIR={config_dir}"]
    if env_vars and env_vars != "-":
        try:
            env_tokens = shlex.split(env_vars)
        except ValueError as e:
            print(
                f"Warning: failed to parse env vars {env_vars!r}: {e}",
                file=sys.stderr,
            )
            env_tokens = []
        for token in env_tokens:
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
                f"title:^{re.escape(title)}$",
                *_cfg._palette_tab_colors(
                    account,
                    color_override=color_override,
                    config_dir_override=config_dir,
                ),
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
                    f"title:^{re.escape(title)}$",
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
                    f"title:^{re.escape(title)}$",
                    pw_mode,
                ]
            }
        )

    steps.append(
        {
            "cmd": [
                "bash",
                "-c",
                f"source ~/.local/lib/cc-manager/cc-common.sh && "
                f"load_accounts && load_defaults && "
                f"sort_tabs {shlex.quote(socket)}",
            ],
            "sleep": 0.5,
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
    try:
        if retry:
            for _ in range(retry + 1):
                r = subprocess.run(cmd, capture_output=True, timeout=30)
                if r.returncode == 0:
                    break
                time.sleep(retry_sleep)
        else:
            subprocess.run(cmd, capture_output=True, timeout=30)
    except subprocess.TimeoutExpired:
        pass
    if sleep_after:
        time.sleep(sleep_after)
"""
    env = os.environ.copy()
    env["_CCM_STEPS"] = json.dumps(steps)

    python = shutil.which("python3") or shutil.which("python")
    if not python:
        return
    with contextlib.suppress(OSError):
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
    cc_color = session.get("cc_color", "")
    group = session.get("group", "")

    account_config_dir = _cfg._accounts.get(account, {}).get("config_dir", "")
    if config_dir_override and config_dir_override != account_config_dir:
        title = _append_override_for_dir(title, config_dir_override)
    else:
        title = _strip_override_marker(title)
        if config_dir_override == account_config_dir:
            config_dir_override = None

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
            color_override=cc_color,
        )
        return True

    ok = do_close(socket, tab_id)
    if not ok:
        return False
    time.sleep(0.3)
    location = compute_insert_location(socket, account, title)
    ok = do_launch(
        socket,
        title,
        cwd,
        account,
        flags,
        pw_override,
        location=location,
        config_dir_override=config_dir_override,
        env_vars=env_vars,
        color_override=cc_color,
        group=group,
    )
    if ok:
        do_sort_tabs(socket)
    return ok


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
    color_override="",
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
    if color_override:
        launch_cmd += ["--var", f"cc_color={color_override}"]
    launch_cmd += ["--var", f"cc_config_dir={config_dir}"]
    if config_dir and config_dir != default_dir:
        launch_cmd += ["--env", f"CLAUDE_CONFIG_DIR={config_dir}"]
    if env_vars and env_vars != "-":
        try:
            env_tokens = shlex.split(env_vars)
        except ValueError as e:
            print(
                f"Warning: failed to parse env vars {env_vars!r}: {e}",
                file=sys.stderr,
            )
            env_tokens = []
        for token in env_tokens:
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
                f"title:^{re.escape(title)}$",
                *_cfg._palette_tab_colors(
                    account,
                    color_override=color_override,
                    config_dir_override=config_dir,
                ),
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
                    f"title:^{re.escape(title)}$",
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
                    f"title:^{re.escape(title)}$",
                    pw_mode,
                ]
            }
        )

    steps.append(
        {
            "cmd": [
                "bash",
                "-c",
                f"source ~/.local/lib/cc-manager/cc-common.sh && "
                f"load_accounts && load_defaults && "
                f"sort_tabs {shlex.quote(target_socket)}",
            ],
            "sleep": 0.5,
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
    try:
        if retry:
            for _ in range(retry + 1):
                r = subprocess.run(cmd, capture_output=True, timeout=30)
                if r.returncode == 0:
                    break
                time.sleep(retry_sleep)
        else:
            subprocess.run(cmd, capture_output=True, timeout=30)
    except subprocess.TimeoutExpired:
        pass
    if sleep_after:
        time.sleep(sleep_after)
"""
    env = os.environ.copy()
    env["_CCM_STEPS"] = json.dumps(steps)

    python = shutil.which("python3") or shutil.which("python")
    if not python:
        return
    with contextlib.suppress(OSError):
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
    cc_color = session.get("cc_color", "")
    group = session.get("group", "")

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
            color_override=cc_color,
        )
        return True

    ok = do_close(source_socket, tab_id)
    if not ok:
        return False
    time.sleep(0.3)
    location = compute_insert_location(target_socket, account, title)
    ok = do_launch(
        target_socket,
        title,
        cwd,
        account,
        flags,
        pw_override,
        location=location,
        config_dir_override=config_dir_override,
        env_vars=env_vars,
        color_override=cc_color,
        group=group,
    )
    if ok:
        do_sort_tabs(target_socket)
    return ok
