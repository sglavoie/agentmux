"""Overlay, picker, peek, log, and hooks dashboard views."""

import curses
import os
import time

from cc_dashboard import config as _cfg
from cc_dashboard.colors import (
    CP_ATTENTION,
    CP_DIM,
    CP_HEADER,
    CP_INPUT_ACTIVE,
    CP_MATCH,
    CP_NORMAL,
    CP_WARN,
    _cp_for_account,
)
from cc_dashboard.drawing import _pad_to_width, safe_addstr
from cc_dashboard.filters import SORT_MODES
from cc_dashboard.views_shared import (
    _draw_peek_search_bar,
    _render_peek_line,
    _shorten_home,
    draw_footer,
)


def draw_override_picker(stdscr, session, override_selected):
    """Draw a config-dir selection overlay for override."""
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

    current_dir = session.get("config_dir", "")
    if not current_dir:
        current_dir = _cfg._accounts.get(session.get("account", ""), {}).get(
            "config_dir", ""
        )

    max_dir_len = max(len(d) for d in display_dirs)
    box_w = min(cols - 4, max(36, max_dir_len + 20))
    box_h = len(dirs) + 4
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)

    title = "Override Config Dir"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    sub = f'for "{session["title"]}"'
    safe_addstr(
        stdscr,
        box_y + 1,
        box_x + max(0, (box_w - len(sub)) // 2),
        sub,
        dim_attr,
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, display in enumerate(display_dirs):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        sel = i == override_selected
        marker = "▸ " if sel else "  "
        suffix = " (current)" if dirs[i] == current_dir else ""
        label = f"{marker}{display}{suffix}"

        attr = normal_attr
        if sel:
            attr |= curses.A_BOLD
        safe_addstr(stdscr, y, box_x + 2, label, attr)


def draw_move_picker(stdscr, session, instance_labels, move_selected, subtitle=None):
    """Draw an instance selection overlay for moving a session."""
    rows, cols = stdscr.getmaxyx()
    current_instance = session.get("instance_label", "")
    targets = [lbl for lbl in instance_labels if lbl != current_instance]
    if not targets:
        return

    max_label_len = max(len(lbl) for lbl in targets)
    box_w = min(cols - 4, max(36, max_label_len + 16))
    box_h = len(targets) + 4
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)

    title = "Move to Instance"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    sub = subtitle if subtitle else f'"{session["title"]}" from {current_instance}'
    safe_addstr(
        stdscr,
        box_y + 1,
        box_x + max(0, (box_w - len(sub)) // 2),
        sub,
        dim_attr,
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, lbl in enumerate(targets):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        sel = i == move_selected
        marker = "▸ " if sel else "  "
        attr = normal_attr | (curses.A_BOLD if sel else 0)
        safe_addstr(stdscr, y, box_x + 2, f"{marker}{lbl}", attr)


def draw_session_action_picker(stdscr, subtitle, action_labels, selected):
    """Draw a centered action picker for selected or marked sessions."""
    rows, cols = stdscr.getmaxyx()
    if not action_labels:
        return

    max_action_len = max(len(label) for label in action_labels)
    title = "Session Actions"
    subtitle = subtitle or ""

    box_w = min(cols - 4, max(42, max(max_action_len, len(subtitle)) + 12))
    box_h = len(action_labels) + 4
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)

    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )
    safe_addstr(
        stdscr,
        box_y + 1,
        box_x + max(0, (box_w - len(subtitle)) // 2),
        subtitle[: box_w - 2],
        dim_attr,
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, label in enumerate(action_labels):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        is_selected = i == selected
        marker = "▸ " if is_selected else "  "
        attr = normal_attr | (curses.A_BOLD if is_selected else 0)
        safe_addstr(stdscr, y, box_x + 2, f"{marker}{label}"[: box_w - 4], attr)


def _format_log_entry(entry, cols):
    """Format a single JSONL activity log entry into a display string."""
    import time as _time

    ts = entry.get("ts")
    time_str = _time.strftime("%H:%M:%S", _time.localtime(ts)) if ts else "??:??:??"

    event = entry.get("event", "?")
    tool = entry.get("tool", "")
    ctx = entry.get("ctx", {})

    if event == "tool":
        if tool in ("Read", "Write", "Edit"):
            detail = ctx.get("file_path", "")
            if detail:
                detail = _shorten_home(detail)
        elif tool == "Bash":
            detail = ctx.get("cmd", "")
        elif tool in ("Grep", "Glob"):
            detail = ctx.get("pattern", "")
        elif tool == "WebFetch":
            detail = ctx.get("url", "")
        elif tool == "WebSearch":
            detail = ctx.get("query", "")
        elif tool == "Task":
            detail = ctx.get("desc", "")
            agent = ctx.get("agent", "")
            if agent:
                detail = f"[{agent}] {detail}"
        else:
            keys = ctx.get("keys", [])
            detail = ", ".join(keys) if keys else ""
        line = f"{time_str}  {event:<5} {tool:<12} {detail}"
    elif event == "stop":
        line = f"{time_str}  stop"
    elif event == "session_end":
        reason = entry.get("reason", "")
        if reason:
            line = f"{time_str}  session_end  {reason}"
        else:
            line = f"{time_str}  session_end"
    elif event == "summary":
        line = f"{time_str}  summary  total_events={entry.get('total_events', '?')}"
    else:
        line = f"{time_str}  {event}"

    return line[: cols - 6] + "..." if len(line) > cols - 3 else line


def draw_actlog_view(
    stdscr,
    title,
    lines,
    scroll,
    summary,
    search_query="",
    search_active=False,
    search_cursor=0,
    match_line_indices=None,
    match_index=0,
):
    """Draw a full-screen scrollable activity log viewer."""
    stdscr.clear()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)
    match_attr = curses.color_pair(CP_MATCH)
    span_attr = curses.color_pair(CP_WARN) | curses.A_BOLD
    current_attr = curses.color_pair(CP_ATTENTION) | curses.A_BOLD

    if match_line_indices is None:
        match_line_indices = []
    match_set = set(match_line_indices)
    current_line_idx = match_line_indices[match_index] if match_line_indices else -1

    safe_addstr(stdscr, 0, 2, f"Activity Log: {title}", header_attr)

    if summary:
        total = summary.get("total", 0)
        stop_count = summary.get("stop_count", 0)
        tool_counts = summary.get("tool_counts", [])
        top_tools = "  ".join(f"{name}:{cnt}" for name, cnt in tool_counts[:5])
        stat_line = f"Events: {total}  Stops: {stop_count}"
        if top_tools:
            stat_line += f"  Top: {top_tools}"
        safe_addstr(stdscr, 1, 2, stat_line[: cols - 3], dim_attr)

        duration = summary.get("duration", "")
        if duration:
            safe_addstr(stdscr, 2, 2, f"Duration: {duration}", dim_attr)

    content_start = 3
    content_height = rows - content_start - 2

    if not lines:
        safe_addstr(stdscr, content_start, 2, "(no log entries)", dim_attr)
    else:
        visible = lines[scroll : scroll + content_height]
        for i, line in enumerate(visible):
            abs_idx = scroll + i
            _render_peek_line(
                stdscr,
                content_start + i,
                line,
                cols,
                search_query,
                abs_idx in match_set,
                abs_idx == current_line_idx,
                normal_attr,
                match_attr,
                span_attr,
                current_attr,
            )

    total = len(lines) if lines else 0
    if search_active:
        _draw_peek_search_bar(
            stdscr,
            search_query,
            search_cursor,
            len(match_line_indices),
        )
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

    draw_footer(stdscr, "actlog_search" if search_active else "actlog")


def draw_actlog_browse(stdscr, entries, selected, scroll):
    """Draw a tabular listing of all activity log files."""
    stdscr.clear()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)

    safe_addstr(stdscr, 0, 2, "Activity Logs — Browse All", header_attr)

    if not entries:
        safe_addstr(stdscr, 2, 4, "No activity log files found", dim_attr)
        draw_footer(stdscr, "actlog_browse")
        return

    id_w = 14
    mtime_w = 18
    count_w = 8
    size_w = 10
    fixed_w = id_w + mtime_w + count_w + 2 + size_w + 5
    title_w = max(20, cols - fixed_w)

    hdr = (
        "Session ID".ljust(id_w)
        + "Title".ljust(title_w)
        + "Modified".ljust(mtime_w)
        + "Events".rjust(count_w)
        + "  "
        + "Size".rjust(size_w)
    )
    header_y = 2
    safe_addstr(stdscr, header_y, 4, hdr[: cols - 5], dim_attr)

    content_height = rows - header_y - 3
    visible = entries[scroll : scroll + content_height]

    for i, entry in enumerate(visible):
        y = header_y + 1 + i
        if y >= rows - 2:
            break

        is_selected = (scroll + i) == selected
        attr = normal_attr | (curses.A_REVERSE if is_selected else 0)

        sid_short = entry["session_id"][: id_w - 2]
        title = (entry.get("title", "") or "ended")[: title_w - 2]
        mtime_str = time.strftime("%Y-%m-%d %H:%M", time.localtime(entry["mtime"]))
        count_str = str(entry["event_count"])

        sz = entry["size"]
        if sz < 1024:
            size_str = f"{sz} B"
        elif sz < 1024 * 1024:
            size_str = f"{sz / 1024:.1f} KB"
        else:
            size_str = f"{sz / (1024 * 1024):.1f} MB"

        row_text = (
            sid_short.ljust(id_w)
            + title.ljust(title_w)
            + mtime_str.ljust(mtime_w)
            + count_str.rjust(count_w)
            + "  "
            + size_str.rjust(size_w)
        )
        safe_addstr(stdscr, y, 4, _pad_to_width(row_text[: cols - 5], cols - 5), attr)

    total = len(entries)
    if total > content_height:
        pos = f"[{scroll + 1}-{min(scroll + content_height, total)}/{total}]"
        safe_addstr(stdscr, rows - 2, 2, pos, dim_attr)

    draw_footer(stdscr, "actlog_browse")


def draw_config_picker(stdscr, config_entries, selected):
    """Draw a centered config file picker overlay."""
    rows, cols = stdscr.getmaxyx()
    if not config_entries:
        return

    display_rows = []
    for label, path in config_entries:
        suffix = "" if os.path.isfile(path) else " (missing)"
        display_rows.append(f"{label}: {_shorten_home(path)}{suffix}")

    max_row_len = max(len(r) for r in display_rows)
    box_w = min(cols - 4, max(40, max_row_len + 10))
    box_h = len(config_entries) + 3
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)

    title = "Config Files"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, display in enumerate(display_rows):
        y = box_y + 2 + i
        if y >= rows - 1:
            break
        sel = i == selected
        marker = "▸ " if sel else "  "
        safe_addstr(
            stdscr,
            y,
            box_x + 2,
            f"{marker}{display}"[: box_w - 4],
            normal_attr | (curses.A_BOLD if sel else 0),
        )


