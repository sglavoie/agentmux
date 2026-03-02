"""Hooks management overlay views and helpers."""

import curses

from cc_dashboard.colors import (
    CP_ATTENTION,
    CP_DIM,
    CP_HEADER,
    CP_INPUT_ACTIVE,
    CP_NORMAL,
    CP_WARN,
    _cp_for_account,
)
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.views_shared import draw_footer

_DRIFT_BADGES = {
    "in_sync": ("synced", CP_DIM),
    "drifted": ("drifted", CP_WARN),
    "missing": ("missing", CP_WARN),
}

type HooksEventRow = (
    tuple[str, str, bool]  # event row
    | tuple[str, str, int, str, dict, str | None]  # extra row (with handler + matcher)
    | tuple[str, str, int, str, bool, bool, str]  # builtin row
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
    from cc_dashboard.config import HOOK_EVENT_META, HOOK_EVENTS, _get_extra_hooks

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

        enabled_count = sum(1 for ev in HOOK_EVENTS if ev not in disabled_events)
        extra_count = sum(len(groups) for groups in extra_hooks.values())

        marker = "▸ " if is_selected else "  "
        name_attr = curses.color_pair(_cp_for_account(acct_name))
        if is_selected:
            name_attr |= curses.A_REVERSE
        display_name = marker + acct_name
        safe_addstr(
            stdscr,
            y,
            col_start,
            display_name[: name_w - 1].ljust(name_w),
            name_attr,
        )

        count_str = f"{enabled_count}/{len(HOOK_EVENTS)} on"
        count_attr = normal_attr if enabled_count == len(HOOK_EVENTS) else dim_attr
        if is_selected:
            count_attr |= curses.A_REVERSE
        safe_addstr(stdscr, y, counts_x, count_str.ljust(12), count_attr)

        custom_str = f"{extra_count} custom"
        custom_attr = normal_attr if extra_count > 0 else dim_attr
        if is_selected:
            custom_attr |= curses.A_REVERSE
        safe_addstr(stdscr, y, custom_x, custom_str.ljust(12), custom_attr)

        if status_x + 8 <= cols:
            drift = drift_status.get(acct_name, "missing")
            badge_text, badge_cp = _DRIFT_BADGES.get(drift, ("?", CP_DIM))
            badge_attr = curses.color_pair(badge_cp)
            if is_selected:
                badge_attr |= curses.A_REVERSE
            safe_addstr(stdscr, y, status_x, f"[{badge_text}]", badge_attr)

    ref_start_y = 3 + len(account_list) + 1
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

            parts = []
            hook_type = handler.get("type", "command")
            if hook_type != "command":
                parts.append(f"[{hook_type}]")
            if matcher:
                parts.append(f"[{matcher}]")
            parts.append(cmd_str)

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
    type_label = "Type:"
    safe_addstr(
        stdscr, y, 2, type_label.ljust(label_w), warn_attr if is_active else dim_attr
    )
    if supports_all_types:
        for i, t in enumerate(_HOOK_TYPES):
            marker = "[*]" if t == hook_type else "[ ]"
            attr = (
                input_attr | curses.A_BOLD
                if (is_active and t == hook_type)
                else dim_attr
            )
            safe_addstr(stdscr, y, field_x + i * 12, f"{marker} {t}", attr)
    else:
        safe_addstr(stdscr, y, field_x, "command (only type for this event)", dim_attr)
    y += 1

    if has_matcher:
        is_active = active_field == _FIELD_MATCHER
        matcher_label = "Matcher:"
        safe_addstr(
            stdscr,
            y,
            2,
            matcher_label.ljust(label_w),
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
        async_label = "Async:"
        safe_addstr(
            stdscr,
            y,
            2,
            async_label.ljust(label_w),
            warn_attr if is_active else dim_attr,
        )
        marker = "[*]" if is_async else "[ ]"
        attr = input_attr | curses.A_BOLD if is_active else dim_attr
        safe_addstr(stdscr, y, field_x, f"{marker} yes   ", attr)
        marker2 = "[ ]" if is_async else "[*]"
        safe_addstr(stdscr, y, field_x + 12, f"{marker2} no", attr)
        y += 1

    is_active = active_field == _FIELD_TIMEOUT
    timeout_label = "Timeout:"
    safe_addstr(
        stdscr, y, 2, timeout_label.ljust(label_w), warn_attr if is_active else dim_attr
    )
    default_t = _HOOK_TYPE_DEFAULTS_TIMEOUT.get(hook_type, 30)
    _draw_text_field(
        stdscr, y, field_x, timeout_val, cursor if is_active else 0, is_active, cols
    )
    if not timeout_val and not is_active:
        safe_addstr(stdscr, y, field_x, f"(default: {default_t}s)", dim_attr)
    y += 1

    safe_addstr(
        stdscr, y, 2, "Tab next  Shift-Tab prev  Enter submit  Esc cancel", dim_attr
    )


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
    for i, opt in enumerate(visible_options):
        y = box_y + i
        if y >= anchor_y:
            break
        is_sel = (scroll + i) == selected
        text = f" ▸ {opt}" if is_sel else f"   {opt}"
        attr = input_attr | curses.A_BOLD if is_sel else normal_attr
        padded = text[:box_w].ljust(box_w)
        safe_addstr(stdscr, y, box_x, padded, attr)

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
