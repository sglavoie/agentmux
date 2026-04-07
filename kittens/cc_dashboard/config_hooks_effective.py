"""Effective hook computation and drift detection for dashboard hooks."""

from __future__ import annotations

import contextlib
import copy
import json
import os
from typing import Any

import cc_dashboard.config_state as _state
from cc_dashboard.config_hooks_meta import HOOK_EVENTS
from cc_dashboard.config_hooks_overrides import _get_extra_hooks
from cc_dashboard.config_hooks_store import (
    _load_example_hooks,
    _resolve_hook_cmd,
    _resolve_log_cmd,
    load_hooks,
)


def _compute_effective_hooks(
    account_name: str, base_hooks: dict[str, Any] | None = None
) -> dict[str, list[dict[str, Any]]]:
    """Compute the effective hooks for an account after applying overrides."""
    if not _state._hooks_global:
        load_hooks()

    if base_hooks is None:
        base_hooks = _state._hooks_global

    override = _state._hooks_overrides.get(account_name, {})
    disabled_events = override.get("disabled_events", [])
    extra_hooks = _get_extra_hooks(override)
    resolved_cmd = _resolve_hook_cmd()
    resolved_log_cmd = _resolve_log_cmd()

    result = {}
    for event in HOOK_EVENTS:
        entries = copy.deepcopy(base_hooks.get(event, []))

        if event in disabled_events:
            filtered = []
            for entry in entries:
                inner_hooks = entry.get("hooks", [])
                inner_hooks = [
                    hook
                    for hook in inner_hooks
                    if "cc-hook-indicator" not in hook.get("command", "")
                ]
                if inner_hooks:
                    entry["hooks"] = inner_hooks
                    filtered.append(entry)
            entries = filtered

        for entry in entries:
            for hook in entry.get("hooks", []):
                cmd = hook.get("command", "")
                if cmd and "cc-hook-indicator" in cmd:
                    cmd = cmd.replace("cc-hook-indicator", resolved_cmd)
                if cmd and "cc-activity-log" in cmd:
                    cmd = cmd.replace("cc-activity-log", resolved_log_cmd)
                hook["command"] = cmd

        for group in extra_hooks.get(event, []):
            group = copy.deepcopy(group)
            for hook in group.get("hooks", []):
                cmd = hook.get("command", "")
                if cmd and "cc-hook-indicator" in cmd:
                    hook["command"] = cmd.replace("cc-hook-indicator", resolved_cmd)
                if cmd and "cc-activity-log" in cmd:
                    hook["command"] = cmd.replace("cc-activity-log", resolved_log_cmd)
            entries.append(group)

        result[event] = entries

    return result


def _detect_drift(account_name: str) -> str:
    """Compare installed settings.json hooks against computed effective hooks."""
    account_info = _state._accounts.get(account_name)
    if not account_info:
        return "missing"

    config_dir = account_info.get("config_dir", "")
    if not config_dir:
        return "missing"

    settings_path = os.path.join(config_dir, "settings.json")
    if not os.path.isfile(settings_path):
        return "missing"

    with contextlib.suppress(json.JSONDecodeError, OSError):
        with open(settings_path) as f:
            data = json.load(f)

        installed_hooks = data.get("hooks", {})
        example_hooks = _load_example_hooks()
        effective = _compute_effective_hooks(account_name, base_hooks=example_hooks)
        resolved_cmd = _resolve_hook_cmd()
        resolved_log_cmd = _resolve_log_cmd()

        override = _state._hooks_overrides.get(account_name, {})
        extra_hooks = _get_extra_hooks(override)

        for event in HOOK_EVENTS:
            installed_entries = installed_hooks.get(event, [])
            effective_entries = effective.get(event, [])

            extra_cmds = set()
            for group in extra_hooks.get(event, []):
                for hook in group.get("hooks", []):
                    cmd = hook.get("command", "")
                    if cmd:
                        extra_cmds.add(cmd)

            def _is_managed(entry, extra_cmds=extra_cmds):
                for hook in entry.get("hooks", []):
                    cmd = hook.get("command", "")
                    if "cc-hook-indicator" in cmd or resolved_cmd in cmd:
                        return True
                    if "cc-activity-log" in cmd or resolved_log_cmd in cmd:
                        return True
                    if cmd in extra_cmds:
                        return True
                return False

            installed_managed = [
                entry for entry in installed_entries if _is_managed(entry)
            ]

            def _normalize(entries):
                entries = copy.deepcopy(entries)
                for entry in entries:
                    for hook in entry.get("hooks", []):
                        cmd = hook.get("command", "")
                        if resolved_cmd != "cc-hook-indicator" and resolved_cmd in cmd:
                            cmd = cmd.replace(resolved_cmd, "cc-hook-indicator")
                        if (
                            resolved_log_cmd != "cc-activity-log"
                            and resolved_log_cmd in cmd
                        ):
                            cmd = cmd.replace(resolved_log_cmd, "cc-activity-log")
                        hook["command"] = cmd
                return entries

            if _normalize(installed_managed) != _normalize(effective_entries):
                return "drifted"

        return "in_sync"

    return "missing"


def _detect_all_drift() -> dict[str, str]:
    """Return {account_name: status} for all configured accounts."""
    return {name: _detect_drift(name) for name in _state._account_names}
