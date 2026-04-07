"""Compatibility exports for dashboard mode handlers.

Implementation now lives in smaller modules grouped by responsibility.
"""

from cc_dashboard.loop_modes_dashboard_actions import (
    handle_actions_mode,
    handle_restart_options_mode,
    handle_sort_pick_mode,
)
from cc_dashboard.loop_modes_dashboard_confirm import handle_confirm_modes
from cc_dashboard.loop_modes_dashboard_ops import (
    handle_color_picker_mode,
    handle_group_actions_mode,
    handle_move_picker_mode,
)

__all__ = [
    "handle_actions_mode",
    "handle_color_picker_mode",
    "handle_confirm_modes",
    "handle_group_actions_mode",
    "handle_move_picker_mode",
    "handle_restart_options_mode",
    "handle_sort_pick_mode",
]
