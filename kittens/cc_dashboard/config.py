"""Compatibility facade for dashboard configuration.

The original monolithic implementation has been split across smaller modules:
- config_data.py: shared JSON/palette loading
- config_core.py: accounts/defaults/sessions logic
- config_hooks.py: hooks config logic
- config_state.py: shared mutable state
"""

from __future__ import annotations

from typing import TYPE_CHECKING

from cc_dashboard import config_core as _core
from cc_dashboard import config_data as _data
from cc_dashboard import config_hooks as _hooks
from cc_dashboard import config_state as _state

if TYPE_CHECKING:
    # config_state exports
    _accounts: dict[str, dict[str, str]]
    _account_names: list[str]
    _sessions: list[dict[str, str]]
    _hooks_global: dict
    _hooks_overrides: dict
    _cached_hook_cmd: str | None
    _cached_log_cmd: str | None
    _default_claude_flags: str
    _default_project_window: str
    _default_sort_sessions: str
    _default_dashboard_refresh: int
    _default_idle_alert_minutes: int
    _default_notify_idle: str

    from cc_dashboard.config_core import (  # noqa: I001
        _all_config_paths as _all_config_paths,
        _config_dir as _config_dir,
        _find_config_session as _find_config_session,
        _palette_tab_colors as _palette_tab_colors,
        _resolve_account_by_config_dir as _resolve_account_by_config_dir,
        _resolve_claude_flags as _resolve_claude_flags,
        _resolve_project_window as _resolve_project_window,
        _sessions_config_filename as _sessions_config_filename,
        _unique_config_dirs as _unique_config_dirs,
        load_accounts as load_accounts,
        load_defaults as load_defaults,
        load_sessions as load_sessions,
        remove_session as remove_session,
    )
    from cc_dashboard.config_hooks import (
        _detect_all_drift as _detect_all_drift,
        _detect_drift as _detect_drift,
        add_extra_hook as add_extra_hook,
        remove_extra_hook as remove_extra_hook,
        toggle_hook_event as toggle_hook_event,
        update_extra_hook as update_extra_hook,
    )

_STATE_EXPORTS = {
    "_accounts",
    "_account_names",
    "_sessions",
    "_hooks_global",
    "_hooks_overrides",
    "_cached_hook_cmd",
    "_cached_log_cmd",
    "_default_claude_flags",
    "_default_project_window",
    "_default_sort_sessions",
    "_default_dashboard_refresh",
    "_default_idle_alert_minutes",
    "_default_notify_idle",
}

__all__ = [
    "_account_names",
    "_accounts",
    "_all_config_paths",
    "_cached_hook_cmd",
    "_cached_log_cmd",
    "_config_dir",
    "_default_claude_flags",
    "_default_dashboard_refresh",
    "_default_idle_alert_minutes",
    "_default_notify_idle",
    "_default_project_window",
    "_default_sort_sessions",
    "_detect_all_drift",
    "_detect_drift",
    "_find_config_session",
    "_hooks_global",
    "_hooks_overrides",
    "_palette_tab_colors",
    "_resolve_account_by_config_dir",
    "_resolve_claude_flags",
    "_resolve_project_window",
    "_sessions",
    "_sessions_config_filename",
    "_unique_config_dirs",
    "add_extra_hook",
    "load_accounts",
    "load_defaults",
    "load_sessions",
    "remove_extra_hook",
    "remove_session",
    "toggle_hook_event",
    "update_extra_hook",
]


def __getattr__(name):
    if name in _STATE_EXPORTS:
        return getattr(_state, name)
    if hasattr(_core, name):
        return getattr(_core, name)
    if hasattr(_data, name):
        return getattr(_data, name)
    if hasattr(_hooks, name):
        return getattr(_hooks, name)
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")


def __dir__():
    return sorted(set(globals()) | _STATE_EXPORTS)
