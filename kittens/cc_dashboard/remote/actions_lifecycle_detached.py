"""Detached subprocess helpers for lifecycle operations."""

import contextlib
import json
import os
import shutil
import subprocess

_DETACHED_RUNNER_SCRIPT = """
import json, os, subprocess, tempfile, time
steps = json.loads(os.environ["_CCM_STEPS"])
log_path = os.path.join(tempfile.gettempdir(), "ccm-detached.log")
def _log(msg):
    try:
        with open(log_path, "a") as f:
            f.write(time.strftime("%H:%M:%S ") + msg + "\\n")
    except OSError:
        pass
for i, step in enumerate(steps):
    cmd = step["cmd"]
    retry = step.get("retry", 0)
    retry_sleep = step.get("retry_sleep", 0.1)
    sleep_after = step.get("sleep", 0)
    try:
        if retry:
            ok = False
            for attempt in range(retry + 1):
                r = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
                if r.returncode == 0:
                    ok = True
                    break
                time.sleep(retry_sleep)
            if not ok:
                e = r.stderr.strip()
                _log(f"step {i} failed after {retry+1} attempts: "
                     f"{cmd[3:6]} rc={r.returncode} err={e}")
        else:
            r = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
            if r.returncode != 0:
                e = r.stderr.strip()
                _log(f"step {i} failed: {cmd[3:6]} "
                     f"rc={r.returncode} err={e}")
    except subprocess.TimeoutExpired:
        _log(f"step {i} timed out: {cmd[3:6]}")
    except OSError as e:
        _log(f"step {i} OSError: {cmd[3:6]} {e}")
    if "launch" in cmd and "--type=tab" in cmd and r.returncode == 0:
        wid = r.stdout.strip()
        if wid:
            for fs in steps[i+1:]:
                fs["cmd"] = [c.replace("__WINDOW_ID__", wid) for c in fs["cmd"]]
    if sleep_after:
        time.sleep(sleep_after)
"""


def _run_detached_steps(steps):
    """Execute a list of step dicts in a fully detached subprocess."""
    env = os.environ.copy()
    env["_CCM_STEPS"] = json.dumps(steps)

    python = shutil.which("python3") or shutil.which("python")
    if not python:
        return
    with contextlib.suppress(OSError):
        subprocess.Popen(
            [python, "-c", _DETACHED_RUNNER_SCRIPT],
            start_new_session=True,
            env=env,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )


def _spawn_detached_close(socket, tab_id):
    """Spawn a detached subprocess to close a tab after the dashboard exits."""
    steps = [
        {
            "cmd": [
                "kitty",
                "@",
                "--to",
                socket,
                "close-tab",
                "--match",
                f"id:{tab_id}",
            ],
            "sleep": 0.3,
        }
    ]
    _run_detached_steps(steps)
