#!/usr/bin/env python3
"""Runtime regression tests for the commands feature.

Run: python3 tests/test_commands_runtime.py
"""

import subprocess
import sys
import tempfile
from pathlib import Path
from types import SimpleNamespace

REPO_ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(REPO_ROOT / "kittens"))

from cc_dashboard import config as cfg
import cc_dashboard.loop_modes_forms as loop_modes_forms  # noqa: E402
from cc_dashboard.loop_helpers import (  # noqa: E402
    _resolve_session_runtime_options,
)
from cc_dashboard.loop_modes_forms import (  # noqa: E402
    _resolve_live_or_config_options,
    handle_override_mode,
)
from cc_dashboard.remote import actions_core, actions_lifecycle  # noqa: E402

PASS = 0
FAIL = 0


def check(label, condition):
    global PASS, FAIL
    if condition:
        PASS += 1
        print(f"  PASS  {label}")
    else:
        FAIL += 1
        print(f"  FAIL  {label}")


def test_python_launch_explicit_disable():
    """commands=['-'] does not spawn an extra shell window."""
    print("\n--- python launch explicit disable ---")
    calls = []
    original_kitty_cmd = actions_core.kitty_cmd
    original_accounts = cfg._accounts
    original_resolve_pw = cfg._resolve_project_window
    original_resolve_flags = cfg._resolve_claude_flags
    original_palette = cfg._palette_tab_colors

    def fake_kitty_cmd(_socket, *args):
        calls.append(args)
        return SimpleNamespace(returncode=0, stdout="[]")

    cfg._accounts = {"work": {"config_dir": ""}}
    cfg._resolve_project_window = lambda override="": (
        "stack" if not override else ("" if override == "-" else override)
    )
    cfg._resolve_claude_flags = lambda override="": override
    cfg._palette_tab_colors = lambda *args, **kwargs: ["active_bg=#000000"]
    actions_core.kitty_cmd = fake_kitty_cmd

    with tempfile.TemporaryDirectory() as tmpdir:
        ok = actions_core.do_launch(
            "unix:/tmp/fake",
            "test",
            tmpdir,
            "work",
            commands=["-"],
        )

    actions_core.kitty_cmd = original_kitty_cmd
    cfg._accounts = original_accounts
    cfg._resolve_project_window = original_resolve_pw
    cfg._resolve_claude_flags = original_resolve_flags
    cfg._palette_tab_colors = original_palette

    window_launches = [call for call in calls if "--type=window" in call]
    layout_calls = [call for call in calls if call and call[0] == "goto-layout"]
    check("launch succeeds", ok is True)
    check("no extra windows launched", len(window_launches) == 0)
    check("layout still applied", len(layout_calls) == 1)


def test_detached_steps_explicit_disable():
    """Detached restart/move steps also suppress extra windows for ['-']."""
    print("\n--- detached launch steps explicit disable ---")
    original_accounts = cfg._accounts
    original_resolve_pw = cfg._resolve_project_window
    original_resolve_flags = cfg._resolve_claude_flags
    original_palette = cfg._palette_tab_colors
    original_compute_location = actions_lifecycle.compute_insert_location

    cfg._accounts = {"work": {"config_dir": ""}}
    cfg._resolve_project_window = lambda override="": (
        "stack" if not override else ("" if override == "-" else override)
    )
    cfg._resolve_claude_flags = lambda override="": override
    cfg._palette_tab_colors = lambda *args, **kwargs: ["active_bg=#000000"]
    actions_lifecycle.compute_insert_location = lambda *args, **kwargs: (None, None)

    with tempfile.TemporaryDirectory() as tmpdir:
        steps = actions_lifecycle._build_launch_steps(
            "unix:/tmp/src",
            "unix:/tmp/dst",
            1,
            "test",
            tmpdir,
            "work",
            commands=["-"],
        )

    cfg._accounts = original_accounts
    cfg._resolve_project_window = original_resolve_pw
    cfg._resolve_claude_flags = original_resolve_flags
    cfg._palette_tab_colors = original_palette
    actions_lifecycle.compute_insert_location = original_compute_location

    window_launches = [step for step in steps if "--type=window" in step["cmd"]]
    layout_calls = [step for step in steps if "goto-layout" in step["cmd"]]
    check("detached steps: no extra windows", len(window_launches) == 0)
    check("detached steps: layout retained", len(layout_calls) == 1)


