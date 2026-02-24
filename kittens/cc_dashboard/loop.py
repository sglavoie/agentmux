"""Main event loop and session fingerprinting for the Claude Code Dashboard."""

import curses

from cc_dashboard import config as _cfg
from cc_dashboard.colors import CP_DIM, CP_NORMAL, init_colors
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.filters import (
    _apply_filters,
    _filter_config_sessions,
    _sort_pick_entries,
)
from cc_dashboard.kitty_remote import (
    compute_insert_location,
    do_close,
    do_focus,
    do_launch,
    do_restart,
    query_sessions,
)
from cc_dashboard.views import (
    DETAIL_PANE_HEIGHT,
    _HELP_LINES,
    _cycle_account,
    _cycle_pw,
    _check_idle_notifications,
    draw_confirm_prompt,
    draw_detail_pane,
    draw_filter_bar,
    draw_footer,
    draw_help_overlay,
    draw_list_view,
    draw_new_form,
    draw_pick_view,
    draw_search_bar,
    draw_status_message,
)


def _session_fingerprint(sessions):
    """Return a hashable fingerprint of session state for change detection."""
    return tuple(
        (
            s["tab_id"],
            s["title"],
            s["status"],
            s["cwd"],
            s["account"],
            s.get("window_count", 1),
            (s.get("activity") or {}).get("last_event_ts"),
            (s.get("activity") or {}).get("last_event_type"),
        )
        for s in sessions
    )


