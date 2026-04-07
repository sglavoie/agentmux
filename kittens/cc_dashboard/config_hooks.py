"""Compatibility facade for dashboard hooks configuration.

The original monolithic implementation has been split across smaller modules:
- config_hooks_meta.py: hook event constants, labels, and examples
- config_hooks_store.py: hooks.json loading, saving, and path resolution
- config_hooks_effective.py: effective-hook computation and drift detection
- config_hooks_overrides.py: override normalization and mutation helpers
"""

from __future__ import annotations

from cc_dashboard import config_hooks_effective as _effective
from cc_dashboard import config_hooks_meta as _meta
from cc_dashboard import config_hooks_overrides as _overrides
from cc_dashboard import config_hooks_store as _store

HOOK_EVENTS = _meta.HOOK_EVENTS
HOOK_EVENT_EXAMPLES = _meta.HOOK_EVENT_EXAMPLES
HOOK_EVENT_META = _meta.HOOK_EVENT_META
_FULL_TYPE_EVENTS = _meta._FULL_TYPE_EVENTS

_compute_effective_hooks = _effective._compute_effective_hooks
_detect_all_drift = _effective._detect_all_drift
_detect_drift = _effective._detect_drift

_get_extra_hooks = _overrides._get_extra_hooks
add_extra_hook = _overrides.add_extra_hook
remove_extra_hook = _overrides.remove_extra_hook
toggle_hook_event = _overrides.toggle_hook_event
update_extra_hook = _overrides.update_extra_hook

_load_example_hooks = _store._load_example_hooks
_resolve_hook_cmd = _store._resolve_hook_cmd
_resolve_hooks_example_path = _store._resolve_hooks_example_path
_resolve_log_cmd = _store._resolve_log_cmd
load_hooks = _store.load_hooks
save_hooks = _store.save_hooks

__all__ = [
    "HOOK_EVENTS",
    "HOOK_EVENT_EXAMPLES",
    "HOOK_EVENT_META",
    "_FULL_TYPE_EVENTS",
    "_compute_effective_hooks",
    "_detect_all_drift",
    "_detect_drift",
    "_get_extra_hooks",
    "_load_example_hooks",
    "_resolve_hook_cmd",
    "_resolve_hooks_example_path",
    "_resolve_log_cmd",
    "add_extra_hook",
    "load_hooks",
    "remove_extra_hook",
    "save_hooks",
    "toggle_hook_event",
    "update_extra_hook",
]
