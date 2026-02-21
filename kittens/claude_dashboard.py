#!/usr/bin/env python3
"""Claude Code Dashboard — interactive session picker kitten for Kitty.

Launch via the kitten action in kitty.conf:
    map ctrl+shift+d kitten /path/to/kittens/claude_dashboard.py

Or run standalone in a Kitty overlay:
    launch --type=overlay python3 /path/to/kittens/claude_dashboard.py

Lists all tabs running Claude Code, color-coded by account (work/personal).
Navigate with j/k or arrows, Enter to focus, x to close, n to create new,
r to refresh, q/Esc to dismiss.
"""

import curses
import glob
import json
import os
import subprocess
import sys


# ── Color pair IDs ───────────────────────────────────────────────────────────

CP_NORMAL = 1
CP_WORK = 2
CP_PERSONAL = 3
CP_WORK_SEL = 4
CP_PERSONAL_SEL = 5
CP_HEADER = 6
CP_DIM = 7
CP_WARN = 8
CP_INPUT = 9
CP_INPUT_ACTIVE = 10

# RGB values (0–255) for custom colors
WORK_FG = (80, 200, 120)
PERSONAL_FG = (100, 160, 230)
SEL_BG = (40, 40, 60)
DIM_FG = (110, 110, 110)
WARN_FG = (220, 180, 50)


# ── Kitty integration ───────────────────────────────────────────────────────


def get_kitty_socket():
    """Discover the Kitty remote control socket."""
    sock = os.environ.get("KITTY_LISTEN_ON", "")
    if sock:
        return sock
    candidates = sorted(
        glob.glob("/tmp/kitty-*"), key=os.path.getmtime, reverse=True
    )
    return f"unix:{candidates[0]}" if candidates else ""


def kitty_cmd(socket, *args):
    """Run a kitty @ remote control command. Returns the CompletedProcess or None."""
    try:
        return subprocess.run(
            ["kitty", "@", "--to", socket] + list(args),
            capture_output=True,
            text=True,
            timeout=5,
        )
    except (subprocess.SubprocessError, OSError):
        return None


def query_sessions(socket):
    """Query Kitty for all tabs that have a Claude process running."""
    result = kitty_cmd(socket, "ls")
    if not result or result.returncode != 0:
        return []
    try:
        data = json.loads(result.stdout)
    except json.JSONDecodeError:
        return []

    sessions = []
    for os_win in data:
        os_focused = os_win.get("is_focused", False)
        for idx, tab in enumerate(os_win.get("tabs", [])):
            has_claude = False
            has_config_dir = False
            windows = tab.get("windows", [])
            cwd = windows[0].get("cwd", "") if windows else ""

            for win in windows:
                for proc in win.get("foreground_processes", []):
                    cmdline = " ".join(proc.get("cmdline", []))
                    if "claude" in cmdline.lower():
                        has_claude = True
                    if "CLAUDE_CONFIG_DIR" in cmdline:
                        has_config_dir = True

            if not has_claude:
                continue

            title = tab.get("title", "")
            sessions.append(
                {
                    "index": idx + 1,
                    "title": title,
                    "account": (
                        "personal"
                        if (has_config_dir or title.startswith("P:"))
                        else "work"
                    ),
                    "cwd": cwd,
                    "status": (
                        "focused"
                        if (os_focused and tab.get("is_focused", False))
                        else "idle"
                    ),
                    "tab_id": tab.get("id", 0),
                }
            )
    return sessions


def do_focus(socket, tab_id):
    """Focus a tab by its Kitty tab ID."""
    kitty_cmd(socket, "focus-tab", "--match", f"id:{tab_id}")


def do_close(socket, tab_id):
    """Close a tab by its Kitty tab ID."""
    kitty_cmd(socket, "close-tab", "--match", f"id:{tab_id}")


