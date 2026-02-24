"""Session filtering and sorting helpers."""


def _filter_sessions(sessions, query):
    """Return sessions matching query (case-insensitive) against title, cwd, or account."""
    if not query:
        return sessions
    q = query.lower()
    return [
        s
        for s in sessions
        if q in s["title"].lower() or q in s["cwd"].lower() or q in s["account"].lower()
    ]


def _filter_by_accounts(sessions, enabled_accounts):
    """Return sessions filtered to only those whose account is in enabled_accounts.

    When enabled_accounts is None, returns the full list (no filtering).
    """
    if enabled_accounts is None:
        return sessions
    return [s for s in sessions if s["account"] in enabled_accounts]


def _apply_filters(sessions, search_query, account_filter):
    """Chain account filtering then text search."""
    result = _filter_by_accounts(sessions, account_filter)
    return _filter_sessions(result, search_query)


def _filter_config_sessions(sessions, query):
    """Return config sessions matching query (case-insensitive) against title, dir, or account."""
    if not query:
        return sessions
    q = query.lower()
    return [
        s
        for s in sessions
        if q in s["title"].lower() or q in s["dir"].lower() or q in s["account"].lower()
    ]


def _sort_pick_entries(entries, running_titles):
    """Sort entries with running sessions first, stopped second, preserving order within each group.

    Returns (sorted_list, running_count).
    """
    running = [e for e in entries if e["title"] in running_titles]
    stopped = [e for e in entries if e["title"] not in running_titles]
    return running + stopped, len(running)
