#!/usr/bin/env python3
"""Override-mode runtime regression tests for the commands feature."""

from commands_runtime_support import (
    _resolve_live_or_config_options,
    check,
    handle_override_mode,
    loop_modes_forms_actions,
)


def test_override_restart_uses_config_commands() -> None:
    """Override-mode restart uses config commands when live data is missing."""
    print("\n--- override restart uses config commands ---")
    captured = {}

    class Cfg:
        def __init__(self):
            self._sessions = [
                {
                    "title": "proj",
                    "flags": "",
                    "pw": "",
                    "env": "",
                    "commands": ["npm run dev"],
                }
            ]
            self._accounts = {"work": {"config_dir": "/cfg/work"}}

        def _unique_config_dirs(self):
            return ["/cfg/other"]

    def fake_restart(socket, _sess, **kwargs):
        captured["socket"] = socket
        captured["commands"] = kwargs.get("commands")

    state = {
        "mode": "override",
        "override_source": "list",
        "override_selected": 0,
        "cfg": Cfg(),
        "filtered": [
            {
                "title": "proj",
                "account": "work",
                "tab_id": 7,
                "socket": "unix:/tmp/home",
                "status": "idle",
                "cwd": "/tmp",
                "cc_flags": "--danger",
                "cc_pw": "",
                "cc_env": "",
                "cc_commands": [],
                "group": "",
                "config_dir": "/cfg/work",
            }
        ],
        "selected": 0,
        "home_socket": "unix:/tmp/home",
        "host_tab_id": None,
        "do_restart": fake_restart,
        "query_all_sessions": lambda managed_only=True: [],
        "refilter": lambda: None,
        "show_all": False,
        "strip_override_marker": lambda title: title,
        "resolve_live_or_config_options": _resolve_live_or_config_options,
    }

    handled = handle_override_mode(10, state)
    check("override restart handled", handled is True)
    check(
        "override restart passes config commands",
        captured.get("commands") == ["npm run dev"],
    )


def test_override_stopped_launch_uses_config_commands() -> None:
    """Override-mode relaunch for stopped sessions uses config commands fallback."""
    print("\n--- override stopped launch uses config commands ---")
    captured = {}
    original_sleep = loop_modes_forms_actions.time.sleep

    class Cfg:
        def __init__(self):
            self._sessions = [
                {
                    "title": "proj",
                    "flags": "",
                    "pw": "",
                    "env": "",
                    "commands": ["shell"],
                }
            ]
            self._accounts = {"work": {"config_dir": "/cfg/work"}}

        def _unique_config_dirs(self):
            return ["/cfg/other"]

    def fake_launch(
        _socket,
        _title,
        _directory,
        _account,
        _flags,
        _pw_override,
        **kwargs,
    ):
        captured["commands"] = kwargs.get("commands")

    loop_modes_forms_actions.time.sleep = lambda _secs: None
    state = {
        "mode": "override",
        "override_source": "list",
        "override_selected": 0,
        "cfg": Cfg(),
        "filtered": [
            {
                "title": "proj",
                "account": "work",
                "tab_id": 7,
                "socket": "unix:/tmp/home",
                "status": "stopped",
                "cwd": "/tmp",
                "cc_flags": "--danger",
                "cc_pw": "",
                "cc_env": "",
                "cc_commands": [],
                "group": "",
                "config_dir": "/cfg/work",
            }
        ],
        "selected": 0,
        "home_socket": "unix:/tmp/home",
        "host_tab_id": None,
        "do_close": lambda socket, tab_id: True,
        "do_launch": fake_launch,
        "compute_insert_location": lambda socket, account, title, **kw: (None, None),
        "append_override_for_dir": lambda title, path: f"{title}@{path}",
        "strip_override_marker": lambda title: title,
        "query_all_sessions": lambda managed_only=True: [],
        "refilter": lambda: None,
        "show_all": False,
        "resolve_live_or_config_options": _resolve_live_or_config_options,
    }

    handled = handle_override_mode(10, state)
    loop_modes_forms_actions.time.sleep = original_sleep
    check("override stopped launch handled", handled is True)
    check(
        "override stopped launch passes config commands",
        captured.get("commands") == ["shell"],
    )


def run_tests() -> None:
    test_override_restart_uses_config_commands()
    test_override_stopped_launch_uses_config_commands()
