"""Detail pane rendering for the dashboard list view."""

import curses
import os
import time

from cc_dashboard import config as _cfg
from cc_dashboard.colors import CP_DIM, CP_HEADER, CP_NORMAL, CP_WARN, _cp_for_account
from cc_dashboard.drawing import safe_addstr
from cc_dashboard.remote.shared import _strip_override_marker, format_duration

DETAIL_PANE_HEIGHT = 15


def draw_detail_pane(stdscr, session, detail_y):
    """Render the detail pane for the selected session starting at detail_y."""
    rows, cols = stdscr.getmaxyx()
    if detail_y + DETAIL_PANE_HEIGHT > rows:
        return

    sep = "--- Detail " + "-" * max(0, cols - 14)
    safe_addstr(stdscr, detail_y, 1, sep, curses.color_pair(CP_DIM))

    cfg_entry = _cfg._find_config_session(session["title"])
    if cfg_entry is None:
        cfg_entry = _cfg._find_config_session(_strip_override_marker(session["title"]))
    if cfg_entry:
        effective_flags = _cfg._resolve_claude_flags(cfg_entry.get("flags", ""))
        effective_split = _cfg._resolve_project_window(cfg_entry.get("pw", ""))
    else:
        effective_flags = _cfg._resolve_claude_flags("")
        effective_split = _cfg._resolve_project_window("")

    home = os.path.expanduser("~")
    full_dir = session["cwd"]
    if full_dir.startswith(home):
        full_dir = "~" + full_dir[len(home) :]

    acct_info = _cfg._accounts.get(session["account"], {})
    acct_config_dir = acct_info.get("config_dir", "")

    config_dir = session.get("config_dir", "")
    config_dir_overridden = bool(
        config_dir and acct_config_dir and config_dir != acct_config_dir
    )
    if not config_dir:
        config_dir = acct_config_dir
    if config_dir and config_dir.startswith(home):
        config_dir = "~" + config_dir[len(home) :]
    if not config_dir:
        config_dir = "~/.claude"

    cc_flags = session.get("cc_flags", "")
    flags_overridden = bool(cc_flags and cc_flags != "-")

    color_name = acct_info.get("color", "gray")
    pids_str = ", ".join(str(p) for p in session.get("pids", []))
    window_count = str(session.get("window_count", 1))

    cmdline_display = ""
    for cl in session.get("cmdlines", []):
        if "claude" in cl.lower():
            cmdline_display = cl
            break

    label_attr = curses.color_pair(CP_HEADER)
    val_attr = curses.color_pair(CP_NORMAL)
    acct_attr = curses.color_pair(_cp_for_account(session["account"]))
    override_attr = curses.color_pair(CP_WARN)

    mid = max(cols // 2, 40)
    lx = 2
    rx = mid

    y = detail_y + 1
    safe_addstr(stdscr, y, lx, " Title: ", label_attr)
    safe_addstr(stdscr, y, lx + 8, session["title"], val_attr)
    safe_addstr(stdscr, y, rx, "Tab ID: ", label_attr)
    safe_addstr(stdscr, y, rx + 8, str(session.get("tab_id", "")), val_attr)

    y += 1
    safe_addstr(stdscr, y, lx, " Account: ", label_attr)
    cc_color = session.get("cc_color", "")
    if cc_color and cc_color != color_name:
        acct_display = f"{session['account']} ({cc_color})"
        acct_display_attr = override_attr
    else:
        acct_display = f"{session['account']} ({color_name})"
        acct_display_attr = acct_attr
    safe_addstr(stdscr, y, lx + 10, acct_display, acct_display_attr)
    safe_addstr(stdscr, y, rx, "Windows: ", label_attr)
    safe_addstr(stdscr, y, rx + 9, window_count, val_attr)

    y += 1
    safe_addstr(stdscr, y, lx, " Directory: ", label_attr)
    safe_addstr(stdscr, y, lx + 12, full_dir[: mid - lx - 13], val_attr)
    safe_addstr(stdscr, y, rx, "PIDs: ", label_attr)
    safe_addstr(
        stdscr, y, rx + 6, pids_str[: cols - rx - 7] if pids_str else "n/a", val_attr
    )

    y += 1
    safe_addstr(stdscr, y, lx, " Config dir: ", label_attr)
    safe_addstr(
        stdscr,
        y,
        lx + 13,
        config_dir[: mid - lx - 14],
        override_attr if config_dir_overridden else val_attr,
    )
    safe_addstr(stdscr, y, rx, "Flags: ", label_attr)
    safe_addstr(
        stdscr,
        y,
        rx + 7,
        (effective_flags if effective_flags else "none")[: cols - rx - 8],
        override_attr if flags_overridden else val_attr,
    )

    y += 1
    safe_addstr(stdscr, y, lx, " Status: ", label_attr)
    safe_addstr(stdscr, y, lx + 9, session["status"], val_attr)
    safe_addstr(stdscr, y, rx, "Split: ", label_attr)
    safe_addstr(
        stdscr,
        y,
        rx + 7,
        effective_split if effective_split else "none",
        val_attr,
    )

    env_display = session.get("cc_env", "")
    if not env_display and cfg_entry:
        env_display = cfg_entry.get("env", "")
    if env_display and env_display != "-":
        y += 1
        safe_addstr(stdscr, y, lx, " Env vars: ", label_attr)
        safe_addstr(stdscr, y, lx + 11, env_display[: cols - lx - 12], val_attr)

    notes_raw = session.get("cc_notes", "")
    if notes_raw:
        note_lines = notes_raw.split("\n")
        # Cap note lines to avoid overflowing the detail pane
        # Reserve ~8 lines for remaining fields (git, socket, cmdline, activity)
        max_note_lines = max(1, (detail_y + DETAIL_PANE_HEIGHT) - y - 9)
        y += 1
        safe_addstr(stdscr, y, lx, " Notes: ", label_attr)
        safe_addstr(stdscr, y, lx + 8, note_lines[0][: cols - lx - 9], val_attr)
        for nl in note_lines[1:max_note_lines]:
            y += 1
            safe_addstr(stdscr, y, lx + 8, nl[: cols - lx - 9], val_attr)
        if len(note_lines) > max_note_lines:
            y += 1
            safe_addstr(
                stdscr,
                y,
                lx + 8,
                f"(+{len(note_lines) - max_note_lines} more lines)",
                curses.color_pair(CP_DIM),
            )

    max_y = detail_y + DETAIL_PANE_HEIGHT - 1

    y += 1
    if y > max_y:
        return
    git_info = session.get("git")
    safe_addstr(stdscr, y, lx, " Branch: ", label_attr)
    if git_info:
        safe_addstr(stdscr, y, lx + 9, git_info["branch"][: mid - lx - 10], val_attr)
        safe_addstr(stdscr, y, rx, "Working tree: ", label_attr)
        if git_info["dirty"]:
            safe_addstr(stdscr, y, rx + 14, "dirty", curses.color_pair(CP_WARN))
        else:
            safe_addstr(stdscr, y, rx + 14, "clean", val_attr)
    else:
        safe_addstr(stdscr, y, lx + 9, "not a git repo", curses.color_pair(CP_DIM))

    y += 1
    if y > max_y:
        return
    safe_addstr(stdscr, y, lx, " Socket: ", label_attr)
    safe_addstr(stdscr, y, lx + 9, session.get("socket", "")[: mid - lx - 10], val_attr)
    safe_addstr(stdscr, y, rx, "Instance: ", label_attr)
    safe_addstr(stdscr, y, rx + 10, session.get("instance_label", ""), val_attr)

    y += 1
    if y > max_y:
        return
    safe_addstr(stdscr, y, lx, " Cmdline: ", label_attr)
    safe_addstr(
        stdscr,
        y,
        lx + 10,
        (cmdline_display if cmdline_display else "n/a")[: cols - lx - 11],
        override_attr if flags_overridden else val_attr,
    )

    activity = session.get("activity")
    if activity:
        now = int(time.time())
        age_str = format_duration(now - activity["first_event_ts"])
        last_active_str = time.strftime(
            "%H:%M:%S", time.localtime(activity["last_event_ts"])
        )

        y += 1
        if y > max_y:
            return
        safe_addstr(stdscr, y, lx, " Session age: ", label_attr)
        safe_addstr(stdscr, y, lx + 14, age_str, val_attr)
        safe_addstr(stdscr, y, rx, "Last active: ", label_attr)
        safe_addstr(stdscr, y, rx + 13, last_active_str, val_attr)

        y += 1
        if y > max_y:
            return
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
        safe_addstr(
            stdscr,
            y,
            parts_x,
            errors_str,
            (
                curses.color_pair(CP_WARN)
                if activity["tool_failure_count"] > 0
                else val_attr
            ),
        )
        parts_x += len(errors_str)
        safe_addstr(
            stdscr,
            y,
            parts_x,
            perms_str,
            (
                curses.color_pair(CP_WARN)
                if activity["permission_count"] > 0
                else val_attr
            ),
        )
        parts_x += len(perms_str)
        safe_addstr(
            stdscr,
            y,
            parts_x,
            f", {activity['task_completed_count']} tasks",
            val_attr,
        )

        y += 1
        if y > max_y:
            return
        model_val = session.get("model", "") or "unknown"
        agent_type_val = session.get("agent_type", "")
        if agent_type_val:
            model_val += f" (agent: {agent_type_val})"
        safe_addstr(stdscr, y, lx, " Model: ", label_attr)
        safe_addstr(stdscr, y, lx + 8, model_val[: mid - lx - 9], val_attr)

        total_sub = activity.get("total_subagents", 0)
        if total_sub > 0:
            active_sub = activity.get("active_subagents", 0)
            sub_str = f"{active_sub} active / {total_sub} total"
            safe_addstr(stdscr, y, rx, "Subagents: ", label_attr)
            safe_addstr(stdscr, y, rx + 11, sub_str, val_attr)
    else:
        y += 1
        safe_addstr(stdscr, y, lx, " No activity data", curses.color_pair(CP_DIM))