def do_launch(socket, title, directory, account):
    """Create a new Claude session tab with the given title, directory, and account."""
    directory = os.path.expanduser(directory)
    cmd = ["launch", "--type=tab", "--tab-title", title, "--cwd", directory]
    if account == "personal":
        cmd += [
            "env",
            f"CLAUDE_CONFIG_DIR={os.path.expanduser('~/.claude-personal')}",
            "claude",
        ]
    else:
        cmd += ["claude"]
    kitty_cmd(socket, *cmd)

    colors = {
        "work": [
            "active_bg=#2d7d46",
            "active_fg=#ffffff",
            "inactive_bg=#1a4a2a",
            "inactive_fg=#aaaaaa",
        ],
        "personal": [
            "active_bg=#2d5a88",
            "active_fg=#ffffff",
            "inactive_bg=#1a3655",
            "inactive_fg=#aaaaaa",
        ],
    }
    kitty_cmd(
        socket,
        "set-tab-color",
        "--match",
        f"title:^{title}$",
        *colors.get(account, colors["work"]),
    )


# ── Curses helpers ───────────────────────────────────────────────────────────


def _c(r, g, b):
    """Convert 0–255 RGB to curses 0–1000 range."""
    return int(r * 1000 / 255), int(g * 1000 / 255), int(b * 1000 / 255)