_SORT_DESCRIPTIONS = {
    "default": "Default Kitty tab-bar order",
    "name": "Alphabetical by title",
    "account": "Grouped by account, alpha within",
    "status": "Error → permission → active → idle → stopped",
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
    """Draw a centered sort mode picker overlay."""
    rows, cols = stdscr.getmaxyx()
    label_col = max(len(v) for v in _SORT_DISPLAY_LABELS.values()) + 2

    display_rows = []
    for mode in SORT_MODES:
        check = "✓ " if mode == current_mode else "  "
        label = _SORT_DISPLAY_LABELS[mode].ljust(label_col)
        display_rows.append(f"{check}{label}{_SORT_DESCRIPTIONS[mode]}")

    max_row_len = max(len(r) for r in display_rows)
    box_w = min(cols - 4, max(40, max_row_len + 10))
    box_h = len(SORT_MODES) + 3
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)

    title = "Sort By"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, display in enumerate(display_rows):
        y = box_y + 2 + i
        if y >= rows - 1:
            break
        sel = i == selected
        marker = "▸ " if sel else "  "
        safe_addstr(
            stdscr,
            y,
            box_x + 2,
            f"{marker}{display}"[: box_w - 4],
            normal_attr | (curses.A_BOLD if sel else 0),
        )


