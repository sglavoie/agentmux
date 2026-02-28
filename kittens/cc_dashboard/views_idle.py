"""Idle alert notifications for dashboard sessions."""

import os
import shutil
import subprocess
import sys
import time

from cc_dashboard import config as _cfg
from cc_dashboard.remote.shared import _ICON_STATES, format_duration


def _find_alerter():
    """Locate the alerter binary, accounting for Kitty's restricted PATH."""
    path = shutil.which("alerter")
    if path:
        return path
    for candidate in ("/opt/homebrew/bin/alerter", "/usr/local/bin/alerter"):
        if os.path.isfile(candidate) and os.access(candidate, os.X_OK):
            return candidate
    return None


_ALERTER_BIN = _find_alerter()
_alerter_warned = False


def _send_notification(title, subtitle, message, group=None, timeout=10):
    """Send a macOS notification via alerter (non-blocking)."""
    global _alerter_warned
    if _ALERTER_BIN is None:
        if not _alerter_warned:
            _alerter_warned = True
            print(
                "cc-manager: alerter not found. Install with: "
                "brew install vjeantet/tap/alerter",
                file=sys.stderr,
            )
        return
    cmd = [
        _ALERTER_BIN,
        "--title",
        title,
        "--subtitle",
        subtitle,
        "--message",
        message,
        "--timeout",
        str(timeout),
    ]
    if group is not None:
        cmd += ["--group", str(group)]
    subprocess.Popen(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def _check_idle_notifications(sessions):
    """Send notifications for sessions that have been idle too long."""
    threshold = _cfg._default_idle_alert_minutes * 60
    if threshold <= 0 or _cfg._default_notify_idle != "on":
        return

    now = int(time.time())
    for s in sessions:
        if s["status"] not in ("idle", "stopped"):
            continue
        activity = s.get("activity")
        if not activity:
            continue
        idle_secs = now - activity["last_event_ts"]
        if idle_secs < threshold:
            continue

        cooldown_file = f"/tmp/ccm-notify-idle-{s['tab_id']}"
        last_event_ts = activity["last_event_ts"]

        if os.path.isfile(cooldown_file):
            try:
                cooldown_mtime = int(os.path.getmtime(cooldown_file))
            except OSError:
                cooldown_mtime = 0
            if cooldown_mtime >= last_event_ts:
                continue

        try:
            with open(cooldown_file, "w") as f:
                f.write("")
            os.utime(cooldown_file, (last_event_ts, last_event_ts))
        except OSError:
            pass

        _send_notification(
            title=s.get("title", "Session"),
            subtitle=f"{_ICON_STATES.get('stop', '')} Session idle",
            message=f"idle for {format_duration(idle_secs)}",
            group=s["tab_id"],
        )
