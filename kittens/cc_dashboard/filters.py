"""Session filtering and sorting helpers."""

import json

from cc_dashboard.config_data import _resolve_data_path


def _load_sort_modes():
    """Load sort modes from icons.json, excluding 'alpha' (legacy alias for 'name')."""
    path = _resolve_data_path("icons.json")
    if path:
        try:
            with open(path) as f:
                data = json.load(f)
            modes = data["sort_modes"]
            # Exclude 'alpha' — it's a legacy alias accepted by the validator
            # but not shown in the dashboard UI (the dashboard uses 'name').
            return tuple(m for m in modes if m != "alpha")
        except (OSError, json.JSONDecodeError, KeyError, TypeError):
            pass
    return ("default", "name", "account", "group", "status", "idle", "activity")


def _filter_sessions(sessions, query):
    """Return sessions matching query against title, cwd, account, or group."""
    if not query:
        return sessions
    q = query.lower()
    return [
        s
        for s in sessions
        if q in s["title"].lower()
        or q in s["cwd"].lower()
        or q in s["account"].lower()
        or q in ((s.get("git") or {}).get("branch", "")).lower()
        or q in s.get("group", "").lower()
    ]


def _filter_by_accounts(sessions, enabled_accounts):
    """Return sessions filtered to only those whose account is in enabled_accounts.

    When enabled_accounts is None, returns the full list (no filtering).
    """
    if enabled_accounts is None:
        return sessions
    return [s for s in sessions if s["account"] in enabled_accounts]


def _filter_by_groups(sessions, enabled_groups):
    """Return sessions filtered to only those whose group is in enabled_groups.

    When enabled_groups is None, returns the full list (no filtering).
    """
    if enabled_groups is None:
        return sessions
    return [
        s for s in sessions if (s.get("group", "") or "(ungrouped)") in enabled_groups
    ]


def _apply_filters(sessions, search_query, account_filter, group_filter=None):
    """Chain account filtering, group filtering, then text search."""
    result = _filter_by_accounts(sessions, account_filter)
    result = _filter_by_groups(result, group_filter)
    return _filter_sessions(result, search_query)


def _filter_pick_by_groups(sessions, enabled_groups):
    """Return config sessions filtered to only those in enabled_groups.

    When enabled_groups is None, returns the full list (no filtering).
    """
    if enabled_groups is None:
        return sessions
    return [
        s for s in sessions if (s.get("group", "") or "(ungrouped)") in enabled_groups
    ]


def _filter_config_sessions(sessions, query):
    """Return config sessions matching query against title, dir, or account."""
    if not query:
        return sessions
    q = query.lower()
    return [
        s
        for s in sessions
        if q in s["title"].lower() or q in s["dir"].lower() or q in s["account"].lower()
    ]


def _sort_pick_entries(entries, running_titles):
    """Sort entries: running first, stopped second, preserving order.

    Returns (sorted_list, running_count).
    """
    running = [e for e in entries if e["title"] in running_titles]
    stopped = [e for e in entries if e["title"] not in running_titles]
    return running + stopped, len(running)


# ── Sort modes ──────────────────────────────────────────────────────────────

SORT_MODES = _load_sort_modes()

SORT_LABELS = {
    "default": "",
    "name": "Name",
    "account": "Account",
    "group": "Group",
    "status": "Status",
    "idle": "Idle",
    "activity": "Activity",
}

_STATUS_PRIORITY = {
    "error": 0,
    "permission": 1,
    "active": 2,
    "focused": 3,
    "idle": 4,
    "stopped": 5,
}


def _apply_sort(sessions, sort_mode):
    """Return a new list of sessions sorted by the given mode."""
    if sort_mode == "default":
        return list(sessions)
    if sort_mode == "name":
        return sorted(sessions, key=lambda s: s["title"].lower())
    if sort_mode == "account":
        return sorted(
            sessions, key=lambda s: (s["account"].lower(), s["title"].lower())
        )
    if sort_mode == "group":
        return sorted(
            sessions,
            key=lambda s: (
                (s.get("group", "") or "zzz").lower(),
                s["title"].lower(),
            ),
        )
    if sort_mode == "status":
        return sorted(sessions, key=lambda s: _STATUS_PRIORITY.get(s["status"], 99))
    if sort_mode == "idle":
        return sorted(
            sessions,
            key=lambda s: (
                0 if s.get("activity") else 1,
                s["activity"]["last_event_ts"] if s.get("activity") else 0,
            ),
        )
    if sort_mode == "activity":
        return sorted(
            sessions,
            key=lambda s: (
                0 if s.get("activity") else 1,
                -s["activity"]["last_event_ts"] if s.get("activity") else 0,
            ),
        )
    return list(sessions)
