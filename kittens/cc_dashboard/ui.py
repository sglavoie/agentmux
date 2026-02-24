"""Curses UI rendering and event loop for the Claude Code Dashboard."""

import curses
import os
import unicodedata

from cc_dashboard import config as _cfg
from cc_dashboard.config import PALETTE
from cc_dashboard.kitty_remote import (
    do_close,
    do_focus,
    do_launch,
    do_restart,
    query_sessions,
)


# Fixed color pair IDs for UI chrome (not account-specific)
CP_NORMAL = 1
CP_HEADER = 2
CP_DIM = 3
CP_WARN = 4
CP_INPUT = 5
CP_INPUT_ACTIVE = 6

# Dynamic account color pairs start at this offset.
# Each account gets two pair IDs: one normal, one selected.
_CP_ACCOUNT_BASE = 10

# RGB values for shared UI colors
SEL_BG = (40, 40, 60)
DIM_FG = (110, 110, 110)
WARN_FG = (220, 180, 50)

# Maps account name -> (cp_normal, cp_selected) curses color pair IDs
_account_cp = {}


# ── Curses helpers ───────────────────────────────────────────────────────────


def _c(r, g, b):
    """Convert 0–255 RGB to curses 0–1000 range."""
    return int(r * 1000 / 255), int(g * 1000 / 255), int(b * 1000 / 255)


def init_colors():
    """Set up curses color pairs dynamically based on loaded accounts."""
    global _account_cp
    curses.start_color()
    curses.use_default_colors()

    _account_cp = {}

    if curses.can_change_color() and curses.COLORS >= 256:
        # Reserve custom color slots starting at 16
        base = 16
        # Shared UI colors
        curses.init_color(base, *_c(*SEL_BG))
        curses.init_color(base + 1, *_c(*DIM_FG))
        curses.init_color(base + 2, *_c(*WARN_FG))
        sel_bg_slot = base
        dim_slot = base + 1
        warn_slot = base + 2

        curses.init_pair(CP_NORMAL, -1, -1)
        curses.init_pair(CP_HEADER, curses.COLOR_WHITE, -1)
        curses.init_pair(CP_DIM, dim_slot, -1)
        curses.init_pair(CP_WARN, warn_slot, -1)
        curses.init_pair(CP_INPUT, -1, -1)
        curses.init_pair(CP_INPUT_ACTIVE, curses.COLOR_WHITE, sel_bg_slot)

        # Dynamically allocate a color slot + two pair IDs per account
        color_slot = base + 3
        pair_id = _CP_ACCOUNT_BASE
        for name in _cfg._account_names:
            color_name = _cfg._accounts[name]["color"]
            _, _, text_fg = PALETTE.get(color_name, PALETTE["gray"])
            curses.init_color(color_slot, *_c(*text_fg))
            curses.init_pair(pair_id, color_slot, -1)           # normal
            curses.init_pair(pair_id + 1, color_slot, sel_bg_slot)  # selected
            _account_cp[name] = (pair_id, pair_id + 1)
            color_slot += 1
            pair_id += 2
    else:
        # Fallback: cycle through a few basic curses colors
        _FALLBACK_COLORS = [
            curses.COLOR_GREEN, curses.COLOR_CYAN, curses.COLOR_RED,
            curses.COLOR_YELLOW, curses.COLOR_MAGENTA, curses.COLOR_WHITE,
        ]

        curses.init_pair(CP_NORMAL, -1, -1)
        curses.init_pair(CP_HEADER, curses.COLOR_WHITE, -1)
        curses.init_pair(CP_DIM, curses.COLOR_WHITE, -1)
        curses.init_pair(CP_WARN, curses.COLOR_YELLOW, -1)
        curses.init_pair(CP_INPUT, -1, -1)
        curses.init_pair(CP_INPUT_ACTIVE, curses.COLOR_WHITE, curses.COLOR_BLACK)

        pair_id = _CP_ACCOUNT_BASE
        for i, name in enumerate(_cfg._account_names):
            fg = _FALLBACK_COLORS[i % len(_FALLBACK_COLORS)]
            curses.init_pair(pair_id, fg, -1)
            curses.init_pair(pair_id + 1, fg, curses.COLOR_BLACK)
            _account_cp[name] = (pair_id, pair_id + 1)
            pair_id += 2


