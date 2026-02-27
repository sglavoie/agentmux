"""Draw functions, help data, cycling helpers, idle notifications."""

import curses
import os
import shutil
import subprocess
import sys
import time

from cc_dashboard import config as _cfg
from cc_dashboard.colors import (
    CP_ATTENTION,
    CP_DIM,
    CP_HEADER,
    CP_INPUT,
    CP_INPUT_ACTIVE,
    CP_MATCH,
    CP_NORMAL,
    CP_WARN,
    _cp_for_account,
)
from cc_dashboard.drawing import (
    _pad_to_width,
    _truncate_to_width,
    safe_addstr,
    shorten_path,
)
from cc_dashboard.filters import SORT_MODES
from cc_dashboard.kitty_remote import (
    _ICON_STATES,
    _strip_override_marker,
    format_duration,
)

# ── Detail pane ──────────────────────────────────────────────────────────────

DETAIL_PANE_HEIGHT = 13


def draw_detail_pane(stdscr, session, detail_y):
    """Render the detail pane for the selected session starting at detail_y."""
    rows, cols = stdscr.getmaxyx()
    if detail_y + DETAIL_PANE_HEIGHT > rows:
        return

    # Separator line
    sep = "─── Detail " + "─" * max(0, cols - 14)
    safe_addstr(stdscr, detail_y, 1, sep, curses.color_pair(CP_DIM))

    # Look up config session for flags/split info; retry without override marker
    cfg_entry = _cfg._find_config_session(session["title"])
    if cfg_entry is None:
        cfg_entry = _cfg._find_config_session(_strip_override_marker(session["title"]))
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
        full_dir = "~" + full_dir[len(home) :]

    config_dir = session.get("config_dir", "")
    if not config_dir:
        acct_info = _cfg._accounts.get(session["account"], {})
        config_dir = acct_info.get("config_dir", "")
    if config_dir and config_dir.startswith(home):
        config_dir = "~" + config_dir[len(home) :]
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
    lx = 2  # left column x
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
    safe_addstr(stdscr, y, lx + 12, full_dir[: mid - lx - 13], val_attr)
    safe_addstr(stdscr, y, rx, "PIDs: ", label_attr)
    safe_addstr(
        stdscr, y, rx + 6, pids_str[: cols - rx - 7] if pids_str else "n/a", val_attr
    )

    # Row 4: Config dir / Flags
    y += 1
    safe_addstr(stdscr, y, lx, " Config dir: ", label_attr)
    safe_addstr(stdscr, y, lx + 13, config_dir[: mid - lx - 14], val_attr)
    safe_addstr(stdscr, y, rx, "Flags: ", label_attr)
    flags_display = effective_flags if effective_flags else "none"
    safe_addstr(stdscr, y, rx + 7, flags_display[: cols - rx - 8], val_attr)

    # Row 5: Status / Split
    y += 1
    safe_addstr(stdscr, y, lx, " Status: ", label_attr)
    safe_addstr(stdscr, y, lx + 9, session["status"], val_attr)
    safe_addstr(stdscr, y, rx, "Split: ", label_attr)
    split_display = effective_split if effective_split else "none"
    safe_addstr(stdscr, y, rx + 7, split_display, val_attr)

    # Row 6: Branch / Working tree
    y += 1
    git_info = session.get("git")
    safe_addstr(stdscr, y, lx, " Branch: ", label_attr)
    if git_info:
        safe_addstr(stdscr, y, lx + 9, git_info["branch"][: mid - lx - 10], val_attr)
        safe_addstr(stdscr, y, rx, "Working tree: ", label_attr)
        if git_info["dirty"]:
            safe_addstr(stdscr, y, rx + 14, "dirty", curses.color_pair(CP_WARN))
        else:
            safe_addstr(stdscr, y, rx + 14, "clean", val_attr)
    else:
        safe_addstr(stdscr, y, lx + 9, "not a git repo", curses.color_pair(CP_DIM))

    # Row 7: Socket / Instance
    y += 1
    socket_display = session.get("socket", "")
    instance_display = session.get("instance_label", "")
    safe_addstr(stdscr, y, lx, " Socket: ", label_attr)
    safe_addstr(stdscr, y, lx + 9, socket_display[: mid - lx - 10], val_attr)
    safe_addstr(stdscr, y, rx, "Instance: ", label_attr)
    safe_addstr(stdscr, y, rx + 10, instance_display, val_attr)

    # Row 7: Cmdline (full width)
    y += 1
    safe_addstr(stdscr, y, lx, " Cmdline: ", label_attr)
    safe_addstr(
        stdscr,
        y,
        lx + 10,
        (cmdline_display if cmdline_display else "n/a")[: cols - lx - 11],
        val_attr,
    )

    # Row 7-8: Activity data
    activity = session.get("activity")
    if activity:
        now = int(time.time())
        age_str = format_duration(now - activity["first_event_ts"])
        last_active_str = time.strftime(
            "%H:%M:%S", time.localtime(activity["last_event_ts"])
        )

        y += 1
        safe_addstr(stdscr, y, lx, " Session age: ", label_attr)
        safe_addstr(stdscr, y, lx + 14, age_str, val_attr)
        safe_addstr(stdscr, y, rx, "Last active: ", label_attr)
        safe_addstr(stdscr, y, rx + 13, last_active_str, val_attr)

        y += 1
        safe_addstr(stdscr, y, lx, " Events: ", label_attr)
        parts_x = lx + 9
        prompts_str = f"{activity['prompt_count']} prompts, "
        tools_str = f"{activity['tool_use_count']} tools, "
        errors_str = f"{activity['tool_failure_count']} errors, "
        perms_str = f"{activity['permission_count']} permissions"
        safe_addstr(stdscr, y, parts_x, prompts_str, val_attr)
        parts_x += len(prompts_str)
        safe_addstr(stdscr, y, parts_x, tools_str, val_attr)
        parts_x += len(tools_str)
        err_attr = (
            curses.color_pair(CP_WARN)
            if activity["tool_failure_count"] > 0
            else val_attr
        )
        safe_addstr(stdscr, y, parts_x, errors_str, err_attr)
        parts_x += len(errors_str)
        perm_attr = (
            curses.color_pair(CP_WARN) if activity["permission_count"] > 0 else val_attr
        )
        safe_addstr(stdscr, y, parts_x, perms_str, perm_attr)
    else:
        y += 1
        safe_addstr(stdscr, y, lx, " No activity data", curses.color_pair(CP_DIM))


