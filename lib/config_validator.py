#!/usr/bin/env python3
"""Validate cc-manager config files.

Checks accounts.toml, sessions.toml, defaults.toml, and hooks.json for
syntax errors, missing required fields, invalid values, and cross-file
consistency (e.g. sessions referencing non-existent accounts).

Exit codes: 0 = all OK (warnings are fine), 1 = errors found.
"""

import json
import os
import sys
import tomllib


def _use_color():
    """Return True if stderr is a terminal (for colored output)."""
    return hasattr(sys.stderr, "isatty") and sys.stderr.isatty()


_COLOR = _use_color()


def _green(s):
    return f"\033[32m{s}\033[0m" if _COLOR else s


def _yellow(s):
    return f"\033[33m{s}\033[0m" if _COLOR else s


def _red(s):
    return f"\033[31m{s}\033[0m" if _COLOR else s


def _load_palette_colors(data_dir):
    """Load valid palette color names from palette.json."""
    hardcoded = {"green", "blue", "red", "orange", "purple",
                 "teal", "pink", "yellow", "gray", "cyan"}
    candidates = [
        os.path.join(data_dir, "palette.json"),
        os.path.expanduser("~/.local/lib/cc-manager/palette.json"),
    ]
    for path in candidates:
        try:
            with open(path) as f:
                data = json.load(f)
            return set(data["colors"].keys())
        except (FileNotFoundError, OSError, json.JSONDecodeError, KeyError, TypeError):
            continue
    return hardcoded


def _load_icons_json(data_dir):
    """Load and return the parsed icons.json data, or None on failure."""
    candidates = [
        os.path.join(data_dir, "icons.json"),
        os.path.expanduser("~/.local/lib/cc-manager/icons.json"),
    ]
    for path in candidates:
        try:
            with open(path) as f:
                return json.load(f)
        except (FileNotFoundError, OSError, json.JSONDecodeError):
            continue
    return None


def _load_hook_events(data_dir):
    """Load valid hook event names from icons.json."""
    data = _load_icons_json(data_dir)
    if data:
        try:
            return set(data["hook_events"])
        except (KeyError, TypeError):
            pass
    return set()


def _load_sort_modes(data_dir):
    """Load valid sort mode names from icons.json."""
    data = _load_icons_json(data_dir)
    if data:
        try:
            return set(data["sort_modes"])
        except (KeyError, TypeError):
            pass
    return {"default", "alpha", "name", "account",
            "group", "status", "idle", "activity"}


class Validator:
    """Accumulates errors and warnings for a single config file."""

    def __init__(self, label):
        self.label = label
        self.errors = []
        self.warnings = []

    def error(self, msg):
        self.errors.append(msg)

    def warn(self, msg):
        self.warnings.append(msg)

    def summary_line(self):
        parts = []
        if self.errors:
            parts.append(_red(f"{len(self.errors)} error(s)"))
        if self.warnings:
            parts.append(_yellow(f"{len(self.warnings)} warning(s)"))
        status = ", ".join(parts) if parts else _green("OK")
        return f"  {self.label} ... {status}"

    def detail_lines(self):
        lines = []
        for e in self.errors:
            lines.append(f"    {_red('ERROR')}: {e}")
        for w in self.warnings:
            lines.append(f"    {_yellow('WARN')}:  {w}")
        return lines


def validate_accounts(path, palette_colors):
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

    for i, entry in enumerate(accounts, start=1):
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
        color = entry.get("color", "gray")
        if not isinstance(color, str):
            v.error(f"account '{name}': 'color' must be a string")
        elif color not in palette_colors:
            v.error(f"account '{name}': unknown color '{color}' "
                    f"(valid: {', '.join(sorted(palette_colors))})")
        if name in account_names:
            v.error(f"duplicate account name '{name}'")
        account_names.add(name)

    return v, account_names


def validate_sessions(path, account_names):
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
    seen_titles = set()

    for i, entry in enumerate(sessions, start=1):
        title = entry.get("title", "")
        directory = entry.get("directory", "")

        if not title:
            v.error(f"entry {i}: missing or empty 'title'")
        if not directory:
            v.error(f"entry {i}: missing or empty 'directory'")
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

    return v


def validate_defaults(path, valid_sort=None):
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
        if not isinstance(refresh, int) or isinstance(refresh, bool):
            v.error("dashboard_refresh must be an integer")
        elif refresh < 0:
            v.error("dashboard_refresh must be non-negative")

    # Validate [notifications] section
    bool_keys = ("error", "permission", "stop", "idle")
    for key in bool_keys:
        val = notifications.get(key)
        if val is not None and not isinstance(val, bool):
            v.error(f"notifications.{key} must be a boolean, got {type(val).__name__}")

    for key in ("idle_alert_minutes", "min_secs"):
        val = notifications.get(key)
        if val is not None:
            if not isinstance(val, (int, float)) or isinstance(val, bool):
                v.error(f"notifications.{key} must be a number")
            elif val < 0:
                v.error(f"notifications.{key} must be non-negative")

    patterns = notifications.get("ignore_patterns")
    if patterns is not None:
        if not isinstance(patterns, list):
            v.error("notifications.ignore_patterns must be an array")
        elif not all(isinstance(p, str) for p in patterns):
            v.error("notifications.ignore_patterns entries must be strings")

    statuses = defaults.get("tab_statuses")
    if statuses is not None:
        if not isinstance(statuses, list):
            v.error("tab_statuses must be an array")
        elif not all(isinstance(s, str) for s in statuses):
            v.error("tab_statuses entries must be strings")

    return v


def validate_hooks(path, valid_events, account_names):
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


def main():
    if len(sys.argv) < 3:
        print(f"Usage: {sys.argv[0]} CONFIG_DIR DATA_DIR", file=sys.stderr)
        sys.exit(2)

    config_dir = sys.argv[1]
    data_dir = sys.argv[2]

    palette_colors = _load_palette_colors(data_dir)
    valid_events = _load_hook_events(data_dir)
    valid_sort = _load_sort_modes(data_dir)

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