def dashboard(stdscr, socket):
    """Run the interactive dashboard loop."""
    curses.curs_set(0)
    init_colors()
    stdscr.timeout(100)

    show_all = False
    sessions = query_sessions(socket, managed_only=True)
    selected = 0

    # Auto-scroll to the focused session, if one exists
    for i, s in enumerate(sessions):
        if s["status"] == "focused":
            selected = i
            break

    show_detail = False
    mode = "list"  # list | confirm | confirm_restart | new | pick | help
    help_scroll = 0
    msg = ""
    msg_frames = 0

    # Pick mode (launch from config) state
    pick_selected = 0
    pick_filtered = list(_cfg._sessions)
    pick_running = set()
    pick_running_count = 0
    pick_query = ""
    pick_cursor = 0
    pick_search_active = False

    # Auto-refresh state
    refresh_interval = _cfg._default_dashboard_refresh
    refresh_ticks = 0
    refresh_threshold = refresh_interval * 10  # each tick is ~100ms

    # Search/filter state
    search_query = ""
    search_active = False
    search_cursor = 0
    filtered = sessions

    # Account filter state
    account_filter = None  # None = show all; set of names = show only these
    account_filter_mode = False  # True when filter bar is interactive

    # New-session form state -- default to first loaded account
    default_account = _cfg._account_names[0] if _cfg._account_names else "work"
    default_pw = (
        _cfg._default_project_window if _cfg._default_project_window else "none"
    )
    new_fields = {
        "title": "",
        "dir": "~/",
        "account": default_account,
        "flags": "",
        "pw": default_pw,
    }
    new_active = 0
    new_cursor = 0

    while True:
        # ── Draw ─────────────────────────────────────────────────
        if mode in ("list", "confirm", "confirm_restart"):
            rows, cols = stdscr.getmaxyx()
            detail_visible = show_detail and rows >= 20 and bool(filtered)
            draw_list_view(stdscr, filtered, selected, detail_visible=detail_visible)
            if detail_visible:
                detail_y = rows - 1 - DETAIL_PANE_HEIGHT
                draw_detail_pane(stdscr, filtered[selected], detail_y)
            if msg and msg_frames > 0:
                draw_status_message(stdscr, msg)
            elif search_active or search_query:
                draw_search_bar(
                    stdscr,
                    search_query,
                    search_cursor,
                    search_active,
                    len(filtered),
                )
            if account_filter is not None or account_filter_mode:
                draw_filter_bar(stdscr, account_filter, account_filter_mode)
            if account_filter_mode:
                # Override footer with filter-mode hints
                rows_now, cols_now = stdscr.getmaxyx()
                safe_addstr(
                    stdscr,
                    rows_now - 1,
                    0,
                    " " * (cols_now - 1),
                    curses.color_pair(CP_NORMAL),
                )
                draw_footer(stdscr, "filter")
            if mode == "confirm" and filtered:
                draw_confirm_prompt(stdscr, filtered[selected], "Close")
            elif mode == "confirm_restart" and filtered:
                draw_confirm_prompt(stdscr, filtered[selected], "Restart")
        elif mode == "new":
            draw_new_form(stdscr, new_fields, new_active, new_cursor)
        elif mode == "pick":
            draw_pick_view(stdscr, pick_filtered, pick_selected, pick_running_count)
            if msg and msg_frames > 0:
                draw_status_message(stdscr, msg)
            elif pick_search_active or pick_query:
                draw_search_bar(
                    stdscr,
                    pick_query,
                    pick_cursor,
                    pick_search_active,
                    len(pick_filtered),
                )
        elif mode == "help":
            draw_help_overlay(stdscr, help_scroll)

        stdscr.refresh()

        # Tick down transient message
        if msg_frames > 0:
            msg_frames -= 1
            if msg_frames == 0:
                msg = ""

        key = stdscr.getch()
        if key == curses.KEY_RESIZE:
            continue
        if key == -1:
            if mode == "list" and not search_active and refresh_threshold > 0:
                refresh_ticks += 1
                if refresh_ticks >= refresh_threshold:
                    refresh_ticks = 0
                    new_sessions = query_sessions(socket, managed_only=(not show_all))
                    _check_idle_notifications(new_sessions)
                    old_fp = _session_fingerprint(filtered)
                    new_filtered = _apply_filters(
                        new_sessions, search_query, account_filter
                    )
                    if _session_fingerprint(new_filtered) != old_fp:
                        # Preserve selection by matching tab_id
                        old_tab_id = (
                            filtered[selected]["tab_id"]
                            if filtered and selected < len(filtered)
                            else None
                        )
                        sessions = new_sessions
                        filtered = new_filtered
                        selected = 0
                        if old_tab_id is not None:
                            for i, s in enumerate(filtered):
                                if s["tab_id"] == old_tab_id:
                                    selected = i
                                    break
                        selected = min(selected, max(0, len(filtered) - 1))
            continue

        # Reset auto-refresh timer on any keypress
        refresh_ticks = 0

        # ── List mode ────────────────────────────────────────────
        if mode == "list":
            # ── Search-active input handling ─────────────────────
            if search_active:
                if key == 27:  # Escape -- cancel search
                    search_active = False
                    search_query = ""
                    search_cursor = 0
                    filtered = _apply_filters(sessions, search_query, account_filter)
                    selected = min(selected, max(0, len(filtered) - 1))
                    continue
                elif key in (10, 13, curses.KEY_ENTER):  # Accept filter
                    search_active = False
                    continue
                elif key in (curses.KEY_UP, ord("k"), ord("K")) and filtered:
                    selected = (selected - 1) % len(filtered)
                    continue
                elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and filtered:
                    selected = (selected + 1) % len(filtered)
                    continue
                elif key in (curses.KEY_BACKSPACE, 127, 8):
                    if search_cursor > 0:
                        search_query = (
                            search_query[: search_cursor - 1]
                            + search_query[search_cursor:]
                        )
                        search_cursor -= 1
                        filtered = _apply_filters(
                            sessions, search_query, account_filter
                        )
                        selected = min(selected, max(0, len(filtered) - 1))
                    continue
                elif key == curses.KEY_LEFT:
                    search_cursor = max(0, search_cursor - 1)
                    continue
                elif key == curses.KEY_RIGHT:
                    search_cursor = min(len(search_query), search_cursor + 1)
                    continue
                elif 32 <= key < 127:
                    search_query = (
                        search_query[:search_cursor]
                        + chr(key)
                        + search_query[search_cursor:]
                    )
                    search_cursor += 1
                    filtered = _apply_filters(sessions, search_query, account_filter)
                    selected = min(selected, max(0, len(filtered) - 1))
                    continue
                else:
                    continue

            # Number keys for account filter toggling (only in filter mode)
            if account_filter_mode and ord("1") <= key <= ord("9"):
                idx = key - ord("1")
                if idx < len(_cfg._account_names):
                    name = _cfg._account_names[idx]
                    if account_filter is None:
                        # First press: solo this account
                        account_filter = {name}
                    elif name in account_filter:
                        # Remove this account
                        account_filter.discard(name)
                        if not account_filter:
                            # Last account removed: clear filter entirely
                            account_filter = None
                    else:
                        # Add this account
                        account_filter.add(name)
                    filtered = _apply_filters(sessions, search_query, account_filter)
                    selected = min(selected, max(0, len(filtered) - 1))
                continue

            if key in (ord("q"), ord("Q")):
                return
            if (
                key == 27
            ):  # Escape -- cascade: filter mode -> search -> account filter -> quit
                if account_filter_mode:
                    account_filter_mode = False
                    account_filter = None
                    filtered = _apply_filters(sessions, search_query, account_filter)
                    selected = min(selected, max(0, len(filtered) - 1))
                elif search_query:
                    search_query = ""
                    search_cursor = 0
                    filtered = _apply_filters(sessions, search_query, account_filter)
                    selected = min(selected, max(0, len(filtered) - 1))
                elif account_filter is not None:
                    account_filter = None
                    filtered = _apply_filters(sessions, search_query, account_filter)
                    selected = min(selected, max(0, len(filtered) - 1))
                else:
                    return
            elif key == ord("f"):
                account_filter_mode = not account_filter_mode
            elif key == ord("/"):
                search_active = True
                search_query = ""
                search_cursor = 0
                filtered = _apply_filters(sessions, search_query, account_filter)
            elif key in (curses.KEY_UP, ord("k"), ord("K")) and filtered:
                selected = (selected - 1) % len(filtered)
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and filtered:
                selected = (selected + 1) % len(filtered)
            elif key in (10, 13, curses.KEY_ENTER) and filtered:
                do_focus(socket, filtered[selected]["tab_id"])
                return
            elif key in (ord("x"), ord("X")) and filtered:
                mode = "confirm"
            elif key in (ord("n"), ord("N")):
                mode = "new"
                new_fields = {
                    "title": "",
                    "dir": "~/",
                    "account": default_account,
                    "flags": "",
                    "pw": default_pw,
                }
                new_active = 0
                new_cursor = 0
            elif key == ord("l"):
                if _cfg._sessions:
                    mode = "pick"
                    pick_selected = 0
                    pick_running = {s["title"] for s in sessions}
                    pick_filtered, pick_running_count = _sort_pick_entries(
                        list(_cfg._sessions), pick_running
                    )
                    pick_query = ""
                    pick_cursor = 0
                    pick_search_active = False
                else:
                    msg = "No sessions configured in sessions.conf"
                    msg_frames = 20
            elif key in (ord("a"), ord("A")):
                show_all = not show_all
                sessions = query_sessions(socket, managed_only=(not show_all))
                filtered = _apply_filters(sessions, search_query, account_filter)
                selected = min(selected, max(0, len(filtered) - 1))
                label = "all" if show_all else "managed"
                msg = f"Showing {label} sessions ({len(filtered)} found)"
                msg_frames = 20
            elif key == ord("d"):
                show_detail = not show_detail
            elif key == ord("r"):
                sessions = query_sessions(socket, managed_only=(not show_all))
                filtered = _apply_filters(sessions, search_query, account_filter)
                selected = min(selected, max(0, len(filtered) - 1))
                msg = f"{len(filtered)} session(s) found"
                msg_frames = 20
            elif key == ord("R") and filtered:
                mode = "confirm_restart"
            elif key == ord("?"):
                mode = "help"
                help_scroll = 0

        # ── Help overlay ──────────────────────────────────────────
        elif mode == "help":
            help_max = max(0, len(_HELP_LINES) - (stdscr.getmaxyx()[0] - 3))
            if key in (ord("?"), ord("q"), ord("Q"), 27):
                mode = "list"
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
                help_scroll = min(help_scroll + 1, help_max)
            elif key in (curses.KEY_UP, ord("k"), ord("K")):
                help_scroll = max(help_scroll - 1, 0)

        # ── Confirm close ────────────────────────────────────────
        elif mode == "confirm":
            if key in (ord("y"), ord("Y")) and filtered:
                do_close(socket, filtered[selected]["tab_id"])
                sessions = query_sessions(socket, managed_only=(not show_all))
                filtered = _apply_filters(sessions, search_query, account_filter)
                selected = min(selected, max(0, len(filtered) - 1))
                msg = "Session closed"
                msg_frames = 20
            mode = "list"

        # ── Confirm restart ──────────────────────────────────────
        elif mode == "confirm_restart":
            if key in (ord("y"), ord("Y")) and filtered:
                do_restart(socket, filtered[selected])
                sessions = query_sessions(socket, managed_only=(not show_all))
                filtered = _apply_filters(sessions, search_query, account_filter)
                selected = min(selected, max(0, len(filtered) - 1))
                msg = "Session restarted"
                msg_frames = 20
            mode = "list"

        # ── New session form ─────────────────────────────────────
        elif mode == "new":
            field_keys = ["title", "dir", "account", "flags", "pw"]
            last_field = len(field_keys) - 1
            fk = field_keys[new_active]

            if key == 27:  # Esc -- cancel
                mode = "list"
            elif key == 9:  # Tab
                if fk == "account":
                    new_fields["account"] = _cycle_account(new_fields["account"])
                elif fk == "pw":
                    new_fields["pw"] = _cycle_pw(new_fields["pw"])
                else:
                    new_active = min(new_active + 1, last_field)
                    new_cursor = len(new_fields[field_keys[new_active]])
            elif key in (10, 13, curses.KEY_ENTER):
                if new_active < last_field:
                    new_active += 1
                    new_cursor = len(new_fields[field_keys[new_active]])
                else:
                    # Submit the form
                    t = new_fields["title"].strip()
                    d = new_fields["dir"].strip()
                    a = new_fields["account"]
                    fl = new_fields["flags"].strip()
                    pw_val = new_fields["pw"]
                    pw_val = "" if pw_val == "none" else pw_val
                    if t and d:
                        loc = compute_insert_location(socket, a, t)
                        do_launch(socket, t, d, a, fl, pw_val, location=loc)
                        sessions = query_sessions(socket, managed_only=(not show_all))
                        filtered = _apply_filters(
                            sessions, search_query, account_filter
                        )
                        selected = max(0, len(filtered) - 1)
                        msg = f'Launched "{t}"'
                        msg_frames = 20
                    else:
                        msg = "Title and directory are required"
                        msg_frames = 20
                    mode = "list"
            elif fk not in ("account", "pw"):
                # Text editing in title / dir fields
                val = new_fields[fk]
                if key in (curses.KEY_BACKSPACE, 127, 8):
                    if new_cursor > 0:
                        new_fields[fk] = val[: new_cursor - 1] + val[new_cursor:]
                        new_cursor -= 1
                elif key == curses.KEY_LEFT:
                    new_cursor = max(0, new_cursor - 1)
                elif key == curses.KEY_RIGHT:
                    new_cursor = min(len(new_fields[fk]), new_cursor + 1)
                elif key == curses.KEY_HOME or key == 1:  # Ctrl+A
                    new_cursor = 0
                elif key == curses.KEY_END or key == 5:  # Ctrl+E
                    new_cursor = len(new_fields[fk])
                elif 32 <= key < 127:
                    new_fields[fk] = val[:new_cursor] + chr(key) + val[new_cursor:]
                    new_cursor += 1

        # ── Pick mode (launch from config) ────────────────────────
        elif mode == "pick":
            if pick_search_active:
                if key == 27:  # Escape -- cancel search
                    pick_search_active = False
                    pick_query = ""
                    pick_cursor = 0
                    pick_filtered, pick_running_count = _sort_pick_entries(
                        list(_cfg._sessions), pick_running
                    )
                    pick_selected = min(pick_selected, max(0, len(pick_filtered) - 1))
                    continue
                elif key in (10, 13, curses.KEY_ENTER):
                    pick_search_active = False
                    continue
                elif key in (curses.KEY_UP, ord("k"), ord("K")) and pick_filtered:
                    pick_selected = (pick_selected - 1) % len(pick_filtered)
                    continue
                elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and pick_filtered:
                    pick_selected = (pick_selected + 1) % len(pick_filtered)
                    continue
                elif key in (curses.KEY_BACKSPACE, 127, 8):
                    if pick_cursor > 0:
                        pick_query = (
                            pick_query[: pick_cursor - 1] + pick_query[pick_cursor:]
                        )
                        pick_cursor -= 1
                        pick_filtered = _filter_config_sessions(
                            _cfg._sessions, pick_query
                        )
                        pick_filtered, pick_running_count = _sort_pick_entries(
                            pick_filtered, pick_running
                        )
                        pick_selected = min(
                            pick_selected, max(0, len(pick_filtered) - 1)
                        )
                    continue
                elif key == curses.KEY_LEFT:
                    pick_cursor = max(0, pick_cursor - 1)
                    continue
                elif key == curses.KEY_RIGHT:
                    pick_cursor = min(len(pick_query), pick_cursor + 1)
                    continue
                elif 32 <= key < 127:
                    pick_query = (
                        pick_query[:pick_cursor] + chr(key) + pick_query[pick_cursor:]
                    )
                    pick_cursor += 1
                    pick_filtered = _filter_config_sessions(_cfg._sessions, pick_query)
                    pick_filtered, pick_running_count = _sort_pick_entries(
                        pick_filtered, pick_running
                    )
                    pick_selected = min(pick_selected, max(0, len(pick_filtered) - 1))
                    continue
                else:
                    continue

            if key in (ord("q"), ord("Q"), 27):  # q or Esc -- back to list
                if pick_query and key == 27:
                    pick_query = ""
                    pick_cursor = 0
                    pick_filtered, pick_running_count = _sort_pick_entries(
                        list(_cfg._sessions), pick_running
                    )
                    pick_selected = min(pick_selected, max(0, len(pick_filtered) - 1))
                else:
                    mode = "list"
            elif key == ord("/"):
                pick_search_active = True
                pick_query = ""
                pick_cursor = 0
                pick_filtered, pick_running_count = _sort_pick_entries(
                    list(_cfg._sessions), pick_running
                )
            elif key in (curses.KEY_UP, ord("k"), ord("K")) and pick_filtered:
                pick_selected = (pick_selected - 1) % len(pick_filtered)
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and pick_filtered:
                pick_selected = (pick_selected + 1) % len(pick_filtered)
            elif key in (10, 13, curses.KEY_ENTER) and pick_filtered:
                entry = pick_filtered[pick_selected]
                pw_val = entry["pw"]
                loc = compute_insert_location(
                    socket, entry["account"], entry["title"]
                )
                do_launch(
                    socket,
                    entry["title"],
                    entry["dir"],
                    entry["account"],
                    entry["flags"],
                    pw_val,
                    location=loc,
                )
                sessions = query_sessions(socket, managed_only=(not show_all))
                filtered = _apply_filters(sessions, search_query, account_filter)
                selected = max(0, len(filtered) - 1)
                msg = f'Launched "{entry["title"]}"'
                msg_frames = 20
                mode = "list"