# ── Idle alert notifications ─────────────────────────────────────────────────


def _find_alerter():
    """Locate the alerter binary, accounting for Kitty's restricted PATH.

    When Kitty is launched as a macOS .app (Dock, Spotlight, etc.) it only
    inherits the minimal system PATH (/usr/bin:/bin:/usr/sbin:/sbin), so
    shutil.which() alone will miss Homebrew-installed binaries.  We fall back
    to probing the standard Homebrew prefix locations directly.
    """
    path = shutil.which("alerter")
    if path:
        return path
    for candidate in ("/opt/homebrew/bin/alerter", "/usr/local/bin/alerter"):
        if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
            return candidate
    return None


_ALERTER_BIN = _find_alerter()
_alerter_warned = False


def _send_notification(title, subtitle, message, group=None, timeout=10):
    """Send a macOS notification via alerter (non-blocking).

    If alerter is not installed, logs a one-time warning to stderr and
    returns silently.  Uses Popen so the curses event loop is never
    blocked while alerter waits for dismissal or timeout.
    """
    global _alerter_warned
    if _ALERTER_BIN is None:
        if not _alerter_warned:
            _alerter_warned = True
            print(
                "cc-manager: alerter not found. Install with: "
                "brew install vjeantet/tap/alerter",
                file=sys.stderr,
            )
        return
    cmd = [
        _ALERTER_BIN,
        "--title",
        title,
        "--subtitle",
        subtitle,
        "--message",
        message,
        "--timeout",
        str(timeout),
    ]
    if group is not None:
        cmd += ["--group", str(group)]
    subprocess.Popen(
        cmd,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )


def _check_idle_notifications(sessions):
    """Send macOS notifications for sessions that have been idle too long.

    Uses per-tab cooldown files whose mtime tracks the last_event_ts that
    triggered the notification.  A new notification only fires when the
    session's last_event_ts has advanced (i.e. new activity occurred and the
    session went idle again).
    """
    threshold = _cfg._default_idle_alert_minutes * 60
    if threshold <= 0 or _cfg._default_notify_idle != "on":
        return

    now = int(time.time())
    for s in sessions:
        if s["status"] not in ("idle", "stopped"):
            continue
        activity = s.get("activity")
        if not activity:
            continue
        idle_secs = now - activity["last_event_ts"]
        if idle_secs < threshold:
            continue

        cooldown_file = f"/tmp/ccm-notify-idle-{s['tab_id']}"
        last_event_ts = activity["last_event_ts"]

        if os.path.isfile(cooldown_file):
            try:
                cooldown_mtime = int(os.path.getmtime(cooldown_file))
            except OSError:
                cooldown_mtime = 0
            if cooldown_mtime >= last_event_ts:
                continue

        # Write the cooldown file with mtime set to last_event_ts so we
        # won't re-notify until activity advances past this timestamp.
        try:
            with open(cooldown_file, "w") as f:
                f.write("")
            os.utime(cooldown_file, (last_event_ts, last_event_ts))
        except OSError:
            pass

        session_title = s.get("title", "Session")
        stop_icon = _ICON_STATES.get("stop", "")
        _send_notification(
            title=session_title,
            subtitle=f"{stop_icon} Session idle",
            message=f"idle for {format_duration(idle_secs)}",
            group=s["tab_id"],
        )


# ── Drawing ──────────────────────────────────────────────────────────────────