def draw_config_action_picker(stdscr, label, path, selected):
    """Draw a centered action picker for a selected config file."""
    rows, cols = stdscr.getmaxyx()
    actions = ["Show contents", "Edit in editor", "Show file path"]
    subtitle = f"{label}: {_shorten_home(path)}"

    max_action_len = max(len(a) for a in actions)
    box_w = min(cols - 4, max(40, max(max_action_len, len(subtitle)) + 10))
    box_h = len(actions) + 4
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    dim_attr = curses.color_pair(CP_DIM)
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)

    title = f"Config: {label}"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )
    safe_addstr(
        stdscr,
        box_y + 1,
        box_x + max(0, (box_w - len(subtitle)) // 2),
        subtitle[: box_w - 2],
        dim_attr,
    )

    normal_attr = curses.color_pair(CP_NORMAL)
    for i, action in enumerate(actions):
        y = box_y + 3 + i
        if y >= rows - 1:
            break
        sel = i == selected
        marker = "▸ " if sel else "  "
        safe_addstr(
            stdscr,
            y,
            box_x + 2,
            f"{marker}{action}",
            normal_attr | (curses.A_BOLD if sel else 0),
        )


def draw_config_show(stdscr, label, path, lines, scroll):
    """Draw a full-screen scrollable file viewer for a config file."""
    stdscr.clear()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)

    safe_addstr(stdscr, 0, 2, f"{label}: {_shorten_home(path)}", header_attr)
    content_height = rows - 3

    if not lines or (len(lines) == 1 and lines[0] == "(file not found)"):
        display_lines = lines if lines else ["(file not found)"]
        for i, line in enumerate(display_lines[:content_height]):
            safe_addstr(stdscr, i + 1, 2, line[: cols - 3], dim_attr)
    else:
        for i, line in enumerate(lines[scroll : scroll + content_height]):
            safe_addstr(stdscr, i + 1, 2, line[: cols - 3], normal_attr)

    total = len(lines) if lines else 0
    if total > content_height:
        pos = f"[{scroll + 1}-{min(scroll + content_height, total)}/{total}]"
        safe_addstr(stdscr, rows - 2, 2, f"j/k to scroll  {pos}", dim_attr)

    safe_addstr(stdscr, rows - 1, 2, "q/Esc Close  R Reload config", dim_attr)


