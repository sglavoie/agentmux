"""Activity counters and JSONL activity log helpers."""

import contextlib
import glob
import json
import os

from cc_dashboard.remote.shared import format_duration


def load_activity(tab_id):
    """Read and parse the activity counts file for a tab, returning a dict or None."""
    path = f"/tmp/ccm-activity-{tab_id}.counts"
    try:
        with open(path) as f:
            lines = f.read().splitlines()
    except OSError:
        return None
    if len(lines) < 9:
        return None
    try:
        return {
            "first_event_ts": int(lines[0]),
            "last_event_ts": int(lines[1]),
            "last_event_type": lines[2],
            "prompt_count": int(lines[3]),
            "tool_use_count": int(lines[4]),
            "tool_failure_count": int(lines[5]),
            "permission_count": int(lines[6]),
            "stop_count": int(lines[7]),
            "last_attention_ts": int(lines[8]),
            "task_completed_count": int(lines[9]) if len(lines) > 9 else 0,
        }
    except (ValueError, IndexError):
        return None


def cleanup_stale_activity(live_tab_ids):
    """Remove orphaned activity files whose tab_id is not in the live set."""
    live = set(str(tid) for tid in live_tab_ids)
    for path in glob.glob("/tmp/ccm-activity-*.counts"):
        fname = os.path.basename(path)
        tab_id_str = fname.replace("ccm-activity-", "").replace(".counts", "")
        if tab_id_str not in live:
            with contextlib.suppress(OSError):
                os.remove(path)


def resolve_session_id(tab_id):
    """Resolve a Kitty tab_id to a Claude session_id via the tab-id cache files.

    Scans /tmp/ccm-tabid-* files (created by cc-activity-log) to find one
    whose contents match the given tab_id, then extracts the session_id UUID
    from the filename. Returns None if no match is found.
    """
    tab_id_str = str(tab_id)
    for path in glob.glob("/tmp/ccm-tabid-*"):
        try:
            with open(path) as f:
                contents = f.read().strip()
        except OSError:
            continue
        if contents == tab_id_str:
            fname = os.path.basename(path)
            prefix = "ccm-tabid-"
            if fname.startswith(prefix):
                return fname[len(prefix) :]
    return None


def _resolve_log_dir():
    """Return the activity log directory path, matching cc-activity-log's logic."""
    return os.environ.get(
        "CC_MANAGER_LOGS",
        os.path.join(
            os.environ.get(
                "XDG_STATE_HOME",
                os.path.join(os.path.expanduser("~"), ".local", "state"),
            ),
            "cc-manager",
            "activity",
        ),
    )


def load_activity_log(session_id):
    """Load and parse a session's JSONL activity log file.

    Returns (entries, summary) where entries is a list of parsed JSON dicts
    and summary contains aggregate statistics. Returns ([], {}) if the file
    does not exist or is empty. Caps at 10,000 entries.
    """
    log_dir = _resolve_log_dir()
    log_path = os.path.join(log_dir, f"{session_id}.jsonl")
    if not os.path.isfile(log_path):
        return [], {}

    entries = []
    try:
        with open(log_path, errors="replace") as f:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                try:
                    entries.append(json.loads(line))
                except (json.JSONDecodeError, ValueError):
                    continue
                if len(entries) >= 10_000:
                    break
    except OSError:
        return [], {}

    if not entries:
        return [], {}

    tool_counts: dict[str, int] = {}
    stop_count = 0
    first_ts = None
    last_ts = None

    for e in entries:
        ts = e.get("ts")
        if ts is not None:
            if first_ts is None:
                first_ts = ts
            last_ts = ts

        event = e.get("event", "")
        if event == "tool":
            tool = e.get("tool", "unknown")
            tool_counts[tool] = tool_counts.get(tool, 0) + 1
        elif event == "stop":
            stop_count += 1

    sorted_tools = sorted(tool_counts.items(), key=lambda x: x[1], reverse=True)

    duration_str = ""
    if first_ts is not None and last_ts is not None:
        duration_str = format_duration(last_ts - first_ts)

    summary = {
        "total": len(entries),
        "tool_counts": sorted_tools,
        "stop_count": stop_count,
        "first_ts": first_ts,
        "last_ts": last_ts,
        "duration": duration_str,
    }

    return entries, summary


def list_activity_logs(sessions):
    """List all JSONL activity log files sorted by mtime descending.

    Returns a list of dicts with session_id, path, mtime, size, event_count,
    and optionally a title by cross-referencing with the running sessions list
    via resolve_session_id.
    """
    log_dir = _resolve_log_dir()
    if not os.path.isdir(log_dir):
        return []

    running_ids = {}
    for sess in sessions:
        sid = resolve_session_id(sess.get("tab_id"))
        if sid:
            running_ids[sid] = sess.get("title", "")

    entries = []
    for path in glob.glob(os.path.join(log_dir, "*.jsonl")):
        fname = os.path.basename(path)
        session_id = fname[:-6]
        try:
            stat = os.stat(path)
        except OSError:
            continue

        event_count = 0
        first_cwd = ""
        try:
            with open(path) as f:
                for line in f:
                    event_count += 1
                    if event_count == 1 and not running_ids.get(session_id):
                        with contextlib.suppress(json.JSONDecodeError, ValueError):
                            first_cwd = json.loads(line.strip()).get("cwd", "")
        except OSError:
            pass

        title = running_ids.get(session_id, "")
        if not title and first_cwd:
            project = os.path.basename(first_cwd.rstrip("/"))
            title = f"{project} (ended)"
        elif not title:
            title = "ended"

        entries.append(
            {
                "session_id": session_id,
                "path": path,
                "mtime": stat.st_mtime,
                "size": stat.st_size,
                "event_count": event_count,
                "title": title,
            }
        )

    entries.sort(key=lambda e: e["mtime"], reverse=True)
    return entries
