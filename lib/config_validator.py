#!/usr/bin/env python3
"""Validate cc-manager config files.

Checks accounts.toml, sessions.toml, defaults.toml, and hooks.json for
syntax errors, missing required fields, invalid values, and cross-file
consistency (e.g. sessions referencing non-existent accounts).

Exit codes: 0 = all OK (warnings are fine), 1 = errors found.
"""

from __future__ import annotations

import json
import os
import sys
import tomllib

from data_loader import load_hook_events, load_palette_colors, load_sort_modes


def _use_color() -> bool:
    """Return True if stderr is a terminal (for colored output)."""
    return hasattr(sys.stderr, "isatty") and sys.stderr.isatty()


_COLOR: bool = _use_color()


def _green(s: str) -> str:
    return f"\033[32m{s}\033[0m" if _COLOR else s


def _yellow(s: str) -> str:
    return f"\033[33m{s}\033[0m" if _COLOR else s


def _red(s: str) -> str:
    return f"\033[31m{s}\033[0m" if _COLOR else s


def _load_palette_colors_for_dir(data_dir: str) -> frozenset[str] | None:
    """Load valid palette color names, preferring *data_dir* then standard paths."""
    return load_palette_colors(data_dir)


def _load_hook_events_for_dir(data_dir: str) -> set[str]:
    """Load valid hook event names, preferring *data_dir* then standard paths."""
    return load_hook_events(data_dir)


def _load_sort_modes_for_dir(data_dir: str) -> set[str]:
    """Load valid sort mode names, preferring *data_dir* then standard paths."""
    return load_sort_modes(data_dir)


class Validator:
    """Accumulates errors and warnings for a single config file."""

    def __init__(self, label: str) -> None:
        self.label: str = label
        self.errors: list[str] = []
        self.warnings: list[str] = []

    def error(self, msg: str) -> None:
        self.errors.append(msg)

    def warn(self, msg: str) -> None:
        self.warnings.append(msg)

    def summary_line(self) -> str:
        parts: list[str] = []
        if self.errors:
            parts.append(_red(f"{len(self.errors)} error(s)"))
        if self.warnings:
            parts.append(_yellow(f"{len(self.warnings)} warning(s)"))
        status = ", ".join(parts) if parts else _green("OK")
        return f"  {self.label} ... {status}"

    def detail_lines(self) -> list[str]:
        lines: list[str] = []
        for e in self.errors:
            lines.append(f"    {_red('ERROR')}: {e}")
        for w in self.warnings:
            lines.append(f"    {_yellow('WARN')}:  {w}")
        return lines


def validate_accounts(
    path: str, palette_colors: frozenset[str] | set[str] | None
) -> tuple[Validator, set[str]]:
    """Validate accounts.toml. Returns (Validator, set of account names)."""
    v = Validator("accounts.toml")
    account_names: set[str] = set()

    if not os.path.isfile(path):
        v.warn("file not found (run 'ccm config init' to create it)")
        return v, account_names

    try:
        with open(path, "rb") as f:
            data = tomllib.load(f)
    except tomllib.TOMLDecodeError as e:
        v.error(f"invalid TOML syntax: {e}")
        return v, account_names

    accounts = data.get("accounts")
    if accounts is None:
        v.warn("no [[accounts]] entries found")
        return v, account_names

    if not isinstance(accounts, list):
        v.error("'accounts' must be an array of tables ([[accounts]])")
        return v, account_names

    known_account_keys = {"name", "config_dir", "color"}

    for i, entry in enumerate(accounts, start=1):
        unknown = set(entry.keys()) - known_account_keys
        if unknown:
            label = entry.get("name", f"entry {i}")
            v.warn(f"account '{label}': unknown key(s): {', '.join(sorted(unknown))}")

        name = entry.get("name", "")
        if not name:
            v.error(f"entry {i}: missing or empty 'name'")
            continue
        if not isinstance(name, str):
            v.error(f"entry {i}: 'name' must be a string")
            continue
        config_dir = entry.get("config_dir", "")
        if not isinstance(config_dir, str):
            v.error(f"account '{name}': 'config_dir' must be a string")
        elif config_dir and not os.path.isdir(os.path.expanduser(config_dir)):
            v.warn(f"account '{name}': config_dir '{config_dir}' does not exist")
        color = entry.get("color", "gray")
        if not isinstance(color, str):
            v.error(f"account '{name}': 'color' must be a string")
        elif palette_colors is not None and color not in palette_colors:
            v.error(f"account '{name}': unknown color '{color}' "
                    f"(valid: {', '.join(sorted(palette_colors))})")
        if name in account_names:
            v.error(f"duplicate account name '{name}'")
        account_names.add(name)

    return v, account_names


