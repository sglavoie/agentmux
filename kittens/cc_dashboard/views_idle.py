"""Idle alert notifications for dashboard sessions."""

import contextlib
import os
import shutil
import subprocess
import sys
import tempfile
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
_active_notifications: list[tuple[subprocess.Popen, str, str]] = []


def _reap_notifications():
    """Reap finished notification subprocesses and focus tab on click."""
    global _active_notifications
    still_running: list[tuple[subprocess.Popen, str, str]] = []
    for proc, socket, tab_id in _active_notifications:
        if proc.poll() is None:
            still_running.append((proc, socket, tab_id))
            continue
        # Process finished — check if user clicked the notification
        if socket and tab_id:
            try:
                stdout = proc.stdout.read() if proc.stdout else b""
                result = stdout.decode().strip()
            except Exception:
                result = ""
            if result and result not in ("@TIMEOUT", "@CLOSED"):
                with contextlib.suppress(OSError):
                    subprocess.Popen(
                        [
                            "kitty",
                            "@",
                            "--to",
                            socket,
                            "focus-tab",
                            "--match",
                            f"id:{tab_id}",
                        ],
                        stdout=subprocess.DEVNULL,
                        stderr=subprocess.DEVNULL,
                    )
    _active_notifications = still_running


def _send_notification(
    title,
    subtitle,
    message,
    group=None,
    timeout=10,
    socket="",
    tab_id="",
):
    """Send a macOS notification via alerter (non-blocking)."""
    global _alerter_warned
    _reap_notifications()
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
    proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    _active_notifications.append((proc, socket, tab_id))


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

        cooldown_file = f"{tempfile.gettempdir()}/ccm-notify-idle-{s['tab_id']}"
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
            socket=s.get("socket", ""),
            tab_id=str(s.get("tab_id", "")),
        )
