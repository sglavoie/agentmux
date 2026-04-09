"""Compatibility exports for dashboard viewer mode handlers.

Implementation now lives in smaller modules grouped by responsibility.
"""

from cc_dashboard.loop_viewers_actlog import handle_actlog_mode
from cc_dashboard.loop_viewers_peek import handle_peek_mode

__all__ = ["handle_actlog_mode", "handle_peek_mode"]
