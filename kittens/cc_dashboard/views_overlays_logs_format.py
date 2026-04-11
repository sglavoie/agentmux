"""Formatting helpers for activity log overlays."""

from __future__ import annotations

from typing import Any

from cc_dashboard.views_shared import _shorten_home


def _format_log_entry(entry: dict[str, Any], cols: int) -> str:
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
