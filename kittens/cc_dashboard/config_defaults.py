"""Defaults configuration helpers for dashboard."""

from __future__ import annotations

import contextlib
import os
import sys
import tomllib

from cc_dashboard import config_state as _state
from cc_dashboard.config_paths import _defaults_config_path, _import_toml_writer


def load_defaults() -> None:
    """Parse defaults config into the shared default settings."""
    _state._default_claude_flags = ""
    _state._default_project_window = ""
    _state._default_sort_sessions = ""
    _state._default_dashboard_refresh = 3
    _state._default_idle_alert_minutes = 0
    _state._default_notify_idle = ""
    _state._default_tab_statuses = []
    _state._default_notes_editor = ""
    _state._default_dim_after_minutes = 0

    path = _defaults_config_path()
    if not os.path.isfile(path):
        return

    try:
        with open(path, "rb") as f:
            data = tomllib.load(f)
    except (tomllib.TOMLDecodeError, OSError) as e:
        print(
            f"Warning: failed to parse {path}: {e}"
            " (check syntax with: ccm config validate)",
            file=sys.stderr,
        )
        return
    defaults = data.get("defaults", {})
    notifications = data.get("notifications", {})

    if "claude_flags" in defaults:
        _state._default_claude_flags = str(defaults["claude_flags"])
    if "project_window" in defaults:
        _state._default_project_window = str(defaults["project_window"])
    if "sort_sessions" in defaults:
        _state._default_sort_sessions = str(defaults["sort_sessions"])
    if "dashboard_refresh" in defaults:
        try:
            _state._default_dashboard_refresh = max(
                0, int(defaults["dashboard_refresh"])
            )
        except (ValueError, TypeError) as e:
            print(
                f"Warning: ignoring invalid config value for dashboard_refresh: {e}",
                file=sys.stderr,
            )
    if "idle_alert_minutes" in notifications:
        try:
            _state._default_idle_alert_minutes = max(
                0, int(notifications["idle_alert_minutes"])
            )
        except (ValueError, TypeError) as e:
            print(
                f"Warning: ignoring invalid config value for idle_alert_minutes: {e}",
                file=sys.stderr,
            )
    if "dim_after_minutes" in notifications:
        try:
            _state._default_dim_after_minutes = max(
                0, int(notifications["dim_after_minutes"])
            )
        except (ValueError, TypeError) as e:
            print(
                f"Warning: ignoring invalid config value for dim_after_minutes: {e}",
                file=sys.stderr,
            )
    if "idle" in notifications:
        val = notifications["idle"]
        _state._default_notify_idle = (
            "on" if val is True else ("" if val is False else str(val))
        )
    if "notes_editor" in defaults:
        _state._default_notes_editor = str(defaults["notes_editor"])
    if "kitty_timeout" in defaults:
        try:
            _state._default_kitty_timeout = max(1, int(defaults["kitty_timeout"]))
        except (ValueError, TypeError) as e:
            print(
                f"Warning: ignoring invalid config value for kitty_timeout: {e}",
                file=sys.stderr,
            )
    if "tab_statuses" in defaults:
        val = defaults["tab_statuses"]
        if isinstance(val, list):
            _state._default_tab_statuses = [str(status) for status in val]


def _save_tab_statuses(statuses: list[str]) -> str:
    """Write tab_statuses back to defaults.toml, preserving other settings.

    Returns an empty string on success, or an error message on failure.
    """
    path = _defaults_config_path()
    toml_writer = _import_toml_writer()

    header = ""
    defaults = {}
    notifications = {}
    if os.path.isfile(path):
        from toml_reader import read_header_comments

        header = read_header_comments(path)
        try:
            with open(path, "rb") as f:
                data = tomllib.load(f)
            defaults = data.get("defaults", {})
            notifications = data.get("notifications", {})
        except tomllib.TOMLDecodeError as e:
            print(f"Warning: failed to parse {path}: {e}", file=sys.stderr)

    defaults["tab_statuses"] = statuses

    out = {}
    if defaults:
        out["defaults"] = defaults
    if notifications:
        out["notifications"] = notifications
    tmp_path = path + ".tmp"
    try:
        with open(tmp_path, "w") as f:
            f.write(toml_writer.dumps(out, header=header))
            f.write("\n")
        os.replace(tmp_path, path)
    except OSError as e:
        print(f"Warning: failed to save {path}: {e}", file=sys.stderr)
        with contextlib.suppress(OSError):
            os.remove(tmp_path)
        return f"Failed to save {os.path.basename(path)}: {e}"

    _state._default_tab_statuses = statuses
    return ""


__all__ = [
    "_defaults_config_path",
    "_import_toml_writer",
    "_save_tab_statuses",
    "load_defaults",
]
