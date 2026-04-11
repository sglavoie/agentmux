#!/usr/bin/env python3
"""Session info overlay for the current Claude Code tab.

Launched via F8, displays a read-only scrollable view of at-a-glance
stats about the current session: account, directory, git info, activity
counters, top tools, and recent log entries.
"""

import curses
import json
import os
import sys
import time


def _kitten_dir():
    """Return the directory containing this kitten script."""
    for candidate in (
        globals().get("__file__"),
        sys.argv[0] if sys.argv else None,
    ):
        if candidate and os.path.isfile(candidate):
            return os.path.dirname(os.path.realpath(candidate))
    return os.path.expanduser("~/.config/kitty")


sys.path.insert(0, _kitten_dir())

# Flush cached cc_dashboard modules so Kitty's long-lived Python process
# always picks up the current source files without requiring a restart.
for _mod in list(sys.modules):
    if _mod.startswith("cc_dashboard"):
        del sys.modules[_mod]

from cc_dashboard import config as _cfg
from cc_dashboard.colors import (
    CP_DIM,
    CP_HEADER,
    CP_NORMAL,
    CP_WARN,
    _cp_for_account,
    init_colors,
)
from cc_dashboard.config import load_accounts, load_defaults
from cc_dashboard.config_notes import read_notes
from cc_dashboard.drawing import safe_addstr, shorten_path
from cc_dashboard.remote.activity import (
    load_activity,
    load_activity_log,
    load_session_meta,
    resolve_session_id,
)
from cc_dashboard.remote.command import get_kitty_socket, kitty_cmd
from cc_dashboard.remote.git_ops import _get_git_info
from cc_dashboard.remote.shared import (
    _detect_state,
    _strip_icons,
    find_current_tab,
    format_duration,
)


def _get_current_session(socket):
    """Look up the current tab's session info via KITTY_WINDOW_ID."""
    result = kitty_cmd(socket, "ls")
    if not result or result.returncode != 0:
        return None
    try:
        data = json.loads(result.stdout)
    except (json.JSONDecodeError, TypeError):
        return None

    found = find_current_tab(data)
    if found is None:
        return None
    tab, cc_win = found
    if cc_win is None:
        return None

    title = _strip_icons(str(tab.get("title", "")))
    windows = tab.get("windows", [])
    window_count = len(windows)

    uv = cc_win.get("user_vars", {})
    cwd = ""
    for fg in cc_win.get("foreground_processes", []):
        c = fg.get("cwd", "")
        if c:
            cwd = c
            break
    return {
        "tab_id": tab.get("id", 0),
        "title": title,
        "full_title": str(tab.get("title", "")),
        "account": uv.get("cc_account", ""),
        "cc_flags": uv.get("cc_flags", ""),
        "cc_pw": uv.get("cc_pw", ""),
        "cc_env": uv.get("cc_env", ""),
        "cc_color": uv.get("cc_color", ""),
        "cc_config_dir": uv.get("cc_config_dir", ""),
        "cwd": cwd,
        "window_count": window_count,
    }


def _gather_info(session):
    """Collect supplementary data for the session."""
    info = {}
    tab_id = session["tab_id"]

    # Activity counters
    info["activity"] = load_activity(tab_id)

    # Session log data
    session_id = resolve_session_id(tab_id)
    info["session_id"] = session_id
    if session_id:
        entries, summary = load_activity_log(session_id)
        info["log_entries"] = entries
        info["log_summary"] = summary
        info["meta"] = load_session_meta(session_id)
    else:
        info["log_entries"] = []
        info["log_summary"] = {}
        info["meta"] = {}

    # Git info
    cwd = session.get("cwd", "")
    if cwd:
        info["git"] = _get_git_info(cwd)
    else:
        info["git"] = None

    # State detection
    info["state"] = _detect_state(session.get("full_title", ""))

    return info