def _cp_for_account(account, selected=False):
    """Return the curses color pair ID for a given account name."""
    cp_normal, cp_sel = _account_cp.get(account, (CP_NORMAL, CP_NORMAL))
    return cp_sel if selected else cp_normal


def safe_addstr(win, y, x, text, attr=0):
    """Write text to the curses window, silently clipping at window boundaries."""
    rows, cols = win.getmaxyx()
    if y < 0 or y >= rows or x >= cols:
        return
    text = text[: cols - x]
    try:
        win.addstr(y, x, text, attr)
    except curses.error:
        pass


def _display_width(text):
    """Return the visual display width of text, accounting for wide characters."""
    w = 0
    for ch in text:
        eaw = unicodedata.east_asian_width(ch)
        w += 2 if eaw in ("W", "F") else 1
    return w


def _pad_to_width(text, width):
    """Pad text with spaces to reach the target visual width."""
    current = _display_width(text)
    return text + " " * max(0, width - current)


def _filter_sessions(sessions, query):
    """Return sessions matching query (case-insensitive) against title, cwd, or account."""
    if not query:
        return sessions
    q = query.lower()
    return [
        s for s in sessions
        if q in s["title"].lower()
        or q in s["cwd"].lower()
        or q in s["account"].lower()
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
        s for s in sessions
        if q in s["title"].lower()
        or q in s["dir"].lower()
        or q in s["account"].lower()
    ]


def _sort_pick_entries(entries, running_titles):
    """Sort entries with running sessions first, stopped second, preserving order within each group.

    Returns (sorted_list, running_count).
    """
    running = [e for e in entries if e["title"] in running_titles]
    stopped = [e for e in entries if e["title"] not in running_titles]
    return running + stopped, len(running)


def shorten_path(path, max_len):
    """Collapse home prefix to ~ and truncate long paths with a leading ellipsis."""
    home = os.path.expanduser("~")
    if path.startswith(home):
        path = "~" + path[len(home) :]
    if max_len < 4:
        return path[:max_len]
    if len(path) <= max_len:
        return path
    return "..." + path[-(max_len - 3) :]


# ── Detail pane ──────────────────────────────────────────────────────────────

DETAIL_PANE_HEIGHT = 9


