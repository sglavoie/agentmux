"""Compatibility facade for split session lifecycle helpers."""

from cc_dashboard.remote import actions_lifecycle_shared as _shared
from cc_dashboard.remote.actions_core import (
    compute_insert_location as _core_compute_insert_location,
)
from cc_dashboard.remote.actions_lifecycle_detached import (
    _run_detached_steps,
    _spawn_detached_close,
)
from cc_dashboard.remote.actions_lifecycle_move import (
    _spawn_detached_move,
    do_move,
)
from cc_dashboard.remote.actions_lifecycle_restart import (
    _spawn_detached_restart,
    do_restart,
)

compute_insert_location = _core_compute_insert_location


def _build_launch_steps(*args, **kwargs):
    """Preserve the legacy monkeypatch seam for detached launch-step tests."""
    _shared.compute_insert_location = compute_insert_location
    return _shared._build_launch_steps(*args, **kwargs)


__all__ = [
    "_build_launch_steps",
    "_run_detached_steps",
    "_spawn_detached_close",
    "_spawn_detached_move",
    "_spawn_detached_restart",
    "compute_insert_location",
    "do_move",
    "do_restart",
]
