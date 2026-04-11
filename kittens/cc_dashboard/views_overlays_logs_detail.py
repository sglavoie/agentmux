"""Activity log entry detail overlay."""

from __future__ import annotations

import curses
import time
from typing import Any

from cc_dashboard.colors import CP_BORDER, CP_DIM, CP_HEADER, CP_NORMAL
from cc_dashboard.drawing import draw_border, safe_addstr


def draw_actlog_detail(stdscr: curses.window, entry: dict[str, Any]) -> None:
    """Draw a centered popup showing all fields of an activity log entry."""
    if not entry:
        return
    rows, cols = stdscr.getmaxyx()
    dim_attr = curses.color_pair(CP_DIM)
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    border_attr = curses.color_pair(CP_BORDER) | curses.A_BOLD

    ts = entry.get("ts")
    event = entry.get("event", "?")
    tool = entry.get("tool", "")
    ctx = entry.get("ctx", {})

    pairs = []
    pairs.append(
        ("Time", time.strftime("%H:%M:%S", time.localtime(ts)) if ts else "??:??:??")
    )
    pairs.append(("Event", event))
    if event == "tool":
        pairs.append(("Tool", tool))
        if tool in ("Read", "Write", "Edit"):
            pairs.append(("File", ctx.get("file_path", "")))
        elif tool == "Bash":
            pairs.append(("Command", ctx.get("cmd", "")))
        elif tool in ("Grep", "Glob"):
            pairs.append(("Pattern", ctx.get("pattern", "")))
        elif tool == "WebFetch":
            pairs.append(("URL", ctx.get("url", "")))
        elif tool == "WebSearch":
            pairs.append(("Query", ctx.get("query", "")))
        elif tool == "Task":
            pairs.append(("Desc", ctx.get("desc", "")))
            agent = ctx.get("agent", "")
            if agent:
                pairs.append(("Agent", agent))
        else:
            keys = ctx.get("keys", [])
            if keys:
                pairs.append(("Keys", ", ".join(keys)))
    else:
        reason = entry.get("reason", "")
        if reason:
            pairs.append(("Reason", reason))
    cwd = entry.get("cwd", "")
    if cwd:
        pairs.append(("CWD", cwd))

    max_label = max(len(label) for label, _ in pairs)
    label_col = max_label + 2
    max_raw_value = max((len(str(v)) for _, v in pairs), default=0)
    box_w = min(cols - 4, max(60, label_col + max_raw_value + 6))
    wrap_w = max(1, box_w - label_col - 6)

    display_rows = []
    for label, value in pairs:
        value_str = str(value)
        if not value_str:
            display_rows.append((label, ""))
            continue
        chunks = [value_str[i : i + wrap_w] for i in range(0, len(value_str), wrap_w)]
        display_rows.append((label, chunks[0]))
        for chunk in chunks[1:]:
            display_rows.append(("", chunk))

    box_h = len(display_rows) + 4
    box_y = max(1, (rows - box_h) // 2)
    box_x = max(1, (cols - box_w) // 2)

    blank_line = " " * box_w
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)
    draw_border(stdscr, box_y, box_x, box_w, box_h, border_attr)

    popup_title = "Entry Detail"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(popup_title)) // 2),
        popup_title,
        header_attr,
    )

    for i, (label, chunk) in enumerate(display_rows):
        y = box_y + 2 + i
        if y >= rows - 1:
            break
        if label:
            text = f"{label + ':':<{label_col}}{chunk}"
        else:
            text = " " * label_col + chunk
        safe_addstr(stdscr, y, box_x + 3, text[: box_w - 6], normal_attr)

    safe_addstr(stdscr, box_y + box_h - 1, box_x + 3, "Esc/q to close", dim_attr)