def draw_list_view(stdscr, sessions, selected, detail_visible=False, sort_label=""):
    """Draw the main session list with header, rows, and footer."""
    rows, cols = stdscr.getmaxyx()
    stdscr.erase()

    # Title
    title = "Claude Code Dashboard"
    title_x = max(0, (cols - len(title)) // 2)
    safe_addstr(
        stdscr,
        1,
        title_x,
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )
    if sort_label:
        indicator = f" [{sort_label}]"
        ind_x = title_x + len(title)
        if ind_x + len(indicator) < cols:
            safe_addstr(stdscr, 1, ind_x, indicator, curses.color_pair(CP_DIM))

    if not sessions:
        safe_addstr(
            stdscr, 4, 3, "No Claude sessions found.", curses.color_pair(CP_DIM)
        )
        safe_addstr(
            stdscr,
            5,
            3,
            "Press n to create a new session, or Ctrl+r to refresh.",
            curses.color_pair(CP_DIM),
        )
        draw_footer(stdscr, "list", exclude={"m Move"})
        return

    # Show Instance column only when sessions span multiple instances
    multi_instance = len({s.get("instance_label", "") for s in sessions}) > 1
    c_inst = 10 if multi_instance else 0

    # Column widths
    c_title = 20
    c_acct = 10
    c_branch = 24
    c_status = 18
    c_dir = max(12, cols - c_title - c_acct - c_inst - c_branch - c_status - 8)

    # Header row
    hdr = (
        f"  {'Title':<{c_title}}"
        f"{'Account':<{c_acct}}"
    )
    if multi_instance:
        hdr += f"{'Instance':<{c_inst}}"
    hdr += (
        f"{'Directory':<{c_dir}}"
        f"{'Branch':<{c_branch}}"
        f"{'Status'}"
    )
    safe_addstr(stdscr, 3, 1, hdr, curses.color_pair(CP_HEADER) | curses.A_BOLD)

    sep = f"  {'─' * c_title}{'─' * c_acct}"
    if multi_instance:
        sep += '─' * c_inst
    sep += f"{'─' * c_dir}{'─' * c_branch}{'─' * c_status}"
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
        mark = "\u25b8" if sel else " "

        cp = _cp_for_account(s["account"], selected=sel)

        short_dir = shorten_path(s["cwd"], c_dir - 1)
        title_padded = _pad_to_width(_truncate_to_width(s["title"], c_title - 1), c_title)

        # Build branch display
        git_info = s.get("git")
        if git_info:
            branch_display = git_info["branch"]
            if git_info["dirty"]:
                branch_display += "*"
            if len(branch_display) > c_branch - 1:
                branch_display = branch_display[: c_branch - 2] + "…"
        else:
            branch_display = "-"

        # Build status text with idle duration from activity data
        status_text = s["status"]
        idle_secs = 0
        activity = s.get("activity")
        if activity:
            now = int(time.time())
            idle_secs = now - activity["last_event_ts"]
            status_text = f"{s['status']} {format_duration(idle_secs)}"

        line = (
            f"{mark} "
            f"{title_padded}"
            f"{s['account']:<{c_acct}}"
        )
        if multi_instance:
            line += f"{s.get('instance_label', ''):<{c_inst}}"
        line += f"{short_dir:<{c_dir}}"
        line += f"{branch_display:<{c_branch}}"

        row_attr = curses.color_pair(cp)
        if sel:
            row_attr |= curses.A_BOLD
        y = 5 + vi
        safe_addstr(stdscr, y, 1, line, row_attr)

        # Render status with attention highlighting for permission/error,
        # or warn highlighting for idle sessions exceeding the threshold
        status_x = 1 + len(line)
        idle_threshold = _cfg._default_idle_alert_minutes * 60
        if s["status"] in ("permission", "error"):
            status_attr = curses.color_pair(CP_ATTENTION) | curses.A_BOLD
        elif (
            idle_threshold > 0
            and s["status"] in ("idle", "stopped")
            and activity
            and idle_secs >= idle_threshold
        ):
            status_attr = curses.color_pair(CP_WARN) | curses.A_BOLD
        else:
            status_attr = row_attr
        safe_addstr(stdscr, y, status_x, status_text, status_attr)

    footer_exclude = None if multi_instance else {"m Move"}
    draw_footer(stdscr, "list", exclude=footer_exclude)


def draw_pick_view(stdscr, entries, selected, running_count):
    """Draw the config session picker list with Running/Stopped sections."""
    rows, cols = stdscr.getmaxyx()
    stdscr.erase()

    title = "Launch from Config"
    safe_addstr(
        stdscr,
        1,
        max(0, (cols - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    if not entries:
        safe_addstr(stdscr, 4, 3, "No sessions configured.", curses.color_pair(CP_DIM))
        safe_addstr(
            stdscr,
            5,
            3,
            "Add entries to sessions.conf, then try again.",
            curses.color_pair(CP_DIM),
        )
        draw_footer(stdscr, "pick")
        return

    c_title = 28
    c_acct = 12
    c_dir = max(12, cols - c_title - c_acct - 6)

    hdr = f"  {'Title':<{c_title}}{'Account':<{c_acct}}{'Directory':<{c_dir}}"
    safe_addstr(stdscr, 3, 1, hdr, curses.color_pair(CP_HEADER) | curses.A_BOLD)
    sep = f"  {'─' * c_title}{'─' * c_acct}{'─' * c_dir}"
    safe_addstr(stdscr, 4, 1, sep, curses.color_pair(CP_DIM))

    # Build visual rows: interleave section headers with entries.
    # Each visual row is either ("header", label) or ("entry", entry_index).
    stopped_count = len(entries) - running_count
    visual_rows: list[tuple[str, str | int]] = []
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
                stdscr,
                screen_y,
                2,
                row[1],
                curses.color_pair(CP_DIM) | curses.A_BOLD,
            )
        else:
            entry_idx = row[1]
            entry = entries[entry_idx]
            sel = entry_idx == selected
            mark = "\u25b8" if sel else " "
            is_running = entry_idx < running_count

            cp = _cp_for_account(entry["account"], selected=sel)
            short_dir = shorten_path(os.path.expanduser(entry["dir"]), c_dir - 1)
            title_padded = _pad_to_width(_truncate_to_width(entry["title"], c_title - 1), c_title)
            line = (
                f"{mark} {title_padded}{entry['account']:<{c_acct}}{short_dir:<{c_dir}}"
            )

            attr = curses.color_pair(cp)
            if sel:
                attr |= curses.A_BOLD
            if is_running and not sel:
                attr = curses.color_pair(CP_DIM)
            safe_addstr(stdscr, screen_y, 1, line, attr)

    draw_footer(stdscr, "pick")


_HELP_SECTIONS = [
    (
        "Navigation",
        [
            ("k / Up", "Move selection up"),
            ("j / Down", "Move selection down"),
            ("Enter", "Focus & switch to selected tab"),
            ("q", "Quit dashboard"),
        ],
    ),
    (
        "Session Actions",
        [
            ("x", "Close session (with confirmation)"),
            ("R", "Restart session (with confirmation)"),
            ("m", "Move session to another instance"),
            ("O", "Override config dir for session"),
            ("r", "Rename selected session"),
            ("n", "Open new session form"),
            ("l", "Launch session from config"),
            ("S", "Save sessions to sessions.conf"),
            ("p", "Peek at session output"),
            ("w", "Clone session to git worktree"),
        ],
    ),
    (
        "View",
        [
            ("d", "Toggle detail pane"),
            ("a", "Toggle all / managed tabs"),
            ("c", "Open config file browser"),
            ("Ctrl+r", "Refresh session list"),
        ],
    ),
    (
        "Sort",
        [("s", "Open sort picker")],
    ),
    (
        "Search",
        [
            ("/", "Start search"),
            ("Enter", "Accept search query"),
            ("Esc", "Cancel search"),
        ],
    ),
    (
        "Account Filter",
        [
            ("f", "Enter / exit filter mode"),
            ("1-9", "Toggle account visibility"),
            ("Esc", "Clear filter"),
        ],
    ),
]

# Pre-build help content as a list of (kind, text, text2) tuples.
# kind: "header" for section headers, "binding" for key-desc pairs, "blank".
_HELP_LINES = []
for _sect_name, _bindings in _HELP_SECTIONS:
    _HELP_LINES.append(("header", _sect_name, ""))
    for _key, _desc in _bindings:
        _HELP_LINES.append(("binding", _key, _desc))
    _HELP_LINES.append(("blank", "", ""))
# Remove trailing blank line
if _HELP_LINES and _HELP_LINES[-1][0] == "blank":
    _HELP_LINES.pop()


def draw_help_overlay(stdscr, scroll):
    """Draw a full-screen help overlay listing all keybindings."""
    stdscr.erase()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)

    key_col = 4
    desc_col = 18

    # Row 0: title, rows 1..rows-3: content, row rows-2: scroll, row rows-1: footer
    safe_addstr(stdscr, 0, 2, "Dashboard Help", header_attr)
    content_height = rows - 3  # available lines for help content
    total = len(_HELP_LINES)
    visible = _HELP_LINES[scroll : scroll + content_height]

    for i, (kind, text1, text2) in enumerate(visible):
        y = i + 1
        if kind == "header":
            safe_addstr(stdscr, y, 2, text1, header_attr)
        elif kind == "binding":
            safe_addstr(stdscr, y, key_col, text1, normal_attr)
            safe_addstr(stdscr, y, desc_col, text2[: cols - desc_col - 1], dim_attr)

    # Scroll indicator when content overflows
    if total > content_height:
        pos = f"[{scroll + 1}-{min(scroll + content_height, total)}/{total}]"
        safe_addstr(stdscr, rows - 2, 2, f"j/k to scroll  {pos}", dim_attr)

    safe_addstr(stdscr, rows - 1, 2, "Press ? / q / Esc to close help", dim_attr)


_FOOTER_HINTS = {
    "list": [
        "? Help",
        "q Quit",
        "Enter Focus",
        "x Close",
        "R Restart",
        "m Move",
        "O CfgDir",
        "r Rename",
        "n New",
        "l Launch",
        "w Worktree",
        "S Save",
        "c Config",
        "d Detail",
        "p Peek",
        "/ Search",
        "a All",
        "f Acct",
        "s Sort",
        "^r Refresh",
    ],
    "rename": ["Enter Confirm", "Esc Cancel"],
    "filter": ["1-N Toggle account", "f Done", "Esc Clear"],
    "confirm": ["y Confirm close", "Any other key Cancel"],
    "confirm_restart": ["y Confirm restart", "Any other key Cancel"],
    "confirm_save": ["y Confirm save", "Any other key Cancel"],
    "new": ["Enter Next/Submit", "Tab Cycle", "Up/Dn Navigate", "Esc Cancel"],
    "new_dir_browse": ["Enter Select", "Up/Dn Navigate", ". Toggle hidden", "Esc Close"],
    "pick": ["Enter Launch", "i Instance", "O CfgDir", "/ Search", "Esc Back", "q Back"],
    "override": ["j/k Navigate", "Enter Confirm", "Esc Cancel"],
    "move": ["j/k Navigate", "Enter Move", "Esc Cancel"],
    "peek": ["j/k Scroll", "^D/^U Half-page", "^F/^B Page", "G/g Top/Bot", "/ Search", "n/N Next/Prev", "r Refresh", "s Screen/Scrollback", "q/Esc Close"],
    "peek_search": ["Enter Confirm", "Esc Cancel"],
    "config_pick": ["j/k Navigate", "Enter Select", "Esc Back"],
    "config_action": ["j/k Navigate", "Enter Confirm", "Esc Back"],
    "sort_pick": ["j/k Navigate", "Enter Select", "Esc Cancel"],
    "worktree": ["Enter Create", "Esc Cancel"],
}


def draw_footer(stdscr, mode, exclude=None):
    """Draw the help-key footer at the bottom of the screen."""
    rows, cols = stdscr.getmaxyx()
    sep = "  "
    avail = cols - 3  # 2-char left margin + 1 safety
    items = _FOOTER_HINTS.get(mode, [])
    if exclude:
        items = [i for i in items if i not in exclude]
    parts: list[str] = []
    length = 0
    for item in items:
        item_len = len(item)
        needed = item_len if not parts else len(sep) + item_len
        if length + needed > avail:
            break
        parts.append(item)
        length += needed
    # Clear the full line first to remove residual text from previous footer
    safe_addstr(stdscr, rows - 1, 0, " " * (cols - 1), curses.color_pair(CP_NORMAL))
    safe_addstr(stdscr, rows - 1, 2, sep.join(parts), curses.color_pair(CP_DIM))


def draw_confirm_prompt(stdscr, session, action="Close"):
    """Draw a confirmation prompt above the footer."""
    rows, _ = stdscr.getmaxyx()
    msg = f'{action} session "{session["title"]}"?  y/n'
    safe_addstr(stdscr, rows - 3, 2, msg, curses.color_pair(CP_WARN) | curses.A_BOLD)


def draw_override_picker(stdscr, session, override_selected):
    """Draw a config dir selection overlay for override.

    Shows all unique config dir paths in a centered box.  The currently
    selected path is marked with an arrow, and the session's current
    config dir is labeled "(current)".
    """
    rows, cols = stdscr.getmaxyx()
    dirs = _cfg._unique_config_dirs()
    if not dirs:
        return

    home = os.path.expanduser("~")

    def _short(p):
        if p.startswith(home):
            return "~" + p[len(home) :]
        return p

    display_dirs = [_short(d) for d in dirs]

    # Determine the session's current config dir for the "(current)" label
    current_dir = _cfg._accounts.get(session.get("account", ""), {}).get(
        "config_dir", ""
    )

    # Box dimensions
    max_dir_len = max(len(d) for d in display_dirs)
    # "  > path (current)" — arrow(2) + path + " (current)"(10) + padding
    box_w = min(cols - 4, max(36, max_dir_len + 20))
    box_h = len(dirs) + 4  # title + subtitle + blank + entries
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    # Draw background
    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)

    # Title
    title = "Override Config Dir"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    # Subtitle: session name
    session_title = session["title"]
    sub = f'for "{session_title}"'
    safe_addstr(
        stdscr,
        box_y + 1,
        box_x + max(0, (box_w - len(sub)) // 2),
        sub,
        dim_attr,
    )

    # Config dir list
    normal_attr = curses.color_pair(CP_NORMAL)
    for i, display in enumerate(display_dirs):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        sel = i == override_selected
        marker = "\u25b8 " if sel else "  "
        suffix = " (current)" if dirs[i] == current_dir else ""
        label = f"{marker}{display}{suffix}"

        attr = normal_attr
        if sel:
            attr |= curses.A_BOLD
        safe_addstr(stdscr, y, box_x + 2, label, attr)


def draw_move_picker(stdscr, session, instance_labels, move_selected):
    """Draw an instance selection overlay for moving a session.

    Shows all instance labels except the session's current one in a
    centered box.  The currently selected target is marked with an arrow.
    """
    rows, cols = stdscr.getmaxyx()
    current_instance = session.get("instance_label", "")
    targets = [lbl for lbl in instance_labels if lbl != current_instance]
    if not targets:
        return

    # Box dimensions
    max_label_len = max(len(lbl) for lbl in targets)
    box_w = min(cols - 4, max(36, max_label_len + 16))
    box_h = len(targets) + 4  # title + subtitle + blank + entries
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    # Draw background
    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)

    # Title
    title = "Move to Instance"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    # Subtitle: session name
    session_title = session["title"]
    sub = f'"{session_title}" from {current_instance}'
    safe_addstr(
        stdscr,
        box_y + 1,
        box_x + max(0, (box_w - len(sub)) // 2),
        sub,
        dim_attr,
    )

    # Instance list
    normal_attr = curses.color_pair(CP_NORMAL)
    for i, lbl in enumerate(targets):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        sel = i == move_selected
        marker = "\u25b8 " if sel else "  "
        label = f"{marker}{lbl}"

        attr = normal_attr
        if sel:
            attr |= curses.A_BOLD
        safe_addstr(stdscr, y, box_x + 2, label, attr)


def _shorten_home(path):
    """Replace the home directory prefix with ~ for display."""
    home = os.path.expanduser("~")
    if path.startswith(home):
        return "~" + path[len(home):]
    return path


def draw_config_picker(stdscr, config_entries, selected):
    """Draw a centered config file picker overlay.

    config_entries is a list of (label, path) tuples from _all_config_paths().
    """
    rows, cols = stdscr.getmaxyx()
    if not config_entries:
        return

    # Build display rows
    display_rows = []
    for label, path in config_entries:
        short = _shorten_home(path)
        suffix = "" if os.path.isfile(path) else " (missing)"
        display_rows.append(f"{label}: {short}{suffix}")

    max_row_len = max(len(r) for r in display_rows)
    box_w = min(cols - 4, max(40, max_row_len + 10))
    box_h = len(config_entries) + 3  # title + blank + entries
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    # Draw background
    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)

    # Title
    title = "Config Files"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    # Entries
    normal_attr = curses.color_pair(CP_NORMAL)
    for i, display in enumerate(display_rows):
        y = box_y + 2 + i
        if y >= rows - 1:
            break
        sel = i == selected
        marker = "\u25b8 " if sel else "  "
        label = f"{marker}{display}"

        attr = normal_attr
        if sel:
            attr |= curses.A_BOLD
        safe_addstr(stdscr, y, box_x + 2, label[: box_w - 4], attr)


_SORT_DESCRIPTIONS = {
    "default": "Default Kitty tab-bar order",
    "name": "Alphabetical by title",
    "account": "Grouped by account, alpha within",
    "status": "Error \u2192 permission \u2192 active \u2192 idle \u2192 stopped",
    "idle": "Most idle first",
    "activity": "Most recently active first",
}

_SORT_DISPLAY_LABELS = {
    "default": "Tab order",
    "name": "Name",
    "account": "Account",
    "status": "Status",
    "idle": "Idle",
    "activity": "Activity",
}


def draw_sort_picker(stdscr, selected, current_mode):
    """Draw a centered sort mode picker overlay.

    selected is the cursor index, current_mode is the active sort mode string.
    """
    rows, cols = stdscr.getmaxyx()

    label_col = max(len(v) for v in _SORT_DISPLAY_LABELS.values()) + 2

    display_rows = []
    for mode in SORT_MODES:
        check = "\u2713 " if mode == current_mode else "  "
        label = _SORT_DISPLAY_LABELS[mode].ljust(label_col)
        desc = _SORT_DESCRIPTIONS[mode]
        display_rows.append(f"{check}{label}{desc}")

    max_row_len = max(len(r) for r in display_rows)
    box_w = min(cols - 4, max(40, max_row_len + 10))
    box_h = len(SORT_MODES) + 3  # title + blank + entries
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    # Draw background
    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)

    # Title
    title = "Sort By"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    # Entries
    normal_attr = curses.color_pair(CP_NORMAL)
    for i, display in enumerate(display_rows):
        y = box_y + 2 + i
        if y >= rows - 1:
            break
        sel = i == selected
        marker = "\u25b8 " if sel else "  "
        label = f"{marker}{display}"

        attr = normal_attr
        if sel:
            attr |= curses.A_BOLD
        safe_addstr(stdscr, y, box_x + 2, label[: box_w - 4], attr)


