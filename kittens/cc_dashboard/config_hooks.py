"""Hooks configuration helpers for dashboard."""

import contextlib
import copy
import json
import os
import shutil

import cc_dashboard.config_core as _core
import cc_dashboard.config_state as _state

HOOK_EVENTS = [
    "SessionStart",
    "UserPromptSubmit",
    "PreToolUse",
    "PermissionRequest",
    "PostToolUse",
    "PostToolUseFailure",
    "Notification",
    "SubagentStart",
    "SubagentStop",
    "Stop",
    "TeammateIdle",
    "TaskCompleted",
    "ConfigChange",
    "WorktreeCreate",
    "WorktreeRemove",
    "PreCompact",
    "SessionEnd",
]

HOOK_EVENT_META = {
    "SessionStart": {
        "abbr": "Strt",
        "desc": "Fires when a session starts or resumes",
        "matcher_desc": "session start type",
        "matcher_examples": "startup|resume|clear|compact",
    },
    "UserPromptSubmit": {
        "abbr": "Pmpt",
        "desc": "Fires when the user submits a prompt",
        "matcher_desc": None,
        "matcher_examples": None,
    },
    "PreToolUse": {
        "abbr": "PreT",
        "desc": "Fires before a tool executes",
        "matcher_desc": "tool name",
        "matcher_examples": (
            "Bash|Edit|Write|Read|Glob|Grep|Agent|WebFetch|WebSearch|mcp__.*"
        ),
    },
    "PermissionRequest": {
        "abbr": "Perm",
        "desc": "Fires when a tool requests permission",
        "matcher_desc": "tool name",
        "matcher_examples": "Bash|Edit|Write|Read|Glob|Grep|Agent|WebFetch|WebSearch",
    },
    "PostToolUse": {
        "abbr": "Tool",
        "desc": "Fires after a tool executes successfully",
        "matcher_desc": "tool name",
        "matcher_examples": "Bash|Edit|Write|Read|Glob|Grep|Agent|WebFetch|WebSearch",
    },
    "PostToolUseFailure": {
        "abbr": "Fail",
        "desc": "Fires after a tool execution fails",
        "matcher_desc": "tool name",
        "matcher_examples": "Bash|Edit|Write|Read|Glob|Grep|Agent|WebFetch|WebSearch",
    },
    "Notification": {
        "abbr": "Ntfy",
        "desc": "Fires on permission or idle notifications",
        "matcher_desc": "notification type",
        "matcher_examples": (
            "permission_prompt|idle_prompt|auth_success|elicitation_dialog"
        ),
    },
    "SubagentStart": {
        "abbr": "AgSt",
        "desc": "Fires when a subagent starts",
        "matcher_desc": "agent type",
        "matcher_examples": "Bash|Explore|Plan",
    },
    "SubagentStop": {
        "abbr": "AgSp",
        "desc": "Fires when a subagent stops",
        "matcher_desc": "agent type",
        "matcher_examples": "Bash|Explore|Plan",
    },
    "Stop": {
        "abbr": "Stop",
        "desc": "Fires when Claude stops generating",
        "matcher_desc": None,
        "matcher_examples": None,
    },
    "TeammateIdle": {
        "abbr": "TmId",
        "desc": "Fires when a teammate becomes idle",
        "matcher_desc": None,
        "matcher_examples": None,
    },
    "TaskCompleted": {
        "abbr": "Task",
        "desc": "Fires when a task is completed",
        "matcher_desc": None,
        "matcher_examples": None,
    },
    "ConfigChange": {
        "abbr": "Conf",
        "desc": "Fires when settings change",
        "matcher_desc": "config source",
        "matcher_examples": (
            "user_settings|project_settings|local_settings|policy_settings|skills"
        ),
    },
    "WorktreeCreate": {
        "abbr": "WkCr",
        "desc": "Fires when a worktree is created",
        "matcher_desc": None,
        "matcher_examples": None,
    },
    "WorktreeRemove": {
        "abbr": "WkRm",
        "desc": "Fires when a worktree is removed",
        "matcher_desc": None,
        "matcher_examples": None,
    },
    "PreCompact": {
        "abbr": "Cmpt",
        "desc": "Fires before context compaction",
        "matcher_desc": "trigger type",
        "matcher_examples": "manual|auto",
    },
    "SessionEnd": {
        "abbr": "End",
        "desc": "Fires when a session ends",
        "matcher_desc": "exit reason",
        "matcher_examples": (
            "clear|logout|prompt_input_exit|bypass_permissions_disabled|other"
        ),
    },
}

