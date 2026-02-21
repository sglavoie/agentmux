#!/usr/bin/env python3
"""Claude Code Dashboard — interactive session picker kitten for Kitty.

Launch via the kitten action in kitty.conf:
    map ctrl+shift+d kitten /path/to/kittens/claude_dashboard.py

Or run standalone in a Kitty overlay:
    launch --type=overlay python3 /path/to/kittens/claude_dashboard.py

Lists all tabs running Claude Code, color-coded by account.
Navigate with j/k or arrows, Enter to focus, x to close, n to create new,
r to refresh, q/Esc to dismiss.
"""

import curses
import glob
import json
import os
import subprocess
import sys


# ── Color palette ────────────────────────────────────────────────────────────
# Each entry maps a color name to (active_bg_rgb, inactive_bg_rgb) tuples.
# active_fg is always (255,255,255) and inactive_fg is always (170,170,170).

PALETTE = {
    "green":  ((0x2d, 0x7d, 0x46), (0x1a, 0x4a, 0x2a)),
    "blue":   ((0x2d, 0x5a, 0x88), (0x1a, 0x36, 0x55)),
    "red":    ((0x8b, 0x2d, 0x2d), (0x55, 0x1a, 0x1a)),
    "orange": ((0x8b, 0x5a, 0x2d), (0x55, 0x3a, 0x1a)),
    "purple": ((0x5a, 0x2d, 0x8b), (0x3a, 0x1a, 0x55)),
    "teal":   ((0x2d, 0x7d, 0x7d), (0x1a, 0x4a, 0x4a)),
    "pink":   ((0x8b, 0x2d, 0x6a), (0x55, 0x1a, 0x42)),
    "yellow": ((0x7d, 0x7d, 0x2d), (0x4a, 0x4a, 0x1a)),
    "gray":   ((0x55, 0x55, 0x55), (0x33, 0x33, 0x33)),
    "cyan":   ((0x2d, 0x6a, 0x8b), (0x1a, 0x42, 0x55)),
}

ACTIVE_FG = (255, 255, 255)
INACTIVE_FG = (170, 170, 170)

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


# ── Account configuration ───────────────────────────────────────────────────

_accounts = {}       # name -> {"config_dir": str, "color": str}
_account_names = []  # ordered list of account names
# Maps account name -> (cp_normal, cp_selected) curses color pair IDs
_account_cp = {}


def _accounts_config_path():
    env = os.environ.get("CC_MANAGER_ACCOUNTS", "")
    if env:
        return env
    xdg = os.environ.get("XDG_CONFIG_HOME", os.path.join(os.path.expanduser("~"), ".config"))
    return os.path.join(xdg, "cc-manager", "accounts.conf")


def load_accounts():
    """Parse accounts.conf into the module-level _accounts dict.

    Falls back to hardcoded work/personal defaults if the file is missing.
    """
    global _accounts, _account_names
    _accounts = {}
    _account_names = []

    path = _accounts_config_path()
    if os.path.isfile(path):
        with open(path) as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                parts = line.split("|")
                if len(parts) < 2:
                    continue
                name = parts[0].strip()
                config_dir = parts[1].strip() if len(parts) > 1 else ""
                color = parts[2].strip() if len(parts) > 2 else "gray"
                if not name:
                    continue
                config_dir = os.path.expanduser(config_dir) if config_dir else ""
                _accounts[name] = {"config_dir": config_dir, "color": color}
                _account_names.append(name)
    else:
        home = os.path.expanduser("~")
        _accounts["work"] = {"config_dir": os.path.join(home, ".claude"), "color": "green"}
        _accounts["personal"] = {"config_dir": os.path.join(home, ".claude-personal"), "color": "blue"}
        _account_names = ["work", "personal"]


def _resolve_account_by_config_dir(config_dir):
    """Reverse-lookup: find an account name matching the given config directory."""
    home = os.path.expanduser("~")
    default_dir = os.path.join(home, ".claude")

    for name, info in _accounts.items():
        d = info["config_dir"]
        if config_dir and d == config_dir:
            return name
        if not config_dir and (not d or d == default_dir):
            return name

    # No config dir detected — return the account that uses the default dir
    if not config_dir:
        for name, info in _accounts.items():
            d = info["config_dir"]
            if not d or d == default_dir:
                return name

    return _account_names[0] if _account_names else "unknown"


def _palette_tab_colors(account):
    """Return a list of kitty set-tab-color arguments for the given account."""
    color_name = _accounts.get(account, {}).get("color", "gray")
    active_bg, inactive_bg = PALETTE.get(color_name, PALETTE["gray"])

    def _hex(rgb):
        return f"#{rgb[0]:02x}{rgb[1]:02x}{rgb[2]:02x}"

    return [
        f"active_bg={_hex(active_bg)}",
        "active_fg=#ffffff",
        f"inactive_bg={_hex(inactive_bg)}",
        "inactive_fg=#aaaaaa",
    ]


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

    home = os.path.expanduser("~")
    default_dir = os.path.join(home, ".claude")

    sessions = []
    for os_win in data:
        os_focused = os_win.get("is_focused", False)
        for idx, tab in enumerate(os_win.get("tabs", [])):
            has_claude = False
            detected_config_dir = ""
            windows = tab.get("windows", [])
            cwd = windows[0].get("cwd", "") if windows else ""

            for win in windows:
                for proc in win.get("foreground_processes", []):
                    cmdline = proc.get("cmdline", [])
                    cmdline_str = " ".join(cmdline)
                    if "claude" in cmdline_str.lower():
                        has_claude = True
                    for arg in cmdline:
                        if "CLAUDE_CONFIG_DIR=" in arg:
                            detected_config_dir = arg.split("=", 1)[1]

            if not has_claude:
                continue

            account = _resolve_account_by_config_dir(detected_config_dir)

            title = tab.get("title", "")
            sessions.append(
                {
                    "index": idx + 1,
                    "title": title,
                    "account": account,
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

    home = os.path.expanduser("~")
    default_dir = os.path.join(home, ".claude")
    config_dir = _accounts.get(account, {}).get("config_dir", "")

    if not config_dir or config_dir == default_dir:
        cmd += ["claude"]
    else:
        cmd += ["env", f"CLAUDE_CONFIG_DIR={config_dir}", "claude"]

    kitty_cmd(socket, *cmd)
    kitty_cmd(
        socket,
        "set-tab-color",
        "--match",
        f"title:^{title}$",
        *_palette_tab_colors(account),
    )


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
        for name in _account_names:
            color_name = _accounts[name]["color"]
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
        for i, name in enumerate(_account_names):
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
        "new": "Enter Next/Submit    Tab Cycle account    Esc Cancel",
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

    draw_footer(stdscr, "new")


# ── Main event loop ─────────────────────────────────────────────────────────


def _cycle_account(current):
    """Return the next account name in the loaded list, wrapping around."""
    if not _account_names:
        return current
    try:
        idx = _account_names.index(current)
    except ValueError:
        idx = -1
    return _account_names[(idx + 1) % len(_account_names)]


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

    # New-session form state — default to first loaded account
    default_account = _account_names[0] if _account_names else "work"
    new_fields = {"title": "", "dir": "~/", "account": default_account}
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
                new_fields = {"title": "", "dir": "~/", "account": default_account}
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
                    new_fields["account"] = _cycle_account(new_fields["account"])
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
    load_accounts()

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