def test_detached_steps_command_window_titles():
    """Detached restart/move steps set --title on command windows."""
    print("\n--- detached launch steps command window titles ---")
    original_accounts = cfg._accounts
    original_resolve_pw = cfg._resolve_project_window
    original_resolve_flags = cfg._resolve_claude_flags
    original_palette = cfg._palette_tab_colors
    original_compute_location = actions_lifecycle.compute_insert_location

    cfg._accounts = {"work": {"config_dir": ""}}
    cfg._resolve_project_window = lambda override="": (
        "stack" if not override else ("" if override == "-" else override)
    )
    cfg._resolve_claude_flags = lambda override="": override
    cfg._palette_tab_colors = lambda *args, **kwargs: ["active_bg=#000000"]
    actions_lifecycle.compute_insert_location = lambda *args, **kwargs: (None, None)

    with tempfile.TemporaryDirectory() as tmpdir:
        steps = actions_lifecycle._build_launch_steps(
            "unix:/tmp/src",
            "unix:/tmp/dst",
            1,
            "test",
            tmpdir,
            "work",
            commands=["npm run dev", "shell"],
        )

    cfg._accounts = original_accounts
    cfg._resolve_project_window = original_resolve_pw
    cfg._resolve_claude_flags = original_resolve_flags
    cfg._palette_tab_colors = original_palette
    actions_lifecycle.compute_insert_location = original_compute_location

    window_launches = [step for step in steps if "--type=window" in step["cmd"]]
    check("detached steps: two command windows launched", len(window_launches) == 2)

    # First window: "npm run dev" command
    cmd0 = window_launches[0]["cmd"]
    check(
        "detached steps: command window has --title",
        "--title" in cmd0 and cmd0[cmd0.index("--title") + 1] == "npm run dev",
    )

    # Second window: "shell" literal
    cmd1 = window_launches[1]["cmd"]
    check(
        "detached steps: shell window has --title",
        "--title" in cmd1 and cmd1[cmd1.index("--title") + 1] == "shell",
    )


def test_override_restart_uses_config_commands():
    """Override-mode restart recovers commands from config when live value is missing."""
    print("\n--- override restart uses config commands ---")
    captured = {}

    class Cfg:
        _sessions = [
            {
                "title": "proj",
                "flags": "",
                "pw": "",
                "env": "",
                "commands": ["npm run dev"],
            }
        ]
        _accounts = {"work": {"config_dir": "/cfg/work"}}

        @staticmethod
        def _unique_config_dirs():
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


def test_override_stopped_launch_uses_config_commands():
    """Override-mode relaunch for stopped sessions uses config commands fallback."""
    print("\n--- override stopped launch uses config commands ---")
    captured = {}
    original_sleep = loop_modes_forms.time.sleep

    class Cfg:
        _sessions = [
            {"title": "proj", "flags": "", "pw": "", "env": "", "commands": ["shell"]}
        ]
        _accounts = {"work": {"config_dir": "/cfg/work"}}

        @staticmethod
        def _unique_config_dirs():
            return ["/cfg/other"]

    def fake_launch(_socket, _title, _directory, _account, _flags, _pw_override, **kwargs):
        captured["commands"] = kwargs.get("commands")

    loop_modes_forms.time.sleep = lambda _secs: None
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
        "compute_insert_location": lambda socket, account, title: (None, None),
        "append_override_for_dir": lambda title, path: f"{title}@{path}",
        "strip_override_marker": lambda title: title,
        "query_all_sessions": lambda managed_only=True: [],
        "refilter": lambda: None,
        "show_all": False,
        "resolve_live_or_config_options": _resolve_live_or_config_options,
    }

    handled = handle_override_mode(10, state)
    loop_modes_forms.time.sleep = original_sleep
    check("override stopped launch handled", handled is True)
    check(
        "override stopped launch passes config commands",
        captured.get("commands") == ["shell"],
    )


def test_bash_launch_explicit_disable():
    """Bash launch_session does not create a legacy shell window for ['-']."""
    print("\n--- bash launch explicit disable ---")
    with tempfile.TemporaryDirectory() as tmpdir:
        call_log = Path(tmpdir) / "calls.log"
        script = f"""
set -euo pipefail

kitty() {{
    printf '%s\n' "$*" >> {str(call_log)!r}
    return 0
}}

expand_tilde() {{ printf '%s\n' "$1"; }}
claude_config_dir_for_account() {{ printf '\n'; }}
_account_color() {{ printf 'green\n'; }}
tab_colors_for_account() {{ printf 'active_bg=#000000\n'; }}
resolve_claude_flags() {{ printf '%s\n' "$1"; }}
escape_regex() {{ printf '%s\n' "$1"; }}
ccm_debug() {{ :; }}
warn() {{ :; }}
err() {{ :; }}
tab_exists() {{ return 1; }}
resolve_project_window() {{
    if [[ -n "$1" ]]; then
        printf '%s\n' "$1"
    else
        printf 'stack\n'
    fi
}}

SOCKET='unix:/tmp/fake'
_COLOR_MAX_RETRIES=1
_COLOR_INITIAL_DELAY_MS=0
_COLOR_BACKOFF_FACTOR_NUM=1
_COLOR_BACKOFF_FACTOR_DEN=1

source {str(REPO_ROOT / "lib" / "cc-launch" / "core.sh")!r}
launch_session 'test' {tmpdir!r} 'work' '' '' '' '' '' '' '-'
"""
        result = subprocess.run(["bash", "-c", script], capture_output=True, text=True)
        calls = call_log.read_text().splitlines() if call_log.exists() else []

    window_calls = [call for call in calls if "--type=window" in call]
    check("bash launch succeeds", result.returncode == 0)
    check("bash launch: no extra windows", len(window_calls) == 0)