def draw_peek_overlay(
    stdscr,
    session_title,
    lines,
    scroll,
    extent,
    error="",
    search_query="",
    search_active=False,
    search_cursor=0,
    match_line_indices=None,
    match_index=0,
):
    """Draw a full-screen scrollable view of a session's terminal output."""
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
    safe_addstr(stdscr, 0, 2, f"Peek: {session_title} [{extent_label}]", header_attr)

    content_height = rows - 3
    if error:
        safe_addstr(stdscr, 2, 2, error, warn_attr)
    elif not lines or (len(lines) == 1 and not lines[0].strip()):
        safe_addstr(stdscr, 2, 2, "(empty output)", dim_attr)
    else:
        for i, line in enumerate(lines[scroll : scroll + content_height]):
            abs_idx = scroll + i
            _render_peek_line(
                stdscr,
                i + 1,
                line,
                cols,
                search_query,
                abs_idx in match_set,
                abs_idx == current_line_idx,
                normal_attr,
                match_attr,
                span_attr,
                current_attr,
            )

    total = len(lines) if lines else 0
    if search_active:
        _draw_peek_search_bar(
            stdscr,
            search_query,
            search_cursor,
            len(match_line_indices),
        )
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


_DRIFT_BADGES = {
    "in_sync": ("synced", CP_DIM),
    "drifted": ("drifted", CP_WARN),
    "missing": ("missing", CP_WARN),
}

type HooksEventRow = (
    tuple[str, str, bool]                            # event row
    | tuple[str, str, int, str, dict, str | None]    # extra row (with handler + matcher)
    | tuple[str, str, int, str, bool, bool, str]     # builtin row
)


_BUILTIN_DESCRIPTIONS = {
    "cc-hook-indicator active prompt": "Sets tab icon to active (prompt)",
    "cc-hook-indicator active tool": "Sets tab icon to active (tool)",
    "cc-hook-indicator active task_complete": "Sets tab icon to active (task done)",
    "cc-hook-indicator active": "Sets tab icon to active",
    "cc-hook-indicator stop": "Sets tab stop icon",
    "cc-hook-indicator permission": "Sets tab permission icon",
    "cc-hook-indicator failure": "Sets tab error icon",
    "cc-activity-log tool": "Logs tool use to activity file",
    "cc-activity-log stop": "Logs stop to activity file",
    "cc-activity-log end": "Logs session end to activity file",
}


def _describe_builtin(cmd):
    """Return a human-readable description for a known builtin command."""
    for prefix, desc in _BUILTIN_DESCRIPTIONS.items():
        if cmd.startswith(prefix):
            return desc
    return ""


def _extract_builtin_commands(hooks_global, event_name):
    """Extract command strings with metadata from hooks_global for a given event.

    Returns a list of (command_str, is_async, type_str) tuples.
    """
    commands = []
    for entry in hooks_global.get(event_name, []):
        is_async = entry.get("async", False)
        type_str = entry.get("type", "")
        for hook in entry.get("hooks", []):
            cmd = hook.get("command", "")
            if cmd:
                # Strip absolute path prefixes for cleaner display
                parts = cmd.split()
                if parts:
                    parts[0] = parts[0].rsplit("/", 1)[-1]
                    commands.append((" ".join(parts), is_async, type_str))
    return commands


def _hooks_event_rows(account_name, hooks_overrides, hooks_global=None):
    """Build a flat list of rows for the hooks event detail view."""
    from cc_dashboard.config import HOOK_EVENTS, _get_extra_hooks

    override = hooks_overrides.get(account_name, {})
    disabled_events = override.get("disabled_events", [])
    extra_hooks = _get_extra_hooks(override)

    result: list[HooksEventRow] = []
    for event in HOOK_EVENTS:
        is_disabled = event in disabled_events
        result.append(("event", event, is_disabled))
        if hooks_global is not None:
            for i, (cmd, is_async, type_str) in enumerate(
                _extract_builtin_commands(hooks_global, event)
            ):
                result.append(
                    ("builtin", event, i, cmd, is_disabled, is_async, type_str)
                )
        for i, group in enumerate(extra_hooks.get(event, [])):
            hooks_list = group.get("hooks", [])
            handler = hooks_list[0] if hooks_list else {}
            matcher = group.get("matcher")
            hook_type = handler.get("type", "command")
            if hook_type == "command":
                cmd_str = str(handler.get("command", ""))
            elif hook_type == "http":
                cmd_str = str(handler.get("url", ""))
            else:
                cmd_str = str(handler.get("prompt", ""))
            result.append(("extra", event, i, cmd_str, handler, matcher))
    return result