def validate_sessions(path: str, account_names: set[str]) -> Validator:
    """Validate sessions.toml."""
    v = Validator("sessions.toml")

    if not os.path.isfile(path):
        v.warn("file not found (run 'ccm config init' to create it)")
        return v

    try:
        with open(path, "rb") as f:
            data = tomllib.load(f)
    except tomllib.TOMLDecodeError as e:
        v.error(f"invalid TOML syntax: {e}")
        return v

    sessions = data.get("sessions")
    if sessions is None:
        v.warn("no [[sessions]] entries found")
        return v

    if not isinstance(sessions, list):
        v.error("'sessions' must be an array of tables ([[sessions]])")
        return v

    valid_pw = {"", "-", "stack", "vsplit", "hsplit", "split"}
    known_session_keys = {
        "title", "directory", "account", "flags",
        "project_window", "env", "group",
    }
    seen_titles = set()

    for i, entry in enumerate(sessions, start=1):
        unknown = set(entry.keys()) - known_session_keys
        if unknown:
            label = entry.get("title", f"entry {i}")
            v.warn(f"session '{label}': unknown key(s): {', '.join(sorted(unknown))}")

        title = entry.get("title", "")
        directory = entry.get("directory", "")

        if not title:
            v.error(f"entry {i}: missing or empty 'title'")
        if not directory:
            v.error(f"entry {i}: missing or empty 'directory'")
        elif not os.path.isdir(os.path.expanduser(directory)):
            label = title or f"entry {i}"
            v.warn(f"session '{label}': directory "
                   f"'{directory}' does not exist")
        if not title:
            continue

        if title in seen_titles:
            v.warn(f"duplicate session title '{title}'")
        seen_titles.add(title)

        account = entry.get("account", "")
        if account and account_names and account not in account_names:
            v.error(f"session '{title}': references unknown account '{account}'")

        pw = entry.get("project_window", "")
        if pw and pw not in valid_pw:
            v.error(f"session '{title}': invalid project_window '{pw}' "
                    f"(valid: -, stack, vsplit, hsplit, split)")

        # 1a: Warn if env values contain spaces (breaks Claude invocations)
        env = entry.get("env", "")
        if env and env != "-":
            for pair in env.split():
                if "=" not in pair:
                    label = title or f"entry {i}"
                    v.warn(f"session '{label}': env token '{pair}' "
                           f"is not a KEY=VALUE pair")

    # 1b: Warn about group names used by only one session (possible typos)
    group_counts: dict[str, int] = {}
    for entry in sessions:
        g = entry.get("group", "")
        if g:
            group_counts[g] = group_counts.get(g, 0) + 1
    if len(group_counts) >= 2:
        for g, count in group_counts.items():
            if count == 1:
                v.warn(f"group '{g}' is used by only 1 session "
                       f"— possible typo?")

    return v