def _build_lines(session, info, cols):
    """Build display content as a list of (kind, ...) tuples.

    Kinds:
      ("header", text)           - section header
      ("kv", key, value)         - single key-value pair
      ("kv2", k1, v1, k2, v2)   - two key-value pairs side by side
      ("tool", rank, name, cnt)  - ranked tool entry
      ("log", text)              - recent activity log line
      ("blank",)                 - empty line
      ("dim", text)              - dim informational text
    """
    lines: list[tuple] = []
    account = session.get("account", "")
    account_color = _cfg._accounts.get(account, {}).get("color", "gray")
    activity = info.get("activity")
    summary = info.get("log_summary", {})
    meta = info.get("meta", {})
    git = info.get("git")
    state = info.get("state") or "idle"

    # === Session ===
    lines.append(("header", "Session"))
    lines.append(("kv2", "Account", f"{account} ({account_color})", "Status", state))
    cwd = session.get("cwd", "")
    cwd_display = shorten_path(cwd, cols - 20) if cwd else "(unknown)"
    lines.append(("kv2", "Directory", cwd_display, "Tab ID", str(session["tab_id"])))

    flags = session.get("cc_flags", "")
    if flags:
        lines.append(("kv", "Flags", flags))

    pw = session.get("cc_pw", "")
    if pw:
        lines.append(("kv", "Project window", pw))

    env = session.get("cc_env", "")
    if env:
        lines.append(("kv", "Env vars", env))

    notes_text = read_notes(session["title"])
    if notes_text:
        note_lines = notes_text.split("\n")
        lines.append(("kv", "Notes", note_lines[0]))
        for nl in note_lines[1:]:
            lines.append(("kv", "", nl))

    if git:
        dirty_str = "dirty" if git["dirty"] else "clean"
        lines.append(("kv2", "Branch", git["branch"], "Worktree", dirty_str))

    model = meta.get("model", "")
    if model:
        lines.append(("kv", "Model", model))

    cc_color = session.get("cc_color", "")
    if cc_color:
        lines.append(("kv", "Color override", cc_color))

    if session.get("window_count", 1) > 1:
        lines.append(("kv", "Windows", str(session["window_count"])))

    lines.append(("blank",))

    # === Activity ===
    lines.append(("header", "Activity"))
    if activity:
        now = int(time.time())
        first_ts = activity.get("first_event_ts", 0)
        last_ts = activity.get("last_event_ts", 0)
        age_str = format_duration(now - first_ts) if first_ts else "?"
        idle_str = format_duration(now - last_ts) if last_ts else "?"

        total_events = (
            activity.get("prompt_count", 0)
            + activity.get("tool_use_count", 0)
            + activity.get("tool_failure_count", 0)
            + activity.get("permission_count", 0)
            + activity.get("stop_count", 0)
        )

        lines.append(("kv2", "Session age", age_str, "Idle time", idle_str))
        lines.append(
            (
                "kv2",
                "Events",
                str(total_events),
                "Stops",
                str(activity.get("stop_count", 0)),
            )
        )
        lines.append(
            (
                "kv2",
                "Prompts",
                str(activity.get("prompt_count", 0)),
                "Tools",
                str(activity.get("tool_use_count", 0)),
            )
        )
        lines.append(
            (
                "kv2",
                "Errors",
                str(activity.get("tool_failure_count", 0)),
                "Permissions",
                str(activity.get("permission_count", 0)),
            )
        )

        subagents = activity.get("total_subagents", 0)
        active_sub = activity.get("active_subagents", 0)
        tasks = activity.get("task_completed_count", 0)
        if subagents or tasks:
            sub_str = f"{active_sub} active / {subagents} total" if subagents else "0"
            lines.append(("kv2", "Subagents", sub_str, "Tasks done", str(tasks)))
    else:
        lines.append(("dim", "No activity data (hooks not installed?)"))

    lines.append(("blank",))

    # === Top Tools ===
    tool_counts = summary.get("tool_counts", [])
    if tool_counts:
        lines.append(("header", "Top Tools"))
        for i, (tool_name, count) in enumerate(tool_counts[:10]):
            lines.append(("tool", i + 1, tool_name, count))
        lines.append(("blank",))

    # === Log Summary ===
    duration = summary.get("duration", "")
    if duration:
        lines.append(("header", "Log Summary"))
        lines.append(
            ("kv2", "Duration", duration, "Total entries", str(summary.get("total", 0)))
        )
        if info.get("session_id"):
            lines.append(("kv", "Session ID", info["session_id"]))
        lines.append(("blank",))

    # === Recent Activity ===
    log_entries = info.get("log_entries", [])
    if log_entries:
        from cc_dashboard.views_overlays_logs import _format_log_entry

        lines.append(("header", "Recent Activity"))
        recent = log_entries[-15:]
        for entry in reversed(recent):
            formatted = _format_log_entry(entry, cols - 6)
            lines.append(("log", formatted))

    return lines


