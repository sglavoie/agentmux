"""Hooks overlay add/edit form views."""

import curses

from cc_dashboard.colors import CP_DIM, CP_INPUT_ACTIVE, CP_NORMAL, CP_WARN
from cc_dashboard.drawing import safe_addstr

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
        safe_addstr(
            stdscr, y, x + len(before), cursor_ch, input_attr | curses.A_REVERSE
        )
        if after_rest:
            safe_addstr(stdscr, y, x + len(before) + 1, after_rest, input_attr)
        return

    safe_addstr(stdscr, y, x, display if display else "(empty)", dim_attr)


def draw_hooks_add_form(stdscr, account_name, event_name, form_state, editing=False):
    """Draw a multi-field form for adding or editing a hook."""
    rows, cols = stdscr.getmaxyx()
    dim_attr = curses.color_pair(CP_DIM)
    warn_attr = curses.color_pair(CP_WARN)
    input_attr = curses.color_pair(CP_INPUT_ACTIVE)

    from cc_dashboard.config import _FULL_TYPE_EVENTS, HOOK_EVENT_META

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

    action = "Edit" if editing else "Add"
    header = f"{action} hook for {event_name} ({account_name})"

    form_lines = 7
    base_y = rows - form_lines - 1
    if base_y < 2:
        base_y = 2

    for dy in range(form_lines + 1):
        safe_addstr(stdscr, base_y + dy, 0, " " * (cols - 1), dim_attr)

    safe_addstr(stdscr, base_y, 2, header, warn_attr)

    label_w = 10
    field_x = 2 + label_w
    y = base_y + 1

    is_active = active_field == _FIELD_TYPE
    safe_addstr(
        stdscr, y, 2, "Type:".ljust(label_w), warn_attr if is_active else dim_attr
    )
    if supports_all_types:
        for i, hook_type_name in enumerate(_HOOK_TYPES):
            marker = "[*]" if hook_type_name == hook_type else "[ ]"
            attr = (
                input_attr | curses.A_BOLD
                if (is_active and hook_type_name == hook_type)
                else dim_attr
            )
            safe_addstr(stdscr, y, field_x + i * 12, f"{marker} {hook_type_name}", attr)
    else:
        safe_addstr(stdscr, y, field_x, "command (only type for this event)", dim_attr)
    y += 1

    if has_matcher:
        is_active = active_field == _FIELD_MATCHER
        safe_addstr(
            stdscr,
            y,
            2,
            "Matcher:".ljust(label_w),
            warn_attr if is_active else dim_attr,
        )
        _draw_text_field(
            stdscr, y, field_x, matcher_val, cursor if is_active else 0, is_active, cols
        )
        hint = meta.get("matcher_examples", "")
        if hint and not matcher_val and not is_active:
            safe_addstr(stdscr, y, field_x, "Tab: pick from examples", dim_attr)
        y += 1

    is_active = active_field == _FIELD_VALUE
    value_label = f"{_HOOK_TYPE_LABELS.get(hook_type, 'Value')}:"
    safe_addstr(
        stdscr, y, 2, value_label.ljust(label_w), warn_attr if is_active else dim_attr
    )
    _draw_text_field(
        stdscr, y, field_x, value_val, cursor if is_active else 0, is_active, cols
    )
    y += 1

    if hook_type == "command":
        is_active = active_field == _FIELD_ASYNC
        safe_addstr(
            stdscr,
            y,
            2,
            "Async:".ljust(label_w),
            warn_attr if is_active else dim_attr,
        )
        marker = "[*]" if is_async else "[ ]"
        attr = input_attr | curses.A_BOLD if is_active else dim_attr
        safe_addstr(stdscr, y, field_x, f"{marker} yes   ", attr)
        marker2 = "[ ]" if is_async else "[*]"
        safe_addstr(stdscr, y, field_x + 12, f"{marker2} no", attr)
        y += 1

    is_active = active_field == _FIELD_TIMEOUT
    safe_addstr(
        stdscr,
        y,
        2,
        "Timeout:".ljust(label_w),
        warn_attr if is_active else dim_attr,
    )
    default_timeout = _HOOK_TYPE_DEFAULTS_TIMEOUT.get(hook_type, 30)
    _draw_text_field(
        stdscr, y, field_x, timeout_val, cursor if is_active else 0, is_active, cols
    )
    if not timeout_val and not is_active:
        safe_addstr(stdscr, y, field_x, f"(default: {default_timeout}s)", dim_attr)
    y += 1

    safe_addstr(
        stdscr, y, 2, "Tab next  Shift-Tab prev  Enter submit  Esc cancel", dim_attr
    )


def draw_hooks_matcher_picker(stdscr, options, selected, scroll):
    """Draw a dropdown picker for matcher examples."""
    rows, cols = stdscr.getmaxyx()
    dim_attr = curses.color_pair(CP_DIM)
    input_attr = curses.color_pair(CP_INPUT_ACTIVE)
    normal_attr = curses.color_pair(CP_NORMAL)

    if not options:
        return

    max_opt_len = max(len(option) for option in options)
    box_w = min(cols - 16, max(24, max_opt_len + 6))

    form_lines = 7
    base_y = rows - form_lines - 1
    if base_y < 2:
        base_y = 2
    anchor_y = base_y + 2
    field_x = 12

    space_above = anchor_y - 1
    max_visible = min(len(options), max(1, space_above))
    box_y = anchor_y - max_visible
    if box_y < 1:
        box_y = 1
        max_visible = anchor_y - 1
    box_x = field_x

    visible_options = options[scroll : scroll + max_visible]
    for i, option in enumerate(visible_options):
        y = box_y + i
        if y >= anchor_y:
            break
        is_selected = (scroll + i) == selected
        text = f" > {option}" if is_selected else f"   {option}"
        attr = input_attr | curses.A_BOLD if is_selected else normal_attr
        safe_addstr(stdscr, y, box_x, text[:box_w].ljust(box_w), attr)

    if len(options) > max_visible:
        indicator = f" [{scroll + 1}-{scroll + len(visible_options)}/{len(options)}]"
        indicator_y = box_y - 1 if box_y > 1 else box_y + len(visible_options)
        if 0 <= indicator_y < rows - 1:
            safe_addstr(stdscr, indicator_y, box_x, indicator[:box_w], dim_attr)


__all__ = [
    "_FIELD_ASYNC",
    "_FIELD_MATCHER",
    "_FIELD_TIMEOUT",
    "_FIELD_TYPE",
    "_FIELD_VALUE",
    "_HOOK_TYPES",
    "draw_hooks_add_form",
    "draw_hooks_matcher_picker",
]