def validate_defaults(path: str, valid_sort: set[str] | None = None) -> Validator:
    """Validate defaults.toml."""
    v = Validator("defaults.toml")

    if not os.path.isfile(path):
        v.warn("file not found (run 'ccm config init' to create it)")
        return v

    try:
        with open(path, "rb") as f:
            data = tomllib.load(f)
    except tomllib.TOMLDecodeError as e:
        v.error(f"invalid TOML syntax: {e}")
        return v

    defaults = data.get("defaults", {})
    notifications = data.get("notifications", {})

    known_defaults_keys = {"claude_flags", "project_window", "sort_sessions",
                           "dashboard_refresh", "tab_statuses",
                           "notes_editor", "kitty_timeout",
                           "idle_poll_interval"}
    unknown_defaults = set(defaults.keys()) - known_defaults_keys
    if unknown_defaults:
        v.warn(f"[defaults]: unknown key(s): {', '.join(sorted(unknown_defaults))}")

    known_notif_keys = {"error", "permission", "stop", "idle",
                        "idle_alert_minutes", "min_secs", "ignore_patterns",
                        "dim_after_minutes", "idle_poll_interval"}
    unknown_notif = set(notifications.keys()) - known_notif_keys
    if unknown_notif:
        v.warn(f"[notifications]: unknown key(s): {', '.join(sorted(unknown_notif))}")

    # Check for unknown top-level sections
    known_sections = {"defaults", "notifications"}
    unknown_sections = set(data.keys()) - known_sections
    if unknown_sections:
        v.warn(f"unknown top-level section(s): {', '.join(sorted(unknown_sections))}")

    # Validate [defaults] section
    sort = defaults.get("sort_sessions")
    if valid_sort is None:
        valid_sort = {"default", "alpha", "name", "account",
                      "group", "status", "idle", "activity"}
    valid_sort = valid_sort | {""}
    if sort is not None and sort not in valid_sort:
        v.error(f"sort_sessions must be one of "
                f"{', '.join(sorted(valid_sort - {''}))} or omitted, "
                f"got '{sort}'")

    pw = defaults.get("project_window")
    valid_pw = {"", "-", "stack", "vsplit", "hsplit", "split"}
    if pw is not None and pw not in valid_pw:
        v.error(f"project_window '{pw}' is invalid "
                f"(valid: -, stack, vsplit, hsplit, split)")

    refresh = defaults.get("dashboard_refresh")
    if refresh is not None:
        if isinstance(refresh, bool) or not isinstance(refresh, int):
            v.error("dashboard_refresh must be an integer")
        elif refresh < 0:
            v.error("dashboard_refresh must be non-negative")

    kitty_timeout = defaults.get("kitty_timeout")
    if kitty_timeout is not None:
        if isinstance(kitty_timeout, bool) or not isinstance(kitty_timeout, int):
            v.error("kitty_timeout must be an integer")
        elif kitty_timeout < 1:
            v.error("kitty_timeout must be at least 1")
        elif kitty_timeout > 300:
            v.error("kitty_timeout must be at most 300")

    idle_poll = defaults.get("idle_poll_interval")
    if idle_poll is not None:
        if isinstance(idle_poll, bool) or not isinstance(idle_poll, int):
            v.error("idle_poll_interval must be an integer")
        elif idle_poll < 5:
            v.error("idle_poll_interval must be at least 5 (seconds)")
        elif idle_poll > 3600:
            v.error("idle_poll_interval must be at most 3600 (seconds)")

    # Validate [notifications] section
    bool_keys = ("error", "permission", "stop", "idle")
    for key in bool_keys:
        val = notifications.get(key)
        if val is not None and not isinstance(val, bool):
            v.error(f"notifications.{key} must be a boolean, got {type(val).__name__}")

    for key in ("idle_alert_minutes", "min_secs"):
        val = notifications.get(key)
        if val is not None:
            if isinstance(val, bool) or not isinstance(val, (int, float)):
                v.error(f"notifications.{key} must be a number")
            elif val < 0:
                v.error(f"notifications.{key} must be non-negative")

    patterns = notifications.get("ignore_patterns")
    if patterns is not None:
        if not isinstance(patterns, list):
            v.error("notifications.ignore_patterns must be an array")
        elif not all(isinstance(p, str) for p in patterns):
            v.error("notifications.ignore_patterns entries must be strings")
        else:
            import re

            for p in patterns:
                try:
                    re.compile(p)
                except re.error as e:
                    v.error(
                        f"notifications.ignore_patterns: invalid regex {p!r}: {e}"
                    )

    statuses = defaults.get("tab_statuses")
    if statuses is not None:
        if not isinstance(statuses, list):
            v.error("tab_statuses must be an array")
        elif not all(isinstance(s, str) for s in statuses):
            v.error("tab_statuses entries must be strings")

    return v


def validate_hooks(
    path: str, valid_events: set[str], account_names: set[str]
) -> Validator:
    """Validate hooks.json."""
    v = Validator("hooks.json")

    if not os.path.isfile(path):
        v.warn("file not found (run 'ccm config init' to create it)")
        return v

    try:
        with open(path) as f:
            data = json.load(f)
    except json.JSONDecodeError as e:
        v.error(f"invalid JSON syntax: {e}")
        return v

    if not isinstance(data, dict):
        v.error("top-level value must be a JSON object")
        return v

    # Validate hooks key
    hooks = data.get("hooks")
    if hooks is None:
        v.error("missing required 'hooks' key")
    elif not isinstance(hooks, dict):
        v.error("'hooks' must be an object")
    else:
        if valid_events:
            for event_name in hooks:
                if event_name not in valid_events:
                    v.warn(f"unknown hook event '{event_name}' "
                           f"(not in icons.json hook_events)")

    # Validate account_overrides
    overrides = data.get("account_overrides")
    if overrides is not None:
        if not isinstance(overrides, dict):
            v.error("'account_overrides' must be an object")
        else:
            for acct, override in overrides.items():
                if account_names and acct not in account_names:
                    v.warn(f"account_overrides: unknown account '{acct}'")
                if not isinstance(override, dict):
                    v.error(f"account_overrides.{acct} must be an object")
                    continue
                disabled = override.get("disabled_events")
                if disabled is not None:
                    if not isinstance(disabled, list):
                        v.error(f"account_overrides.{acct}"
                                ".disabled_events must be an array")
                    elif valid_events:
                        for ev in disabled:
                            if ev not in valid_events:
                                v.warn(f"account_overrides.{acct}: "
                                       f"unknown disabled event '{ev}'")
                extra = override.get("extra_commands") or override.get("extra_hooks")
                if extra is not None:
                    if not isinstance(extra, dict):
                        v.error(f"account_overrides.{acct}"
                                ".extra_commands must be an object")
                    elif valid_events:
                        for ev in extra:
                            if ev not in valid_events:
                                v.warn(f"account_overrides.{acct}: "
                                       f"unknown extra_commands event '{ev}'")

    return v


