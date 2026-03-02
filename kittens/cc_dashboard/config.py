"""Compatibility facade for dashboard configuration.

The original monolithic implementation has been split across smaller modules:
- config_data.py: shared JSON/palette loading
- config_core.py: accounts/defaults/sessions logic
- config_hooks.py: hooks config logic
- config_state.py: shared mutable state
"""

from cc_dashboard import config_core as _core
from cc_dashboard import config_data as _data
from cc_dashboard import config_hooks as _hooks
from cc_dashboard import config_state as _state

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