def _draw(stdscr, session, lines, scroll):
    """Render the session info overlay."""
    stdscr.erase()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)
    warn_attr = curses.color_pair(CP_WARN)

    account = session.get("account", "")
    account_attr = curses.color_pair(_cp_for_account(account)) | curses.A_BOLD

    # Title row
    title_text = f"Session Info: {session.get('title', '')}"
    safe_addstr(stdscr, 0, 2, title_text, account_attr)
    safe_addstr(stdscr, 1, 2, "\u2500" * min(cols - 4, len(title_text) + 4), dim_attr)

    # Content area
    content_start = 2
    content_height = max(1, rows - 4)  # leave room for footer
    visible = lines[scroll : scroll + content_height]

    key_col = 4
    val_col = 18
    key2_col = cols // 2 + 2 if cols > 60 else 40
    val2_col = key2_col + 14

    for i, line in enumerate(visible):
        y = content_start + i
        if y >= rows - 2:
            break
        kind = line[0]

        if kind == "header":
            safe_addstr(stdscr, y, 2, f"=== {line[1]} ===", header_attr)
        elif kind == "kv":
            safe_addstr(stdscr, y, key_col, f"{line[1]}:", dim_attr)
            safe_addstr(
                stdscr, y, val_col, str(line[2])[: cols - val_col - 1], normal_attr
            )
        elif kind == "kv2":
            safe_addstr(stdscr, y, key_col, f"{line[1]}:", dim_attr)
            safe_addstr(
                stdscr, y, val_col, str(line[2])[: key2_col - val_col - 2], normal_attr
            )
            safe_addstr(stdscr, y, key2_col, f"{line[3]}:", dim_attr)
            safe_addstr(
                stdscr, y, val2_col, str(line[4])[: cols - val2_col - 1], normal_attr
            )
        elif kind == "tool":
            rank_str = f"  {line[3]:>4}x  {line[2]}"
            safe_addstr(stdscr, y, key_col, rank_str, normal_attr)
        elif kind == "log":
            safe_addstr(
                stdscr, y, key_col, str(line[1])[: cols - key_col - 1], dim_attr
            )
        elif kind == "dim":
            safe_addstr(stdscr, y, key_col, str(line[1]), warn_attr)
        # "blank" -> skip

    # Scroll indicator
    if len(lines) > content_height:
        end = min(scroll + content_height, len(lines))
        pos = f"[{scroll + 1}-{end}/{len(lines)}]"
        safe_addstr(stdscr, rows - 2, 2, f"j/k scroll  {pos}", dim_attr)

    # Footer
    safe_addstr(stdscr, rows - 1, 2, "q/Esc Close  r Refresh", dim_attr)


def _run(stdscr, session):
    """Curses event loop for the session info overlay."""
    curses.set_escdelay(25)
    curses.curs_set(0)
    init_colors()
    stdscr.timeout(-1)

    rows, cols = stdscr.getmaxyx()
    info = _gather_info(session)
    lines = _build_lines(session, info, cols)
    scroll = 0
    content_height = max(1, rows - 4)

    while True:
        max_scroll = max(0, len(lines) - content_height)
        scroll = min(scroll, max_scroll)
        _draw(stdscr, session, lines, scroll)
        stdscr.refresh()

        key = stdscr.getch()
        if key == -1:
            continue

        if key == curses.KEY_RESIZE:
            rows, cols = stdscr.getmaxyx()
            content_height = rows - 4
            lines = _build_lines(session, info, cols)
            continue

        # Dismiss
        if key in (27, ord("q"), ord("Q")):
            break

        # Scroll down
        if key in (ord("j"), curses.KEY_DOWN):
            if scroll < max_scroll:
                scroll += 1
            continue

        # Scroll up
        if key in (ord("k"), curses.KEY_UP):
            if scroll > 0:
                scroll -= 1
            continue

        # Half-page down
        if key == 4:  # Ctrl+D
            scroll = min(scroll + content_height // 2, max_scroll)
            continue

        # Half-page up
        if key == 21:  # Ctrl+U
            scroll = max(scroll - content_height // 2, 0)
            continue

        # Top
        if key == ord("g"):
            scroll = 0
            continue

        # Bottom
        if key == ord("G"):
            scroll = max_scroll
            continue

        # Refresh
        if key == ord("r"):
            info = _gather_info(session)
            lines = _build_lines(session, info, cols)
            continue


def main(args=None):
    load_accounts()
    load_defaults()

    socket = os.environ.get("KITTY_LISTEN_ON", "") or get_kitty_socket()
    session = _get_current_session(socket)
    if session is None:
        # Not a Claude session — show a brief message
        def _show_msg(stdscr):
            curses.set_escdelay(25)
            curses.curs_set(0)
            init_colors()
            stdscr.timeout(-1)
            dim_attr = curses.color_pair(CP_DIM)
            safe_addstr(stdscr, 0, 2, "Not a Claude Code session", dim_attr)
            safe_addstr(stdscr, 1, 2, "Press any key to close", dim_attr)
            stdscr.getch()

        curses.wrapper(_show_msg)
        return ""

    curses.wrapper(_run, session)
    return ""


if __name__ == "__main__":
    main()
