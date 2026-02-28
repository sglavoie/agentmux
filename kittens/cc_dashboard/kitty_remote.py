"""Kitty remote control integration for the Claude Code Dashboard.

Compatibility facade that re-exports the legacy kitty_remote API from the
newly split cc_dashboard.remote modules.
"""

from cc_dashboard.remote.actions import (
    compute_insert_location,
    do_close,
    do_cross_instance_focus,
    do_focus,
    do_launch,
    do_move,
    do_open_editor,
    do_rename,
    do_restart,
    do_sort_tabs,
    generate_clone_title,
    run_hooks_sync,
)
from cc_dashboard.remote.activity import (
    cleanup_stale_activity,
    list_activity_logs,
    load_activity,
    load_activity_log,
    resolve_session_id,
)
from cc_dashboard.remote.command import (
    get_all_kitty_sockets,
    get_host_tab_id,
    get_kitty_socket,
    get_window_text,
    kitty_cmd,
)
from cc_dashboard.remote.git_ops import (
    check_git_safety,
    detect_worktree,
    do_create_worktree,
    do_remove_worktree,
)
from cc_dashboard.remote.sessions import query_all_sessions, query_sessions
from cc_dashboard.remote.shared import (
    OVERRIDE_MARKER,
    format_duration,
)

__all__ = [
    "OVERRIDE_MARKER",
    "check_git_safety",
    "cleanup_stale_activity",
    "compute_insert_location",
    "detect_worktree",
    "do_close",
    "do_create_worktree",
    "do_cross_instance_focus",
    "do_focus",
    "do_launch",
    "do_move",
    "do_open_editor",
    "do_remove_worktree",
    "do_rename",
    "do_restart",
    "do_sort_tabs",
    "format_duration",
    "generate_clone_title",
    "get_all_kitty_sockets",
    "get_host_tab_id",
    "get_kitty_socket",
    "get_window_text",
    "kitty_cmd",
    "list_activity_logs",
    "load_activity",
    "load_activity_log",
    "query_all_sessions",
    "query_sessions",
    "resolve_session_id",
    "run_hooks_sync",
]
