"""Shared helpers for hooks overlay views."""

from typing import Any, Literal, cast

type HookHandler = dict[str, Any]
type HooksEventHeaderRow = tuple[Literal["event"], str, bool]
type HooksExtraRow = tuple[Literal["extra"], str, int, str, HookHandler, str | None]
type HooksBuiltinRow = tuple[Literal["builtin"], str, int, str, bool, bool, str]
type HooksEventRow = HooksEventHeaderRow | HooksExtraRow | HooksBuiltinRow

_DRIFT_BADGES = {
    "in_sync": ("synced", 4),
    "drifted": ("drifted", 7),
    "missing": ("missing", 7),
}

_BUILTIN_DESCRIPTIONS = {
    "cc-hook-indicator active prompt": "Sets tab icon to active (prompt)",
    "cc-hook-indicator active tool": "Sets tab icon to active (tool)",
    "cc-hook-indicator active task_complete": "Sets tab icon to active (task done)",
    "cc-hook-indicator active": "Sets tab icon to active",
    "cc-hook-indicator stop": "Sets tab stop icon",
    "cc-hook-indicator permission": "Sets tab permission icon",
    "cc-hook-indicator failure": "Sets tab error icon",
    "cc-activity-log tool": "Logs tool use to activity file",
    "cc-activity-log stop": "Logs stop to activity file",
    "cc-activity-log end": "Logs session end to activity file",
}


def _describe_builtin(cmd):
    """Return a human-readable description for a known builtin command."""
    for prefix, desc in _BUILTIN_DESCRIPTIONS.items():
        if cmd.startswith(prefix):
            return desc
    return ""


def _extract_builtin_commands(hooks_global, event_name):
    """Extract command strings with metadata from hooks_global for a given event."""
    commands = []
    for entry in hooks_global.get(event_name, []):
        is_async = entry.get("async", False)
        type_str = entry.get("type", "")
        for hook in entry.get("hooks", []):
            cmd = hook.get("command", "")
            if cmd:
                parts = cmd.split()
                if parts:
                    parts[0] = parts[0].rsplit("/", 1)[-1]
                    commands.append((" ".join(parts), is_async, type_str))
    return commands


def _hooks_event_rows(account_name, hooks_overrides, hooks_global=None):
    """Build a flat list of rows for the hooks event detail view."""
    from cc_dashboard.config import HOOK_EVENTS, _get_extra_hooks

    override = hooks_overrides.get(account_name, {})
    disabled_events = override.get("disabled_events", [])
    extra_hooks = _get_extra_hooks(override)

    result: list[HooksEventRow] = []
    for event in HOOK_EVENTS:
        is_disabled = event in disabled_events
        result.append(("event", event, is_disabled))
        if hooks_global is not None:
            for i, (cmd, is_async, type_str) in enumerate(
                _extract_builtin_commands(hooks_global, event)
            ):
                result.append(
                    ("builtin", event, i, cmd, is_disabled, is_async, type_str)
                )
        for i, group in enumerate(extra_hooks.get(event, [])):
            hooks_list = group.get("hooks", [])
            handler = cast(HookHandler, hooks_list[0] if hooks_list else {})
            matcher = group.get("matcher")
            hook_type = handler.get("type", "command")
            if hook_type == "command":
                cmd_str = str(handler.get("command", ""))
            elif hook_type == "http":
                cmd_str = str(handler.get("url", ""))
            else:
                cmd_str = str(handler.get("prompt", ""))
            result.append(("extra", event, i, cmd_str, handler, matcher))
    return result


def _as_event_row(row: HooksEventRow) -> HooksEventHeaderRow:
    return cast(HooksEventHeaderRow, row)


def _as_builtin_row(row: HooksEventRow) -> HooksBuiltinRow:
    return cast(HooksBuiltinRow, row)


def _as_extra_row(row: HooksEventRow) -> HooksExtraRow:
    return cast(HooksExtraRow, row)


__all__ = [
    "HookHandler",
    "HooksEventRow",
    "_DRIFT_BADGES",
    "_as_builtin_row",
    "_as_event_row",
    "_as_extra_row",
    "_describe_builtin",
    "_hooks_event_rows",
]
