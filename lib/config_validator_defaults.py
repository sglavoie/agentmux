#!/usr/bin/env python3
"""Validation logic for defaults.toml."""

from __future__ import annotations

import re
import os
import tomllib

from data_loader import load_sort_modes

from config_validator_shared import Validator


def validate_defaults(path: str, valid_sort: set[str] | None = None) -> Validator:
    """Validate defaults.toml."""
    validator = Validator("defaults.toml")

    if not os.path.isfile(path):
        validator.warn("file not found (run 'ccm config init' to create it)")
        return validator

    try:
        with open(path, "rb") as file_obj:
            data = tomllib.load(file_obj)
    except tomllib.TOMLDecodeError as exc:
        validator.error(f"invalid TOML syntax: {exc}")
        return validator

    defaults = data.get("defaults", {})
    notifications = data.get("notifications", {})

    known_defaults_keys = {
        "claude_flags",
        "project_window",
        "sort_sessions",
        "dashboard_refresh",
        "tab_statuses",
        "notes_editor",
        "kitty_timeout",
        "idle_poll_interval",
    }
    unknown_defaults = set(defaults.keys()) - known_defaults_keys
    if unknown_defaults:
        validator.warn(
            f"[defaults]: unknown key(s): {', '.join(sorted(unknown_defaults))}"
        )

    known_notification_keys = {
        "error",
        "permission",
        "stop",
        "idle",
        "idle_alert_minutes",
        "min_secs",
        "ignore_patterns",
        "dim_after_minutes",
        "idle_poll_interval",
    }
    unknown_notifications = set(notifications.keys()) - known_notification_keys
    if unknown_notifications:
        validator.warn(
            f"[notifications]: unknown key(s): {', '.join(sorted(unknown_notifications))}"
        )

    known_sections = {"defaults", "notifications"}
    unknown_sections = set(data.keys()) - known_sections
    if unknown_sections:
        validator.warn(
            f"unknown top-level section(s): {', '.join(sorted(unknown_sections))}"
        )

    sort_sessions = defaults.get("sort_sessions")
    if valid_sort is None:
        valid_sort = load_sort_modes()
    valid_sort = valid_sort | {""}
    if sort_sessions is not None and sort_sessions not in valid_sort:
        validator.error(
            f"sort_sessions must be one of "
            f"{', '.join(sorted(valid_sort - {''}))} or omitted, "
            f"got '{sort_sessions}'"
        )

    project_window = defaults.get("project_window")
    valid_project_window = {"", "-", "stack", "vsplit", "hsplit", "split"}
    if project_window is not None and project_window not in valid_project_window:
        validator.error(
            f"project_window '{project_window}' is invalid (valid: -, stack, vsplit, hsplit, split)"
        )

    dashboard_refresh = defaults.get("dashboard_refresh")
    if dashboard_refresh is not None:
        if isinstance(dashboard_refresh, bool) or not isinstance(
            dashboard_refresh, int
        ):
            validator.error("dashboard_refresh must be an integer")
        elif dashboard_refresh < 0:
            validator.error("dashboard_refresh must be non-negative")

    kitty_timeout = defaults.get("kitty_timeout")
    if kitty_timeout is not None:
        if isinstance(kitty_timeout, bool) or not isinstance(kitty_timeout, int):
            validator.error("kitty_timeout must be an integer")
        elif kitty_timeout < 1:
            validator.error("kitty_timeout must be at least 1")
        elif kitty_timeout > 300:
            validator.error("kitty_timeout must be at most 300")

    idle_poll_interval = defaults.get("idle_poll_interval")
    if idle_poll_interval is not None:
        if isinstance(idle_poll_interval, bool) or not isinstance(
            idle_poll_interval, int
        ):
            validator.error("idle_poll_interval must be an integer")
        elif idle_poll_interval < 5:
            validator.error("idle_poll_interval must be at least 5 (seconds)")
        elif idle_poll_interval > 3600:
            validator.error("idle_poll_interval must be at most 3600 (seconds)")

    for key in ("error", "permission", "stop", "idle"):
        value = notifications.get(key)
        if value is not None and not isinstance(value, bool):
            validator.error(
                f"notifications.{key} must be a boolean, got {type(value).__name__}"
            )

    for key in ("idle_alert_minutes", "min_secs"):
        value = notifications.get(key)
        if value is not None:
            if isinstance(value, bool) or not isinstance(value, (int, float)):
                validator.error(f"notifications.{key} must be a number")
            elif value < 0:
                validator.error(f"notifications.{key} must be non-negative")

    patterns = notifications.get("ignore_patterns")
    if patterns is not None:
        if not isinstance(patterns, list):
            validator.error("notifications.ignore_patterns must be an array")
        elif not all(isinstance(pattern, str) for pattern in patterns):
            validator.error("notifications.ignore_patterns entries must be strings")
        else:
            for pattern in patterns:
                try:
                    re.compile(pattern)
                except re.error as exc:
                    validator.error(
                        f"notifications.ignore_patterns: invalid regex {pattern!r}: {exc}"
                    )

    tab_statuses = defaults.get("tab_statuses")
    if tab_statuses is not None:
        if not isinstance(tab_statuses, list):
            validator.error("tab_statuses must be an array")
        elif not all(isinstance(status, str) for status in tab_statuses):
            validator.error("tab_statuses entries must be strings")

    return validator