def draw_config_action_picker(stdscr, label, path, selected):
    """Draw a centered action picker for a selected config file."""
    rows, cols = stdscr.getmaxyx()
    actions = ["Show contents", "Edit in editor", "Show file path"]

    short_path = _shorten_home(path)
    subtitle = f"{label}: {short_path}"

    max_action_len = max(len(a) for a in actions)
    box_w = min(cols - 4, max(40, max(max_action_len, len(subtitle)) + 10))
    box_h = len(actions) + 4  # title + subtitle + blank + entries
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    # Draw background
    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)

    # Title
    title = f"Config: {label}"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    # Subtitle
    safe_addstr(
        stdscr,
        box_y + 1,
        box_x + max(0, (box_w - len(subtitle)) // 2),
        subtitle[: box_w - 2],
        dim_attr,
    )

    # Actions
    normal_attr = curses.color_pair(CP_NORMAL)
    for i, action in enumerate(actions):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        sel = i == selected
        marker = "\u25b8 " if sel else "  "
        text = f"{marker}{action}"

        attr = normal_attr
        if sel:
            attr |= curses.A_BOLD
        safe_addstr(stdscr, y, box_x + 2, text, attr)


def draw_config_show(stdscr, label, path, lines, scroll):
    """Draw a full-screen scrollable file viewer for a config file."""
    stdscr.erase()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)

    # Row 0: title
    short_path = _shorten_home(path)
    title = f"{label}: {short_path}"
    safe_addstr(stdscr, 0, 2, title, header_attr)

    content_height = rows - 3  # rows 1 to rows-3 for content

    if not lines or (len(lines) == 1 and lines[0] == "(file not found)"):
        display_lines = lines if lines else ["(file not found)"]
        for i, line in enumerate(display_lines[:content_height]):
            safe_addstr(stdscr, i + 1, 2, line[: cols - 3], dim_attr)
    else:
        visible = lines[scroll : scroll + content_height]
        for i, line in enumerate(visible):
            safe_addstr(stdscr, i + 1, 2, line[: cols - 3], normal_attr)

    # Scroll indicator
    total = len(lines) if lines else 0
    if total > content_height:
        pos = f"[{scroll + 1}-{min(scroll + content_height, total)}/{total}]"
        safe_addstr(stdscr, rows - 2, 2, f"j/k to scroll  {pos}", dim_attr)

    safe_addstr(stdscr, rows - 1, 2, "Press q / Esc to close", dim_attr)


