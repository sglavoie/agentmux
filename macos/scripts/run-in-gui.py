#!/usr/bin/env python3
"""Run a local hosted-test command in the current user's GUI login session.

Xcode's LaunchServices launcher can abort in a Background bootstrap session.
Use a temporary, invocation-owned LaunchAgent; never switch users or hosts.
"""

import os
from pathlib import Path
import plistlib
import re
import signal
import subprocess
import sys
import tempfile
import time
import uuid


def launchctl(*args):
    return subprocess.run(["/bin/launchctl", *args], capture_output=True, text=True)


def run(command):
    domain = f"gui/{os.getuid()}"
    if launchctl("print", domain).returncode:
        raise RuntimeError("No GUI login session is available. Log in to this Mac's desktop and retry mac-test.")
    label = f"io.github.sglavoie.agentmux.test-{uuid.uuid4()}"
    service = f"{domain}/{label}"
    # The private directory also protects the forwarded environment in the plist.
    with tempfile.TemporaryDirectory(prefix="agentmux-gui-test-") as temporary:
        directory = Path(temporary)
        stdout, stderr = directory / "stdout", directory / "stderr"
        stdout.touch()
        stderr.touch()
        plist = directory / "job.plist"
        plist.write_bytes(plistlib.dumps({
            "Label": label,
            "ProgramArguments": command,
            "WorkingDirectory": os.getcwd(),
            "EnvironmentVariables": dict(os.environ),
            "RunAtLoad": True,
            "StandardOutPath": str(stdout),
            "StandardErrorPath": str(stderr),
        }))
        loaded = False
        try:
            result = launchctl("bootstrap", domain, str(plist))
            if result.returncode:
                raise RuntimeError(f"Cannot start tests in the GUI login session: {result.stderr.strip()}")
            loaded = True
            started = False
            deadline = time.monotonic() + 30
            while True:
                status = launchctl("print", service)
                if status.returncode:
                    raise RuntimeError("The temporary GUI test job disappeared before reporting an exit status.")
                if re.search(r"^\tstate = running$", status.stdout, re.MULTILINE):
                    started = True
                if re.search(r"^\tstate = not running$", status.stdout, re.MULTILINE):
                    code = re.search(r"^\tlast exit code = (\d+)\b", status.stdout, re.MULTILINE)
                    if code:
                        return int(code.group(1))
                    killed = re.search(r"^\tlast terminating signal = (.+)$", status.stdout, re.MULTILINE)
                    if killed:
                        number = killed.group(1).rsplit(": ", 1)[-1]
                        if number.isdigit():
                            return 128 + int(number)
                        raise RuntimeError(f"GUI test job terminated by {killed.group(1)}.")
                if not started and time.monotonic() >= deadline:
                    raise RuntimeError("GUI test job did not start within 30 seconds. Check the desktop login session.")
                time.sleep(0.25)
        finally:
            # Only unload our unique job, including when the caller is cancelled.
            # launchd also terminates this job's remaining process group.
            if loaded:
                result = launchctl("bootout", service)
                if result.returncode and launchctl("print", service).returncode == 0:
                    raise RuntimeError(f"Could not unload temporary GUI test job {service}.")
            sys.stdout.buffer.write(stdout.read_bytes())
            sys.stdout.buffer.flush()
            sys.stderr.buffer.write(stderr.read_bytes())
            sys.stderr.buffer.flush()


def interrupted(signum, _frame):
    raise SystemExit(128 + signum)


def main():
    if len(sys.argv) < 2:
        print("usage: run-in-gui.py COMMAND [ARG ...]", file=sys.stderr)
        return 2
    for signum in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(signum, interrupted)
    try:
        return run(sys.argv[1:])
    except (OSError, RuntimeError) as error:
        print(f"gui-test: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