def draw_hooks_view(
    stdscr, accounts, drift_status, hooks_global, hooks_overrides, selected
):
    """Draw the hooks overview showing all accounts with status."""
    stdscr.clear()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)

    safe_addstr(stdscr, 0, 2, "Hooks Manager", header_attr)

    if not accounts:
        safe_addstr(stdscr, 2, 4, "No accounts configured", dim_attr)
        draw_footer(stdscr, "hooks")
        return

    account_list = list(accounts)
    from cc_dashboard.config import HOOK_EVENTS, HOOK_EVENT_META, _get_extra_hooks

    # Section A: Account summary rows
    col_start = 4
    name_w = 18
    counts_x = col_start + name_w
    custom_x = counts_x + 12
    status_x = custom_x + 12

    safe_addstr(stdscr, 2, col_start, "Account".ljust(name_w), dim_attr)
    safe_addstr(stdscr, 2, counts_x, "Status".ljust(12), dim_attr)
    safe_addstr(stdscr, 2, custom_x, "Hooks".ljust(12), dim_attr)
    if status_x + 8 <= cols:
        safe_addstr(stdscr, 2, status_x, "Sync", dim_attr)

    for idx, acct_name in enumerate(account_list):
        y = 3 + idx
        if y >= rows - 2:
            break

        is_selected = idx == selected
        override = hooks_overrides.get(acct_name, {})
        disabled_events = override.get("disabled_events", [])
        extra_hooks = _get_extra_hooks(override)

        enabled_count = sum(
            1 for ev in HOOK_EVENTS if ev not in disabled_events
        )
        extra_count = sum(len(groups) for groups in extra_hooks.values())

        # Selection marker
        marker = "\u25b8 " if is_selected else "  "
        name_attr = curses.color_pair(_cp_for_account(acct_name))
        if is_selected:
            name_attr |= curses.A_REVERSE
        display_name = marker + acct_name
        safe_addstr(
            stdscr, y, col_start,
            display_name[: name_w - 1].ljust(name_w),
            name_attr,
        )

        # Enabled count
        count_str = f"{enabled_count}/{len(HOOK_EVENTS)} on"
        count_attr = normal_attr if enabled_count == len(HOOK_EVENTS) else dim_attr
        if is_selected:
            count_attr |= curses.A_REVERSE
        safe_addstr(stdscr, y, counts_x, count_str.ljust(12), count_attr)

        # Custom hooks count
        custom_str = f"{extra_count} custom"
        custom_attr = normal_attr if extra_count > 0 else dim_attr
        if is_selected:
            custom_attr |= curses.A_REVERSE
        safe_addstr(stdscr, y, custom_x, custom_str.ljust(12), custom_attr)

        # Drift badge
        if status_x + 8 <= cols:
            drift = drift_status.get(acct_name, "missing")
            badge_text, badge_cp = _DRIFT_BADGES.get(drift, ("?", CP_DIM))
            badge_attr = curses.color_pair(badge_cp)
            if is_selected:
                badge_attr |= curses.A_REVERSE
            safe_addstr(stdscr, y, status_x, f"[{badge_text}]", badge_attr)

    # Section B: Event reference (if space permits)
    ref_start_y = 3 + len(account_list) + 1
    # Need at least a header row + a few events to be useful
    if ref_start_y + 2 < rows - 2:
        safe_addstr(stdscr, ref_start_y, col_start, "Hook Events:", dim_attr)
        event_name_w = 22
        for i, event in enumerate(HOOK_EVENTS):
            ey = ref_start_y + 1 + i
            if ey >= rows - 2:
                break
            meta = HOOK_EVENT_META.get(event, {})
            desc = meta.get("desc", "")
            safe_addstr(stdscr, ey, col_start, event.ljust(event_name_w), dim_attr)
            desc_x = col_start + event_name_w
            max_desc = cols - desc_x - 1
            if max_desc > 0:
                safe_addstr(stdscr, ey, desc_x, desc[:max_desc], dim_attr)

    draw_footer(stdscr, "hooks")


