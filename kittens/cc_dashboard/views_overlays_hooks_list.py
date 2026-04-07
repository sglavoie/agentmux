"""Hooks overview and event-list overlay views."""

import curses

from cc_dashboard.colors import (
    CP_ATTENTION,
    CP_DIM,
    CP_HEADER,
    CP_NORMAL,
    _cp_for_account,
)
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.views_overlays_hooks_shared import (
    _DRIFT_BADGES,
    _as_builtin_row,
    _as_event_row,
    _as_extra_row,
    _describe_builtin,
    _hooks_event_rows,
)
from cc_dashboard.views_shared import draw_footer


def draw_hooks_view(
    stdscr, accounts, drift_status, hooks_global, hooks_overrides, selected
):
    """Draw the hooks overview showing all accounts with status."""
    del hooks_global

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

        marker = "> " if is_selected else "  "
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
    avail = max(1, rows - 4)
    total = len(event_rows)
    if total <= avail:
        scroll_offset = 0
    else:
        scroll_offset = max(0, min(selected - avail // 2, total - avail))

    for vi in range(scroll_offset, min(scroll_offset + avail, total)):
        row = event_rows[vi]
        y = 2 + (vi - scroll_offset)
        if y >= rows - 2:
            break

        is_selected = vi == selected
        marker = "> " if is_selected else "  "

        if row[0] == "event":
            _, event_name, is_disabled = _as_event_row(row)
            status_attr = dim_attr if is_disabled else normal_attr
            if is_selected:
                status_attr |= curses.A_REVERSE

            safe_addstr(stdscr, y, 2, marker, normal_attr)
            safe_addstr(stdscr, y, 4, event_name.ljust(22), status_attr)
            badge = " off" if is_disabled else " on"
            badge_attr = dim_attr if is_disabled else curses.color_pair(CP_ATTENTION)
            if is_selected:
                badge_attr |= curses.A_REVERSE
            safe_addstr(stdscr, y, 26, badge, badge_attr)
            continue

        if row[0] == "builtin":
            _, _event_name, _cmd_idx, cmd_str, is_disabled, is_async, _type_str = (
                _as_builtin_row(row)
            )
            attr = dim_attr if is_disabled else normal_attr
            if is_selected:
                attr |= curses.A_REVERSE
            safe_addstr(stdscr, y, 2, marker, normal_attr)
            tag = " async" if is_async else ""
            desc = _describe_builtin(cmd_str)
            suffix = f"{tag} | {desc}" if desc else tag
            display = f"    - {cmd_str}{suffix}"
            safe_addstr(stdscr, y, 4, display[: (cols - 6)], attr)
            continue

        _, _event_name, _cmd_idx, cmd_str, handler, matcher = _as_extra_row(row)
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


__all__ = ["draw_hooks_event_view", "draw_hooks_view"]
