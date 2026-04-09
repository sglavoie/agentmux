"""Compatibility facade for split tab action helpers."""

from cc_dashboard.remote.actions_launch import compute_insert_location, do_launch
from cc_dashboard.remote.actions_tabs import (
    do_close,
    do_cross_instance_focus,
    do_focus,
    do_rename,
    generate_clone_title,
)
from cc_dashboard.remote.actions_lifecycle import (
    _spawn_detached_close,
    do_move,
    do_restart,
)
from cc_dashboard.remote.actions_misc import (
    do_open_editor,
    run_hooks_sync,
)
from cc_dashboard.remote.actions_sorting import do_sort_tabs

__all__ = [
    "_spawn_detached_close",
    "compute_insert_location",
    "do_close",
    "do_cross_instance_focus",
    "do_focus",
    "do_launch",
    "do_move",
    "do_open_editor",
    "do_rename",
    "do_restart",
    "do_sort_tabs",
    "generate_clone_title",
    "run_hooks_sync",
]