def quick_validate(config_dir: str, data_dir: str) -> list[tuple[str, str]]:
    """Run validation and return a list of (level, message) tuples.

    level is 'error' or 'warning'.  Intended for programmatic callers
    (e.g. the dashboard) that want to surface config issues without
    printing directly to stderr.
    """
    palette_colors = load_palette_colors(data_dir)
    valid_events = load_hook_events(data_dir)
    valid_sort = load_sort_modes(data_dir)

    accounts_path = os.environ.get(
        "CC_MANAGER_ACCOUNTS", os.path.join(config_dir, "accounts.toml")
    )
    sessions_path = os.environ.get(
        "CC_MANAGER_CONFIG", os.path.join(config_dir, "sessions.toml")
    )
    defaults_path = os.environ.get(
        "CC_MANAGER_DEFAULTS", os.path.join(config_dir, "defaults.toml")
    )
    hooks_path = os.path.join(config_dir, "hooks.json")

    v_accounts, account_names = validate_accounts(accounts_path, palette_colors)
    v_sessions = validate_sessions(sessions_path, account_names)
    v_defaults = validate_defaults(defaults_path, valid_sort)
    v_hooks = validate_hooks(hooks_path, valid_events, account_names)

    issues = []
    for v in [v_accounts, v_sessions, v_defaults, v_hooks]:
        for e in v.errors:
            issues.append(("error", f"{v.label}: {e}"))
        for w in v.warnings:
            issues.append(("warning", f"{v.label}: {w}"))
    return issues


def main() -> None:
    if len(sys.argv) < 3:
        print(f"Usage: {sys.argv[0]} CONFIG_DIR DATA_DIR", file=sys.stderr)
        sys.exit(2)

    config_dir = sys.argv[1]
    data_dir = sys.argv[2]

    palette_colors = _load_palette_colors_for_dir(data_dir)
    if palette_colors is None:
        print(_yellow("  WARN") + ": could not load palette.json; "
              "skipping color validation", file=sys.stderr)
    valid_events = _load_hook_events_for_dir(data_dir)
    valid_sort = _load_sort_modes_for_dir(data_dir)

    accounts_path = os.environ.get("CC_MANAGER_ACCOUNTS",
                                   os.path.join(config_dir, "accounts.toml"))
    sessions_path = os.environ.get("CC_MANAGER_CONFIG",
                                   os.path.join(config_dir, "sessions.toml"))
    defaults_path = os.environ.get("CC_MANAGER_DEFAULTS",
                                   os.path.join(config_dir, "defaults.toml"))
    hooks_path = os.path.join(config_dir, "hooks.json")

    v_accounts, account_names = validate_accounts(accounts_path, palette_colors)
    v_sessions = validate_sessions(sessions_path, account_names)
    v_defaults = validate_defaults(defaults_path, valid_sort)
    v_hooks = validate_hooks(hooks_path, valid_events, account_names)

    validators = [v_accounts, v_sessions, v_defaults, v_hooks]

    print("Validating cc-manager configuration:\n")
    for v in validators:
        print(v.summary_line())
        for line in v.detail_lines():
            print(line)

    total_errors = sum(len(v.errors) for v in validators)
    total_warnings = sum(len(v.warnings) for v in validators)

    print()
    if total_errors:
        print(_red(f"Found {total_errors} error(s)") +
              (f" and {total_warnings} warning(s)." if total_warnings else "."))
        sys.exit(1)
    elif total_warnings:
        print(_yellow(f"All clear with {total_warnings} warning(s)."))
    else:
        print(_green("All configuration files are valid."))


if __name__ == "__main__":
    main()
