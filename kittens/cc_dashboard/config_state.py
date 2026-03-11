"""Shared mutable state for dashboard configuration."""

# Account configuration
_accounts: dict[str, dict[str, str]] = {}
_account_names: list[str] = []

# Session configuration
_sessions: list[dict[str, str]] = []

# Hooks configuration
_hooks_global: dict = {}
_hooks_overrides: dict = {}
_cached_hook_cmd: str | None = None
_cached_log_cmd: str | None = None

# Global defaults
_default_claude_flags = ""
_default_project_window = ""
_default_sort_sessions = ""
_default_dashboard_refresh = 3
_default_idle_alert_minutes = 0
_default_notify_idle = ""
_default_tab_statuses: list[str] = []
_default_notes_editor = ""
_default_kitty_timeout = 5
