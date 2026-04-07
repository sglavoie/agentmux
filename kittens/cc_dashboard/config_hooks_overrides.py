"""Override mutation helpers for dashboard hooks configuration."""

from __future__ import annotations

import contextlib
from typing import Any

import cc_dashboard.config_state as _state
from cc_dashboard.config_hooks_store import load_hooks, save_hooks


def _get_extra_hooks(override: dict[str, Any]) -> dict[str, Any]:
    """Read extra_hooks from an override dict, falling back to extra_commands.

    The old format stored bare hook handler dicts under extra_commands:
        {"event": [{"type": "command", "command": "...", "async": true}]}
    The new format stores matcher-group objects under extra_hooks:
        {"event": [{"matcher": "...", "hooks": [{...}]}]}
    This helper reads extra_hooks first, then falls back to extra_commands
    and wraps each bare handler into a matcher-group.
    """
    if "extra_hooks" in override:
        result: dict[str, Any] = override["extra_hooks"]
        return result
    old = override.get("extra_commands", {})
    if not old:
        return {}
    converted: dict[str, Any] = {}
    for event, handlers in old.items():
        converted[event] = [{"hooks": [h]} for h in handlers]
    return converted


def toggle_hook_event(account_name: str, event_name: str) -> bool:
    """Toggle a hook event for an account.

    Returns True if the event is now enabled, False if disabled.
    """
    if not _state._hooks_global:
        load_hooks()

    if account_name not in _state._hooks_overrides:
        _state._hooks_overrides[account_name] = {}
    override = _state._hooks_overrides[account_name]

    if "disabled_events" not in override:
        override["disabled_events"] = []

    disabled = override["disabled_events"]
    if event_name in disabled:
        disabled.remove(event_name)
        enabled = True
    else:
        disabled.append(event_name)
        enabled = False

    if not disabled:
        del override["disabled_events"]
    if not override:
        del _state._hooks_overrides[account_name]

    save_hooks(_state._hooks_global, _state._hooks_overrides)
    return enabled


def add_extra_hook(
    account_name: str,
    event_name: str,
    handler: dict[str, Any],
    matcher: str | None = None,
) -> None:
    """Add an extra hook for an account and event."""
    if not _state._hooks_global:
        load_hooks()

    if account_name not in _state._hooks_overrides:
        _state._hooks_overrides[account_name] = {}
    override = _state._hooks_overrides[account_name]

    if "extra_commands" in override and "extra_hooks" not in override:
        override["extra_hooks"] = _get_extra_hooks(override)
        del override["extra_commands"]

    if "extra_hooks" not in override:
        override["extra_hooks"] = {}
    if event_name not in override["extra_hooks"]:
        override["extra_hooks"][event_name] = []

    group: dict[str, Any] = {"hooks": [handler]}
    if matcher:
        group["matcher"] = matcher
    override["extra_hooks"][event_name].append(group)

    save_hooks(_state._hooks_global, _state._hooks_overrides)


def update_extra_hook(
    account_name: str,
    event_name: str,
    group_idx: int,
    new_handler: dict[str, Any],
    new_matcher: str | None = None,
) -> None:
    """Replace an extra hook group at a given index."""
    if not _state._hooks_global:
        load_hooks()

    override = _state._hooks_overrides.get(account_name, {})
    if "extra_commands" in override and "extra_hooks" not in override:
        override["extra_hooks"] = _get_extra_hooks(override)
        del override["extra_commands"]

    extras = override.get("extra_hooks", {}).get(event_name, [])
    if group_idx < 0 or group_idx >= len(extras):
        return

    group: dict[str, Any] = {"hooks": [new_handler]}
    if new_matcher:
        group["matcher"] = new_matcher
    extras[group_idx] = group

    save_hooks(_state._hooks_global, _state._hooks_overrides)


def remove_extra_hook(account_name: str, event_name: str, group_idx: int) -> None:
    """Remove an extra hook group by index for an account and event."""
    if not _state._hooks_global:
        load_hooks()

    override = _state._hooks_overrides.get(account_name, {})

    if "extra_commands" in override and "extra_hooks" not in override:
        override["extra_hooks"] = _get_extra_hooks(override)
        del override["extra_commands"]

    extras = override.get("extra_hooks", {}).get(event_name, [])
    if group_idx < 0 or group_idx >= len(extras):
        return

    extras.pop(group_idx)

    if not extras:
        del override["extra_hooks"][event_name]
    if not override.get("extra_hooks"):
        with contextlib.suppress(KeyError):
            del override["extra_hooks"]
    if not override:
        with contextlib.suppress(KeyError):
            del _state._hooks_overrides[account_name]

    save_hooks(_state._hooks_global, _state._hooks_overrides)
