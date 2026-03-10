"""Activity counters and JSONL activity log helpers."""

import contextlib
import glob
import json
import os
import tempfile

from cc_dashboard.remote.shared import format_duration

_CCM_TMPDIR = tempfile.gettempdir()


def load_activity(tab_id):
    """Read and parse the activity counts file for a tab, returning a dict or None."""
    path = f"{_CCM_TMPDIR}/ccm-activity-{tab_id}.counts"
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
            "active_subagents": int(lines[10]) if len(lines) > 10 else 0,
            "total_subagents": int(lines[11]) if len(lines) > 11 else 0,
        }
    except (ValueError, IndexError):
        return None


def load_session_meta(session_id):
    """Read session-level metadata from the first line of a JSONL activity log.

    Returns a dict with 'model' and 'agent_type' keys if the first entry
    is a session_start event, or an empty dict otherwise.
    """
    if not session_id:
        return {}
    log_dir = _resolve_log_dir()
    log_path = os.path.join(log_dir, f"{session_id}.jsonl")
    try:
        with open(log_path) as f:
            first_line = f.readline().strip()
        if not first_line:
            return {}
        entry = json.loads(first_line)
        if entry.get("event") != "session_start":
            return {}
        return {
            "model": entry.get("model", ""),
            "agent_type": entry.get("agent_type", ""),
        }
    except (OSError, json.JSONDecodeError, ValueError):
        return {}


def cleanup_stale_activity(live_tab_ids):
    """Remove orphaned activity files whose tab_id is not in the live set."""
    live = set(str(tid) for tid in live_tab_ids)
    for path in glob.glob(f"{_CCM_TMPDIR}/ccm-activity-*.counts"):
        fname = os.path.basename(path)
        tab_id_str = fname.replace("ccm-activity-", "").replace(".counts", "")
        if tab_id_str not in live:
            with contextlib.suppress(OSError):
                os.remove(path)


def resolve_session_id(tab_id):
    """Resolve a Kitty tab_id to a Claude session_id via the tab-id cache files.

    Scans ccm-tabid-* files in the temp directory (created by
    cc-activity-log) to find one whose contents match the given
    tab_id, then extracts the session_id UUID from the filename.
    Returns None if no match is found.
    """
    tab_id_str = str(tab_id)
    for path in glob.glob(f"{_CCM_TMPDIR}/ccm-tabid-*"):
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
    subagent_start_count = 0
    subagent_stop_count = 0
    task_completed_count = 0
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
        elif event == "subagent_start":
            subagent_start_count += 1
        elif event == "subagent_stop":
            subagent_stop_count += 1
        elif event == "task_completed":
            task_completed_count += 1

    sorted_tools = sorted(tool_counts.items(), key=lambda x: x[1], reverse=True)

    duration_str = ""
    if first_ts is not None and last_ts is not None:
        duration_str = format_duration(last_ts - first_ts)

    summary = {
        "total": len(entries),
        "tool_counts": sorted_tools,
        "stop_count": stop_count,
        "subagent_start_count": subagent_start_count,
        "subagent_stop_count": subagent_stop_count,
        "task_completed_count": task_completed_count,
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
                    if (
                        not first_cwd
                        and event_count <= 5
                        and not running_ids.get(session_id)
                    ):
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


def search_all_logs(
    query, log_index, max_results=200, max_entries_per_file=5000, cols=120
):
    """Search across all activity log files for a case-insensitive substring match.

    Returns a list of result dicts with session_id, title, formatted_line,
    entry_index (line number within file), and ts.  Iterates log files in
    log_index order (most-recent first) and stops once max_results is reached.
    """
    from cc_dashboard.views_overlays_logs import _format_log_entry

    query_lower = query.lower()
    results = []
    total_scanned = 0

    for log_entry in log_index:
        session_id = log_entry["session_id"]
        path = log_entry["path"]
        title = log_entry.get("title", "") or session_id[:12]

        try:
            with open(path, errors="replace") as f:
                for entry_index, line in enumerate(f):
                    if entry_index >= max_entries_per_file:
                        break
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        entry = json.loads(line)
                    except (json.JSONDecodeError, ValueError):
                        continue

                    formatted = _format_log_entry(entry, cols)
                    total_scanned += 1

                    if query_lower in formatted.lower():
                        results.append(
                            {
                                "session_id": session_id,
                                "title": title,
                                "formatted_line": formatted,
                                "entry_index": entry_index,
                                "ts": entry.get("ts"),
                            }
                        )
                        if len(results) >= max_results:
                            return results, total_scanned
        except OSError:
            continue

    return results, total_scanned
