"""Main event loop and session fingerprinting for the Claude Code Dashboard."""

import curses
import os
import shutil
import time

from cc_dashboard import config as _cfg
from cc_dashboard.colors import CP_NORMAL, CP_WARN, init_colors
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.filters import (
    SORT_LABELS,
    SORT_MODES,
    _apply_filters,
    _apply_sort,
    _filter_config_sessions,
    _sort_pick_entries,
)
from cc_dashboard.kitty_remote import (
    OVERRIDE_MARKER,
    _append_override_marker,
    _strip_override_marker,
    compute_insert_location,
    generate_clone_title,
    _spawn_detached_close,
    do_close,
    do_cross_instance_focus,
    do_focus,
    do_create_worktree,
    do_launch,
    do_remove_worktree,
    check_git_safety,
    detect_worktree,
    do_move,
    do_open_editor,
    do_rename,
    do_restart,
    get_all_kitty_sockets,
    get_host_tab_id,
    get_kitty_socket,
    get_window_text,
    query_all_sessions,
)
from cc_dashboard.kitty_remote import list_activity_logs, load_activity_log, resolve_session_id, run_hooks_sync
from cc_dashboard.views import (
    _HELP_LINES,
    DETAIL_PANE_HEIGHT,
    _check_idle_notifications,
    _cycle_account,
    _cycle_instance,
    _cycle_pw,
    _hooks_event_rows,
    _shorten_home,
    draw_config_action_picker,
    draw_config_picker,
    draw_config_show,
    draw_batch_confirm_prompt,
    draw_confirm_prompt,
    draw_delete_confirm,
    draw_delete_view,
    draw_hooks_cmd_input,
    draw_hooks_event_view,
    draw_hooks_sync_confirm,
    draw_hooks_view,
    draw_actlog_browse,
    draw_actlog_view,
    _format_log_entry,
    draw_sort_picker,
    draw_peek_overlay,
    draw_detail_pane,
    draw_filter_bar,
    draw_footer,
    draw_help_overlay,
    draw_list_view,
    draw_move_picker,
    draw_new_form,
    draw_override_picker,
    draw_pick_view,
    draw_rename_input,
    draw_search_bar,
    draw_status_message,
    draw_clone_input,
    draw_worktree_input,
    _scan_directories,
    draw_dir_dropdown,
)


def _compute_peek_matches(lines, query):
    """Return sorted list of line indices containing query (case-insensitive)."""
    if not query:
        return []
    q = query.lower()
    return [i for i, line in enumerate(lines) if q in line.lower()]


def _rstrip_blank_lines(lines):
    """Remove trailing blank/whitespace-only lines from a list."""
    while lines and not lines[-1].strip():
        lines.pop()
    return lines


def _session_fingerprint(sessions):
    """Return a hashable fingerprint of session state for change detection."""
    return tuple(
        (
            s.get("socket", ""),
            s["tab_id"],
            s["title"],
            s["status"],
            s["cwd"],
            s["account"],
            s.get("window_count", 1),
            (s.get("activity") or {}).get("last_event_ts"),
            (s.get("activity") or {}).get("last_event_type"),
            (s.get("git") or {}).get("branch"),
            (s.get("git") or {}).get("dirty"),
        )
        for s in sessions
    )