def _render_peek_line(stdscr, row, line, cols, query, is_match, is_current,
                      normal_attr, match_attr, span_attr, current_attr):
    """Render a single peek line with optional match-span highlighting."""
    clipped = line[: cols - 1]
    if not query or not is_match:
        safe_addstr(stdscr, row, 0, clipped, normal_attr)
        return

    q_lower = query.lower()
    q_len = len(query)
    line_lower = clipped.lower()
    bg_attr = match_attr
    hl_attr = current_attr if is_current else span_attr
    x = 0
    pos = 0
    while pos < len(clipped):
        idx = line_lower.find(q_lower, pos)
        if idx == -1:
            safe_addstr(stdscr, row, x, clipped[pos:], bg_attr)
            break
        if idx > pos:
            safe_addstr(stdscr, row, x, clipped[pos:idx], bg_attr)
            x += idx - pos
        safe_addstr(stdscr, row, x, clipped[idx : idx + q_len], hl_attr)
        x += q_len
        pos = idx + q_len
    else:
        pass


def _draw_peek_search_bar(stdscr, query, cursor_pos, match_count):
    """Draw the peek search input bar at rows - 2."""
    rows, _cols = stdscr.getmaxyx()
    y = rows - 2

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
        safe_addstr(stdscr, y, x + len(before) + 1, after_rest,
                    curses.color_pair(CP_INPUT_ACTIVE))
    hint = f"  ({match_count} matches)"
    safe_addstr(stdscr, y, x + len(query) + 1, hint, curses.color_pair(CP_DIM))