_FULL_TYPE_EVENTS = {
    "PermissionRequest",
    "PostToolUse",
    "PostToolUseFailure",
    "PreToolUse",
    "Stop",
    "SubagentStop",
    "TaskCompleted",
    "UserPromptSubmit",
}


def _resolve_hooks_example_path():
    """Check repo layout then installed layout for hooks.example.json"""
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


def load_hooks():
    """Parse hooks.json into the shared hooks dicts.

    Falls back to hooks.example.json if hooks.json is missing.
    Returns (_hooks_global, _hooks_overrides).
    """
    _state._hooks_global = {}
    _state._hooks_overrides = {}

    path = _core._hooks_config_path()
    if not os.path.isfile(path):
        path = _resolve_hooks_example_path()
    if not path or not os.path.isfile(path):
        return _state._hooks_global, _state._hooks_overrides

    with contextlib.suppress(json.JSONDecodeError, OSError):
        with open(path) as f:
            data = json.load(f)
        _state._hooks_global = data.get("hooks", {})
        _state._hooks_overrides = data.get("account_overrides", {})

    return _state._hooks_global, _state._hooks_overrides


def _resolve_hook_cmd():
    """Resolve the absolute path to cc-hook-indicator, with caching"""
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


def _resolve_log_cmd():
    """Resolve the absolute path to cc-activity-log, with caching"""
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


def _load_example_hooks():
    """Load the base hooks from hooks.example.json (what install-hooks.sh uses)."""
    path = _resolve_hooks_example_path()
    if not path:
        return {}
    with contextlib.suppress(json.JSONDecodeError, OSError), open(path) as f:
        return json.load(f).get("hooks", {})
    return {}


def _compute_effective_hooks(account_name, base_hooks=None):
    """Compute the effective hooks for an account after applying overrides.

    Uses base_hooks as the starting point (defaults to _hooks_global).
    Returns a dict of {event_name: [hook_entries]} with cc-hook-indicator
    paths resolved to their absolute location.
    """
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
                    h
                    for h in inner_hooks
                    if "cc-hook-indicator" not in h.get("command", "")
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


def _detect_drift(account_name):
    """Compare installed settings.json hooks against computed effective hooks.

    Returns "missing", "drifted", or "in_sync".
    """
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

            installed_managed = [e for e in installed_entries if _is_managed(e)]

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


def _detect_all_drift():
    """Return {account_name: status} for all configured accounts"""
    return {name: _detect_drift(name) for name in _state._account_names}


def save_hooks(global_hooks, account_overrides):
    """Write hooks configuration to hooks.json.

    Creates a .bak copy before overwriting, following the same pattern as
    save_sessions.
    """
    path = _core._hooks_config_path()
    if os.path.isfile(path):
        shutil.copy2(path, path + ".bak")

    with open(path, "w") as f:
        json.dump(
            {"hooks": global_hooks, "account_overrides": account_overrides},
            f,
            indent=2,
        )
        f.write("\n")

    _state._hooks_global = global_hooks
    _state._hooks_overrides = account_overrides


def toggle_hook_event(account_name, event_name):
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


def _get_extra_hooks(override):
    """Read extra_hooks from an override dict, falling back to extra_commands.

    The old format stored bare hook handler dicts under extra_commands:
        {"event": [{"type": "command", "command": "...", "async": true}]}
    The new format stores matcher-group objects under extra_hooks:
        {"event": [{"matcher": "...", "hooks": [{...}]}]}
    This helper reads extra_hooks first, then falls back to extra_commands
    and wraps each bare handler into a matcher-group.
    """
    if "extra_hooks" in override:
        return override["extra_hooks"]
    old = override.get("extra_commands", {})
    if not old:
        return {}
    converted = {}
    for event, handlers in old.items():
        converted[event] = [{"hooks": [h]} for h in handlers]
    return converted


def add_extra_hook(account_name, event_name, handler, matcher=None):
    """Add an extra hook for an account and event.

    handler is a dict like {"type": "command", "command": "...", "async": true}.
    matcher is an optional string for tool/event matching.
    """
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

    group = {"hooks": [handler]}
    if matcher:
        group["matcher"] = matcher
    override["extra_hooks"][event_name].append(group)

    save_hooks(_state._hooks_global, _state._hooks_overrides)


def update_extra_hook(
    account_name, event_name, group_idx, new_handler, new_matcher=None
):
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

    group = {"hooks": [new_handler]}
    if new_matcher:
        group["matcher"] = new_matcher
    extras[group_idx] = group

    save_hooks(_state._hooks_global, _state._hooks_overrides)


def remove_extra_hook(account_name, event_name, group_idx):
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
