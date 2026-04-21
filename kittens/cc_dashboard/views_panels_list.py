"""Main list and filter bar views."""

import curses
import time

from cc_dashboard import config as _cfg
from cc_dashboard.colors import (
    CP_ATTENTION,
    CP_DIM,
    CP_HEADER,
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
from cc_dashboard.filters import _session_groups
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
        max_badge = cols - badge_x - 1
        group_badge = _truncate_to_width(f" [Group: {groups_str}]", max_badge)
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

    show_groups = sort_mode == "group"
    visual_rows: list[tuple] = []
    if show_groups:
        group_map: dict[str, list[int]] = {}
        for i, s in enumerate(sessions):
            for g in _session_groups(s):
                group_map.setdefault(g, []).append(i)
        for gn in sorted(
            group_map, key=lambda g: g.lower() if g != "(ungrouped)" else "zzz"
        ):
            indices = group_map[gn]
            is_collapsed = collapsed_groups is not None and gn in collapsed_groups
            visual_rows.append(("header", gn, len(indices), is_collapsed))
            if not is_collapsed:
                for idx in indices:
                    visual_rows.append(("session", idx))
    else:
        for i in range(len(sessions)):
            visual_rows.append(("session", i))

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

        session_index = row[1]
        session = sessions[session_index]
        sel = session_index == selected
        is_marked = marked and session["tab_id"] in marked
        if sel and is_marked:
            mark = ">*"
        elif sel:
            mark = "> "
        elif is_marked:
            mark = " *"
        else:
            mark = "  "

        cc_color = session.get("cc_color", "")
        if cc_color:
            cp = _cp_for_palette_color(cc_color, selected=sel)
        else:
            cp = _cp_for_account(session["account"], selected=sel)
        short_dir = shorten_path(session["cwd"], c_dir - 1)
        title_padded = _pad_to_width(
            _truncate_to_width(session["title"], c_title - 1), c_title
        )

        git_info = session.get("git")
        if git_info:
            branch_display = git_info["branch"]
            if git_info["dirty"]:
                branch_display += "*"
            if len(branch_display) > c_branch - 1:
                branch_display = branch_display[: c_branch - 2] + "~"
        else:
            branch_display = "-"

        status_text = session["status"]
        idle_secs = 0
        activity = session.get("activity")
        if activity:
            idle_secs = int(time.time()) - activity["last_event_ts"]
            status_text = f"{session['status']} {format_duration(idle_secs)}"

        if activity and activity.get("active_subagents", 0) > 0:
            status_text += f" +{activity['active_subagents']}ag"

        dimmed = session.get("dimmed")
        if dimmed:
            status_text += " [dim]"

        if session.get("muted"):
            status_text += " [mute]"

        acct_padded = _pad_to_width(
            _truncate_to_width(session["account"], c_acct - 1), c_acct
        )
        line = f"{mark}{title_padded}{acct_padded}"
        if multi_instance:
            inst_padded = _pad_to_width(
                _truncate_to_width(session.get("instance_label", ""), c_inst - 1),
                c_inst,
            )
            line += inst_padded
        notes_indicator = "Y" if session.get("cc_notes", "") else " "
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
        if session["status"] in ("permission", "error"):
            status_attr = curses.color_pair(CP_ATTENTION) | curses.A_BOLD
        elif (
            idle_threshold > 0
            and session["status"] in ("idle", "stopped")
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
    all_groups: set[str] = set()
    for s in sessions:
        all_groups.update(_session_groups(s))
    groups = sorted(all_groups)

    prefix = "Groups: "
    prefix_attr = (
        curses.color_pair(CP_WARN) if group_filter_mode else curses.color_pair(CP_DIM)
    )

    row = 3
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