def init_colors():
    """Set up curses color pairs, with a fallback for limited-color terminals."""
    curses.start_color()
    curses.use_default_colors()

    if curses.can_change_color() and curses.COLORS >= 256:
        base = 16
        curses.init_color(base, *_c(*WORK_FG))
        curses.init_color(base + 1, *_c(*PERSONAL_FG))
        curses.init_color(base + 2, *_c(*SEL_BG))
        curses.init_color(base + 3, *_c(*DIM_FG))
        curses.init_color(base + 4, *_c(*WARN_FG))

        curses.init_pair(CP_NORMAL, -1, -1)
        curses.init_pair(CP_WORK, base, -1)
        curses.init_pair(CP_PERSONAL, base + 1, -1)
        curses.init_pair(CP_WORK_SEL, base, base + 2)
        curses.init_pair(CP_PERSONAL_SEL, base + 1, base + 2)
        curses.init_pair(CP_HEADER, curses.COLOR_WHITE, -1)
        curses.init_pair(CP_DIM, base + 3, -1)
        curses.init_pair(CP_WARN, base + 4, -1)
        curses.init_pair(CP_INPUT, -1, -1)
        curses.init_pair(CP_INPUT_ACTIVE, curses.COLOR_WHITE, base + 2)
    else:
        curses.init_pair(CP_NORMAL, -1, -1)
        curses.init_pair(CP_WORK, curses.COLOR_GREEN, -1)
        curses.init_pair(CP_PERSONAL, curses.COLOR_CYAN, -1)
        curses.init_pair(CP_WORK_SEL, curses.COLOR_GREEN, curses.COLOR_BLACK)
        curses.init_pair(CP_PERSONAL_SEL, curses.COLOR_CYAN, curses.COLOR_BLACK)
        curses.init_pair(CP_HEADER, curses.COLOR_WHITE, -1)
        curses.init_pair(CP_DIM, curses.COLOR_WHITE, -1)
        curses.init_pair(CP_WARN, curses.COLOR_YELLOW, -1)
        curses.init_pair(CP_INPUT, -1, -1)
        curses.init_pair(CP_INPUT_ACTIVE, curses.COLOR_WHITE, curses.COLOR_BLACK)


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

        if s["account"] == "work":
            cp = CP_WORK_SEL if sel else CP_WORK
        else:
            cp = CP_PERSONAL_SEL if sel else CP_PERSONAL

        short_dir = shorten_path(s["cwd"], c_dir - 1)
        line = (
            f"{mark} "
            f'{s["index"]:<{c_tab}}'
            f'{s["title"]:<{c_title}}'
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
        "list": "↑/k Up  ↓/j Down  Enter Focus  x Close  n New  r Refresh  q Quit",
        "confirm": "y Confirm close    Any other key Cancel",
        "new": "Enter Next/Submit    Tab Toggle account    Esc Cancel",
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

    labels = ["Title:", "Directory:", "Account:"]
    field_keys = ["title", "dir", "account"]

    for i, (label, key) in enumerate(zip(labels, field_keys)):
        y = 4 + i * 2
        is_active = i == active_field

        safe_addstr(stdscr, y, 4, f"{label:>12}", curses.color_pair(CP_HEADER))

        val = fields[key]
        if key == "account":
            display = f" {val} "
            if is_active:
                display += "  (Tab to toggle)"
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

    draw_footer(stdscr, "new")


# ── Main event loop ─────────────────────────────────────────────────────────


def dashboard(stdscr, socket):
    """Run the interactive dashboard loop."""
    curses.curs_set(0)
    init_colors()
    stdscr.timeout(100)

    sessions = query_sessions(socket)
    selected = 0

    # Auto-scroll to the focused session, if one exists
    for i, s in enumerate(sessions):
        if s["status"] == "focused":
            selected = i
            break

    mode = "list"  # list | confirm | new
    msg = ""
    msg_frames = 0

    # New-session form state
    new_fields = {"title": "", "dir": "~/", "account": "work"}
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
                new_fields = {"title": "", "dir": "~/", "account": "work"}
                new_active = 0
                new_cursor = 0
            elif key in (ord("r"), ord("R")):
                sessions = query_sessions(socket)
                selected = min(selected, max(0, len(sessions) - 1))
                msg = f"{len(sessions)} session(s) found"
                msg_frames = 20

        # ── Confirm close ────────────────────────────────────────
        elif mode == "confirm":
            if key in (ord("y"), ord("Y")) and sessions:
                do_close(socket, sessions[selected]["tab_id"])
                sessions = query_sessions(socket)
                selected = min(selected, max(0, len(sessions) - 1))
                msg = "Session closed"
                msg_frames = 20
            mode = "list"

        # ── New session form ─────────────────────────────────────
        elif mode == "new":
            field_keys = ["title", "dir", "account"]
            fk = field_keys[new_active]

            if key == 27:  # Esc — cancel
                mode = "list"
            elif key == 9:  # Tab
                if fk == "account":
                    new_fields["account"] = (
                        "personal"
                        if new_fields["account"] == "work"
                        else "work"
                    )
                else:
                    new_active = min(new_active + 1, 2)
                    new_cursor = len(new_fields[field_keys[new_active]])
            elif key in (10, 13, curses.KEY_ENTER):
                if new_active < 2:
                    new_active += 1
                    new_cursor = len(new_fields[field_keys[new_active]])
                else:
                    # Submit the form
                    t = new_fields["title"].strip()
                    d = new_fields["dir"].strip()
                    a = new_fields["account"]
                    if t and d:
                        do_launch(socket, t, d, a)
                        sessions = query_sessions(socket)
                        selected = max(0, len(sessions) - 1)
                        msg = f'Launched "{t}"'
                        msg_frames = 20
                    else:
                        msg = "Title and directory are required"
                        msg_frames = 20
                    mode = "list"
            elif fk != "account":
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


# ── Entry points ─────────────────────────────────────────────────────────────


def main(args=None):
    """Entry point for both the Kitty kitten protocol and standalone usage.

    When launched via ``kitten /path/to/claude_dashboard.py``, Kitty calls this
    function in a child process attached to an overlay window. When run directly,
    ``curses.wrapper`` manages the terminal setup and teardown.
    """
    socket = get_kitty_socket()
    if not socket:
        print(
            "Error: No kitty socket found. "
            "Is Kitty running with remote control enabled?"
        )
        input("Press Enter to close...")
        return ""

    curses.wrapper(lambda stdscr: dashboard(stdscr, socket))
    return ""


# Kitty kitten protocol: handle_result runs in the Kitty main process after
# main() returns.  All tab actions are performed from within main() via
# subprocess calls to kitty @, so handle_result is intentionally a no-op.
try:
    from kittens.tui.handler import result_handler

    @result_handler(no_ui=True)
    def handle_result(args, answer, target_window_id, boss):
        pass

except ImportError:
    pass


if __name__ == "__main__":
    main(sys.argv)