def dashboard(stdscr):
    """Run the interactive dashboard loop."""
    curses.curs_set(0)
    init_colors()
    stdscr.timeout(100)

    # Determine the "home" socket — the Kitty instance running this dashboard.
    # Used for host-tab detection (restart/close of the tab we live in).
    home_socket = os.environ.get("KITTY_LISTEN_ON", "") or get_kitty_socket()

    # Discover all active Kitty instances for the instance selector
    discovered_sockets = get_all_kitty_sockets()  # [(socket, label), ...]
    instance_labels = [label for _, label in discovered_sockets]
    instance_socket_map = {label: sock for sock, label in discovered_sockets}
    # Find the home instance label for defaults
    home_instance_label = ""
    for sock, label in discovered_sockets:
        if sock == home_socket:
            home_instance_label = label
            break
    if not home_instance_label and instance_labels:
        home_instance_label = instance_labels[0]
    multi_instance = len(discovered_sockets) > 1

    show_all = False
    sessions = query_all_sessions(managed_only=True)
    selected = 0
    marked = set()  # set of tab_ids for multi-select
    batch_targets = []  # resolved sessions for batch operations

    # Detect the tab that hosts this dashboard overlay so we can handle
    # restart/close operations on it safely.
    host_tab_id = get_host_tab_id(home_socket)

    # The window ID of this overlay kitten (from KITTY_WINDOW_ID).
    # get-text on this ID returns the dashboard's own output, not the
    # underlying session, so we skip it when peeking.
    _host_wid_str = os.environ.get("KITTY_WINDOW_ID", "")
    host_window_id = int(_host_wid_str) if _host_wid_str.isdigit() else 0

    # Auto-scroll to the focused session, if one exists
    for i, s in enumerate(sessions):
        if s["status"] == "focused":
            selected = i
            break

    show_detail = False
    mode = "list"  # list | confirm | confirm_restart | confirm_batch_close | confirm_batch_restart | confirm_save | rename | worktree | new | pick | delete | help | move_picker | config_pick | config_action | config_show | sort_pick | hooks | hooks_event | hooks_sync_confirm | hooks_cmd_input
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
    pick_target_instance = home_instance_label  # target instance for pick launches

    # Auto-refresh state
    refresh_interval = _cfg._default_dashboard_refresh
    refresh_ticks = 0
    refresh_threshold = refresh_interval * 10  # each tick is ~100ms

    # Search/filter state
    search_query = ""
    search_active = False
    search_cursor = 0
    filtered = sessions
    sort_mode = "default"

    # Account filter state
    account_filter = None  # None = show all; set of names = show only these
    account_filter_mode = False  # True when filter bar is interactive

    # Rename state
    rename_value = ""
    rename_cursor = 0

    # Override (account picker) state
    override_selected = 0
    override_source = "list"  # "list" or "pick" -- where O was pressed

    # Move picker state
    move_selected = 0

    # Sort picker state
    sort_selected = 0

    # Delete mode state
    delete_entries: list[dict] = []
    delete_selected = 0
    delete_step = "select"  # select | confirm_config | confirm_data
    delete_running: set[str] = set()
    delete_search_active = False
    delete_query = ""
    delete_cursor = 0
    delete_filtered: list[dict] = []
    delete_running_count = 0
    delete_target_entry: dict = {}  # the entry being confirmed for deletion
    delete_warnings: list[str] = []  # git safety warnings for confirm_data step

    # Config browser state
    config_selected = 0
    config_action_selected = 0
    config_file_label = ""
    config_file_path = ""
    config_show_lines = []
    config_show_scroll = 0

    # Hooks manager state
    hooks_drift = {}           # {account_name: "in_sync"|"drifted"|"missing"}
    hooks_selected = 0         # cursor position in hooks account list
    hooks_event_selected = 0   # cursor position in hooks event list
    hooks_account = ""         # account name when in hooks_event mode
    hooks_cmd_value = ""       # text input for custom command
    hooks_cmd_cursor = 0       # cursor position in command input
    hooks_target_event = ""    # event name for command input
    hooks_sync_scope = ""      # "all" or account name for sync confirm

    # Peek state
    peek_lines = []
    peek_scroll = 0
    peek_extent = "screen"
    peek_session_title = ""
    peek_error = ""
    peek_search_query = ""
    peek_search_active = False
    peek_search_cursor = 0
    peek_match_lines = []
    peek_match_index = 0

    # Activity log viewer state
    actlog_lines, actlog_scroll, actlog_session_title, actlog_session_id = [], 0, "", ""
    actlog_summary = {}
    actlog_search_query, actlog_search_active, actlog_search_cursor = "", False, 0
    actlog_match_lines, actlog_match_index = [], 0

    # Activity log browser state
    actlog_browse_entries, actlog_browse_selected, actlog_browse_scroll = [], 0, 0

    # New-session form state -- default to first loaded account
    default_account = _cfg._account_names[0] if _cfg._account_names else "work"
    default_pw = (
        _cfg._default_project_window if _cfg._default_project_window else "none"
    )

    def _make_new_fields():
        fields = {
            "title": "",
            "dir": "~/",
            "account": default_account,
            "flags": "",
            "pw": default_pw,
            "env": "",
            "save": "yes",
        }
        if multi_instance:
            fields["instance"] = home_instance_label
        return fields

    new_fields = _make_new_fields()
    new_active = 0
    new_cursor = 0
    form_mode = "new"  # "new" or "edit"
    edit_original_title = ""
    dir_browse_active = False
    dir_browse_entries = []
    dir_browse_selected = 0
    dir_browse_scroll = 0
    dir_browse_show_hidden = False

    def _reload_all_config():
        """Re-read all config files from disk and refresh derived state."""
        nonlocal default_account, default_pw, refresh_interval, refresh_threshold
        _cfg.load_accounts()
        _cfg.load_defaults()
        _cfg.load_sessions()
        default_account = _cfg._account_names[0] if _cfg._account_names else "work"
        default_pw = (
            _cfg._default_project_window if _cfg._default_project_window else "none"
        )
        refresh_interval = _cfg._default_dashboard_refresh
        refresh_threshold = refresh_interval * 10

    def _refilter():
        nonlocal filtered, selected
        old_tab_id = (
            filtered[selected]["tab_id"]
            if filtered and selected < len(filtered)
            else None
        )
        filtered = _apply_sort(
            _apply_filters(sessions, search_query, account_filter), sort_mode
        )
        if old_tab_id is not None:
            for i, s in enumerate(filtered):
                if s["tab_id"] == old_tab_id:
                    selected = i
                    break
        selected = min(selected, max(0, len(filtered) - 1))

    while True:
        # ── Draw ─────────────────────────────────────────────────
        if mode in ("list", "confirm", "confirm_restart", "confirm_batch_close", "confirm_batch_restart", "confirm_save", "rename", "worktree", "clone", "move_picker", "config_pick", "config_action", "sort_pick") or (
            mode == "override" and override_source == "list"
        ):
            rows, _cols = stdscr.getmaxyx()
            detail_visible = show_detail and rows >= 20 and bool(filtered)
            draw_list_view(stdscr, filtered, selected, detail_visible=detail_visible, sort_label=SORT_LABELS[sort_mode], marked=marked)
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
            elif mode == "confirm_batch_close":
                draw_batch_confirm_prompt(stdscr, batch_targets, "Close")
                draw_footer(stdscr, "confirm_batch_close")
            elif mode == "confirm_batch_restart":
                draw_batch_confirm_prompt(stdscr, batch_targets, "Restart")
                draw_footer(stdscr, "confirm_batch_restart")
            elif mode == "confirm_save":
                rows_now, _ = stdscr.getmaxyx()
                prompt = f"Save {len(sessions)} session(s) to sessions.conf? y/n"
                safe_addstr(
                    stdscr,
                    rows_now - 3,
                    2,
                    prompt,
                    curses.color_pair(CP_WARN) | curses.A_BOLD,
                )
            elif mode == "rename":
                draw_rename_input(stdscr, rename_value, rename_cursor)
                draw_footer(stdscr, "rename")
            elif mode == "worktree" and filtered:
                draw_worktree_input(stdscr, filtered[selected], worktree_branch, worktree_cursor)
                draw_footer(stdscr, "worktree")
            elif mode == "clone" and filtered:
                draw_clone_input(stdscr, filtered[selected], clone_title, clone_cursor)
                draw_footer(stdscr, "clone")
            elif mode == "override" and filtered:
                draw_override_picker(stdscr, filtered[selected], override_selected)
                draw_footer(stdscr, "override")
            elif mode == "move_picker" and filtered:
                draw_move_picker(stdscr, filtered[selected], instance_labels, move_selected)
                draw_footer(stdscr, "move")
            elif mode == "config_pick":
                draw_config_picker(stdscr, _cfg._all_config_paths(), config_selected)
                draw_footer(stdscr, "config_pick")
            elif mode == "config_action":
                draw_config_action_picker(stdscr, config_file_label, config_file_path, config_action_selected)
                draw_footer(stdscr, "config_action")
            elif mode == "sort_pick":
                draw_sort_picker(stdscr, sort_selected, sort_mode)
                draw_footer(stdscr, "sort_pick")
        elif mode == "config_show":
            draw_config_show(stdscr, config_file_label, config_file_path, config_show_lines, config_show_scroll)
        elif mode == "peek":
            draw_peek_overlay(
                stdscr, peek_session_title, peek_lines, peek_scroll, peek_extent,
                error=peek_error,
                search_query=peek_search_query,
                search_active=peek_search_active,
                search_cursor=peek_search_cursor,
                match_line_indices=peek_match_lines,
                match_index=peek_match_index,
            )
        elif mode == "new":
            form_title = "Edit Session" if form_mode == "edit" else "New Session"
            draw_new_form(stdscr, new_fields, new_active, new_cursor,
                          multi_instance=(multi_instance and form_mode == "new"),
                          form_title=form_title)
            if dir_browse_active:
                draw_dir_dropdown(stdscr, dir_browse_entries, dir_browse_selected,
                                  dir_browse_scroll, dir_browse_show_hidden)
                draw_footer(stdscr, "new_dir_browse")
        elif mode == "pick" or (mode == "override" and override_source == "pick"):
            draw_pick_view(stdscr, pick_filtered, pick_selected, pick_running_count)
            if msg and msg_frames > 0:
                draw_status_message(stdscr, msg)
            elif multi_instance and not pick_search_active and not pick_query:
                draw_status_message(stdscr, f"Target: {pick_target_instance}  (i to cycle)")
            elif pick_search_active or pick_query:
                draw_search_bar(
                    stdscr,
                    pick_query,
                    pick_cursor,
                    pick_search_active,
                    len(pick_filtered),
                )
            if mode == "override" and pick_filtered:
                draw_override_picker(
                    stdscr, pick_filtered[pick_selected], override_selected
                )
                draw_footer(stdscr, "override")
        elif mode == "delete":
            draw_delete_view(stdscr, delete_filtered, delete_selected, delete_running_count)
            if delete_step in ("confirm_config", "confirm_data"):
                draw_delete_confirm(stdscr, delete_step, delete_target_entry, delete_warnings)
                draw_footer(stdscr, "delete_confirm")
            elif msg and msg_frames > 0:
                draw_status_message(stdscr, msg)
            elif delete_search_active or delete_query:
                draw_search_bar(
                    stdscr,
                    delete_query,
                    delete_cursor,
                    delete_search_active,
                    len(delete_filtered),
                )
        elif mode == "hooks":
            draw_hooks_view(stdscr, _cfg._accounts, hooks_drift,
                            _cfg._hooks_global, _cfg._hooks_overrides, hooks_selected)
        elif mode == "hooks_event":
            draw_hooks_event_view(stdscr, hooks_account,
                                  _cfg._hooks_global, _cfg._hooks_overrides,
                                  hooks_drift.get(hooks_account, "missing"), hooks_event_selected)
        elif mode == "hooks_sync_confirm":
            draw_hooks_sync_confirm(stdscr, hooks_sync_scope)
        elif mode == "hooks_cmd_input":
            draw_hooks_cmd_input(stdscr, hooks_account, hooks_target_event,
                                 hooks_cmd_value, hooks_cmd_cursor)
        elif mode == "actlog":
            draw_actlog_view(
                stdscr, actlog_session_title, actlog_lines, actlog_scroll,
                actlog_summary,
                search_query=actlog_search_query,
                search_active=actlog_search_active,
                search_cursor=actlog_search_cursor,
                match_line_indices=actlog_match_lines,
                match_index=actlog_match_index,
            )
        elif mode == "actlog_browse":
            draw_actlog_browse(stdscr, actlog_browse_entries, actlog_browse_selected,
                               actlog_browse_scroll)
            if msg and msg_frames > 0:
                draw_status_message(stdscr, msg)
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
                    _reload_all_config()
                    new_sessions = query_all_sessions(managed_only=(not show_all))
                    _check_idle_notifications(new_sessions)
                    old_fp = _session_fingerprint(filtered)
                    new_filtered = _apply_sort(
                        _apply_filters(new_sessions, search_query, account_filter),
                        sort_mode,
                    )
                    if _session_fingerprint(new_filtered) != old_fp:
                        # Preserve selection by matching tab_id
                        old_tab_id = (
                            filtered[selected]["tab_id"]
                            if filtered and selected < len(filtered)
                            else None
                        )
                        sessions = new_sessions
                        marked &= {s["tab_id"] for s in sessions}
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
                    _refilter()
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
                        _refilter()
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
                    _refilter()
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
                    _refilter()
                continue

            if key in (ord("q"), ord("Q")):
                return
            if (
                key == 27
            ):  # Escape -- cascade: marks -> filter mode -> search -> account filter -> quit
                if marked:
                    marked.clear()
                elif account_filter_mode:
                    account_filter_mode = False
                    account_filter = None
                    _refilter()
                elif search_query:
                    search_query = ""
                    search_cursor = 0
                    _refilter()
                elif account_filter is not None:
                    account_filter = None
                    _refilter()
                else:
                    return
            elif key == ord("f"):
                account_filter_mode = not account_filter_mode
            elif key == ord("/"):
                search_active = True
                search_query = ""
                search_cursor = 0
                _refilter()
            elif key in (curses.KEY_UP, ord("k"), ord("K")) and filtered:
                selected = (selected - 1) % len(filtered)
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and filtered:
                selected = (selected + 1) % len(filtered)
            elif key == ord(" ") and filtered:
                tid = filtered[selected]["tab_id"]
                marked.symmetric_difference_update({tid})
                selected = min(selected + 1, len(filtered) - 1)
            elif key == ord("U") and marked:
                marked.clear()
            elif key in (10, 13, curses.KEY_ENTER) and filtered:
                sess = filtered[selected]
                do_cross_instance_focus(
                    sess.get("socket", home_socket), sess["tab_id"], home_socket
                )
                return
            elif key in (ord("x"), ord("X")) and filtered:
                if marked:
                    batch_targets = [s for s in filtered if s["tab_id"] in marked]
                    if batch_targets:
                        mode = "confirm_batch_close"
                    else:
                        mode = "confirm"
                else:
                    mode = "confirm"
            elif key in (ord("n"), ord("N")):
                mode = "new"
                form_mode = "new"
                new_fields = _make_new_fields()
                new_active = 0
                new_cursor = 0
                dir_browse_active = False
            elif key == ord("e") and filtered:
                sess = filtered[selected]
                stripped = _strip_override_marker(sess["title"])
                cfg_entry = _cfg._find_config_session(stripped)
                if cfg_entry is None:
                    msg = "Session not in config (use S to save first)"
                    msg_frames = 20
                else:
                    mode = "new"
                    form_mode = "edit"
                    edit_original_title = cfg_entry["title"]
                    pw_val = cfg_entry.get("pw", "")
                    new_fields = {
                        "title": cfg_entry["title"],
                        "dir": cfg_entry["dir"],
                        "account": cfg_entry["account"],
                        "flags": cfg_entry.get("flags", ""),
                        "pw": pw_val if pw_val else "none",
                        "env": cfg_entry.get("env", ""),
                    }
                    new_active = 0
                    new_cursor = len(new_fields["title"])
                    dir_browse_active = False
            elif key == ord("l"):
                _reload_all_config()
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
                    pick_target_instance = home_instance_label
                else:
                    msg = "No sessions configured in sessions.conf"
                    msg_frames = 20
            elif key == ord("D"):
                _reload_all_config()
                if _cfg._sessions:
                    mode = "delete"
                    delete_step = "select"
                    delete_running = {s["title"] for s in sessions}
                    # Build entries with type tags
                    delete_entries = []
                    for entry in _cfg._sessions:
                        e = dict(entry)
                        expanded = os.path.expanduser(e["dir"])
                        if os.path.isdir(expanded):
                            wt = detect_worktree(expanded)
                            if wt["is_worktree"]:
                                e["type_tag"] = "[worktree]"
                                e["wt_main_repo"] = wt["main_repo"]
                            elif os.path.isdir(os.path.join(expanded, ".git")):
                                e["type_tag"] = "[repo]"
                            else:
                                e["type_tag"] = "[dir]"
                        else:
                            e["type_tag"] = "[n/a]"
                        delete_entries.append(e)
                    delete_filtered, delete_running_count = _sort_pick_entries(
                        delete_entries, delete_running
                    )
                    delete_selected = 0
                    delete_query = ""
                    delete_cursor = 0
                    delete_search_active = False
                else:
                    msg = "No sessions configured in sessions.conf"
                    msg_frames = 20
            elif key in (ord("a"), ord("A")):
                show_all = not show_all
                sessions = query_all_sessions(managed_only=(not show_all))
                _refilter()
                label = "all" if show_all else "managed"
                msg = f"Showing {label} sessions ({len(filtered)} found)"
                msg_frames = 20
            elif key == ord("d"):
                show_detail = not show_detail
            elif key == ord("s"):
                mode = "sort_pick"
                sort_selected = SORT_MODES.index(sort_mode)
            elif key == ord("p") and filtered:
                sess = filtered[selected]
                win_id = sess.get("claude_window_id", 0)
                sess_socket = sess.get("socket", home_socket)
                peek_session_title = sess["title"]
                peek_extent = "screen"
                peek_scroll = 0
                peek_search_query = ""
                peek_search_active = False
                peek_search_cursor = 0
                peek_match_lines = []
                peek_match_index = 0
                if not win_id:
                    peek_error = "No window ID available for this session"
                    peek_lines = []
                elif host_window_id and win_id == host_window_id:
                    peek_error = "Dashboard overlay is active on this window"
                    peek_lines = []
                else:
                    text = get_window_text(sess_socket, win_id, peek_extent)
                    if text is None:
                        peek_error = "Failed to capture window text"
                        peek_lines = []
                    else:
                        peek_error = ""
                        peek_lines = _rstrip_blank_lines(text.splitlines())
                mode = "peek"
            elif key == 18:  # Ctrl+r -- refresh
                sessions = query_all_sessions(managed_only=(not show_all))
                marked &= {s["tab_id"] for s in sessions}
                _refilter()
                msg = f"{len(filtered)} session(s) found"
                msg_frames = 20
            elif key == ord("r") and filtered:
                mode = "rename"
                title = filtered[selected]["title"]
                # Strip the account prefix (X:) for editing
                if len(title) >= 2 and title[0].isupper() and title[1] == ":":
                    rename_value = title[2:]
                else:
                    rename_value = title
                # Strip trailing override marker so user doesn't see/edit it
                rename_value = _strip_override_marker(rename_value)
                rename_cursor = len(rename_value)
            elif key == ord("w") and filtered:
                sess = filtered[selected]
                if not sess.get("git"):
                    msg = "Not a git repository"
                    msg_frames = 20
                else:
                    mode = "worktree"
                    worktree_branch = ""
                    worktree_cursor = 0
            elif key == ord("R") and filtered:
                if marked:
                    batch_targets = [s for s in filtered if s["tab_id"] in marked]
                    if batch_targets:
                        mode = "confirm_batch_restart"
                    else:
                        mode = "confirm_restart"
                else:
                    mode = "confirm_restart"
            elif key == ord("S"):
                if sessions:
                    mode = "confirm_save"
                else:
                    msg = "No sessions to save"
                    msg_frames = 20
            elif key == ord("O") and filtered:
                mode = "override"
                override_source = "list"
                # Pre-select the session's current config dir
                current_dir = filtered[selected].get("config_dir", "")
                if not current_dir:
                    current_dir = _cfg._accounts.get(filtered[selected]["account"], {}).get(
                        "config_dir", ""
                    )
                dirs = _cfg._unique_config_dirs()
                override_selected = 0
                for i, d in enumerate(dirs):
                    if d == current_dir:
                        override_selected = i
                        break
            elif key == ord("m") and filtered and not multi_instance:
                msg = "Only one Kitty instance running"
                msg_frames = 20
            elif key == ord("m") and filtered and multi_instance:
                if marked:
                    batch_targets = [s for s in filtered if s["tab_id"] in marked]
                if not marked:
                    batch_targets = []
                sess = filtered[selected]
                current_inst = sess.get("instance_label", "")
                targets = [lbl for lbl in instance_labels if lbl != current_inst]
                if not targets:
                    msg = "No other instances to move to"
                    msg_frames = 20
                elif len(targets) == 1 and not batch_targets:
                    # Only one target, single session — move immediately
                    target_socket = instance_socket_map.get(targets[0], "")
                    if not target_socket:
                        msg = f"Instance {targets[0]} is no longer available"
                        msg_frames = 20
                    else:
                        is_host = host_tab_id is not None and sess["tab_id"] == host_tab_id
                        sess_socket = sess.get("socket", home_socket)
                        r_flags = sess.get("cc_flags", "")
                        r_pw = sess.get("cc_pw", "")
                        r_env = sess.get("cc_env", "")
                        if not r_flags and not r_pw and not r_env:
                            cfg_entry = _cfg._find_config_session(sess["title"])
                            if cfg_entry is None:
                                cfg_entry = _cfg._find_config_session(
                                    _strip_override_marker(sess["title"])
                                )
                            r_flags = cfg_entry.get("flags", "") if cfg_entry else ""
                            r_pw = cfg_entry.get("pw", "") if cfg_entry else ""
                            r_env = cfg_entry.get("env", "") if cfg_entry else ""
                        ok = do_move(
                            sess_socket,
                            target_socket,
                            sess,
                            flags=r_flags,
                            pw_override=r_pw,
                            host_tab_id=host_tab_id,
                            env_vars=r_env,
                        )
                        if is_host:
                            return
                        sessions = query_all_sessions(managed_only=(not show_all))
                        _refilter()
                        if ok:
                            msg = f"Session moved to {targets[0]}"
                        else:
                            msg = f"Instance {sess.get('instance_label', sess_socket)} is no longer available"
                        msg_frames = 20
                elif len(targets) == 1 and batch_targets:
                    # Only one target, batch move — move all marked immediately
                    target_socket = instance_socket_map.get(targets[0], "")
                    if not target_socket:
                        msg = f"Instance {targets[0]} is no longer available"
                        msg_frames = 20
                    else:
                        has_host = host_tab_id is not None
                        to_move = [s for s in batch_targets if s.get("instance_label", "") != targets[0]]
                        if not to_move:
                            msg = "All marked sessions already on target instance"
                            msg_frames = 20
                        else:
                            targets_sorted = sorted(
                                to_move,
                                key=lambda s: (1 if has_host and s["tab_id"] == host_tab_id else 0),
                            )
                            moved = 0
                            for t in targets_sorted:
                                r_flags = t.get("cc_flags", "")
                                r_pw = t.get("cc_pw", "")
                                r_env = t.get("cc_env", "")
                                if not r_flags and not r_pw and not r_env:
                                    cfg_entry = _cfg._find_config_session(t["title"])
                                    if cfg_entry is None:
                                        cfg_entry = _cfg._find_config_session(
                                            _strip_override_marker(t["title"])
                                        )
                                    r_flags = cfg_entry.get("flags", "") if cfg_entry else ""
                                    r_pw = cfg_entry.get("pw", "") if cfg_entry else ""
                                    r_env = cfg_entry.get("env", "") if cfg_entry else ""
                                t_socket = t.get("socket", home_socket)
                                is_host = has_host and t["tab_id"] == host_tab_id
                                ok = do_move(
                                    t_socket, target_socket, t,
                                    flags=r_flags, pw_override=r_pw,
                                    host_tab_id=host_tab_id,
                                    env_vars=r_env,
                                )
                                if is_host:
                                    return
                                if ok:
                                    moved += 1
                            marked.clear()
                            sessions = query_all_sessions(managed_only=(not show_all))
                            _refilter()
                            msg = f"Moved {moved} session(s) to {targets[0]}"
                            msg_frames = 20
                else:
                    mode = "move_picker"
                    move_selected = 0
            elif key == ord("?"):
                mode = "help"
                help_scroll = 0
            elif key == ord("c") and filtered:
                sess = filtered[selected]
                clone_title = generate_clone_title(sessions, sess["title"])
                clone_cursor = len(clone_title)
                mode = "clone"
            elif key == ord("C"):
                mode = "config_pick"
                config_selected = 0
            elif key == ord("H"):
                _cfg.load_hooks()
                hooks_drift = _cfg._detect_all_drift()
                hooks_selected = 0
                mode = "hooks"
            elif key == ord("L") and filtered:
                sess = filtered[selected]
                sid = resolve_session_id(sess["tab_id"])
                if sid:
                    entries, summary = load_activity_log(sid)
                    if entries:
                        actlog_session_title = sess["title"]
                        actlog_session_id = sid
                        actlog_summary = summary
                        actlog_lines = [_format_log_entry(e, stdscr.getmaxyx()[1]) for e in entries]
                        actlog_scroll = max(0, len(actlog_lines) - (stdscr.getmaxyx()[0] - 5))
                        actlog_search_query = ""
                        actlog_search_active = False
                        actlog_search_cursor = 0
                        actlog_match_lines = []
                        actlog_match_index = 0
                        mode = "actlog"
                    else:
                        msg = "No activity log entries for this session"
                        msg_frames = 20
                else:
                    msg = "No activity log found for this session"
                    msg_frames = 20

        # ── Sort picker ───────────────────────────────────────────
        elif mode == "sort_pick":
            if key in (27, ord("q"), ord("Q")):
                mode = "list"
            elif key in (curses.KEY_UP, ord("k"), ord("K")):
                sort_selected = (sort_selected - 1) % len(SORT_MODES)
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
                sort_selected = (sort_selected + 1) % len(SORT_MODES)
            elif key in (10, 13, curses.KEY_ENTER):
                sort_mode = SORT_MODES[sort_selected]
                _refilter()
                label = SORT_LABELS[sort_mode]
                msg = f"Sort: {label}" if label else "Sort: default (tab order)"
                msg_frames = 15
                mode = "list"

        # ── Hooks overview ────────────────────────────────────────
        elif mode == "hooks":
            account_list = list(_cfg._accounts)
            if key in (ord("j"), curses.KEY_DOWN) and account_list:
                hooks_selected = (hooks_selected + 1) % len(account_list)
            elif key in (ord("k"), curses.KEY_UP) and account_list:
                hooks_selected = (hooks_selected - 1) % len(account_list)
            elif key in (10, 13, curses.KEY_ENTER) and account_list:
                hooks_account = account_list[hooks_selected]
                hooks_event_selected = 0
                mode = "hooks_event"
            elif key == ord("s") and account_list:
                hooks_sync_scope = account_list[hooks_selected]
                mode = "hooks_sync_confirm"
            elif key == ord("S"):
                hooks_sync_scope = "all"
                mode = "hooks_sync_confirm"
            elif key in (ord("q"), 27):
                mode = "list"

        # ── Hooks event detail ───────────────────────────────────
        elif mode == "hooks_event":
            event_rows = _hooks_event_rows(hooks_account, _cfg._hooks_overrides)
            if key in (ord("j"), curses.KEY_DOWN) and event_rows:
                hooks_event_selected = (hooks_event_selected + 1) % len(event_rows)
            elif key in (ord("k"), curses.KEY_UP) and event_rows:
                hooks_event_selected = (hooks_event_selected - 1) % len(event_rows)
            elif key == ord(" ") and event_rows and hooks_event_selected < len(event_rows):
                row = event_rows[hooks_event_selected]
                if row[0] == "event":
                    _, event_name, _ = row
                    enabled = _cfg.toggle_hook_event(hooks_account, event_name)
                    hooks_drift[hooks_account] = _cfg._detect_drift(hooks_account)
                    state = "enabled" if enabled else "disabled"
                    msg = f"{event_name}: {state}"
                    msg_frames = 15
            elif key == ord("a") and event_rows and hooks_event_selected < len(event_rows):
                row = event_rows[hooks_event_selected]
                # Determine which event this row belongs to
                if row[0] == "event":
                    hooks_target_event = row[1]
                elif row[0] == "extra":
                    hooks_target_event = row[1]
                hooks_cmd_value = ""
                hooks_cmd_cursor = 0
                mode = "hooks_cmd_input"
            elif key == ord("d") and event_rows and hooks_event_selected < len(event_rows):
                row = event_rows[hooks_event_selected]
                if row[0] == "extra":
                    _, event_name, cmd_idx, cmd_str = row
                    _cfg.remove_extra_command(hooks_account, event_name, cmd_idx)
                    hooks_drift[hooks_account] = _cfg._detect_drift(hooks_account)
                    # Adjust selection if we removed the last row
                    new_rows = _hooks_event_rows(hooks_account, _cfg._hooks_overrides)
                    hooks_event_selected = min(hooks_event_selected, max(0, len(new_rows) - 1))
                    msg = "Command removed"
                    msg_frames = 15
            elif key == ord("s"):
                hooks_sync_scope = hooks_account
                mode = "hooks_sync_confirm"
            elif key == 27:
                mode = "hooks"

        # ── Hooks sync confirm ───────────────────────────────────
        elif mode == "hooks_sync_confirm":
            if key in (ord("y"), ord("Y")):
                if hooks_sync_scope == "all":
                    ok, output = run_hooks_sync()
                    scope_msg = "all accounts"
                else:
                    ok, output = run_hooks_sync(hooks_sync_scope)
                    scope_msg = hooks_sync_scope
                # Refresh drift
                hooks_drift = _cfg._detect_all_drift()
                if ok:
                    msg = f"Hooks synced for {scope_msg}"
                else:
                    msg = f"Sync failed: {output[:60]}"
                msg_frames = 25
                mode = "hooks"
            else:
                # Any non-y key cancels
                if hooks_sync_scope == "all" or hooks_sync_scope not in _cfg._accounts:
                    mode = "hooks"
                else:
                    mode = "hooks_event"

        # ── Hooks command input ──────────────────────────────────
        elif mode == "hooks_cmd_input":
            if key == 27:  # Esc -- cancel
                mode = "hooks_event"
            elif key in (10, 13, curses.KEY_ENTER):
                cmd = hooks_cmd_value.strip()
                if cmd:
                    _cfg.add_extra_command(hooks_account, hooks_target_event, cmd)
                    hooks_drift[hooks_account] = _cfg._detect_drift(hooks_account)
                    msg = "Command added"
                    msg_frames = 15
                mode = "hooks_event"
            elif key in (curses.KEY_BACKSPACE, 127, 8):
                if hooks_cmd_cursor > 0:
                    hooks_cmd_value = (
                        hooks_cmd_value[: hooks_cmd_cursor - 1]
                        + hooks_cmd_value[hooks_cmd_cursor:]
                    )
                    hooks_cmd_cursor -= 1
            elif key == curses.KEY_LEFT:
                hooks_cmd_cursor = max(0, hooks_cmd_cursor - 1)
            elif key == curses.KEY_RIGHT:
                hooks_cmd_cursor = min(len(hooks_cmd_value), hooks_cmd_cursor + 1)
            elif key == curses.KEY_HOME or key == 1:  # Ctrl+A
                hooks_cmd_cursor = 0
            elif key == curses.KEY_END or key == 5:  # Ctrl+E
                hooks_cmd_cursor = len(hooks_cmd_value)
            elif 32 <= key < 127:
                hooks_cmd_value = (
                    hooks_cmd_value[:hooks_cmd_cursor]
                    + chr(key)
                    + hooks_cmd_value[hooks_cmd_cursor:]
                )
                hooks_cmd_cursor += 1

        # ── Activity log viewer ────────────────────────────────────
        elif mode == "actlog":
            content_height = stdscr.getmaxyx()[0] - 5
            max_scroll = max(0, len(actlog_lines) - content_height)

            # ── Tier 1: search input active ──
            if actlog_search_active:
                if key == 27:  # Esc — cancel search
                    actlog_search_active = False
                    actlog_search_query = ""
                    actlog_search_cursor = 0
                    actlog_match_lines = []
                    actlog_match_index = 0
                elif key in (10, 13, curses.KEY_ENTER):  # Enter — confirm
                    actlog_search_active = False
                    if actlog_match_lines:
                        actlog_scroll = min(actlog_match_lines[0], max_scroll)
                        actlog_match_index = 0
                elif key in (127, 8, curses.KEY_BACKSPACE):
                    if actlog_search_cursor > 0:
                        actlog_search_query = (
                            actlog_search_query[: actlog_search_cursor - 1]
                            + actlog_search_query[actlog_search_cursor:]
                        )
                        actlog_search_cursor -= 1
                        actlog_match_lines = _compute_peek_matches(actlog_lines, actlog_search_query)
                        actlog_match_index = 0
                elif key == curses.KEY_LEFT:
                    actlog_search_cursor = max(0, actlog_search_cursor - 1)
                elif key == curses.KEY_RIGHT:
                    actlog_search_cursor = min(len(actlog_search_query), actlog_search_cursor + 1)
                elif 32 <= key <= 126:
                    ch = chr(key)
                    actlog_search_query = (
                        actlog_search_query[: actlog_search_cursor]
                        + ch
                        + actlog_search_query[actlog_search_cursor:]
                    )
                    actlog_search_cursor += 1
                    actlog_match_lines = _compute_peek_matches(actlog_lines, actlog_search_query)
                    actlog_match_index = 0
                continue

            # ── Tier 2: normal actlog navigation ──
            if key in (27, ord("q"), ord("Q")):
                if actlog_search_query:
                    actlog_search_query = ""
                    actlog_match_lines = []
                    actlog_match_index = 0
                else:
                    mode = "list"
            elif key == ord("/"):
                actlog_search_active = True
                actlog_search_query = ""
                actlog_search_cursor = 0
                actlog_match_lines = []
                actlog_match_index = 0
            elif key == ord("n"):
                if actlog_match_lines:
                    actlog_match_index = (actlog_match_index + 1) % len(actlog_match_lines)
                    actlog_scroll = min(actlog_match_lines[actlog_match_index], max_scroll)
                else:
                    actlog_scroll = min(actlog_scroll + content_height // 2, max_scroll)
            elif key == ord("N"):
                if actlog_match_lines:
                    actlog_match_index = (actlog_match_index - 1) % len(actlog_match_lines)
                    actlog_scroll = min(actlog_match_lines[actlog_match_index], max_scroll)
                else:
                    actlog_scroll = max(actlog_scroll - content_height // 2, 0)
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
                actlog_scroll = min(actlog_scroll + 1, max_scroll)
            elif key in (curses.KEY_UP, ord("k"), ord("K")):
                actlog_scroll = max(actlog_scroll - 1, 0)
            elif key == ord("G"):
                actlog_scroll = max_scroll
            elif key == ord("g"):
                actlog_scroll = 0
            elif key == 4:  # Ctrl+d
                actlog_scroll = min(actlog_scroll + content_height // 2, max_scroll)
            elif key == 21:  # Ctrl+u
                actlog_scroll = max(actlog_scroll - content_height // 2, 0)
            elif key in (6, curses.KEY_NPAGE):  # Ctrl+f / Page Down
                actlog_scroll = min(actlog_scroll + content_height, max_scroll)
            elif key in (2, curses.KEY_PPAGE):  # Ctrl+b / Page Up
                actlog_scroll = max(actlog_scroll - content_height, 0)
            elif key == ord("r"):
                # Reload log from disk
                if actlog_session_id:
                    entries, summary = load_activity_log(actlog_session_id)
                    if entries:
                        actlog_summary = summary
                        actlog_lines = [_format_log_entry(e, stdscr.getmaxyx()[1]) for e in entries]
                        actlog_scroll = min(actlog_scroll, max(0, len(actlog_lines) - content_height))
                        actlog_match_lines = _compute_peek_matches(actlog_lines, actlog_search_query)
                        actlog_match_index = 0
            elif key == ord("b"):
                actlog_browse_entries = list_activity_logs(sessions)
                actlog_browse_selected = 0
                actlog_browse_scroll = 0
                mode = "actlog_browse"

        # ── Activity log browser ─────────────────────────────────
        elif mode == "actlog_browse":
            if key in (ord("q"), ord("Q"), 27):
                mode = "list"
            elif key in (ord("j"), curses.KEY_DOWN) and actlog_browse_entries:
                actlog_browse_selected = (actlog_browse_selected + 1) % len(actlog_browse_entries)
                # Scroll to keep selection visible
                browse_content_h = stdscr.getmaxyx()[0] - 5
                if actlog_browse_selected >= actlog_browse_scroll + browse_content_h:
                    actlog_browse_scroll = actlog_browse_selected - browse_content_h + 1
                elif actlog_browse_selected < actlog_browse_scroll:
                    actlog_browse_scroll = actlog_browse_selected
            elif key in (ord("k"), curses.KEY_UP) and actlog_browse_entries:
                actlog_browse_selected = (actlog_browse_selected - 1) % len(actlog_browse_entries)
                browse_content_h = stdscr.getmaxyx()[0] - 5
                if actlog_browse_selected >= actlog_browse_scroll + browse_content_h:
                    actlog_browse_scroll = actlog_browse_selected - browse_content_h + 1
                elif actlog_browse_selected < actlog_browse_scroll:
                    actlog_browse_scroll = actlog_browse_selected
            elif key in (10, 13, curses.KEY_ENTER) and actlog_browse_entries:
                chosen = actlog_browse_entries[actlog_browse_selected]
                sid = chosen["session_id"]
                entries, summary = load_activity_log(sid)
                if entries:
                    actlog_session_title = chosen.get("title", "") or sid[:12]
                    actlog_session_id = sid
                    actlog_summary = summary
                    actlog_lines = [_format_log_entry(e, stdscr.getmaxyx()[1]) for e in entries]
                    actlog_scroll = max(0, len(actlog_lines) - (stdscr.getmaxyx()[0] - 5))
                    actlog_search_query = ""
                    actlog_search_active = False
                    actlog_search_cursor = 0
                    actlog_match_lines = []
                    actlog_match_index = 0
                    mode = "actlog"
                else:
                    msg = "Log file is empty"
                    msg_frames = 20

        # ── Config file picker ────────────────────────────────────
        elif mode == "config_pick":
            config_entries = _cfg._all_config_paths()
            if key in (27, ord("q"), ord("Q")):
                mode = "list"
            elif key in (curses.KEY_UP, ord("k"), ord("K")):
                if config_entries:
                    config_selected = (config_selected - 1) % len(config_entries)
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
                if config_entries:
                    config_selected = (config_selected + 1) % len(config_entries)
            elif key in (10, 13, curses.KEY_ENTER) and config_entries:
                config_file_label, config_file_path = config_entries[config_selected]
                config_action_selected = 0
                mode = "config_action"
            elif key in (ord("r"), ord("R")):
                _reload_all_config()
                msg = "Config reloaded from disk"
                msg_frames = 20

        # ── Config action picker ──────────────────────────────────
        elif mode == "config_action":
            if key == 27:
                mode = "config_pick"
            elif key in (curses.KEY_UP, ord("k"), ord("K")):
                config_action_selected = (config_action_selected - 1) % 3
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
                config_action_selected = (config_action_selected + 1) % 3
            elif key in (10, 13, curses.KEY_ENTER):
                if config_action_selected == 0:  # Show contents
                    try:
                        with open(config_file_path, errors="replace") as f:
                            config_show_lines = f.read().splitlines()
                    except OSError:
                        config_show_lines = ["(file not found)"]
                    config_show_scroll = 0
                    mode = "config_show"
                elif config_action_selected == 1:  # Edit in editor
                    do_open_editor(home_socket, config_file_path)
                    msg = f"Opened {config_file_label} in editor — press R to reload config"
                    msg_frames = 30
                    mode = "config_pick"
                elif config_action_selected == 2:  # Show file path
                    msg = _shorten_home(config_file_path)
                    msg_frames = 30
                    mode = "list"

        # ── Config file viewer ────────────────────────────────────
        elif mode == "config_show":
            if key in (27, ord("q"), ord("Q")):
                mode = "config_pick"
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
                content_height = stdscr.getmaxyx()[0] - 3
                max_scroll = max(0, len(config_show_lines) - content_height)
                config_show_scroll = min(config_show_scroll + 1, max_scroll)
            elif key in (curses.KEY_UP, ord("k"), ord("K")):
                config_show_scroll = max(config_show_scroll - 1, 0)
            elif key in (ord("r"), ord("R")):
                _reload_all_config()
                msg = "Config reloaded from disk"
                msg_frames = 20

        # ── Peek overlay ─────────────────────────────────────────
        elif mode == "peek":
            content_height = stdscr.getmaxyx()[0] - 3
            max_scroll = max(0, len(peek_lines) - content_height)

            # ── Tier 1: search input active ──
            if peek_search_active:
                if key == 27:  # Esc — cancel search
                    peek_search_active = False
                    peek_search_query = ""
                    peek_search_cursor = 0
                    peek_match_lines = []
                    peek_match_index = 0
                elif key in (10, 13, curses.KEY_ENTER):  # Enter — confirm
                    peek_search_active = False
                    if peek_match_lines:
                        peek_scroll = min(peek_match_lines[0], max_scroll)
                        peek_match_index = 0
                elif key in (127, 8, curses.KEY_BACKSPACE):
                    if peek_search_cursor > 0:
                        peek_search_query = (
                            peek_search_query[: peek_search_cursor - 1]
                            + peek_search_query[peek_search_cursor:]
                        )
                        peek_search_cursor -= 1
                        peek_match_lines = _compute_peek_matches(peek_lines, peek_search_query)
                        peek_match_index = 0
                elif key == curses.KEY_LEFT:
                    peek_search_cursor = max(0, peek_search_cursor - 1)
                elif key == curses.KEY_RIGHT:
                    peek_search_cursor = min(len(peek_search_query), peek_search_cursor + 1)
                elif 32 <= key <= 126:
                    ch = chr(key)
                    peek_search_query = (
                        peek_search_query[: peek_search_cursor]
                        + ch
                        + peek_search_query[peek_search_cursor:]
                    )
                    peek_search_cursor += 1
                    peek_match_lines = _compute_peek_matches(peek_lines, peek_search_query)
                    peek_match_index = 0
                continue

            # ── Tier 2: normal peek navigation ──
            if key in (27, ord("q"), ord("Q")):
                if peek_search_query:
                    peek_search_query = ""
                    peek_match_lines = []
                    peek_match_index = 0
                else:
                    mode = "list"
            elif key == ord("/"):
                peek_search_active = True
                peek_search_query = ""
                peek_search_cursor = 0
                peek_match_lines = []
                peek_match_index = 0
            elif key == ord("n"):
                if peek_match_lines:
                    peek_match_index = (peek_match_index + 1) % len(peek_match_lines)
                    peek_scroll = min(peek_match_lines[peek_match_index], max_scroll)
            elif key == ord("N"):
                if peek_match_lines:
                    peek_match_index = (peek_match_index - 1) % len(peek_match_lines)
                    peek_scroll = min(peek_match_lines[peek_match_index], max_scroll)
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
                peek_scroll = min(peek_scroll + 1, max_scroll)
            elif key in (curses.KEY_UP, ord("k"), ord("K")):
                peek_scroll = max(peek_scroll - 1, 0)
            elif key == ord("G"):
                peek_scroll = max_scroll
            elif key == ord("g"):
                peek_scroll = 0
            elif key == 4:  # Ctrl+d
                peek_scroll = min(peek_scroll + content_height // 2, max_scroll)
            elif key == 21:  # Ctrl+u
                peek_scroll = max(peek_scroll - content_height // 2, 0)
            elif key in (6, curses.KEY_NPAGE):  # Ctrl+f / Page Down
                peek_scroll = min(peek_scroll + content_height, max_scroll)
            elif key in (2, curses.KEY_PPAGE):  # Ctrl+b / Page Up
                peek_scroll = max(peek_scroll - content_height, 0)
            elif key == curses.KEY_HOME:
                peek_scroll = 0
            elif key == curses.KEY_END:
                peek_scroll = max_scroll
            elif key == ord("r"):
                sess = filtered[selected] if filtered else None
                if sess:
                    win_id = sess.get("claude_window_id", 0)
                    sess_socket = sess.get("socket", home_socket)
                    if win_id:
                        text = get_window_text(sess_socket, win_id, peek_extent)
                        if text is not None:
                            peek_lines = _rstrip_blank_lines(text.splitlines())
                            peek_error = ""
                            peek_scroll = min(peek_scroll, max(0, len(peek_lines) - content_height))
                        else:
                            peek_error = "Failed to capture window text"
                            peek_lines = []
                            peek_scroll = 0
                        peek_match_lines = _compute_peek_matches(peek_lines, peek_search_query)
                        peek_match_index = 0
            elif key == ord("s"):
                peek_extent = "all" if peek_extent == "screen" else "screen"
                peek_scroll = 0
                sess = filtered[selected] if filtered else None
                if sess:
                    win_id = sess.get("claude_window_id", 0)
                    sess_socket = sess.get("socket", home_socket)
                    if win_id:
                        text = get_window_text(sess_socket, win_id, peek_extent)
                        if text is not None:
                            peek_lines = _rstrip_blank_lines(text.splitlines())
                            peek_error = ""
                        else:
                            peek_error = "Failed to capture window text"
                            peek_lines = []
                    peek_match_lines = _compute_peek_matches(peek_lines, peek_search_query)
                    peek_match_index = 0

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
                target = filtered[selected]
                target_socket = target.get("socket", home_socket)
                if host_tab_id is not None and target["tab_id"] == host_tab_id:
                    _spawn_detached_close(target_socket, target["tab_id"])
                    return
                else:
                    ok = do_close(target_socket, target["tab_id"])
                    sessions = query_all_sessions(managed_only=(not show_all))
                    _refilter()
                    if ok:
                        msg = "Session closed"
                    else:
                        inst = target.get("instance_label", target_socket)
                        msg = f"Instance {inst} is no longer available"
                    msg_frames = 20
            mode = "list"

        # ── Confirm restart ──────────────────────────────────────
        elif mode == "confirm_restart":
            if key in (ord("y"), ord("Y")) and filtered:
                sess = filtered[selected]
                is_host = host_tab_id is not None and sess["tab_id"] == host_tab_id
                r_flags = sess.get("cc_flags", "")
                r_pw = sess.get("cc_pw", "")
                r_env = sess.get("cc_env", "")
                if not r_flags and not r_pw and not r_env:
                    cfg_entry = _cfg._find_config_session(sess["title"])
                    if cfg_entry is None:
                        cfg_entry = _cfg._find_config_session(
                            _strip_override_marker(sess["title"])
                        )
                    r_flags = cfg_entry.get("flags", "") if cfg_entry else ""
                    r_pw = cfg_entry.get("pw", "") if cfg_entry else ""
                    r_env = cfg_entry.get("env", "") if cfg_entry else ""
                sess_socket = sess.get("socket", home_socket)
                ok = do_restart(
                    sess_socket, sess, flags=r_flags, pw_override=r_pw,
                    host_tab_id=host_tab_id,
                    env_vars=r_env,
                )
                if is_host:
                    # Detached process will close+relaunch the host tab;
                    # the dashboard overlay will be destroyed with it.
                    return
                sessions = query_all_sessions(managed_only=(not show_all))
                _refilter()
                if ok:
                    msg = "Session restarted"
                else:
                    inst = sess.get("instance_label", sess_socket)
                    msg = f"Instance {inst} is no longer available"
                msg_frames = 20
            mode = "list"

        # ── Confirm batch close ─────────────────────────────────
        elif mode == "confirm_batch_close":
            if key in (ord("y"), ord("Y")) and batch_targets:
                # Sort so host tab is processed last
                has_host = host_tab_id is not None
                targets_sorted = sorted(
                    batch_targets,
                    key=lambda s: (1 if has_host and s["tab_id"] == host_tab_id else 0),
                )
                closed = 0
                for t in targets_sorted:
                    t_socket = t.get("socket", home_socket)
                    if has_host and t["tab_id"] == host_tab_id:
                        _spawn_detached_close(t_socket, t["tab_id"])
                        return
                    if do_close(t_socket, t["tab_id"]):
                        closed += 1
                marked.clear()
                sessions = query_all_sessions(managed_only=(not show_all))
                _refilter()
                msg = f"Closed {closed} session(s)"
                msg_frames = 20
            mode = "list"

        # ── Confirm batch restart ──────────────────────────────
        elif mode == "confirm_batch_restart":
            if key in (ord("y"), ord("Y")) and batch_targets:
                has_host = host_tab_id is not None
                targets_sorted = sorted(
                    batch_targets,
                    key=lambda s: (1 if has_host and s["tab_id"] == host_tab_id else 0),
                )
                restarted = 0
                for t in targets_sorted:
                    r_flags = t.get("cc_flags", "")
                    r_pw = t.get("cc_pw", "")
                    r_env = t.get("cc_env", "")
                    if not r_flags and not r_pw and not r_env:
                        cfg_entry = _cfg._find_config_session(t["title"])
                        if cfg_entry is None:
                            cfg_entry = _cfg._find_config_session(
                                _strip_override_marker(t["title"])
                            )
                        r_flags = cfg_entry.get("flags", "") if cfg_entry else ""
                        r_pw = cfg_entry.get("pw", "") if cfg_entry else ""
                        r_env = cfg_entry.get("env", "") if cfg_entry else ""
                    t_socket = t.get("socket", home_socket)
                    is_host = has_host and t["tab_id"] == host_tab_id
                    ok = do_restart(
                        t_socket, t, flags=r_flags, pw_override=r_pw,
                        host_tab_id=host_tab_id,
                        env_vars=r_env,
                    )
                    if is_host:
                        return
                    if ok:
                        restarted += 1
                marked.clear()
                sessions = query_all_sessions(managed_only=(not show_all))
                _refilter()
                msg = f"Restarted {restarted} session(s)"
                msg_frames = 20
            mode = "list"

        # ── Confirm save ────────────────────────────────────────
        elif mode == "confirm_save":
            if key in (ord("y"), ord("Y")):
                count, _ = _cfg.save_sessions(sessions)
                msg = f"Saved {count} session(s) to sessions.conf"
                msg_frames = 25
            mode = "list"

        # ── Override (account picker) ─────────────────────────────
        elif mode == "override":
            cancel_mode = "pick" if override_source == "pick" else "list"
            if key in (27, ord("q"), ord("Q")):  # Esc / q -- cancel
                mode = cancel_mode
            elif key in (curses.KEY_UP, ord("k"), ord("K")):
                dirs = _cfg._unique_config_dirs()
                if dirs:
                    override_selected = (override_selected - 1) % len(dirs)
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
                dirs = _cfg._unique_config_dirs()
                if dirs:
                    override_selected = (override_selected + 1) % len(dirs)
            elif key in (10, 13, curses.KEY_ENTER):
                dirs = _cfg._unique_config_dirs()
                selected_path = dirs[override_selected] if dirs else ""

                if override_source == "pick" and pick_filtered:
                    entry = pick_filtered[pick_selected]
                    account = entry["account"]
                    title = entry["title"]
                    current_dir = _cfg._accounts.get(account, {}).get("config_dir", "")
                    cdo = None
                    if selected_path != current_dir:
                        title = _append_override_marker(title)
                        cdo = selected_path
                    pw_val = entry["pw"]
                    target_socket = instance_socket_map.get(
                        pick_target_instance, home_socket
                    ) if multi_instance else home_socket
                    loc = compute_insert_location(target_socket, account, title)
                    do_launch(
                        target_socket,
                        title,
                        entry["dir"],
                        account,
                        entry["flags"],
                        pw_val,
                        location=loc,
                        config_dir_override=cdo,
                        env_vars=entry.get("env", ""),
                    )
                    return

                elif override_source == "list" and filtered:
                    sess = filtered[selected]
                    sess_socket = sess.get("socket", home_socket)
                    current_dir = _cfg._accounts.get(sess["account"], {}).get(
                        "config_dir", ""
                    )
                    if selected_path == current_dir:
                        msg = "Same config dir selected, no change"
                        msg_frames = 20
                    else:
                        if sess["status"] == "stopped":
                            ok = do_close(sess_socket, sess["tab_id"])
                            if not ok:
                                inst = sess.get("instance_label", sess_socket)
                                msg = f"Instance {inst} is no longer available"
                                msg_frames = 20
                                mode = "list"
                                continue
                            time.sleep(0.3)
                            new_title = _append_override_marker(sess["title"])
                            account = sess["account"]
                            location = compute_insert_location(
                                sess_socket, account, new_title
                            )
                            do_launch(
                                sess_socket,
                                new_title,
                                sess["cwd"],
                                account,
                                sess.get("cc_flags", ""),
                                sess.get("cc_pw", ""),
                                location=location,
                                config_dir_override=selected_path,
                                env_vars=sess.get("cc_env", ""),
                            )
                            return
                        else:
                            is_host = (
                                host_tab_id is not None
                                and sess["tab_id"] == host_tab_id
                            )
                            do_restart(
                                sess_socket,
                                sess,
                                flags=sess.get("cc_flags", ""),
                                pw_override=sess.get("cc_pw", ""),
                                config_dir_override=selected_path,
                                host_tab_id=host_tab_id,
                                env_vars=sess.get("cc_env", ""),
                            )
                            if is_host:
                                return
                        sessions = query_all_sessions(managed_only=(not show_all))
                        _refilter()
                        home = os.path.expanduser("~")
                        display_path = selected_path
                        if display_path.startswith(home):
                            display_path = "~" + display_path[len(home) :]
                        msg = f'Config dir overridden to "{display_path}"'
                        msg_frames = 20
                    mode = "list"

                else:
                    mode = cancel_mode

        # ── Move picker ──────────────────────────────────────────
        elif mode == "move_picker":
            sess = filtered[selected] if filtered else None
            current_inst = sess.get("instance_label", "") if sess else ""
            targets = [lbl for lbl in instance_labels if lbl != current_inst]

            if key in (27, ord("q"), ord("Q")):  # Esc / q -- cancel
                mode = "list"
            elif key in (curses.KEY_UP, ord("k"), ord("K")):
                if targets:
                    move_selected = (move_selected - 1) % len(targets)
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
                if targets:
                    move_selected = (move_selected + 1) % len(targets)
            elif key in (10, 13, curses.KEY_ENTER) and sess and targets:
                target_label = targets[move_selected]
                target_socket = instance_socket_map.get(target_label, "")
                if not target_socket:
                    msg = f"Instance {target_label} is no longer available"
                    msg_frames = 20
                    mode = "list"
                elif marked:
                    # Batch move all marked sessions
                    to_move = [
                        s for s in filtered
                        if s["tab_id"] in marked and s.get("instance_label", "") != target_label
                    ]
                    if not to_move:
                        msg = "All marked sessions already on target instance"
                        msg_frames = 20
                    else:
                        has_host = host_tab_id is not None
                        targets_sorted = sorted(
                            to_move,
                            key=lambda s: (1 if has_host and s["tab_id"] == host_tab_id else 0),
                        )
                        moved = 0
                        for t in targets_sorted:
                            r_flags = t.get("cc_flags", "")
                            r_pw = t.get("cc_pw", "")
                            r_env = t.get("cc_env", "")
                            if not r_flags and not r_pw and not r_env:
                                cfg_entry = _cfg._find_config_session(t["title"])
                                if cfg_entry is None:
                                    cfg_entry = _cfg._find_config_session(
                                        _strip_override_marker(t["title"])
                                    )
                                r_flags = cfg_entry.get("flags", "") if cfg_entry else ""
                                r_pw = cfg_entry.get("pw", "") if cfg_entry else ""
                                r_env = cfg_entry.get("env", "") if cfg_entry else ""
                            t_socket = t.get("socket", home_socket)
                            is_host = has_host and t["tab_id"] == host_tab_id
                            ok = do_move(
                                t_socket, target_socket, t,
                                flags=r_flags, pw_override=r_pw,
                                host_tab_id=host_tab_id,
                                env_vars=r_env,
                            )
                            if is_host:
                                return
                            if ok:
                                moved += 1
                        marked.clear()
                        sessions = query_all_sessions(managed_only=(not show_all))
                        _refilter()
                        msg = f"Moved {moved} session(s) to {target_label}"
                        msg_frames = 20
                    mode = "list"
                else:
                    is_host = host_tab_id is not None and sess["tab_id"] == host_tab_id
                    sess_socket = sess.get("socket", home_socket)
                    r_flags = sess.get("cc_flags", "")
                    r_pw = sess.get("cc_pw", "")
                    r_env = sess.get("cc_env", "")
                    if not r_flags and not r_pw and not r_env:
                        cfg_entry = _cfg._find_config_session(sess["title"])
                        if cfg_entry is None:
                            cfg_entry = _cfg._find_config_session(
                                _strip_override_marker(sess["title"])
                            )
                        r_flags = cfg_entry.get("flags", "") if cfg_entry else ""
                        r_pw = cfg_entry.get("pw", "") if cfg_entry else ""
                        r_env = cfg_entry.get("env", "") if cfg_entry else ""
                    ok = do_move(
                        sess_socket,
                        target_socket,
                        sess,
                        flags=r_flags,
                        pw_override=r_pw,
                        host_tab_id=host_tab_id,
                        env_vars=r_env,
                    )
                    if is_host:
                        return
                    sessions = query_all_sessions(managed_only=(not show_all))
                    _refilter()
                    if ok:
                        msg = f"Session moved to {target_label}"
                    else:
                        inst = sess.get("instance_label", sess_socket)
                        msg = f"Instance {inst} is no longer available"
                    msg_frames = 20
                    mode = "list"

        # ── Rename mode ──────────────────────────────────────────
        elif mode == "rename":
            if key == 27:  # Esc -- cancel
                mode = "list"
            elif key in (10, 13, curses.KEY_ENTER):
                new_name = rename_value.strip()
                if not new_name:
                    msg = "Title cannot be empty"
                    msg_frames = 20
                    mode = "list"
                else:
                    # Re-prepend the account prefix if the original title had one
                    orig_title = filtered[selected]["title"]
                    had_override = orig_title.endswith(OVERRIDE_MARKER)
                    if (
                        len(orig_title) >= 2
                        and orig_title[0].isupper()
                        and orig_title[1] == ":"
                    ):
                        full_title = orig_title[:2] + new_name
                    else:
                        full_title = new_name
                    # Re-append override marker if original had one
                    if had_override:
                        full_title = _append_override_marker(full_title)
                    old_tab_id = filtered[selected]["tab_id"]
                    rename_socket = filtered[selected].get("socket", home_socket)
                    ok = do_rename(rename_socket, old_tab_id, full_title)
                    sessions = query_all_sessions(managed_only=(not show_all))
                    _refilter()
                    if ok:
                        msg = f'Renamed to "{full_title}"'
                    else:
                        inst = filtered[selected].get("instance_label", rename_socket) if filtered else rename_socket
                        msg = f"Instance {inst} is no longer available"
                    msg_frames = 20
                    mode = "list"
            elif key in (curses.KEY_BACKSPACE, 127, 8):
                if rename_cursor > 0:
                    rename_value = (
                        rename_value[: rename_cursor - 1] + rename_value[rename_cursor:]
                    )
                    rename_cursor -= 1
            elif key == curses.KEY_LEFT:
                rename_cursor = max(0, rename_cursor - 1)
            elif key == curses.KEY_RIGHT:
                rename_cursor = min(len(rename_value), rename_cursor + 1)
            elif key == curses.KEY_HOME or key == 1:  # Ctrl+A
                rename_cursor = 0
            elif key == curses.KEY_END or key == 5:  # Ctrl+E
                rename_cursor = len(rename_value)
            elif 32 <= key < 127:
                before = rename_value[:rename_cursor]
                after = rename_value[rename_cursor:]
                rename_value = before + chr(key) + after
                rename_cursor += 1

        # ── Worktree mode ────────────────────────────────────────
        elif mode == "worktree":
            if key == 27:  # Esc -- cancel
                mode = "list"
            elif key in (10, 13, curses.KEY_ENTER):
                branch = worktree_branch.strip()
                if not branch:
                    msg = "Branch name cannot be empty"
                    msg_frames = 20
                    mode = "list"
                else:
                    sess = filtered[selected]
                    result = do_create_worktree(sess["cwd"], branch)
                    if isinstance(result, str):
                        msg = result
                        msg_frames = 40
                        mode = "list"
                    else:
                        wt_path = result["worktree_path"]
                        proj = result["project_name"]
                        orig_title = sess["title"]
                        if len(orig_title) >= 2 and orig_title[0].isupper() and orig_title[1] == ":":
                            new_title = orig_title[:2] + f"{proj}-{branch}"
                        else:
                            new_title = f"{proj}-{branch}"
                        # Prefer live user vars for flags/pw/env; fall back to config
                        flags = sess.get("cc_flags", "")
                        pw_val = sess.get("cc_pw", "")
                        env_val = sess.get("cc_env", "")
                        if not flags and not pw_val and not env_val:
                            stripped = _strip_override_marker(orig_title)
                            if len(stripped) >= 2 and stripped[0].isupper() and stripped[1] == ":":
                                stripped = stripped[2:]
                            src_cfg = next((s for s in _cfg._sessions if s["title"] == stripped), None)
                            flags = src_cfg["flags"] if src_cfg else ""
                            pw_val = src_cfg["pw"] if src_cfg else ""
                            env_val = src_cfg["env"] if src_cfg else ""
                        # Launch
                        target_socket = sess.get("socket", home_socket)
                        loc = compute_insert_location(target_socket, sess["account"], new_title)
                        do_launch(target_socket, new_title, wt_path, sess["account"], flags, pw_val, location=loc, env_vars=env_val)
                        # Persist (append_session handles ~ collapse)
                        _cfg.append_session(new_title, wt_path, sess["account"], flags, pw_val, env=env_val)
                        # Refresh
                        sessions = query_all_sessions(managed_only=(not show_all))
                        _refilter()
                        msg = f"Worktree created: {os.path.basename(wt_path)}"
                        msg_frames = 40
                        mode = "list"
            elif key in (curses.KEY_BACKSPACE, 127, 8):
                if worktree_cursor > 0:
                    worktree_branch = (
                        worktree_branch[: worktree_cursor - 1] + worktree_branch[worktree_cursor:]
                    )
                    worktree_cursor -= 1
            elif key == curses.KEY_LEFT:
                worktree_cursor = max(0, worktree_cursor - 1)
            elif key == curses.KEY_RIGHT:
                worktree_cursor = min(len(worktree_branch), worktree_cursor + 1)
            elif key == curses.KEY_HOME or key == 1:  # Ctrl+A
                worktree_cursor = 0
            elif key == curses.KEY_END or key == 5:  # Ctrl+E
                worktree_cursor = len(worktree_branch)
            elif 32 <= key < 127:
                before = worktree_branch[:worktree_cursor]
                after = worktree_branch[worktree_cursor:]
                worktree_branch = before + chr(key) + after
                worktree_cursor += 1

        # ── Clone mode ─────────────────────────────────────────
        elif mode == "clone":
            if key == 27:  # Esc -- cancel
                mode = "list"
            elif key in (10, 13, curses.KEY_ENTER):
                title = clone_title.strip()
                if not title:
                    msg = "Title cannot be empty"
                    msg_frames = 20
                    mode = "list"
                else:
                    sess = filtered[selected]
                    # Prefer live user vars for flags/pw/env; fall back to config
                    flags = sess.get("cc_flags", "")
                    pw_val = sess.get("cc_pw", "")
                    env_val = sess.get("cc_env", "")
                    if not flags and not pw_val and not env_val:
                        orig_title = sess["title"]
                        stripped = _strip_override_marker(orig_title)
                        if len(stripped) >= 2 and stripped[0].isupper() and stripped[1] == ":":
                            stripped = stripped[2:]
                        src_cfg = next((s for s in _cfg._sessions if s["title"] == stripped), None)
                        flags = src_cfg["flags"] if src_cfg else ""
                        pw_val = src_cfg["pw"] if src_cfg else ""
                        env_val = src_cfg["env"] if src_cfg else ""
                    # Launch
                    target_socket = sess.get("socket", home_socket)
                    loc = compute_insert_location(target_socket, sess["account"], title)
                    do_launch(target_socket, title, sess["cwd"], sess["account"], flags, pw_val, location=loc, env_vars=env_val)
                    # Persist
                    _cfg.append_session(title, sess["cwd"], sess["account"], flags, pw_val, env=env_val)
                    # Refresh
                    sessions = query_all_sessions(managed_only=(not show_all))
                    _refilter()
                    msg = f"Cloned as \"{title}\""
                    msg_frames = 40
                    mode = "list"
            elif key in (curses.KEY_BACKSPACE, 127, 8):
                if clone_cursor > 0:
                    clone_title = (
                        clone_title[: clone_cursor - 1] + clone_title[clone_cursor:]
                    )
                    clone_cursor -= 1
            elif key == curses.KEY_LEFT:
                clone_cursor = max(0, clone_cursor - 1)
            elif key == curses.KEY_RIGHT:
                clone_cursor = min(len(clone_title), clone_cursor + 1)
            elif key == curses.KEY_HOME or key == 1:  # Ctrl+A
                clone_cursor = 0
            elif key == curses.KEY_END or key == 5:  # Ctrl+E
                clone_cursor = len(clone_title)
            elif 32 <= key < 127:
                before = clone_title[:clone_cursor]
                after = clone_title[clone_cursor:]
                clone_title = before + chr(key) + after
                clone_cursor += 1

        # ── New / Edit session form ──────────────────────────────
        elif mode == "new":
            if form_mode == "edit":
                field_keys = ["title", "dir", "account", "flags", "pw", "env"]
            else:
                field_keys = ["title", "dir", "account", "flags", "pw", "env", "save"]
                if multi_instance:
                    field_keys.insert(-1, "instance")
            last_field = len(field_keys) - 1
            fk = field_keys[new_active]

            if dir_browse_active and fk == "dir":
                if key == 27:  # Escape - close dropdown
                    dir_browse_active = False
                elif key == curses.KEY_UP:
                    dir_browse_selected = max(0, dir_browse_selected - 1)
                    if dir_browse_selected < dir_browse_scroll:
                        dir_browse_scroll = dir_browse_selected
                elif key == curses.KEY_DOWN:
                    if dir_browse_entries:
                        dir_browse_selected = min(
                            len(dir_browse_entries) - 1, dir_browse_selected + 1
                        )
                        max_vis = min(10, stdscr.getmaxyx()[0] - 10)
                        if dir_browse_selected >= dir_browse_scroll + max_vis:
                            dir_browse_scroll = dir_browse_selected - max_vis + 1
                elif key in (10, 13, curses.KEY_ENTER, 9):  # Enter or Tab - accept & drill
                    if dir_browse_entries:
                        selected_name = dir_browse_entries[dir_browse_selected]
                        val = new_fields["dir"]
                        if val.endswith("/"):
                            base = val
                        else:
                            idx = val.rfind("/")
                            base = val[:idx + 1] if idx >= 0 else ""
                        new_fields["dir"] = base + selected_name + "/"
                        new_cursor = len(new_fields["dir"])
                        dir_browse_entries = _scan_directories(
                            new_fields["dir"], show_hidden=dir_browse_show_hidden
                        )
                        dir_browse_selected = 0
                        dir_browse_scroll = 0
                        if not dir_browse_entries:
                            dir_browse_active = False
                    else:
                        dir_browse_active = False
                elif key == ord("."):  # Toggle hidden directories
                    dir_browse_show_hidden = not dir_browse_show_hidden
                    dir_browse_entries = _scan_directories(
                        new_fields["dir"], show_hidden=dir_browse_show_hidden
                    )
                    dir_browse_selected = 0
                    dir_browse_scroll = 0
                elif key in (curses.KEY_BACKSPACE, 127, 8):
                    if new_cursor > 0:
                        val = new_fields["dir"]
                        new_fields["dir"] = val[:new_cursor - 1] + val[new_cursor:]
                        new_cursor -= 1
                        dir_browse_entries = _scan_directories(
                            new_fields["dir"], show_hidden=dir_browse_show_hidden
                        )
                        dir_browse_selected = 0
                        dir_browse_scroll = 0
                        if not new_fields["dir"]:
                            dir_browse_active = False
                elif 32 <= key < 127:
                    val = new_fields["dir"]
                    new_fields["dir"] = val[:new_cursor] + chr(key) + val[new_cursor:]
                    new_cursor += 1
                    dir_browse_entries = _scan_directories(
                        new_fields["dir"], show_hidden=dir_browse_show_hidden
                    )
                    dir_browse_selected = 0
                    dir_browse_scroll = 0
            elif key == 27:  # Esc -- cancel
                dir_browse_active = False
                form_mode = "new"
                mode = "list"
            elif key == curses.KEY_UP:
                new_active = max(new_active - 1, 0)
                new_cursor = len(new_fields[field_keys[new_active]])
            elif key == curses.KEY_DOWN:
                new_active = min(new_active + 1, last_field)
                new_cursor = len(new_fields[field_keys[new_active]])
            elif key == 9:  # Tab
                if fk == "account":
                    new_fields["account"] = _cycle_account(new_fields["account"])
                elif fk == "pw":
                    new_fields["pw"] = _cycle_pw(new_fields["pw"])
                elif fk == "save":
                    new_fields["save"] = "no" if new_fields["save"] == "yes" else "yes"
                elif fk == "instance":
                    new_fields["instance"] = _cycle_instance(
                        new_fields["instance"], instance_labels
                    )
                elif fk == "dir":
                    dir_browse_entries = _scan_directories(
                        new_fields["dir"], show_hidden=dir_browse_show_hidden
                    )
                    dir_browse_active = True
                    dir_browse_selected = 0
                    dir_browse_scroll = 0
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
                    if form_mode == "edit":
                        if not t or not d:
                            msg = "Title and directory are required"
                            msg_frames = 20
                        elif t != edit_original_title and _cfg._find_config_session(t):
                            msg = "A session with that title already exists"
                            msg_frames = 20
                        else:
                            edit_env = new_fields.get("env", "")
                            _cfg.update_session(edit_original_title, t, d, a, fl, pw_val, env=edit_env)
                            # Check if the edited session is currently running
                            running_titles = {s["title"] for s in sessions}
                            stripped_running = {_strip_override_marker(rt) for rt in running_titles}
                            if edit_original_title in stripped_running:
                                msg = "Config updated (restart session to apply)"
                            else:
                                msg = "Config updated"
                            msg_frames = 20
                            mode = "list"
                    else:
                        # Use the selected instance's socket, or home_socket
                        target_socket = home_socket
                        if multi_instance and "instance" in new_fields:
                            target_socket = instance_socket_map.get(
                                new_fields["instance"], home_socket
                            )
                        if t and d:
                            env_val = new_fields.get("env", "").strip()
                            loc = compute_insert_location(target_socket, a, t)
                            do_launch(target_socket, t, d, a, fl, pw_val, location=loc, env_vars=env_val)
                            if new_fields.get("save") == "yes":
                                _cfg.append_session(t, d, a, fl, pw_val, env=env_val)
                            return
                        else:
                            msg = "Title and directory are required"
                            msg_frames = 20
                        mode = "list"
            elif fk not in ("account", "pw", "instance", "save"):
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
            elif key == ord("i") and multi_instance:
                pick_target_instance = _cycle_instance(
                    pick_target_instance, instance_labels
                )
            elif key == ord("O") and pick_filtered:
                mode = "override"
                override_source = "pick"
                current_dir = pick_filtered[pick_selected].get("config_dir", "")
                if not current_dir:
                    current_dir = _cfg._accounts.get(
                        pick_filtered[pick_selected]["account"], {}
                    ).get("config_dir", "")
                dirs = _cfg._unique_config_dirs()
                override_selected = 0
                for i, d in enumerate(dirs):
                    if d == current_dir:
                        override_selected = i
                        break
            elif key in (10, 13, curses.KEY_ENTER) and pick_filtered:
                entry = pick_filtered[pick_selected]
                pw_val = entry["pw"]
                target_socket = instance_socket_map.get(
                    pick_target_instance, home_socket
                ) if multi_instance else home_socket
                loc = compute_insert_location(target_socket, entry["account"], entry["title"])
                do_launch(
                    target_socket,
                    entry["title"],
                    entry["dir"],
                    entry["account"],
                    entry["flags"],
                    pw_val,
                    location=loc,
                    env_vars=entry.get("env", ""),
                )
                return

        # ── Delete mode ──────────────────────────────────────────
        elif mode == "delete":
            if delete_step == "confirm_config":
                if key == ord("y"):
                    title = delete_target_entry["title"]
                    _cfg.remove_session(title)
                    delete_step = "confirm_data"
                    # Check if directory exists and is valid for deletion
                    expanded = os.path.expanduser(delete_target_entry["dir"])
                    if os.path.isdir(expanded) and delete_target_entry.get("type_tag") == "[repo]":
                        delete_warnings = check_git_safety(expanded)
                    else:
                        delete_warnings = []
                    if not os.path.isdir(expanded) or delete_target_entry.get("type_tag") == "[n/a]":
                        msg = f'Removed "{title}" from config (directory not found)'
                        msg_frames = 30
                        # Rebuild entries from refreshed config
                        delete_entries = []
                        for entry in _cfg._sessions:
                            e = dict(entry)
                            exp = os.path.expanduser(e["dir"])
                            if os.path.isdir(exp):
                                wt = detect_worktree(exp)
                                if wt["is_worktree"]:
                                    e["type_tag"] = "[worktree]"
                                    e["wt_main_repo"] = wt["main_repo"]
                                elif os.path.isdir(os.path.join(exp, ".git")):
                                    e["type_tag"] = "[repo]"
                                else:
                                    e["type_tag"] = "[dir]"
                            else:
                                e["type_tag"] = "[n/a]"
                            delete_entries.append(e)
                        delete_filtered, delete_running_count = _sort_pick_entries(
                            delete_entries, delete_running
                        )
                        delete_selected = min(delete_selected, max(0, len(delete_filtered) - 1))
                        delete_step = "select"
                else:
                    delete_step = "select"
            elif delete_step == "confirm_data":
                if key == ord("y"):
                    title = delete_target_entry["title"]
                    expanded = os.path.expanduser(delete_target_entry["dir"])
                    type_tag = delete_target_entry.get("type_tag", "[dir]")
                    err = None
                    if type_tag == "[worktree]":
                        main_repo = delete_target_entry.get("wt_main_repo", "")
                        err = do_remove_worktree(main_repo, expanded)
                    else:
                        try:
                            shutil.rmtree(expanded)
                        except OSError as exc:
                            err = str(exc)
                    if err:
                        msg = f"Error: {err}"
                    else:
                        msg = f'Deleted "{title}" data at {expanded}'
                    msg_frames = 30
                else:
                    msg = "Config entry removed, directory kept"
                    msg_frames = 30
                # Rebuild entries from refreshed config
                delete_entries = []
                for entry in _cfg._sessions:
                    e = dict(entry)
                    exp = os.path.expanduser(e["dir"])
                    if os.path.isdir(exp):
                        wt = detect_worktree(exp)
                        if wt["is_worktree"]:
                            e["type_tag"] = "[worktree]"
                            e["wt_main_repo"] = wt["main_repo"]
                        elif os.path.isdir(os.path.join(exp, ".git")):
                            e["type_tag"] = "[repo]"
                        else:
                            e["type_tag"] = "[dir]"
                    else:
                        e["type_tag"] = "[n/a]"
                    delete_entries.append(e)
                delete_filtered, delete_running_count = _sort_pick_entries(
                    delete_entries, delete_running
                )
                delete_selected = min(delete_selected, max(0, len(delete_filtered) - 1))
                delete_step = "select"
            elif delete_search_active:
                if key == 27:
                    delete_search_active = False
                    delete_query = ""
                    delete_cursor = 0
                    delete_filtered, delete_running_count = _sort_pick_entries(
                        delete_entries, delete_running
                    )
                    delete_selected = min(delete_selected, max(0, len(delete_filtered) - 1))
                    continue
                elif key in (10, 13, curses.KEY_ENTER):
                    delete_search_active = False
                    continue
                elif key in (curses.KEY_UP, ord("k"), ord("K")) and delete_filtered:
                    delete_selected = (delete_selected - 1) % len(delete_filtered)
                    continue
                elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and delete_filtered:
                    delete_selected = (delete_selected + 1) % len(delete_filtered)
                    continue
                elif key in (curses.KEY_BACKSPACE, 127, 8):
                    if delete_cursor > 0:
                        delete_query = (
                            delete_query[: delete_cursor - 1] + delete_query[delete_cursor:]
                        )
                        delete_cursor -= 1
                        delete_filtered = _filter_config_sessions(
                            delete_entries, delete_query
                        )
                        delete_filtered, delete_running_count = _sort_pick_entries(
                            delete_filtered, delete_running
                        )
                        delete_selected = min(
                            delete_selected, max(0, len(delete_filtered) - 1)
                        )
                    continue
                elif key == curses.KEY_LEFT:
                    delete_cursor = max(0, delete_cursor - 1)
                    continue
                elif key == curses.KEY_RIGHT:
                    delete_cursor = min(len(delete_query), delete_cursor + 1)
                    continue
                elif 32 <= key < 127:
                    delete_query = (
                        delete_query[:delete_cursor] + chr(key) + delete_query[delete_cursor:]
                    )
                    delete_cursor += 1
                    delete_filtered = _filter_config_sessions(delete_entries, delete_query)
                    delete_filtered, delete_running_count = _sort_pick_entries(
                        delete_filtered, delete_running
                    )
                    delete_selected = min(delete_selected, max(0, len(delete_filtered) - 1))
                    continue
                else:
                    continue
            else:
                # delete_step == "select", not searching
                if key in (ord("q"), ord("Q"), 27):
                    if delete_query and key == 27:
                        delete_query = ""
                        delete_cursor = 0
                        delete_filtered, delete_running_count = _sort_pick_entries(
                            delete_entries, delete_running
                        )
                        delete_selected = min(delete_selected, max(0, len(delete_filtered) - 1))
                    else:
                        mode = "list"
                elif key == ord("/"):
                    delete_search_active = True
                    delete_query = ""
                    delete_cursor = 0
                    delete_filtered, delete_running_count = _sort_pick_entries(
                        delete_entries, delete_running
                    )
                elif key in (curses.KEY_UP, ord("k"), ord("K")) and delete_filtered:
                    delete_selected = (delete_selected - 1) % len(delete_filtered)
                elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and delete_filtered:
                    delete_selected = (delete_selected + 1) % len(delete_filtered)
                elif key in (10, 13, curses.KEY_ENTER) and delete_filtered:
                    delete_target_entry = dict(delete_filtered[delete_selected])
                    delete_target_entry["is_running"] = delete_target_entry["title"] in delete_running
                    delete_step = "confirm_config"
