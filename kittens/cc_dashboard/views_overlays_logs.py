"""Activity log overlay views."""

import curses
import time

from cc_dashboard.colors import (
    CP_ATTENTION,
    CP_BORDER,
    CP_DIM,
    CP_HEADER,
    CP_INPUT_ACTIVE,
    CP_MATCH,
    CP_NORMAL,
    CP_WARN,
)
from cc_dashboard.drawing import _pad_to_width, draw_border, safe_addstr
from cc_dashboard.views_shared import (
    _draw_peek_search_bar,
    _render_peek_line,
    _shorten_home,
    draw_footer,
)


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
    selected=-1,
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
            if abs_idx == selected:
                safe_addstr(
                    stdscr,
                    content_start + i,
                    0,
                    line[: cols - 1],
                    normal_attr | curses.A_REVERSE,
                )
                continue
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


def draw_actlog_browse(
    stdscr,
    entries,
    selected,
    scroll,
    search_query="",
    search_active=False,
    search_cursor=0,
    match_line_indices=None,
    match_index=0,
):
    """Draw a tabular listing of all activity log files."""
    stdscr.clear()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)
    match_attr = curses.color_pair(CP_MATCH)

    if match_line_indices is None:
        match_line_indices = []
    match_set = set(match_line_indices)

    safe_addstr(stdscr, 0, 2, "Activity Logs -- Browse All", header_attr)

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

        abs_idx = scroll + i
        is_selected = abs_idx == selected
        is_match = abs_idx in match_set
        if is_selected:
            attr = normal_attr | curses.A_REVERSE
        elif is_match:
            attr = match_attr
        else:
            attr = normal_attr

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
        safe_addstr(stdscr, rows - 2, 2, pos, dim_attr)

    draw_footer(stdscr, "actlog_browse_search" if search_active else "actlog_browse")


def draw_actlog_gsearch(
    stdscr,
    query,
    cursor,
    results,
    selected,
    scroll,
    loading=False,
    total_scanned=0,
    log_index=None,
    search_active=True,
):
    """Draw the global activity log search view."""
    stdscr.clear()
    rows, cols = stdscr.getmaxyx()
    header_attr = curses.color_pair(CP_HEADER) | curses.A_BOLD
    normal_attr = curses.color_pair(CP_NORMAL)
    dim_attr = curses.color_pair(CP_DIM)
    warn_attr = curses.color_pair(CP_WARN)
    span_attr = curses.color_pair(CP_WARN) | curses.A_BOLD

    safe_addstr(stdscr, 0, 2, "Global Log Search", header_attr)

    # Search input row
    prefix = "/ "
    safe_addstr(stdscr, 1, 2, prefix, warn_attr)
    x = 2 + len(prefix)
    before = query[:cursor]
    after = query[cursor:]
    cursor_ch = after[0] if after else " "
    after_rest = after[1:] if len(after) > 1 else ""

    input_attr = curses.color_pair(CP_INPUT_ACTIVE)
    if search_active:
        safe_addstr(stdscr, 1, x, before, input_attr)
        safe_addstr(
            stdscr, 1, x + len(before), cursor_ch, input_attr | curses.A_REVERSE
        )
        if after_rest:
            safe_addstr(stdscr, 1, x + len(before) + 1, after_rest, input_attr)
    else:
        safe_addstr(stdscr, 1, x, query, input_attr)

    # Result count indicator
    result_x = x + len(query) + 2
    n_files = len(log_index) if log_index else 0
    if results:
        info = f"({len(results)} results in {n_files} files)"
        safe_addstr(stdscr, 1, result_x, info, dim_attr)
    elif query and len(query) >= 3 and not loading:
        info = f"(0 results in {n_files} files)"
        safe_addstr(stdscr, 1, result_x, info, dim_attr)

    # Status row
    if loading:
        safe_addstr(stdscr, 2, 4, "Loading...", warn_attr)
    elif not query or len(query) < 3:
        safe_addstr(stdscr, 2, 4, "(type 3+ chars to search)", dim_attr)

    # Column header
    title_w = min(20, max(10, cols // 5))
    safe_addstr(stdscr, 3, 4, "Session".ljust(title_w) + "Time      Details", dim_attr)

    # Results area
    content_start = 4
    content_height = rows - content_start - 2
    if content_height < 1:
        draw_footer(
            stdscr, "actlog_gsearch_search" if search_active else "actlog_gsearch"
        )
        return

    visible = results[scroll : scroll + content_height]
    for i, result in enumerate(visible):
        y = content_start + i
        if y >= rows - 2:
            break

        abs_idx = scroll + i
        is_selected = abs_idx == selected

        title_short = (result.get("title", "") or "?")[: title_w - 2]
        ts = result.get("ts")
        time_str = time.strftime("%H:%M:%S", time.localtime(ts)) if ts else "??:??:??"
        detail = result.get("formatted_line", "")
        # Strip the time prefix from formatted_line since we show it separately
        if detail.startswith(time_str):
            detail = detail[len(time_str) :].lstrip()

        row_text = title_short.ljust(title_w) + time_str + "  " + detail
        clipped = row_text[: cols - 5]

        attr = normal_attr | curses.A_REVERSE if is_selected else normal_attr

        if query and not is_selected:
            # Use _render_peek_line for match highlighting
            _render_peek_line(
                stdscr,
                y,
                " " * 4 + clipped,
                cols,
                query,
                True,
                False,
                normal_attr,
                normal_attr,
                span_attr,
                span_attr,
            )
        else:
            safe_addstr(stdscr, y, 4, _pad_to_width(clipped, cols - 5), attr)

    # Scroll position
    total = len(results)
    if total > content_height:
        pos = f"[{scroll + 1}-{min(scroll + content_height, total)}/{total}]"
        safe_addstr(stdscr, rows - 2, 2, pos, dim_attr)

    draw_footer(stdscr, "actlog_gsearch_search" if search_active else "actlog_gsearch")


def draw_actlog_detail(stdscr, entry):
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
    label_col = max_label + 2  # label + ": "
    max_raw_value = max((len(str(v)) for _, v in pairs), default=0)
    box_w = min(cols - 4, max(60, label_col + max_raw_value + 6))
    wrap_w = max(1, box_w - label_col - 6)

    # Build word-wrapped display rows as (label, chunk) pairs
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

    box_h = len(display_rows) + 4  # title row + blank + content + close hint
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