def draw_hooks_event_view(
    stdscr, account_name, hooks_global, hooks_overrides, drift_status, selected
):
    """Draw event detail for a single account's hooks."""
    stdscr.clear()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)

    title = f"Hooks: {account_name}"
    safe_addstr(stdscr, 0, 2, title, header_attr)

    drift = drift_status if isinstance(drift_status, str) else "missing"
    badge_text, badge_cp = _DRIFT_BADGES.get(drift, ("?", CP_DIM))
    safe_addstr(
        stdscr,
        0,
        2 + len(title) + 2,
        f"[{badge_text}]",
        curses.color_pair(badge_cp),
    )

    event_rows = _hooks_event_rows(account_name, hooks_overrides, hooks_global)
    for idx, row in enumerate(event_rows):
        y = 2 + idx
        if y >= rows - 2:
            break

        is_selected = idx == selected
        if row[0] == "event":
            _, event_name, is_disabled = row
            marker = "▸ " if is_selected else "  "
            status_attr = dim_attr if is_disabled else normal_attr
            if is_selected:
                status_attr |= curses.A_REVERSE

            safe_addstr(stdscr, y, 2, marker, normal_attr)
            safe_addstr(stdscr, y, 4, event_name.ljust(22), status_attr)
            badge = " off" if is_disabled else " on"
            badge_a = dim_attr if is_disabled else curses.color_pair(CP_ATTENTION)
            if is_selected:
                badge_a |= curses.A_REVERSE
            safe_addstr(stdscr, y, 26, badge, badge_a)
        elif row[0] == "builtin":
            _, _event_name, _cmd_idx, cmd_str, is_disabled, is_async, _type_str = row
            marker = "▸ " if is_selected else "  "
            attr = dim_attr if is_disabled else normal_attr
            if is_selected:
                attr |= curses.A_REVERSE
            safe_addstr(stdscr, y, 2, marker, normal_attr)
            tag = " async" if is_async else ""
            desc = _describe_builtin(cmd_str)
            suffix = f"{tag} | {desc}" if desc else tag
            display = f"    - {cmd_str}{suffix}"
            safe_addstr(stdscr, y, 4, display[: (cols - 6)], attr)
        else:
            _, _event_name, _cmd_idx, cmd_str, handler, matcher = row
            marker = "▸ " if is_selected else "  "
            attr = dim_attr | (curses.A_REVERSE if is_selected else 0)
            safe_addstr(stdscr, y, 2, marker, normal_attr)

            # Build rich display: [type] [matcher] value  props
            parts = []
            hook_type = handler.get("type", "command")
            if hook_type != "command":
                parts.append(f"[{hook_type}]")
            if matcher:
                parts.append(f"[{matcher}]")
            parts.append(cmd_str)

            # Property tags
            props = []
            if hook_type == "command" and handler.get("async"):
                props.append("async")
            timeout = handler.get("timeout")
            if timeout is not None:
                props.append(f"timeout:{timeout}")
            if props:
                parts.append(" ".join(props))

            display = "    + " + " ".join(parts)
            safe_addstr(stdscr, y, 4, display[: (cols - 6)], attr)

    draw_footer(stdscr, "hooks_event")


def draw_hooks_delete_confirm(stdscr, hook_label):
    """Draw an inline delete confirmation prompt at the bottom of the screen."""
    rows, _cols = stdscr.getmaxyx()
    attr = curses.color_pair(CP_WARN) | curses.A_BOLD
    dim_attr = curses.color_pair(CP_DIM)

    safe_addstr(stdscr, rows - 5, 2, f"Delete hook: {hook_label}?", attr)
    safe_addstr(
        stdscr,
        rows - 4,
        2,
        "This removes the custom hook from hooks.json.",
        dim_attr,
    )
    safe_addstr(stdscr, rows - 3, 2, "y/n", attr)


def draw_hooks_sync_confirm(stdscr, scope_label):
    """Draw a centered sync confirmation overlay."""
    rows, _cols = stdscr.getmaxyx()
    attr = curses.color_pair(CP_WARN) | curses.A_BOLD
    dim_attr = curses.color_pair(CP_DIM)

    safe_addstr(stdscr, rows - 5, 2, f"Sync hooks for {scope_label}?", attr)
    safe_addstr(
        stdscr,
        rows - 4,
        2,
        "settings.json will be backed up. User-defined entries preserved.",
        dim_attr,
    )
    safe_addstr(stdscr, rows - 3, 2, "y/n", attr)


# Hook type options and their value labels
_HOOK_TYPES = ["command", "http", "prompt", "agent"]
_HOOK_TYPE_LABELS = {
    "command": "Command",
    "http": "URL",
    "prompt": "Prompt",
    "agent": "Prompt",
}
_HOOK_TYPE_DEFAULTS_TIMEOUT = {
    "command": 600,
    "http": 30,
    "prompt": 30,
    "agent": 60,
}

# Field indices for the add/edit form
_FIELD_TYPE = 0
_FIELD_MATCHER = 1
_FIELD_VALUE = 2
_FIELD_ASYNC = 3
_FIELD_TIMEOUT = 4


