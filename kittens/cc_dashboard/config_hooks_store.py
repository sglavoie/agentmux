"""Persistence and path helpers for dashboard hooks configuration."""

from __future__ import annotations

import contextlib
import json
import os
import shutil
import sys
from typing import Any

import cc_dashboard.config_paths as _paths
import cc_dashboard.config_state as _state


def _resolve_hooks_example_path() -> str | None:
    """Check repo layout then installed layout for hooks.example.json."""
    this_dir = os.path.dirname(os.path.realpath(__file__))
    repo_path = os.path.join(this_dir, "..", "..", "config", "hooks.example.json")
    if os.path.isfile(repo_path):
        return os.path.realpath(repo_path)
    installed_path = os.path.join(
        os.path.expanduser("~"), ".local", "lib", "cc-manager", "hooks.example.json"
    )
    if os.path.isfile(installed_path):
        return installed_path
    return None


def load_hooks() -> tuple[dict[str, Any], dict[str, Any]]:
    """Parse hooks.json into the shared hooks dicts.

    Falls back to hooks.example.json if hooks.json is missing.
    Returns (_hooks_global, _hooks_overrides).
    """
    _state._hooks_global = {}
    _state._hooks_overrides = {}

    path: str | None = _paths._hooks_config_path()
    if not path or not os.path.isfile(path):
        path = _resolve_hooks_example_path()
    if not path or not os.path.isfile(path):
        return _state._hooks_global, _state._hooks_overrides

    with contextlib.suppress(json.JSONDecodeError, OSError):
        with open(path) as f:
            data = json.load(f)
        _state._hooks_global = data.get("hooks", {})
        _state._hooks_overrides = data.get("account_overrides", {})

    return _state._hooks_global, _state._hooks_overrides


def _resolve_hook_cmd() -> str:
    """Resolve the absolute path to cc-hook-indicator, with caching."""
    if _state._cached_hook_cmd is not None:
        return _state._cached_hook_cmd

    local_bin = os.path.join(
        os.path.expanduser("~"), ".local", "bin", "cc-hook-indicator"
    )
    if os.path.isfile(local_bin) and os.access(local_bin, os.X_OK):
        _state._cached_hook_cmd = local_bin
        return _state._cached_hook_cmd

    which_result = shutil.which("cc-hook-indicator")
    if which_result:
        _state._cached_hook_cmd = which_result
        return _state._cached_hook_cmd

    _state._cached_hook_cmd = "cc-hook-indicator"
    return _state._cached_hook_cmd


def _resolve_log_cmd() -> str:
    """Resolve the absolute path to cc-activity-log, with caching."""
    if _state._cached_log_cmd is not None:
        return _state._cached_log_cmd

    local_bin = os.path.join(
        os.path.expanduser("~"), ".local", "bin", "cc-activity-log"
    )
    if os.path.isfile(local_bin) and os.access(local_bin, os.X_OK):
        _state._cached_log_cmd = local_bin
        return _state._cached_log_cmd

    which_result = shutil.which("cc-activity-log")
    if which_result:
        _state._cached_log_cmd = which_result
        return _state._cached_log_cmd

    _state._cached_log_cmd = "cc-activity-log"
    return _state._cached_log_cmd


def _load_example_hooks() -> dict[str, Any]:
    """Load the base hooks from hooks.example.json."""
    path = _resolve_hooks_example_path()
    if not path:
        return {}
    with contextlib.suppress(json.JSONDecodeError, OSError), open(path) as f:
        result: dict[str, Any] = json.load(f).get("hooks", {})
        return result
    return {}


def save_hooks(global_hooks: dict[str, Any], account_overrides: dict[str, Any]) -> bool:
    """Write hooks configuration to hooks.json.

    Creates a .bak copy before overwriting, following the same pattern as
    save_sessions. Uses an atomic write (write to .tmp then os.replace) so a
    crash mid-write never leaves a partially-written hooks.json.

    Returns True on success, False on failure.
    """
    path = _paths._hooks_config_path()
    if os.path.isfile(path):
        shutil.copy2(path, path + ".bak")

    tmp_path = path + ".tmp"
    try:
        with open(tmp_path, "w") as f:
            json.dump(
                {"hooks": global_hooks, "account_overrides": account_overrides},
                f,
                indent=2,
            )
            f.write("\n")
        os.replace(tmp_path, path)
    except OSError as e:
        print(f"warning: failed to save hooks.json: {e}", file=sys.stderr)
        with contextlib.suppress(OSError):
            os.remove(tmp_path)
        return False

    _state._hooks_global = global_hooks
    _state._hooks_overrides = account_overrides
    return True
