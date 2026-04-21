"""Shared relaunch helpers for lifecycle actions."""

import os
import shlex
import sys

from cc_dashboard import config as _cfg
from cc_dashboard.remote.actions_launch import (
    _normalize_commands,
    compute_insert_location,
)
from cc_dashboard.remote.shared import (
    _ICON_MUTE,
    _append_override_for_dir,
    _strip_override_marker,
)


def _prepare_relaunch_session(session, commands=None, config_dir_override=None):
    """Normalize session data before restart or move operations."""
    account = session["account"]
    title = session["title"]
    normalized_commands = _normalize_commands(
        session.get("cc_commands") if commands is None else commands
    )
    account_config_dir = _cfg._accounts.get(account, {}).get("config_dir", "")

    if config_dir_override and config_dir_override != account_config_dir:
        title = _append_override_for_dir(title, config_dir_override)
    else:
        title = _strip_override_marker(title)
        if config_dir_override == account_config_dir:
            config_dir_override = None

    return {
        "account": account,
        "cc_color": session.get("cc_color", ""),
        "commands": normalized_commands,
        "config_dir_override": config_dir_override,
        "cwd": session["cwd"],
        "groups": session.get("groups", []),
        "mute": "1" if session.get("muted") else "",
        "tab_id": session["tab_id"],
        "title": title,
    }


def _build_launch_steps(
    close_socket,
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
    groups=None,
    exclude_tab_id_for_location=None,
    mute="",
    commands=None,
):
    """Build detached close-and-launch steps for restart and move flows."""
    directory = os.path.expanduser(cwd)
    commands = _normalize_commands(commands)
    home = os.path.expanduser("~")
    default_dir = os.path.join(home, ".claude")
    config_dir = (
        config_dir_override
        if config_dir_override
        else _cfg._accounts.get(account, {}).get("config_dir", "")
    )
    if mute == "1" and _ICON_MUTE not in title:
        title = _ICON_MUTE + title

    shell = os.environ.get("SHELL", "/bin/zsh")
    effective_flags = _cfg._resolve_claude_flags(flags)
    shell_cmd = f"exec claude {effective_flags}" if effective_flags else "exec claude"

    location = compute_insert_location(
        target_socket, account, title, exclude_tab_id=exclude_tab_id_for_location
    )
    loc_type, loc_id = location
    loc_args: list[str] = []

    steps: list[dict] = [
        {
            "cmd": [
                "kitty",
                "@",
                "--to",
                close_socket,
                "close-tab",
                "--match",
                f"id:{tab_id}",
            ],
            "sleep": 0.5,
        }
    ]

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
        "--var",
        f"cc_groups={chr(0x1F).join(groups or [])}",
        "--var",
        f"cc_commands={chr(0x1F).join(commands)}",
    ]
    if mute == "1":
        launch_cmd += ["--var", "cc_mute=1"]
    if color_override:
        launch_cmd += ["--var", f"cc_color={color_override}"]
    launch_cmd += ["--var", f"cc_config_dir={config_dir}"]
    if config_dir and config_dir != default_dir:
        launch_cmd += ["--env", f"CLAUDE_CONFIG_DIR={config_dir}"]
    if env_vars and env_vars != "-":
        try:
            env_tokens = shlex.split(env_vars)
        except ValueError as error:
            print(
                f"Warning: failed to parse env vars {env_vars!r}: {error}",
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
                "window_id:__WINDOW_ID__",
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
                                "window_id:__WINDOW_ID__",
                                "--title",
                                "shell",
                                "--cwd",
                                directory,
                                "--keep-focus",
                            ]
                        }
                    )
                elif cmd_entry:
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
                                "window_id:__WINDOW_ID__",
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
                            ]
                        }
                    )
        else:
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
                        "window_id:__WINDOW_ID__",
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
                    "window_id:__WINDOW_ID__",
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
    return steps
