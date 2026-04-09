"""Idle-time refresh helpers for the dashboard event loop."""

from cc_dashboard.filters import _apply_filters, _apply_sort
from cc_dashboard.loop_helpers import _session_fingerprint
from cc_dashboard.remote.activity import search_all_logs
from cc_dashboard.remote.command import get_all_kitty_sockets
from cc_dashboard.views import _check_idle_notifications


def handle_idle_tick(stdscr, state: dict) -> None:
    """Process periodic refresh and deferred searches while idle."""
    _refresh_sessions_if_needed(state)
    _run_global_activity_search_if_ready(stdscr, state)


def _refresh_sessions_if_needed(state: dict) -> None:
    if (
        state["mode"] != "list"
        or state["search_active"]
        or state["refresh_threshold"] <= 0
    ):
        return

    state["refresh_ticks"] += 1
    if state["refresh_ticks"] < state["refresh_threshold"]:
        return

    state["refresh_ticks"] = 0
    state["reload_all_config"]()
    new_sockets = get_all_kitty_sockets()
    if new_sockets:
        state["instance_labels"] = [label for _, label in new_sockets]
        state["instance_socket_map"] = {label: sock for sock, label in new_sockets}
        state["multi_instance"] = len(new_sockets) > 1
        if state["home_socket"] not in {sock for sock, _ in new_sockets}:
            state["home_socket"] = new_sockets[0][0]
            state["home_instance_label"] = new_sockets[0][1]

    new_sessions = state["query_all_sessions"](managed_only=(not state["show_all"]))
    _check_idle_notifications(new_sessions)

    old_fingerprint = _session_fingerprint(state["filtered"])
    new_filtered = _apply_sort(
        _apply_filters(
            new_sessions,
            state["search_query"],
            state["account_filter"],
            state["group_filter"],
        ),
        state["sort_mode"],
    )
    if _session_fingerprint(new_filtered) == old_fingerprint:
        return

    old_tab_id = (
        state["filtered"][state["selected"]]["tab_id"]
        if state["filtered"] and state["selected"] < len(state["filtered"])
        else None
    )
    state["sessions"] = new_sessions
    state["marked"] &= {sess["tab_id"] for sess in state["sessions"]}
    state["filtered"] = new_filtered
    state["selected"] = 0
    if old_tab_id is not None:
        for index, sess in enumerate(state["filtered"]):
            if sess["tab_id"] == old_tab_id:
                state["selected"] = index
                break
    state["selected"] = min(state["selected"], max(0, len(state["filtered"]) - 1))


def _run_global_activity_search_if_ready(stdscr, state: dict) -> None:
    if state["mode"] != "actlog_gsearch" or state["actlog_gsearch_debounce_ticks"] <= 0:
        return

    state["actlog_gsearch_debounce_ticks"] -= 1
    if state["actlog_gsearch_debounce_ticks"] != 0:
        return

    query = state["actlog_gsearch_query"]
    if len(query) < 3 or query == state["actlog_gsearch_last_query"]:
        return

    state["actlog_gsearch_loading"] = True
    results, total_scanned = search_all_logs(
        query,
        state["actlog_gsearch_log_index"],
        cols=stdscr.getmaxyx()[1],
    )
    state["actlog_gsearch_results"] = results
    state["actlog_gsearch_total_scanned"] = total_scanned
    state["actlog_gsearch_selected"] = 0
    state["actlog_gsearch_scroll"] = 0
    state["actlog_gsearch_last_query"] = query
    state["actlog_gsearch_loading"] = False
