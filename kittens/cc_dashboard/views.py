"""Draw functions, help data, cycling helpers, idle notifications."""

import curses
import os
import subprocess
import time

from cc_dashboard import config as _cfg
from cc_dashboard.colors import (
    CP_ATTENTION,
    CP_DIM,
    CP_HEADER,
    CP_INPUT,
    CP_INPUT_ACTIVE,
    CP_NORMAL,
    CP_WARN,
    _cp_for_account,
)
from cc_dashboard.drawing import _pad_to_width, safe_addstr, shorten_path
from cc_dashboard.kitty_remote import format_duration

# ── Detail pane ──────────────────────────────────────────────────────────────

DETAIL_PANE_HEIGHT = 11


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

    # Row 6: Cmdline (full width)
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

        title = s.get("title", "Session")
        subprocess.run(
            [
                "osascript",
                "-e",
                f'display notification "{title} has been idle for '
                f'{format_duration(idle_secs)}" with title "cc-manager" '
                f'subtitle "Session idle"',
            ],
            capture_output=True,
        )


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
    c_status = 18
    c_dir = max(12, cols - c_tab - c_title - c_acct - c_status - 8)

    # Header row
    hdr = (
        f"  {'Tab':<{c_tab}}"
        f"{'Title':<{c_title}}"
        f"{'Account':<{c_acct}}"
        f"{'Directory':<{c_dir}}"
        f"{'Status'}"
    )
    safe_addstr(stdscr, 3, 1, hdr, curses.color_pair(CP_HEADER) | curses.A_BOLD)

    sep = f"  {'─' * c_tab}{'─' * c_title}{'─' * c_acct}{'─' * c_dir}{'─' * c_status}"
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
        title_padded = _pad_to_width(s["title"], c_title)

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
            f"{s['index']:<{c_tab}}"
            f"{title_padded}"
            f"{s['account']:<{c_acct}}"
            f"{short_dir:<{c_dir}}"
        )

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

    draw_footer(stdscr, "list")


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
            title_padded = _pad_to_width(entry["title"], c_title)
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
            ("n", "Open new session form"),
            ("l", "Launch session from config"),
        ],
    ),
    (
        "View",
        [
            ("d", "Toggle detail pane"),
            ("a", "Toggle all / managed tabs"),
            ("r", "Refresh session list"),
        ],
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
        "n New",
        "l Launch",
        "d Detail",
        "/ Search",
        "a All",
        "f Acct",
        "r Refresh",
    ],
    "filter": ["1-N Toggle account", "f Done", "Esc Clear"],
    "confirm": ["y Confirm close", "Any other key Cancel"],
    "confirm_restart": ["y Confirm restart", "Any other key Cancel"],
    "new": ["Enter Next/Submit", "Tab Cycle", "Esc Cancel"],
    "pick": ["Enter Launch", "/ Search", "Esc Back", "q Back"],
}


def draw_footer(stdscr, mode):
    """Draw the help-key footer at the bottom of the screen."""
    rows, cols = stdscr.getmaxyx()
    sep = "  "
    avail = cols - 3  # 2-char left margin + 1 safety
    items = _FOOTER_HINTS.get(mode, [])
    parts: list[str] = []
    length = 0
    for item in items:
        item_len = len(item)
        needed = item_len if not parts else len(sep) + item_len
        if length + needed > avail:
            break
        parts.append(item)
        length += needed
    safe_addstr(stdscr, rows - 1, 2, sep.join(parts), curses.color_pair(CP_DIM))


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

    for i, (label, key) in enumerate(zip(labels, field_keys, strict=False)):
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
