"""Rendering helpers for activity log and preferences modes."""

from cc_dashboard.loop_modes_prefs import PREF_FIELDS
from cc_dashboard.views import (
    draw_actlog_browse,
    draw_actlog_detail,
    draw_actlog_gsearch,
    draw_actlog_view,
    draw_footer,
    draw_prefs_array_editor,
    draw_prefs_view,
    draw_status_message,
)


def render_activity_modes(stdscr, state):
    """Render activity log and preferences modes."""
    mode = state["mode"]

    if mode == "actlog":
        draw_actlog_view(
            stdscr,
            state["actlog_session_title"],
            state["actlog_lines"],
            state["actlog_scroll"],
            state["actlog_summary"],
            search_query=state["actlog_search_query"],
            search_active=state["actlog_search_active"],
            search_cursor=state["actlog_search_cursor"],
            match_line_indices=state["actlog_match_lines"],
            match_index=state["actlog_match_index"],
            selected=state.get("actlog_selected", -1),
        )
        return True

    if mode == "actlog_detail":
        draw_actlog_view(
            stdscr,
            state["actlog_session_title"],
            state["actlog_lines"],
            state["actlog_scroll"],
            state["actlog_summary"],
            selected=state.get("actlog_selected", -1),
        )
        draw_actlog_detail(stdscr, state.get("actlog_detail_entry", {}))
        draw_footer(stdscr, "actlog_detail")
        return True

    if mode == "actlog_browse":
        draw_actlog_browse(
            stdscr,
            state["actlog_browse_entries"],
            state["actlog_browse_selected"],
            state["actlog_browse_scroll"],
            search_query=state["actlog_browse_search_query"],
            search_active=state["actlog_browse_search_active"],
            search_cursor=state["actlog_browse_search_cursor"],
            match_line_indices=state["actlog_browse_match_lines"],
            match_index=state["actlog_browse_match_index"],
        )
        if state["msg"] and state["msg_frames"] > 0:
            draw_status_message(stdscr, state["msg"])
        return True

    if mode == "prefs":
        draw_prefs_view(
            stdscr,
            PREF_FIELDS,
            state["prefs_values"],
            state["prefs_selected"],
            state["prefs_editing"],
            state["prefs_edit_value"],
            state["prefs_cursor"],
        )
        draw_footer(stdscr, "prefs")
        return True

    if mode == "prefs_array":
        draw_prefs_view(
            stdscr,
            PREF_FIELDS,
            state["prefs_values"],
            state["prefs_selected"],
            False,
            "",
            0,
        )
        draw_prefs_array_editor(
            stdscr,
            state["prefs_array_items"],
            state["prefs_array_selected"],
            state["prefs_array_editing"],
            state["prefs_array_edit_value"],
            state["prefs_array_cursor"],
        )
        draw_footer(stdscr, "prefs_array")
        return True

    if mode == "actlog_gsearch":
        draw_actlog_gsearch(
            stdscr,
            state["actlog_gsearch_query"],
            state["actlog_gsearch_cursor"],
            state["actlog_gsearch_results"],
            state["actlog_gsearch_selected"],
            state["actlog_gsearch_scroll"],
            loading=state["actlog_gsearch_loading"],
            total_scanned=state["actlog_gsearch_total_scanned"],
            log_index=state["actlog_gsearch_log_index"],
            search_active=state["actlog_gsearch_search_active"],
        )
        return True

    return False
