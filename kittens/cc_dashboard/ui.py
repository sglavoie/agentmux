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
            active_bg, _ = PALETTE.get(color_name, PALETTE["gray"])
            curses.init_color(color_slot, *_c(*active_bg))
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


# ── Drawing ──────────────────────────────────────────────────────────────────


def draw_list_view(stdscr, sessions, selected):
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
    c_status = 8
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


def draw_footer(stdscr, mode):
    """Draw the help-key footer at the bottom of the screen."""
    rows, cols = stdscr.getmaxyx()

    hints = {
        "list": "↑/k Up  ↓/j Down  Enter Focus  x Close  n New  a All  r Refresh  q Quit",
        "confirm": "y Confirm close    Any other key Cancel",
        "new": "Enter Next/Submit    Tab Cycle    Esc Cancel",
    }
    text = hints.get(mode, "")
    safe_addstr(stdscr, rows - 1, 2, text[: cols - 3], curses.color_pair(CP_DIM))


def draw_confirm_prompt(stdscr, session):
    """Draw the close-confirmation prompt above the footer."""
    rows, _ = stdscr.getmaxyx()
    msg = f'Close session "{session["title"]}"?  y/n'
    safe_addstr(stdscr, rows - 3, 2, msg, curses.color_pair(CP_WARN) | curses.A_BOLD)


def draw_status_message(stdscr, message):
    """Draw a transient status message above the footer."""
    rows, _ = stdscr.getmaxyx()
    safe_addstr(stdscr, rows - 3, 2, message, curses.color_pair(CP_WARN))


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

    mode = "list"  # list | confirm | new
    msg = ""
    msg_frames = 0

    # New-session form state — default to first loaded account
    default_account = _cfg._account_names[0] if _cfg._account_names else "work"
    default_pw = _cfg._default_project_window if _cfg._default_project_window else "none"
    new_fields = {"title": "", "dir": "~/", "account": default_account, "flags": "", "pw": default_pw}
    new_active = 0
    new_cursor = 0

    while True:
        # ── Draw ─────────────────────────────────────────────────
        if mode in ("list", "confirm"):
            draw_list_view(stdscr, sessions, selected)
            if msg and msg_frames > 0:
                draw_status_message(stdscr, msg)
            if mode == "confirm" and sessions:
                draw_confirm_prompt(stdscr, sessions[selected])
        elif mode == "new":
            draw_new_form(stdscr, new_fields, new_active, new_cursor)

        stdscr.refresh()

        # Tick down transient message
        if msg_frames > 0:
            msg_frames -= 1
            if msg_frames == 0:
                msg = ""

        key = stdscr.getch()
        if key == -1 or key == curses.KEY_RESIZE:
            continue

        # ── List mode ────────────────────────────────────────────
        if mode == "list":
            if key in (ord("q"), ord("Q"), 27):
                return
            if key in (curses.KEY_UP, ord("k"), ord("K")) and sessions:
                selected = (selected - 1) % len(sessions)
            elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and sessions:
                selected = (selected + 1) % len(sessions)
            elif key in (10, 13, curses.KEY_ENTER) and sessions:
                do_focus(socket, sessions[selected]["tab_id"])
                return
            elif key in (ord("x"), ord("X")) and sessions:
                mode = "confirm"
            elif key in (ord("n"), ord("N")):
                mode = "new"
                new_fields = {"title": "", "dir": "~/", "account": default_account, "flags": "", "pw": default_pw}
                new_active = 0
                new_cursor = 0
            elif key in (ord("a"), ord("A")):
                show_all = not show_all
                sessions = query_sessions(socket, managed_only=(not show_all))
                selected = min(selected, max(0, len(sessions) - 1))
                label = "all" if show_all else "managed"
                msg = f"Showing {label} sessions ({len(sessions)} found)"
                msg_frames = 20
            elif key in (ord("r"), ord("R")):
                sessions = query_sessions(socket, managed_only=(not show_all))
                selected = min(selected, max(0, len(sessions) - 1))
                msg = f"{len(sessions)} session(s) found"
                msg_frames = 20

        # ── Confirm close ────────────────────────────────────────
        elif mode == "confirm":
            if key in (ord("y"), ord("Y")) and sessions:
                do_close(socket, sessions[selected]["tab_id"])
                sessions = query_sessions(socket, managed_only=(not show_all))
                selected = min(selected, max(0, len(sessions) - 1))
                msg = "Session closed"
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
                        selected = max(0, len(sessions) - 1)
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