def draw_peek_overlay(stdscr, session_title, lines, scroll, extent, error="",
                      search_query="", search_active=False, search_cursor=0,
                      match_line_indices=None, match_index=0):
    """Draw a full-screen scrollable view of a session's terminal output."""
    # clear() forces a full terminal repaint, preventing colored backgrounds
    # from the list view bleeding through on rows the peek doesn't draw over.
    stdscr.clear()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)
    warn_attr = curses.color_pair(CP_WARN)
    match_attr = curses.color_pair(CP_MATCH)
    span_attr = curses.color_pair(CP_WARN) | curses.A_BOLD
    current_attr = curses.color_pair(CP_ATTENTION) | curses.A_BOLD

    if match_line_indices is None:
        match_line_indices = []
    match_set = set(match_line_indices)
    current_line_idx = match_line_indices[match_index] if match_line_indices else -1

    extent_label = "scrollback" if extent == "all" else "screen"
    title = f"Peek: {session_title} [{extent_label}]"
    safe_addstr(stdscr, 0, 2, title, header_attr)

    content_height = rows - 3

    if error:
        safe_addstr(stdscr, 2, 2, error, warn_attr)
    elif not lines or (len(lines) == 1 and not lines[0].strip()):
        safe_addstr(stdscr, 2, 2, "(empty output)", dim_attr)
    else:
        visible = lines[scroll : scroll + content_height]
        for i, line in enumerate(visible):
            abs_idx = scroll + i
            is_match = abs_idx in match_set
            is_current = abs_idx == current_line_idx
            _render_peek_line(
                stdscr, i + 1, line, cols, search_query,
                is_match, is_current, normal_attr, match_attr,
                span_attr, current_attr,
            )

    total = len(lines) if lines else 0
    if search_active:
        _draw_peek_search_bar(stdscr, search_query, search_cursor, len(match_line_indices))
    elif match_line_indices:
        indicator = f"[match {match_index + 1}/{len(match_line_indices)}]"
        if total > content_height:
            pos = f"[{scroll + 1}-{min(scroll + content_height, total)}/{total}]"
            safe_addstr(stdscr, rows - 2, 2, f"{indicator}  {pos}", dim_attr)
        else:
            safe_addstr(stdscr, rows - 2, 2, indicator, dim_attr)
    elif total > content_height:
        pos = f"[{scroll + 1}-{min(scroll + content_height, total)}/{total}]"
        safe_addstr(stdscr, rows - 2, 2, f"j/k to scroll  {pos}", dim_attr)

    draw_footer(stdscr, "peek_search" if search_active else "peek")


