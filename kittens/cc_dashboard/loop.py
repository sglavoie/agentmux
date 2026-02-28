"""Thin compatibility entrypoint for the dashboard loop.

The full dashboard implementation lives in `cc_dashboard.loop_dashboard` and
shared helpers are in `cc_dashboard.loop_helpers`.
"""

from cc_dashboard.loop_dashboard import dashboard

__all__ = ["dashboard"]
