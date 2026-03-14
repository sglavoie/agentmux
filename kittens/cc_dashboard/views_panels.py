"""Main list, picker, forms, and directory browser views."""

import curses
import os
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
    _cp_for_palette_color,
)
from cc_dashboard.drawing import (
    _display_width,
    _pad_to_width,
    _truncate_to_width,
    safe_addstr,
    shorten_path,
)
from cc_dashboard.remote.shared import format_duration
from cc_dashboard.views_detail import DETAIL_PANE_HEIGHT
from cc_dashboard.views_shared import draw_footer


def draw_list_view(
    stdscr,
    sessions,
    selected,
    detail_visible=False,
    sort_label="",
    marked=None,
    collapsed_groups=None,
    sort_mode="default",
    group_filter=None,
):
    """Draw the main session list with header, rows, and footer."""
    rows, cols = stdscr.getmaxyx()
    stdscr.clear()

    title = "Claude Code Dashboard"
    title_x = max(0, (cols - len(title)) // 2)
    safe_addstr(
        stdscr,
        1,
        title_x,
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )
    badge_x = title_x + len(title)
    if sort_label:
        indicator = f" [{sort_label}]"
        if badge_x + len(indicator) < cols:
            safe_addstr(stdscr, 1, badge_x, indicator, curses.color_pair(CP_DIM))
            badge_x += len(indicator)
    if group_filter:
        groups_str = ",".join(sorted(group_filter))
        group_badge = f" [Group: {groups_str}]"
        if badge_x + len(group_badge) < cols:
            safe_addstr(stdscr, 1, badge_x, group_badge, curses.color_pair(CP_DIM))
            badge_x += len(group_badge)
    if marked:
        mark_badge = f" [{len(marked)} marked]"
        if badge_x + len(mark_badge) < cols:
            safe_addstr(stdscr, 1, badge_x, mark_badge, curses.color_pair(CP_WARN))

    if not sessions:
        safe_addstr(
            stdscr,
            4,
            3,
            "No Claude sessions found.",
            curses.color_pair(CP_DIM),
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

    multi_instance = len({s.get("instance_label", "") for s in sessions}) > 1
    c_inst = 10 if multi_instance else 0

    c_title = 20
    c_acct = 10
    c_notes = 6
    c_branch = 24
    c_status = 18
    fixed = c_title + c_acct + c_inst + c_notes + c_branch + c_status + 8
    c_dir = max(12, cols - fixed)

    hdr = f"  {'Title':<{c_title}}{'Account':<{c_acct}}"
    if multi_instance:
        hdr += f"{'Instance':<{c_inst}}"
    hdr += f"{'Directory':<{c_dir}}{'Notes':<{c_notes}}{'Branch':<{c_branch}}{'Status'}"
    safe_addstr(stdscr, 3, 1, hdr, curses.color_pair(CP_HEADER) | curses.A_BOLD)

    sep = f"  {'-' * c_title}{'-' * c_acct}"
    if multi_instance:
        sep += "-" * c_inst
    sep += f"{'-' * c_dir}{'-' * c_notes}{'-' * c_branch}{'-' * c_status}"
    safe_addstr(stdscr, 4, 1, sep, curses.color_pair(CP_DIM))

    avail = max(1, rows - 8)
    if detail_visible:
        avail -= DETAIL_PANE_HEIGHT
    avail = max(1, avail)

    # Build visual row list: headers + sessions for group sort, flat otherwise
    show_groups = sort_mode == "group"
    visual_rows: list[tuple] = []
    if show_groups:
        i = 0
        while i < len(sessions):
            g = sessions[i].get("group", "") or "(ungrouped)"
            j = i
            while (
                j < len(sessions)
                and (sessions[j].get("group", "") or "(ungrouped)") == g
            ):
                j += 1
            count = j - i
            is_collapsed = collapsed_groups is not None and g in collapsed_groups
            visual_rows.append(("header", g, count, is_collapsed))
            if not is_collapsed:
                for k in range(i, j):
                    visual_rows.append(("session", k))
            i = j
    else:
        for i in range(len(sessions)):
            visual_rows.append(("session", i))

    # Find selected entry in visual rows for scrolling
    sel_vi = 0
    for vi, row in enumerate(visual_rows):
        if row[0] == "session" and row[1] == selected:
            sel_vi = vi
            break

    total_visual = len(visual_rows)
    if total_visual <= avail:
        offset = 0
    else:
        offset = max(0, min(sel_vi - avail // 2, total_visual - avail))

    for vi in range(offset, min(offset + avail, total_visual)):
        row = visual_rows[vi]
        y = 5 + (vi - offset)

        if row[0] == "header":
            _, group_name, count, is_collapsed = row
            prefix = "[+]" if is_collapsed else "[-]"
            header_text = f"  {prefix} {group_name} ({count}) "
            remaining = cols - len(header_text) - 2
            if remaining > 0:
                header_text += "-" * remaining
            safe_addstr(stdscr, y, 1, header_text, curses.color_pair(CP_DIM))
            continue

        si = row[1]
        s = sessions[si]
        sel = si == selected
        is_marked = marked and s["tab_id"] in marked
        if sel and is_marked:
            mark = ">*"
        elif sel:
            mark = "> "
        elif is_marked:
            mark = " *"
        else:
            mark = "  "

        cc_color = s.get("cc_color", "")
        if cc_color:
            cp = _cp_for_palette_color(cc_color, selected=sel)
        else:
            cp = _cp_for_account(s["account"], selected=sel)
        short_dir = shorten_path(s["cwd"], c_dir - 1)
        title_padded = _pad_to_width(
            _truncate_to_width(s["title"], c_title - 1), c_title
        )

        git_info = s.get("git")
        if git_info:
            branch_display = git_info["branch"]
            if git_info["dirty"]:
                branch_display += "*"
            if len(branch_display) > c_branch - 1:
                branch_display = branch_display[: c_branch - 2] + "~"
        else:
            branch_display = "-"

        status_text = s["status"]
        idle_secs = 0
        activity = s.get("activity")
        if activity:
            idle_secs = int(time.time()) - activity["last_event_ts"]
            status_text = f"{s['status']} {format_duration(idle_secs)}"

        if activity and activity.get("active_subagents", 0) > 0:
            status_text += f" +{activity['active_subagents']}ag"

        dimmed = s.get("dimmed")
        if dimmed:
            status_text += " [dim]"

        line = f"{mark}{title_padded}{s['account']:<{c_acct}}"
        if multi_instance:
            line += f"{s.get('instance_label', ''):<{c_inst}}"
        notes_indicator = "Y" if s.get("cc_notes", "") else " "
        line += f"{short_dir:<{c_dir}}"
        line += f"{notes_indicator:<{c_notes}}"
        line += f"{branch_display:<{c_branch}}"

        row_attr = curses.color_pair(cp)
        if sel:
            row_attr |= curses.A_BOLD
        if dimmed and not sel:
            row_attr |= curses.A_DIM
        safe_addstr(stdscr, y, 1, line, row_attr)

        status_x = 1 + _display_width(line)
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
        if dimmed and not sel:
            status_attr |= curses.A_DIM
        safe_addstr(stdscr, y, status_x, status_text, status_attr)

    footer_exclude = None if multi_instance else {"m Move"}
    draw_footer(stdscr, "list", exclude=footer_exclude)


def draw_pick_view(
    stdscr,
    entries,
    selected,
    running_count,
    marked=None,
    group_filter=None,
):
    """Draw the config session picker list with Running/Stopped sections."""
    rows, cols = stdscr.getmaxyx()
    stdscr.clear()

    if marked is None:
        marked = set()
    mark_count = len(marked)
    title = (
        f"Launch from Config ({mark_count} marked)"
        if mark_count
        else "Launch from Config"
    )
    safe_addstr(
        stdscr,
        1,
        max(0, (cols - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    if group_filter is not None:
        filter_label = "Group: " + ", ".join(sorted(group_filter))
        safe_addstr(
            stdscr,
            2,
            max(0, (cols - len(filter_label)) // 2),
            filter_label,
            curses.color_pair(CP_DIM),
        )

    if not entries:
        safe_addstr(stdscr, 4, 3, "No sessions configured.", curses.color_pair(CP_DIM))
        safe_addstr(
            stdscr,
            5,
            3,
            "Add entries to the sessions config, then try again.",
            curses.color_pair(CP_DIM),
        )
        draw_footer(stdscr, "pick")
        return

    c_title = 28
    c_acct = 12
    c_dir = max(12, cols - c_title - c_acct - 6)

    hdr = f"  {'Title':<{c_title}}{'Account':<{c_acct}}{'Directory':<{c_dir}}"
    safe_addstr(stdscr, 3, 1, hdr, curses.color_pair(CP_HEADER) | curses.A_BOLD)
    sep = f"  {'-' * c_title}{'-' * c_acct}{'-' * c_dir}"
    safe_addstr(stdscr, 4, 1, sep, curses.color_pair(CP_DIM))

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

    sel_visual = 0
    for vi, row in enumerate(visual_rows):
        if row[0] == "entry" and row[1] == selected:
            sel_visual = vi
            break

    avail = max(1, rows - 8)
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
            is_marked = entry["title"] in marked
            is_running = entry_idx < running_count
            if sel and is_marked:
                mark = ">*"
            elif is_marked:
                mark = " *"
            elif sel:
                mark = "> "
            else:
                mark = "  "

            cp = _cp_for_account(entry["account"], selected=sel)
            short_dir = shorten_path(os.path.expanduser(entry["dir"]), c_dir - 1)
            title_padded = _pad_to_width(
                _truncate_to_width(entry["title"], c_title - 1), c_title
            )
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


def draw_delete_view(stdscr, entries, selected, running_count):
    """Draw the delete session picker list with Running/Stopped sections."""
    rows, cols = stdscr.getmaxyx()
    stdscr.clear()

    title = "Delete Session"
    safe_addstr(
        stdscr,
        1,
        max(0, (cols - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    if not entries:
        safe_addstr(stdscr, 4, 3, "No sessions configured.", curses.color_pair(CP_DIM))
        draw_footer(stdscr, "delete")
        return

    c_title = 24
    c_acct = 12
    c_type = 12
    c_dir = max(12, cols - c_title - c_acct - c_type - 6)

    hdr = (
        f"  {'Title':<{c_title}}{'Account':<{c_acct}}"
        f"{'Type':<{c_type}}{'Directory':<{c_dir}}"
    )
    safe_addstr(stdscr, 3, 1, hdr, curses.color_pair(CP_HEADER) | curses.A_BOLD)
    sep = f"  {'-' * c_title}{'-' * c_acct}{'-' * c_type}{'-' * c_dir}"
    safe_addstr(stdscr, 4, 1, sep, curses.color_pair(CP_DIM))

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

    sel_visual = 0
    for vi, row in enumerate(visual_rows):
        if row[0] == "entry" and row[1] == selected:
            sel_visual = vi
            break

    avail = max(1, rows - 8)
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
            mark = ">" if sel else " "
            is_running = entry_idx < running_count

            cp = _cp_for_account(entry["account"], selected=sel)
            short_dir = shorten_path(os.path.expanduser(entry["dir"]), c_dir - 1)
            title_padded = _pad_to_width(
                _truncate_to_width(entry["title"], c_title - 1), c_title
            )
            type_tag = entry.get("type_tag", "[dir]")
            line = (
                f"{mark} {title_padded}{entry['account']:<{c_acct}}"
                f"{type_tag:<{c_type}}{short_dir:<{c_dir}}"
            )

            attr = curses.color_pair(cp)
            if sel:
                attr |= curses.A_BOLD
            if is_running and not sel:
                attr = curses.color_pair(CP_DIM)
            safe_addstr(stdscr, screen_y, 1, line, attr)

    draw_footer(stdscr, "delete")


def draw_delete_confirm(stdscr, delete_step, entry, warnings=None):
    """Draw the confirmation prompt for the delete flow."""
    if warnings is None:
        warnings = []
    rows, _ = stdscr.getmaxyx()
    title = entry["title"]
    if delete_step == "confirm_config":
        warning = " (session is running)" if entry.get("is_running", False) else ""
        safe_addstr(
            stdscr,
            rows - 3,
            2,
            f'Remove "{title}" from sessions config?{warning}  y/n',
            curses.color_pair(CP_WARN) | curses.A_BOLD,
        )
    elif delete_step == "confirm_data":
        type_tag = entry.get("type_tag", "[dir]")
        type_label = {"[worktree]": "worktree", "[repo]": "git repo"}.get(
            type_tag, "directory"
        )
        path = os.path.expanduser(entry["dir"])
        prompt_row = rows - 3
        if warnings:
            for i, w in enumerate(warnings):
                wy = prompt_row - len(warnings) + i
                if wy >= 1:
                    safe_addstr(stdscr, wy, 4, f"! {w}", curses.color_pair(CP_WARN))
        safe_addstr(
            stdscr,
            prompt_row,
            2,
            f"Delete {type_label} at {path}?  y/n",
            curses.color_pair(CP_WARN) | curses.A_BOLD,
        )


def draw_filter_bar(stdscr, account_filter, account_filter_mode):
    """Draw the account filter bar on row 2."""
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
        attr = curses.color_pair(_cp_for_account(name) if enabled else CP_DIM)
        safe_addstr(stdscr, 2, x, label, attr)
        x += len(label) + 2
        if x >= cols - 2:
            break


def draw_group_filter_bar(stdscr, sessions, group_filter, group_filter_mode):
    """Draw the group filter bar on row 3 (below account filter bar)."""
    _, cols = stdscr.getmaxyx()
    groups = sorted({s.get("group", "") or "(ungrouped)" for s in sessions})

    if group_filter_mode:
        prefix = "Groups: "
        prefix_attr = curses.color_pair(CP_WARN)
    else:
        prefix = "Groups: "
        prefix_attr = curses.color_pair(CP_DIM)

    row = 3
    # Clear the entire row to prevent bleed-through from the table header
    safe_addstr(stdscr, row, 0, " " * (cols - 1), curses.color_pair(CP_DIM))
    safe_addstr(stdscr, row, 2, prefix, prefix_attr)
    x = 2 + len(prefix)

    for i, name in enumerate(groups):
        if i >= 9:
            break
        label = f"{i + 1}:{name}"
        enabled = group_filter is None or name in group_filter
        attr = curses.color_pair(CP_DIM)
        if enabled:
            attr = curses.color_pair(CP_NORMAL) | curses.A_BOLD
        safe_addstr(stdscr, row, x, label, attr)
        x += len(label) + 2
        if x >= cols - 2:
            break


def draw_new_form(
    stdscr,
    fields,
    active_field,
    cursor_pos,
    multi_instance=False,
    form_title="New Session",
):
    """Draw the new-session or edit-session form."""
    _rows, cols = stdscr.getmaxyx()
    stdscr.clear()

    safe_addstr(
        stdscr,
        1,
        max(0, (cols - len(form_title)) // 2),
        form_title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    labels = [
        "Title:",
        "Directory:",
        "Account:",
        "Flags:",
        "Split:",
        "Env vars:",
        "Group:",
    ]
    field_keys = ["title", "dir", "account", "flags", "pw", "env", "group"]
    cycle_fields = {"account", "pw"}

    if "save" in fields:
        labels.append("Save:")
        field_keys.append("save")
        cycle_fields.add("save")

    if multi_instance and "instance" in fields:
        labels.insert(-1, "Instance:")
        field_keys.insert(-1, "instance")
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
            if is_active:
                before = val[:cursor_pos]
                after = val[cursor_pos:]
                safe_addstr(stdscr, y, 17, before, curses.color_pair(CP_INPUT_ACTIVE))
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

    hint_y = 4 + len(field_keys) * 2
    safe_addstr(
        stdscr,
        hint_y,
        17,
        'Flags/Split: empty = inherit global, "-" = disable  |  Env: KEY=VAL KEY2=VAL2',
        curses.color_pair(CP_DIM),
    )

    draw_footer(stdscr, "new")


def draw_restart_options_form(
    stdscr,
    session,
    fields,
    active_field,
    cursor_pos,
    *,
    multi_instance=False,
):
    """Draw restart options form for a selected running session."""
    _rows, cols = stdscr.getmaxyx()
    stdscr.clear()

    title = "Restart Options"
    safe_addstr(
        stdscr,
        1,
        max(0, (cols - len(title)) // 2),
        title,
        curses.color_pair(CP_HEADER) | curses.A_BOLD,
    )

    subtitle = session.get("title", "")
    safe_addstr(
        stdscr,
        2,
        max(0, (cols - len(subtitle)) // 2),
        subtitle,
        curses.color_pair(CP_DIM),
    )

    labels = ["Flags:", "Split:", "Env vars:", "Config dir:"]
    field_keys = ["flags", "pw", "env", "config_dir"]
    cycle_fields = {"pw", "config_dir"}

    if multi_instance and "instance" in fields:
        labels.append("Instance:")
        field_keys.append("instance")
        cycle_fields.add("instance")

    for i, (label, key) in enumerate(zip(labels, field_keys, strict=False)):
        y = 5 + i * 2
        is_active = i == active_field
        safe_addstr(stdscr, y, 4, f"{label:>12}", curses.color_pair(CP_HEADER))

        val = fields[key]
        if key in cycle_fields:
            display = f" {val} "
            if is_active:
                display += "  (Tab to cycle)"
            cp = CP_INPUT_ACTIVE if is_active else CP_INPUT
            safe_addstr(stdscr, y, 17, display[: cols - 20], curses.color_pair(cp))
        else:
            if is_active:
                before = val[:cursor_pos]
                after = val[cursor_pos:]
                safe_addstr(stdscr, y, 17, before, curses.color_pair(CP_INPUT_ACTIVE))
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

    hint_y = 5 + len(field_keys) * 2
    safe_addstr(
        stdscr,
        hint_y,
        17,
        'Flags/Split: empty = inherit global, "-" = disable  |  Env: KEY=VAL KEY2=VAL2',
        curses.color_pair(CP_DIM),
    )

    draw_footer(stdscr, "restart_options")


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
        results = []
        for entry in os.scandir(scan_dir):
            try:
                is_dir = entry.is_dir(follow_symlinks=True)
            except OSError:
                continue
            if (
                is_dir
                and (show_hidden or not entry.name.startswith("."))
                and (not partial or partial.lower() in entry.name.lower())
            ):
                results.append(entry.name)
        entries = sorted(results)
    except OSError:
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
            stdscr,
            dropdown_y,
            dropdown_x,
            "(no matching directories)",
            curses.color_pair(CP_DIM),
        )
        return
    visible = entries[scroll : scroll + max_visible]
    max_width = cols - dropdown_x - 2
    for i, name in enumerate(visible):
        y = dropdown_y + i
        if y >= rows - 2:
            break
        abs_idx = scroll + i
        is_sel = abs_idx == selected
        display = ("> " if is_sel else "  ") + name + "/"
        display = display[:max_width]
        attr = (
            curses.color_pair(CP_INPUT_ACTIVE) | curses.A_BOLD
            if is_sel
            else curses.color_pair(CP_NORMAL)
        )
        safe_addstr(stdscr, y, dropdown_x, display, attr)
    total = len(entries)
    if total > max_visible:
        indicator = f"[{scroll + 1}-{min(scroll + max_visible, total)}/{total}]"
        ind_y = dropdown_y + len(visible)
        if ind_y < rows - 2:
            safe_addstr(stdscr, ind_y, dropdown_x, indicator, curses.color_pair(CP_DIM))