def draw_status_message(stdscr, message):
    """Draw a transient status message above the footer."""
    rows, _ = stdscr.getmaxyx()
    safe_addstr(stdscr, rows - 3, 2, message, curses.color_pair(CP_WARN))


def draw_search_bar(stdscr, query, cursor_pos, active, match_count):
    """Draw the search/filter bar on the status line."""
    rows, _cols = stdscr.getmaxyx()
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
            stdscr,
            y,
            x + len(before),
            cursor_ch,
            curses.color_pair(CP_INPUT_ACTIVE) | curses.A_REVERSE,
        )
        if after_rest:
            safe_addstr(
                stdscr,
                y,
                x + len(before) + 1,
                after_rest,
                curses.color_pair(CP_INPUT_ACTIVE),
            )
        hint = f"  ({match_count} matches)"
        safe_addstr(
            stdscr,
            y,
            x + len(query) + 1,
            hint,
            curses.color_pair(CP_DIM),
        )
    else:
        # Accepted filter display
        text = f'Filter: "{query}" ({match_count} matches)  [Esc clear]'
        safe_addstr(stdscr, y, 2, text, curses.color_pair(CP_DIM))


def draw_rename_input(stdscr, current_value, cursor_pos):
    """Draw a single-line rename input on the status line area."""
    rows, _cols = stdscr.getmaxyx()
    y = rows - 3

    prefix = "Rename: "
    before = current_value[:cursor_pos]
    after = current_value[cursor_pos:]
    cursor_ch = after[0] if after else " "
    after_rest = after[1:] if len(after) > 1 else ""

    safe_addstr(stdscr, y, 2, prefix, curses.color_pair(CP_WARN))
    x = 2 + len(prefix)
    safe_addstr(stdscr, y, x, before, curses.color_pair(CP_INPUT_ACTIVE))
    safe_addstr(
        stdscr,
        y,
        x + len(before),
        cursor_ch,
        curses.color_pair(CP_INPUT_ACTIVE) | curses.A_REVERSE,
    )
    if after_rest:
        safe_addstr(
            stdscr,
            y,
            x + len(before) + 1,
            after_rest,
            curses.color_pair(CP_INPUT_ACTIVE),
        )


