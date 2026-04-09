#!/usr/bin/env python3
"""Launch-related runtime regression tests for the commands feature."""

import subprocess
import tempfile
from pathlib import Path
from types import SimpleNamespace

from commands_runtime_support import (
    REPO_ROOT,
    actions_core,
    actions_lifecycle,
    cfg,
    check,
)


def test_python_launch_explicit_disable() -> None:
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


def test_detached_steps_explicit_disable() -> None:
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


def test_detached_steps_command_window_titles() -> None:
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

    cmd0 = window_launches[0]["cmd"]
    check(
        "detached steps: command window has --title",
        "--title" in cmd0 and cmd0[cmd0.index("--title") + 1] == "npm run dev",
    )

    cmd1 = window_launches[1]["cmd"]
    check(
        "detached steps: shell window has --title",
        "--title" in cmd1 and cmd1[cmd1.index("--title") + 1] == "shell",
    )


def test_bash_launch_explicit_disable() -> None:
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


def run_tests() -> None:
    test_python_launch_explicit_disable()
    test_detached_steps_explicit_disable()
    test_detached_steps_command_window_titles()
    test_bash_launch_explicit_disable()