def _draw_text_field(stdscr, y, x, value, cursor_pos, active, cols):
    """Draw a text input field with cursor rendering."""
    max_w = max(0, cols - x - 2)
    display = value[:max_w]
    input_attr = curses.color_pair(CP_INPUT_ACTIVE)
    dim_attr = curses.color_pair(CP_DIM)

    if active:
        before = display[:cursor_pos]
        after = display[cursor_pos:]
        cursor_ch = after[0] if after else " "
        after_rest = after[1:] if len(after) > 1 else ""
        safe_addstr(stdscr, y, x, before, input_attr)
        safe_addstr(stdscr, y, x + len(before), cursor_ch, input_attr | curses.A_REVERSE)
        if after_rest:
            safe_addstr(stdscr, y, x + len(before) + 1, after_rest, input_attr)
    else:
        safe_addstr(stdscr, y, x, display if display else "(empty)", dim_attr)


def draw_hooks_add_form(stdscr, account_name, event_name, form_state, editing=False):
    """Draw a multi-field form for adding or editing a hook.

    form_state is a dict with keys: type, matcher, value, async_, timeout,
    field (active field index), cursor (cursor pos in active text field).
    """
    rows, cols = stdscr.getmaxyx()
    dim_attr = curses.color_pair(CP_DIM)
    warn_attr = curses.color_pair(CP_WARN)
    input_attr = curses.color_pair(CP_INPUT_ACTIVE)

    from cc_dashboard.config import HOOK_EVENT_META, _FULL_TYPE_EVENTS

    meta = HOOK_EVENT_META.get(event_name, {})
    has_matcher = meta.get("matcher_desc") is not None
    supports_all_types = event_name in _FULL_TYPE_EVENTS

    hook_type = form_state.get("type", "command")
    matcher_val = form_state.get("matcher", "")
    value_val = form_state.get("value", "")
    is_async = form_state.get("async_", True)
    timeout_val = form_state.get("timeout", "")
    active_field = form_state.get("field", _FIELD_VALUE)
    cursor = form_state.get("cursor", 0)

    # Build visible fields list (index, label, render_fn)
    # We always show type, value, timeout. Matcher shown conditionally.
    # Async shown only for command type.
    action = "Edit" if editing else "Add"
    header = f"{action} hook for {event_name} ({account_name})"

    # Calculate form Y position: bottom of screen
    form_lines = 7
    base_y = rows - form_lines - 1
    if base_y < 2:
        base_y = 2

    # Clear form area
    for dy in range(form_lines + 1):
        safe_addstr(stdscr, base_y + dy, 0, " " * (cols - 1), dim_attr)

    safe_addstr(stdscr, base_y, 2, header, warn_attr)

    label_w = 10
    field_x = 2 + label_w
    y = base_y + 1

    # Type field
    is_active = active_field == _FIELD_TYPE
    type_label = "Type:"
    safe_addstr(stdscr, y, 2, type_label.ljust(label_w), warn_attr if is_active else dim_attr)
    if supports_all_types:
        for i, t in enumerate(_HOOK_TYPES):
            marker = "[*]" if t == hook_type else "[ ]"
            attr = input_attr | curses.A_BOLD if (is_active and t == hook_type) else dim_attr
            safe_addstr(stdscr, y, field_x + i * 12, f"{marker} {t}", attr)
    else:
        safe_addstr(stdscr, y, field_x, "command (only type for this event)", dim_attr)
    y += 1

    # Matcher field (optional)
    if has_matcher:
        is_active = active_field == _FIELD_MATCHER
        matcher_label = "Matcher:"
        safe_addstr(stdscr, y, 2, matcher_label.ljust(label_w), warn_attr if is_active else dim_attr)
        _draw_text_field(stdscr, y, field_x, matcher_val, cursor if is_active else 0, is_active, cols)
        hint = meta.get("matcher_examples", "")
        if hint and not matcher_val and not is_active:
            safe_addstr(stdscr, y, field_x, "Tab: pick from examples", dim_attr)
        y += 1

    # Value field
    is_active = active_field == _FIELD_VALUE
    value_label = f"{_HOOK_TYPE_LABELS.get(hook_type, 'Value')}:"
    safe_addstr(stdscr, y, 2, value_label.ljust(label_w), warn_attr if is_active else dim_attr)
    _draw_text_field(stdscr, y, field_x, value_val, cursor if is_active else 0, is_active, cols)
    y += 1

    # Async field (command type only)
    if hook_type == "command":
        is_active = active_field == _FIELD_ASYNC
        async_label = "Async:"
        safe_addstr(stdscr, y, 2, async_label.ljust(label_w), warn_attr if is_active else dim_attr)
        marker = "[*]" if is_async else "[ ]"
        attr = input_attr | curses.A_BOLD if is_active else dim_attr
        safe_addstr(stdscr, y, field_x, f"{marker} yes   ", attr)
        marker2 = "[ ]" if is_async else "[*]"
        safe_addstr(stdscr, y, field_x + 12, f"{marker2} no", attr)
        y += 1

    # Timeout field
    is_active = active_field == _FIELD_TIMEOUT
    timeout_label = "Timeout:"
    safe_addstr(stdscr, y, 2, timeout_label.ljust(label_w), warn_attr if is_active else dim_attr)
    default_t = _HOOK_TYPE_DEFAULTS_TIMEOUT.get(hook_type, 30)
    _draw_text_field(stdscr, y, field_x, timeout_val, cursor if is_active else 0, is_active, cols)
    if not timeout_val and not is_active:
        safe_addstr(stdscr, y, field_x, f"(default: {default_t}s)", dim_attr)
    y += 1

    # Navigation hint
    safe_addstr(stdscr, y, 2, "Tab next  Shift-Tab prev  Enter submit  Esc cancel", dim_attr)