def draw_worktree_input(stdscr, session, branch_value, cursor_pos):
    """Draw a two-line worktree input on the status line area."""
    rows, _cols = stdscr.getmaxyx()

    # Source info line
    source_text = f'Clone "{session["title"]}" \u2192 worktree'
    safe_addstr(stdscr, rows - 4, 2, source_text, curses.color_pair(CP_DIM))

    # Branch input line
    prefix = "Branch: "
    before = branch_value[:cursor_pos]
    after = branch_value[cursor_pos:]
    cursor_ch = after[0] if after else " "
    after_rest = after[1:] if len(after) > 1 else ""

    y = rows - 3
    safe_addstr(stdscr, y, 2, prefix, curses.color_pair(CP_WARN))
    x = 2 + len(prefix)
    safe_addstr(stdscr, y, x, before, curses.color_pair(CP_INPUT_ACTIVE))
    safe_addstr(
        stdscr,
        y,
        x + len(before),
        cursor_ch,
        curses.color_pair(CP_INPUT_ACTIVE) | curses.A_REVERSE,
    )
    if after_rest:
        safe_addstr(
            stdscr,
            y,
            x + len(before) + 1,
            after_rest,
            curses.color_pair(CP_INPUT_ACTIVE),
        )


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


def draw_new_form(stdscr, fields, active_field, cursor_pos, multi_instance=False):
    """Draw the new-session creation form."""
    _rows, cols = stdscr.getmaxyx()
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
    cycle_fields = {"account", "pw"}

    if multi_instance:
        labels.append("Instance:")
        field_keys.append("instance")
        cycle_fields.add("instance")

    for i, (label, key) in enumerate(zip(labels, field_keys, strict=False)):
        y = 4 + i * 2
        is_active = i == active_field

        safe_addstr(stdscr, y, 4, f"{label:>12}", curses.color_pair(CP_HEADER))

        val = fields[key]
        if key in cycle_fields:
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
                safe_addstr(stdscr, y, 17, before, curses.color_pair(CP_INPUT_ACTIVE))
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
        stdscr,
        hint_y,
        17,
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


def _cycle_instance(current, instance_labels):
    """Return the next instance label from the list, wrapping around."""
    if not instance_labels:
        return current
    try:
        idx = instance_labels.index(current)
    except ValueError:
        idx = -1
    return instance_labels[(idx + 1) % len(instance_labels)]


# ── Directory browser ───────────────────────────────────────────────────────


def _scan_directories(field_value, max_results=50, show_hidden=False):
    """Scan filesystem for directories matching the current dir field input."""
    expanded = os.path.expanduser(field_value)
    if field_value.endswith("/") or expanded.endswith("/"):
        scan_dir = expanded
        partial = ""
    else:
        scan_dir = os.path.dirname(expanded)
        partial = os.path.basename(expanded)
    if not os.path.isdir(scan_dir):
        return []
    try:
        entries = sorted(
            entry.name
            for entry in os.scandir(scan_dir)
            if entry.is_dir(follow_symlinks=True)
            and (show_hidden or not entry.name.startswith("."))
            and (not partial or partial.lower() in entry.name.lower())
        )
    except PermissionError:
        return []
    return entries[:max_results]


def draw_dir_dropdown(stdscr, entries, selected, scroll, show_hidden=False):
    """Draw a directory autocomplete dropdown below the dir field."""
    rows, cols = stdscr.getmaxyx()
    dropdown_y = 7
    dropdown_x = 17
    max_visible = min(10, rows - dropdown_y - 3)
    if max_visible < 1:
        return
    if not entries:
        safe_addstr(
            stdscr, dropdown_y, dropdown_x,
            "(no matching directories)", curses.color_pair(CP_DIM),
        )
        return
    visible = entries[scroll:scroll + max_visible]
    max_width = cols - dropdown_x - 2
    for i, name in enumerate(visible):
        y = dropdown_y + i
        if y >= rows - 2:
            break
        abs_idx = scroll + i
        is_sel = abs_idx == selected
        prefix = "> " if is_sel else "  "
        display = (prefix + name + "/")[:max_width]
        if is_sel:
            attr = curses.color_pair(CP_INPUT_ACTIVE) | curses.A_BOLD
        else:
            attr = curses.color_pair(CP_NORMAL)
        safe_addstr(stdscr, y, dropdown_x, display, attr)
    total = len(entries)
    if total > max_visible:
        indicator = f"[{scroll + 1}-{min(scroll + max_visible, total)}/{total}]"
        ind_y = dropdown_y + len(visible)
        if ind_y < rows - 2:
            safe_addstr(stdscr, ind_y, dropdown_x, indicator, curses.color_pair(CP_DIM))