def test_resolve_config_lookup_with_account_prefix():
    """Config lookup matches titles with X: account prefix (e.g. W:proj)."""
    print("\n--- resolve config lookup with account prefix ---")

    class Cfg:
        _sessions = [
            {
                "title": "W:proj",
                "flags": "--verbose",
                "pw": "stack",
                "env": "FOO=bar",
                "commands": ["npm run dev"],
            }
        ]

    # Session title includes account prefix, matching config title
    sess = {
        "title": "W:proj",
        "cc_flags": "",
        "cc_pw": "",
        "cc_env": "",
        "cc_commands": [],
    }
    flags, pw, env, commands = _resolve_live_or_config_options(
        sess, Cfg(), lambda t: t
    )
    check("config lookup matches W:proj title", flags == "--verbose")
    check("config lookup pw with prefix", pw == "stack")
    check("config lookup env with prefix", env == "FOO=bar")
    check("config lookup commands with prefix", commands == ["npm run dev"])

    # Session title has override emoji stripped, but X: prefix preserved
    sess2 = {
        "title": "W:proj",
        "cc_flags": "",
        "cc_pw": "",
        "cc_env": "",
        "cc_commands": [],
    }
    flags2, pw2, env2, commands2 = _resolve_live_or_config_options(
        sess2, Cfg(), lambda t: t
    )
    check("config lookup still works after emoji strip", flags2 == "--verbose")
    check("config lookup commands after emoji strip", commands2 == ["npm run dev"])


def test_resolve_config_lookup_fallback_without_prefix():
    """Config lookup falls back to bare title when config omits X: prefix."""
    print("\n--- resolve config lookup fallback without prefix ---")

    class Cfg:
        _sessions = [
            {
                "title": "proj",
                "flags": "--fallback",
                "pw": "",
                "env": "",
                "commands": ["shell"],
            }
        ]

    sess = {
        "title": "W:proj",
        "cc_flags": "",
        "cc_pw": "",
        "cc_env": "",
        "cc_commands": [],
    }
    flags, pw, env, commands = _resolve_live_or_config_options(
        sess, Cfg(), lambda t: t
    )
    check("fallback lookup matches bare title", flags == "--fallback")
    check("fallback lookup commands", commands == ["shell"])


def test_runtime_options_fallback_without_prefix():
    """_resolve_session_runtime_options falls back to bare title."""
    print("\n--- runtime options fallback without prefix ---")
    original_sessions = cfg._sessions
    original_find = cfg._find_config_session

    cfg._sessions = [
        {
            "title": "proj",
            "flags": "--from-config",
            "pw": "stack",
            "env": "FOO=1",
            "commands": ["npm run dev"],
        }
    ]

    def _find(title):
        for s in cfg._sessions:
            if s["title"] == title:
                return s
        return None

    cfg._find_config_session = _find

    sess = {
        "title": "W:proj",
        "cc_flags": "",
        "cc_pw": "",
        "cc_env": "",
        "cc_commands": [],
    }
    flags, pw, env, commands = _resolve_session_runtime_options(sess)

    cfg._sessions = original_sessions
    cfg._find_config_session = original_find

    check("runtime options fallback flags", flags == "--from-config")
    check("runtime options fallback pw", pw == "stack")
    check("runtime options fallback env", env == "FOO=1")
    check("runtime options fallback commands", commands == ["npm run dev"])


if __name__ == "__main__":
    print("commands runtime tests")

    test_python_launch_explicit_disable()
    test_detached_steps_explicit_disable()
    test_detached_steps_command_window_titles()
    test_override_restart_uses_config_commands()
    test_override_stopped_launch_uses_config_commands()
    test_bash_launch_explicit_disable()
    test_resolve_config_lookup_with_account_prefix()
    test_resolve_config_lookup_fallback_without_prefix()
    test_runtime_options_fallback_without_prefix()

    print(f"\n{PASS} passed, {FAIL} failed")
    if FAIL:
        sys.exit(1)
