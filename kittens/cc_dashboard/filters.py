"""Session filtering and sorting helpers."""

from __future__ import annotations

from typing import Any

from cc_dashboard.config_data import load_sort_modes as _load_sort_modes


def _filter_sessions(
    sessions: list[dict[str, Any]], query: str
) -> list[dict[str, Any]]:
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


def _filter_by_accounts(
    sessions: list[dict[str, Any]], enabled_accounts: set[str] | None
) -> list[dict[str, Any]]:
    """Return sessions filtered to only those whose account is in enabled_accounts.

    When enabled_accounts is None, returns the full list (no filtering).
    """
    if enabled_accounts is None:
        return sessions
    return [s for s in sessions if s["account"] in enabled_accounts]


def _filter_by_groups(
    sessions: list[dict[str, Any]], enabled_groups: set[str] | None
) -> list[dict[str, Any]]:
    """Return sessions filtered to only those whose group is in enabled_groups.

    When enabled_groups is None, returns the full list (no filtering).
    """
    if enabled_groups is None:
        return sessions
    return [
        s for s in sessions if (s.get("group", "") or "(ungrouped)") in enabled_groups
    ]


def _apply_filters(
    sessions: list[dict[str, Any]],
    search_query: str,
    account_filter: set[str] | None,
    group_filter: set[str] | None = None,
) -> list[dict[str, Any]]:
    """Chain account filtering, group filtering, then text search."""
    result = _filter_by_accounts(sessions, account_filter)
    result = _filter_by_groups(result, group_filter)
    return _filter_sessions(result, search_query)


def _filter_pick_by_groups(
    sessions: list[dict[str, Any]], enabled_groups: set[str] | None
) -> list[dict[str, Any]]:
    """Return config sessions filtered to only those in enabled_groups.

    When enabled_groups is None, returns the full list (no filtering).
    """
    if enabled_groups is None:
        return sessions
    return [
        s for s in sessions if (s.get("group", "") or "(ungrouped)") in enabled_groups
    ]


def _filter_config_sessions(
    sessions: list[dict[str, Any]], query: str
) -> list[dict[str, Any]]:
    """Return config sessions matching query against title, dir, or account."""
    if not query:
        return sessions
    q = query.lower()
    return [
        s
        for s in sessions
        if q in s["title"].lower() or q in s["dir"].lower() or q in s["account"].lower()
    ]


def _sort_config_entries(
    entries: list[dict[str, Any]], sort_mode: str
) -> list[dict[str, Any]]:
    """Sort config session entries by the given mode."""
    if sort_mode == "default":
        return entries
    if sort_mode == "account":
        return sorted(entries, key=lambda s: (s["account"].lower(), s["title"].lower()))
    if sort_mode == "group":
        return sorted(
            entries,
            key=lambda s: (
                (s.get("group", "") or "zzz").lower(),
                s["title"].lower(),
            ),
        )
    # name sort (also fallback for status/idle/activity which don't apply)
    return sorted(entries, key=lambda s: s["title"].lower())


def _sort_pick_entries(
    entries: list[dict[str, Any]],
    running_titles: set[str],
    sort_mode: str = "name",
) -> tuple[list[dict[str, Any]], int]:
    """Sort entries: running first, stopped second, sorted within each group.

    Returns (sorted_list, running_count).
    """
    running = [e for e in entries if e["title"] in running_titles]
    stopped = [e for e in entries if e["title"] not in running_titles]
    running = _sort_config_entries(running, sort_mode)
    stopped = _sort_config_entries(stopped, sort_mode)
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


def _apply_sort(
    sessions: list[dict[str, Any]], sort_mode: str
) -> list[dict[str, Any]]:
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
