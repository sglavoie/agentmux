"""Hooks overlay confirmation dialogs and popups."""

import curses

from cc_dashboard.colors import CP_BORDER, CP_DIM, CP_HEADER, CP_NORMAL, CP_WARN
from cc_dashboard.drawing import draw_border, safe_addstr
from cc_dashboard.views_overlays_hooks_shared import _describe_builtin


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


def draw_hooks_info(stdscr, handler, matcher, event_name):
    """Draw a centered popup showing all properties of a hook."""
    rows, cols = stdscr.getmaxyx()
    dim_attr = curses.color_pair(CP_DIM)
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)

    hook_type = handler.get("type", "command")
    lines = [("Event", event_name), ("Type", hook_type)]
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

    max_label = max(len(label) for label, _ in lines)
    max_value = max(len(str(value)) for _, value in lines)
    box_w = min(cols - 4, max(40, max_label + 2 + max_value + 6))
    box_h = len(lines) + 4
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)
    draw_border(
        stdscr, box_y, box_x, box_w, box_h, curses.color_pair(CP_BORDER) | curses.A_BOLD
    )

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


def draw_hooks_example(stdscr, event_name):
    """Draw a centered popup showing a practical hook example for an event."""
    from cc_dashboard.config import HOOK_EVENT_EXAMPLES, HOOK_EVENT_META

    rows, cols = stdscr.getmaxyx()
    dim_attr = curses.color_pair(CP_DIM)
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)

    meta = HOOK_EVENT_META.get(event_name, {})
    example = HOOK_EVENT_EXAMPLES.get(event_name, {})

    lines = [("Event", event_name)]
    desc = meta.get("desc", "")
    if desc:
        lines.append(("Description", desc))
    lines.append(("Type", example.get("type", "command")))
    if "command" in example:
        lines.append(("Command", example["command"]))
    if "matcher" in example:
        lines.append(("Matcher", example["matcher"]))
    lines.append(("Async", "yes" if example.get("async") else "no"))
    if "timeout" in example:
        lines.append(("Timeout", f"{example['timeout']}s"))

    matcher_desc = meta.get("matcher_desc")
    if matcher_desc:
        examples_str = meta.get("matcher_examples", "")
        lines.append(("", ""))
        lines.append(("Matcher matches", matcher_desc))
        if examples_str:
            lines.append(("Valid matchers", examples_str))

    max_label = max(len(label) for label, _ in lines)
    max_value = max(len(str(value)) for _, value in lines)
    box_w = min(cols - 4, max(44, max_label + 2 + max_value + 6))
    box_h = len(lines) + 4
    box_y = max(0, (rows - box_h) // 2)
    box_x = max(0, (cols - box_w) // 2)

    blank_line = " " * box_w
    for dy in range(box_h):
        safe_addstr(stdscr, box_y + dy, box_x, blank_line, dim_attr)
    draw_border(
        stdscr, box_y, box_x, box_w, box_h, curses.color_pair(CP_BORDER) | curses.A_BOLD
    )

    title = f"Example: {event_name}"
    safe_addstr(
        stdscr,
        box_y,
        box_x + max(0, (box_w - len(title)) // 2),
        title,
        header_attr,
    )

    for i, (label, value) in enumerate(lines):
        y = box_y + 2 + i
        if y >= rows - 1:
            break
        if not label and not value:
            continue
        text = f"{label + ':':<{max_label + 2}}{value}"
        safe_addstr(stdscr, y, box_x + 3, text[: box_w - 6], normal_attr)

    safe_addstr(stdscr, box_y + box_h - 1, box_x + 3, "Esc/q to close", dim_attr)


__all__ = [
    "draw_hooks_delete_confirm",
    "draw_hooks_example",
    "draw_hooks_info",
    "draw_hooks_sync_confirm",
]