def draw_hooks_matcher_picker(stdscr, options, selected, scroll):
    """Draw a dropdown picker for matcher examples, anchored above the matcher field."""
    rows, cols = stdscr.getmaxyx()
    dim_attr = curses.color_pair(CP_DIM)
    input_attr = curses.color_pair(CP_INPUT_ACTIVE)
    normal_attr = curses.color_pair(CP_NORMAL)

    if not options:
        return

    max_opt_len = max(len(o) for o in options)
    box_w = min(cols - 16, max(24, max_opt_len + 6))

    # Anchor: the matcher field is at base_y+2 in the form, field_x=12
    # The form base_y is rows - form_lines - 1 where form_lines=7
    form_lines = 7
    base_y = rows - form_lines - 1
    if base_y < 2:
        base_y = 2
    # matcher row is base_y + 2 (after header and type row)
    anchor_y = base_y + 2
    field_x = 12

    # Draw above the matcher field, using all available space (row 1 to anchor_y-1)
    space_above = anchor_y - 1  # rows 1 .. anchor_y-1
    max_visible = min(len(options), max(1, space_above))
    box_y = anchor_y - max_visible
    if box_y < 1:
        box_y = 1
        max_visible = anchor_y - 1
    box_x = field_x

    # Draw the dropdown box
    visible_options = options[scroll : scroll + max_visible]
    for i, opt in enumerate(visible_options):
        y = box_y + i
        if y >= anchor_y:
            break
        is_sel = (scroll + i) == selected
        text = f" \u25b8 {opt}" if is_sel else f"   {opt}"
        attr = input_attr | curses.A_BOLD if is_sel else normal_attr
        padded = text[:box_w].ljust(box_w)
        safe_addstr(stdscr, y, box_x, padded, attr)

    # Scroll indicator at top if scrolled down, or at bottom if more items below
    if len(options) > max_visible:
        indicator = f" [{scroll + 1}-{scroll + len(visible_options)}/{len(options)}]"
        ind_y = box_y - 1 if box_y > 1 else box_y + len(visible_options)
        if 0 <= ind_y < rows - 1:
            safe_addstr(stdscr, ind_y, box_x, indicator[:box_w], dim_attr)


def draw_hooks_info(stdscr, handler, matcher, event_name):
    """Draw a centered popup showing all properties of a hook."""
    rows, cols = stdscr.getmaxyx()
    dim_attr = curses.color_pair(CP_DIM)
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)

    hook_type = handler.get("type", "command")

    # Build property lines
    lines = []
    lines.append(("Event", event_name))
    lines.append(("Type", hook_type))
    if matcher:
        lines.append(("Matcher", matcher))
    if hook_type == "command":
        lines.append(("Command", handler.get("command", "")))
        lines.append(("Async", "yes" if handler.get("async") else "no"))
    elif hook_type == "http":
        lines.append(("URL", handler.get("url", "")))
    else:
        lines.append(("Prompt", handler.get("prompt", "")))
    timeout = handler.get("timeout")
    if timeout is not None:
        lines.append(("Timeout", f"{timeout}s"))
    status_msg = handler.get("statusMessage")
    if status_msg:
        lines.append(("Status msg", status_msg))

    # Calculate box size
    max_label = max(len(k) for k, _ in lines)
    max_val = max(len(str(v)) for _, v in lines)
    content_w = max_label + 2 + max_val
    box_w = min(cols - 4, max(40, content_w + 6))
    box_h = len(lines) + 4
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)

    title = "Hook Details"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        header_attr,
    )

    desc = _describe_builtin(handler.get("command", ""))
    if desc:
        safe_addstr(
            stdscr,
            box_y + 1,
            box_x + max(0, (box_w - len(desc)) // 2),
            desc,
            dim_attr,
        )

    for i, (label, value) in enumerate(lines):
        y = box_y + 2 + i
        if y >= rows - 1:
            break
        text = f"{label + ':':<{max_label + 2}}{value}"
        safe_addstr(stdscr, y, box_x + 3, text[: box_w - 6], normal_attr)

    safe_addstr(stdscr, box_y + box_h - 1, box_x + 3, "Esc/q to close", dim_attr)