def draw_detail_pane(stdscr, session, detail_y):
    """Render the detail pane for the selected session starting at detail_y."""
    rows, cols = stdscr.getmaxyx()
    if detail_y + DETAIL_PANE_HEIGHT > rows:
        return

    # Separator line
    sep = "─── Detail " + "─" * max(0, cols - 14)
    safe_addstr(stdscr, detail_y, 1, sep, curses.color_pair(CP_DIM))

    # Look up config session for flags/split info
    cfg_entry = _cfg._find_config_session(session["title"])
    effective_flags = ""
    effective_split = ""
    if cfg_entry:
        effective_flags = _cfg._resolve_claude_flags(cfg_entry.get("flags", ""))
        effective_split = _cfg._resolve_project_window(cfg_entry.get("pw", ""))
    else:
        effective_flags = _cfg._resolve_claude_flags("")
        effective_split = _cfg._resolve_project_window("")

    # Resolve display values
    home = os.path.expanduser("~")
    full_dir = session["cwd"]
    if full_dir.startswith(home):
        full_dir = "~" + full_dir[len(home):]

    config_dir = session.get("config_dir", "")
    if not config_dir:
        acct_info = _cfg._accounts.get(session["account"], {})
        config_dir = acct_info.get("config_dir", "")
    if config_dir and config_dir.startswith(home):
        config_dir = "~" + config_dir[len(home):]
    if not config_dir:
        config_dir = "~/.claude"

    color_name = _cfg._accounts.get(session["account"], {}).get("color", "gray")
    pids_str = ", ".join(str(p) for p in session.get("pids", []))
    window_count = str(session.get("window_count", 1))

    # Find the first cmdline that contains "claude" for display
    cmdline_display = ""
    for cl in session.get("cmdlines", []):
        if "claude" in cl.lower():
            cmdline_display = cl
            break

    label_attr = curses.color_pair(CP_HEADER)
    val_attr = curses.color_pair(CP_NORMAL)
    acct_cp = _cp_for_account(session["account"])
    acct_attr = curses.color_pair(acct_cp)

    # Two-column layout: left column ends around mid-screen, right column starts there
    mid = max(cols // 2, 40)
    lx = 2   # left column x
    rx = mid  # right column x

    # Row 1: Title / Tab ID
    y = detail_y + 1
    safe_addstr(stdscr, y, lx, " Title: ", label_attr)
    safe_addstr(stdscr, y, lx + 8, session["title"], val_attr)
    safe_addstr(stdscr, y, rx, "Tab ID: ", label_attr)
    safe_addstr(stdscr, y, rx + 8, str(session.get("tab_id", "")), val_attr)

    # Row 2: Account (color) / Windows
    y += 1
    safe_addstr(stdscr, y, lx, " Account: ", label_attr)
    acct_text = f"{session['account']} ({color_name})"
    safe_addstr(stdscr, y, lx + 10, acct_text, acct_attr)
    safe_addstr(stdscr, y, rx, "Windows: ", label_attr)
    safe_addstr(stdscr, y, rx + 9, window_count, val_attr)

    # Row 3: Directory / PIDs
    y += 1
    safe_addstr(stdscr, y, lx, " Directory: ", label_attr)
    safe_addstr(stdscr, y, lx + 12, full_dir[:mid - lx - 13], val_attr)
    safe_addstr(stdscr, y, rx, "PIDs: ", label_attr)
    safe_addstr(stdscr, y, rx + 6, pids_str[:cols - rx - 7] if pids_str else "n/a", val_attr)

    # Row 4: Config dir / Flags
    y += 1
    safe_addstr(stdscr, y, lx, " Config dir: ", label_attr)
    safe_addstr(stdscr, y, lx + 13, config_dir[:mid - lx - 14], val_attr)
    safe_addstr(stdscr, y, rx, "Flags: ", label_attr)
    flags_display = effective_flags if effective_flags else "none"
    safe_addstr(stdscr, y, rx + 7, flags_display[:cols - rx - 8], val_attr)

    # Row 5: Status / Split
    y += 1
    safe_addstr(stdscr, y, lx, " Status: ", label_attr)
    safe_addstr(stdscr, y, lx + 9, session["status"], val_attr)
    safe_addstr(stdscr, y, rx, "Split: ", label_attr)
    split_display = effective_split if effective_split else "none"
    safe_addstr(stdscr, y, rx + 7, split_display, val_attr)

    # Row 6: Cmdline (full width)
    y += 1
    safe_addstr(stdscr, y, lx, " Cmdline: ", label_attr)
    safe_addstr(stdscr, y, lx + 10, (cmdline_display if cmdline_display else "n/a")[:cols - lx - 11], val_attr)


# ── Drawing ──────────────────────────────────────────────────────────────────


def draw_list_view(stdscr, sessions, selected, detail_visible=False):
    """Draw the main session list with header, rows, and footer."""
    rows, cols = stdscr.getmaxyx()
    stdscr.erase()

    # Title
    title = "Claude Code Dashboard"
    safe_addstr(
        stdscr,
        1,
        max(0, (cols - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    if not sessions:
        safe_addstr(
            stdscr, 4, 3, "No Claude sessions found.", curses.color_pair(CP_DIM)
        )
        safe_addstr(
            stdscr,
            5,
            3,
            "Press n to create a new session, or r to refresh.",
            curses.color_pair(CP_DIM),
        )
        draw_footer(stdscr, "list")
        return

    # Column widths
    c_tab = 5
    c_title = 28
    c_acct = 10
    c_status = 11
    c_dir = max(12, cols - c_tab - c_title - c_acct - c_status - 8)

    # Header row
    hdr = (
        f'  {"Tab":<{c_tab}}'
        f'{"Title":<{c_title}}'
        f'{"Account":<{c_acct}}'
        f'{"Directory":<{c_dir}}'
        f'{"Status"}'
    )
    safe_addstr(stdscr, 3, 1, hdr, curses.color_pair(CP_HEADER) | curses.A_BOLD)

    sep = (
        f'  {"─" * c_tab}'
        f'{"─" * c_title}'
        f'{"─" * c_acct}'
        f'{"─" * c_dir}'
        f'{"─" * c_status}'
    )
    safe_addstr(stdscr, 4, 1, sep, curses.color_pair(CP_DIM))

    # Scrollable session rows
    avail = rows - 8
    if detail_visible:
        avail -= DETAIL_PANE_HEIGHT
    avail = max(1, avail)
    if len(sessions) <= avail:
        offset = 0
    else:
        offset = max(0, min(selected - avail // 2, len(sessions) - avail))

    for vi, si in enumerate(range(offset, min(offset + avail, len(sessions)))):
        s = sessions[si]
        sel = si == selected
        mark = "▸" if sel else " "

        cp = _cp_for_account(s["account"], selected=sel)

        short_dir = shorten_path(s["cwd"], c_dir - 1)
        title_padded = _pad_to_width(s["title"], c_title)
        line = (
            f"{mark} "
            f'{s["index"]:<{c_tab}}'
            f'{title_padded}'
            f'{s["account"]:<{c_acct}}'
            f"{short_dir:<{c_dir}}"
            f'{s["status"]}'
        )

        attr = curses.color_pair(cp)
        if sel:
            attr |= curses.A_BOLD
        safe_addstr(stdscr, 5 + vi, 1, line, attr)

    draw_footer(stdscr, "list")


def draw_pick_view(stdscr, entries, selected, running_count):
    """Draw the config session picker list with Running/Stopped sections."""
    rows, cols = stdscr.getmaxyx()
    stdscr.erase()

    title = "Launch from Config"
    safe_addstr(
        stdscr, 1, max(0, (cols - len(title)) // 2), title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    if not entries:
        safe_addstr(stdscr, 4, 3, "No sessions configured.", curses.color_pair(CP_DIM))
        safe_addstr(stdscr, 5, 3, "Add entries to sessions.conf, then try again.", curses.color_pair(CP_DIM))
        draw_footer(stdscr, "pick")
        return

    c_title = 28
    c_acct = 12
    c_dir = max(12, cols - c_title - c_acct - 6)

    hdr = f'  {"Title":<{c_title}}{"Account":<{c_acct}}{"Directory":<{c_dir}}'
    safe_addstr(stdscr, 3, 1, hdr, curses.color_pair(CP_HEADER) | curses.A_BOLD)
    sep = f'  {"─" * c_title}{"─" * c_acct}{"─" * c_dir}'
    safe_addstr(stdscr, 4, 1, sep, curses.color_pair(CP_DIM))

    # Build visual rows: interleave section headers with entries.
    # Each visual row is either ("header", label) or ("entry", entry_index).
    stopped_count = len(entries) - running_count
    visual_rows = []
    if running_count > 0:
        visual_rows.append(("header", f"Running ({running_count})"))
        for i in range(running_count):
            visual_rows.append(("entry", i))
    if stopped_count > 0:
        visual_rows.append(("header", f"Stopped ({stopped_count})"))
        for i in range(running_count, len(entries)):
            visual_rows.append(("entry", i))

    # Find the visual index of the selected entry for scroll computation.
    sel_visual = 0
    for vi, row in enumerate(visual_rows):
        if row[0] == "entry" and row[1] == selected:
            sel_visual = vi
            break

    avail = rows - 8
    total_visual = len(visual_rows)
    if total_visual <= avail:
        offset = 0
    else:
        offset = max(0, min(sel_visual - avail // 2, total_visual - avail))

    for vi in range(offset, min(offset + avail, total_visual)):
        row = visual_rows[vi]
        screen_y = 5 + (vi - offset)

        if row[0] == "header":
            safe_addstr(
                stdscr, screen_y, 2, row[1],
                curses.color_pair(CP_DIM) | curses.A_BOLD,
            )
        else:
            entry_idx = row[1]
            entry = entries[entry_idx]
            sel = entry_idx == selected
            mark = "▸" if sel else " "
            is_running = entry_idx < running_count

            cp = _cp_for_account(entry["account"], selected=sel)
            short_dir = shorten_path(os.path.expanduser(entry["dir"]), c_dir - 1)
            title_padded = _pad_to_width(entry["title"], c_title)
            line = f"{mark} {title_padded}{entry['account']:<{c_acct}}{short_dir:<{c_dir}}"

            attr = curses.color_pair(cp)
            if sel:
                attr |= curses.A_BOLD
            if is_running and not sel:
                attr = curses.color_pair(CP_DIM)
            safe_addstr(stdscr, screen_y, 1, line, attr)

    draw_footer(stdscr, "pick")


def draw_footer(stdscr, mode):
    """Draw the help-key footer at the bottom of the screen."""
    rows, cols = stdscr.getmaxyx()

    hints = {
        "list": "↑/k Up  ↓/j Down  Enter Focus  x Close  R Restart  n New  l Launch  d Detail  / Search  a All  f Acct  r Refresh  q Quit",
        "filter": "1-N Toggle account  f Done  Esc Clear",
        "confirm": "y Confirm close    Any other key Cancel",
        "confirm_restart": "y Confirm restart    Any other key Cancel",
        "new": "Enter Next/Submit    Tab Cycle    Esc Cancel",
        "pick": "↑/k Up  ↓/j Down  Enter Launch  / Search  Esc Back  q Back",
    }
    text = hints.get(mode, "")
    safe_addstr(stdscr, rows - 1, 2, text[: cols - 3], curses.color_pair(CP_DIM))


def draw_confirm_prompt(stdscr, session, action="Close"):
    """Draw a confirmation prompt above the footer."""
    rows, _ = stdscr.getmaxyx()
    msg = f'{action} session "{session["title"]}"?  y/n'
    safe_addstr(stdscr, rows - 3, 2, msg, curses.color_pair(CP_WARN) | curses.A_BOLD)


def draw_status_message(stdscr, message):
    """Draw a transient status message above the footer."""
    rows, _ = stdscr.getmaxyx()
    safe_addstr(stdscr, rows - 3, 2, message, curses.color_pair(CP_WARN))


def draw_search_bar(stdscr, query, cursor_pos, active, match_count):
    """Draw the search/filter bar on the status line."""
    rows, cols = stdscr.getmaxyx()
    y = rows - 3

    if active:
        # Active search input: / query|
        prefix = "/ "
        before = query[:cursor_pos]
        after = query[cursor_pos:]
        cursor_ch = after[0] if after else " "
        after_rest = after[1:] if len(after) > 1 else ""

        safe_addstr(stdscr, y, 2, prefix, curses.color_pair(CP_WARN))
        x = 2 + len(prefix)
        safe_addstr(stdscr, y, x, before, curses.color_pair(CP_INPUT_ACTIVE))
        safe_addstr(
            stdscr, y, x + len(before), cursor_ch,
            curses.color_pair(CP_INPUT_ACTIVE) | curses.A_REVERSE,
        )
        if after_rest:
            safe_addstr(
                stdscr, y, x + len(before) + 1, after_rest,
                curses.color_pair(CP_INPUT_ACTIVE),
            )
        hint = f"  ({match_count} matches)"
        safe_addstr(
            stdscr, y, x + len(query) + 1, hint,
            curses.color_pair(CP_DIM),
        )
    else:
        # Accepted filter display
        text = f'Filter: "{query}" ({match_count} matches)  [Esc clear]'
        safe_addstr(stdscr, y, 2, text, curses.color_pair(CP_DIM))


def draw_filter_bar(stdscr, account_filter, account_filter_mode):
    """Draw the account filter bar on row 2.

    Each account is shown as N:name in its account color when enabled,
    or in CP_DIM when disabled. The prefix indicates whether filter mode
    is actively interactive.
    """
    _, cols = stdscr.getmaxyx()

    if account_filter_mode:
        prefix = "Filter: "
        prefix_attr = curses.color_pair(CP_WARN)
    else:
        prefix = "Accounts: "
        prefix_attr = curses.color_pair(CP_DIM)

    safe_addstr(stdscr, 2, 2, prefix, prefix_attr)
    x = 2 + len(prefix)

    for i, name in enumerate(_cfg._account_names):
        if i >= 9:
            break
        label = f"{i + 1}:{name}"
        enabled = account_filter is None or name in account_filter
        if enabled:
            cp = _cp_for_account(name)
            attr = curses.color_pair(cp)
        else:
            attr = curses.color_pair(CP_DIM)
        safe_addstr(stdscr, 2, x, label, attr)
        x += len(label) + 2
        if x >= cols - 2:
            break


def draw_new_form(stdscr, fields, active_field, cursor_pos):
    """Draw the new-session creation form."""
    rows, cols = stdscr.getmaxyx()
    stdscr.erase()

    safe_addstr(
        stdscr,
        1,
        max(0, (cols - 11) // 2),
        "New Session",
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    labels = ["Title:", "Directory:", "Account:", "Flags:", "Split:"]
    field_keys = ["title", "dir", "account", "flags", "pw"]

    for i, (label, key) in enumerate(zip(labels, field_keys)):
        y = 4 + i * 2
        is_active = i == active_field

        safe_addstr(stdscr, y, 4, f"{label:>12}", curses.color_pair(CP_HEADER))

        val = fields[key]
        if key in ("account", "pw"):
            display = f" {val} "
            if is_active:
                display += "  (Tab to cycle)"
            cp = CP_INPUT_ACTIVE if is_active else CP_INPUT
            safe_addstr(stdscr, y, 17, display, curses.color_pair(cp))
        else:
            # Text field with visible cursor
            if is_active:
                before = val[:cursor_pos]
                after = val[cursor_pos:]
                safe_addstr(
                    stdscr, y, 17, before, curses.color_pair(CP_INPUT_ACTIVE)
                )
                # Cursor character (reversed)
                cursor_ch = after[0] if after else " "
                safe_addstr(
                    stdscr,
                    y,
                    17 + len(before),
                    cursor_ch,
                    curses.color_pair(CP_INPUT_ACTIVE) | curses.A_REVERSE,
                )
                if len(after) > 1:
                    safe_addstr(
                        stdscr,
                        y,
                        17 + len(before) + 1,
                        after[1:],
                        curses.color_pair(CP_INPUT_ACTIVE),
                    )
            else:
                safe_addstr(
                    stdscr,
                    y,
                    17,
                    val[: cols - 20],
                    curses.color_pair(CP_INPUT),
                )

    # Hint below the last field
    hint_y = 4 + len(field_keys) * 2
    safe_addstr(
        stdscr, hint_y, 17,
        'Flags/Split: empty = inherit global, "-" = disable',
        curses.color_pair(CP_DIM),
    )

    draw_footer(stdscr, "new")


# ── Cycling helpers ──────────────────────────────────────────────────────────


def _cycle_account(current):
    """Return the next account name in the loaded list, wrapping around."""
    if not _cfg._account_names:
        return current
    try:
        idx = _cfg._account_names.index(current)
    except ValueError:
        idx = -1
    return _cfg._account_names[(idx + 1) % len(_cfg._account_names)]


_PW_OPTIONS = ["none", "stack", "vsplit", "hsplit", "split"]


def _cycle_pw(current):
    """Return the next project window option, wrapping around."""
    try:
        idx = _PW_OPTIONS.index(current)
    except ValueError:
        idx = -1
    return _PW_OPTIONS[(idx + 1) % len(_PW_OPTIONS)]


# ── Main event loop ─────────────────────────────────────────────────────────


def _session_fingerprint(sessions):
    """Return a hashable fingerprint of session state for change detection."""
    return tuple(
        (s["tab_id"], s["title"], s["status"], s["cwd"], s["account"],
         s.get("window_count", 1))
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
    mode = "list"  # list | confirm | confirm_restart | new | pick
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
    account_filter = None       # None = show all; set of names = show only these
    account_filter_mode = False  # True when filter bar is interactive

    # New-session form state — default to first loaded account
    default_account = _cfg._account_names[0] if _cfg._account_names else "work"
    default_pw = _cfg._default_project_window if _cfg._default_project_window else "none"
    new_fields = {"title": "", "dir": "~/", "account": default_account, "flags": "", "pw": default_pw}
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
                    stdscr, search_query, search_cursor,
                    search_active, len(filtered),
                )
            if account_filter is not None or account_filter_mode:
                draw_filter_bar(stdscr, account_filter, account_filter_mode)
            if account_filter_mode:
                # Override footer with filter-mode hints
                rows_now, cols_now = stdscr.getmaxyx()
                safe_addstr(stdscr, rows_now - 1, 0, " " * (cols_now - 1), curses.color_pair(CP_NORMAL))
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
                    stdscr, pick_query, pick_cursor,
                    pick_search_active, len(pick_filtered),
                )

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
                    old_fp = _session_fingerprint(filtered)
                    new_filtered = _apply_filters(new_sessions, search_query, account_filter)
                    if _session_fingerprint(new_filtered) != old_fp:
                        # Preserve selection by matching tab_id
                        old_tab_id = filtered[selected]["tab_id"] if filtered and selected < len(filtered) else None
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
                if key == 27:  # Escape — cancel search
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
                        filtered = _apply_filters(sessions, search_query, account_filter)
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
            if key == 27:  # Escape — cascade: filter mode → search → account filter → quit
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
                new_fields = {"title": "", "dir": "~/", "account": default_account, "flags": "", "pw": default_pw}
                new_active = 0
                new_cursor = 0
            elif key == ord("l"):
                if _cfg._sessions:
                    mode = "pick"
                    pick_selected = 0
                    pick_running = {s["title"] for s in sessions}
                    pick_filtered, pick_running_count = _sort_pick_entries(list(_cfg._sessions), pick_running)
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

            if key == 27:  # Esc — cancel
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
                        do_launch(socket, t, d, a, fl, pw_val)
                        sessions = query_sessions(socket, managed_only=(not show_all))
                        filtered = _apply_filters(sessions, search_query, account_filter)
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
                        new_fields[fk] = (
                            val[: new_cursor - 1] + val[new_cursor:]
                        )
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
                    new_fields[fk] = (
                        val[:new_cursor] + chr(key) + val[new_cursor:]
                    )
                    new_cursor += 1

        # ── Pick mode (launch from config) ────────────────────────
        elif mode == "pick":
            if pick_search_active:
                if key == 27:  # Escape — cancel search
                    pick_search_active = False
                    pick_query = ""
                    pick_cursor = 0
                    pick_filtered, pick_running_count = _sort_pick_entries(list(_cfg._sessions), pick_running)
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
                        pick_query = pick_query[:pick_cursor - 1] + pick_query[pick_cursor:]
                        pick_cursor -= 1
                        pick_filtered = _filter_config_sessions(_cfg._sessions, pick_query)
                        pick_filtered, pick_running_count = _sort_pick_entries(pick_filtered, pick_running)
                        pick_selected = min(pick_selected, max(0, len(pick_filtered) - 1))
                    continue
                elif key == curses.KEY_LEFT:
                    pick_cursor = max(0, pick_cursor - 1)
                    continue
                elif key == curses.KEY_RIGHT:
                    pick_cursor = min(len(pick_query), pick_cursor + 1)
                    continue
                elif 32 <= key < 127:
                    pick_query = pick_query[:pick_cursor] + chr(key) + pick_query[pick_cursor:]
                    pick_cursor += 1
                    pick_filtered = _filter_config_sessions(_cfg._sessions, pick_query)
                    pick_filtered, pick_running_count = _sort_pick_entries(pick_filtered, pick_running)
                    pick_selected = min(pick_selected, max(0, len(pick_filtered) - 1))
                    continue
                else:
                    continue

            if key in (ord("q"), ord("Q"), 27):  # q or Esc — back to list
                if pick_query and key == 27:
                    pick_query = ""
                    pick_cursor = 0
                    pick_filtered, pick_running_count = _sort_pick_entries(list(_cfg._sessions), pick_running)
                    pick_selected = min(pick_selected, max(0, len(pick_filtered) - 1))
                else:
                    mode = "list"
            elif key == ord("/"):
                pick_search_active = True
                pick_query = ""
                pick_cursor = 0
                pick_filtered, pick_running_count = _sort_pick_entries(list(_cfg._sessions), pick_running)
            elif key in (curses.KEY_UP, ord("k"), ord("K")) and pick_filtered:
                pick_selected = (pick_selected - 1) % len(pick_filtered)
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and pick_filtered:
                pick_selected = (pick_selected + 1) % len(pick_filtered)
            elif key in (10, 13, curses.KEY_ENTER) and pick_filtered:
                entry = pick_filtered[pick_selected]
                pw_val = entry["pw"]
                do_launch(socket, entry["title"], entry["dir"], entry["account"], entry["flags"], pw_val)
                sessions = query_sessions(socket, managed_only=(not show_all))
                filtered = _apply_filters(sessions, search_query, account_filter)
                selected = max(0, len(filtered) - 1)
                msg = f'Launched "{entry["title"]}"'
                msg_frames = 20
                mode = "list"
