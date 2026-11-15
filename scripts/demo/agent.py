#!/usr/bin/env python3
"""Deterministic demo agent. No model calls, credentials, or network access."""

import json
import os
from pathlib import Path
import subprocess
import sys
import termios
import tty
import time
from datetime import datetime, timezone

if any(arg in ("--version", "-V", "-v") for arg in sys.argv[1:]):
    print("1.0.0 (Agent Mux demo fixture)")
    raise SystemExit(0)
if "--help" in sys.argv[1:] or "-h" in sys.argv[1:]:
    # Agent Mux probes Codex's help before choosing its launch flags. A help
    # probe must never emit session hooks or become an interactive agent.
    print("Scripted Agent Mux demo agent. Options: --version --no-daemon")
    raise SystemExit(0)

harness = Path(sys.argv[0]).name
project = Path.cwd().name
session_id = {"claude": "11111111", "codex": "22222222", "copilot": "33333333"}[harness]
session_id += "-1111-4111-8111-111111111111"


def hook(event, **fields):
    payload = {"session_id": session_id, "hook_event_name": event,
               "cwd": str(Path.cwd()), **fields}
    result = subprocess.run(
        [os.environ["AMX_BIN"], "hook-indicator", "--harness", harness,
         "--runtime-session", os.environ["AMX_RUNTIME_SESSION_ID"], "--strict", event],
        input=json.dumps(payload), text=True, capture_output=True, timeout=15,
    )
    if result.returncode:
        print("Demo hook failed:", result.stderr, flush=True)
        raise SystemExit(result.returncode)


def heading():
    print("\033[2J\033[H", end="")
    print(f"\033[1;36m{harness.title()}  /  {project}\033[0m")
    print("\033[2mScripted demo data · no live AI requests\033[0m\n")


def waiting():
    heading()
    print("  ✓ Reviewed the release changes\n")
    question = ("Would you like to run the following command?" if harness == "codex"
                else "Do you want to proceed?" if harness == "claude"
                else "Do you want to run this command?")
    print(f"│ {question}")
    print("│ make check   (scripted; no command is executed)")
    print("│ ❯ 1. Yes")
    print("│   2. No")
    print("│ Press enter to confirm · y to approve", flush=True)
    hook("PermissionRequest", tool_name="Bash")


# Saved, clearly labeled evidence lets Briefing exercise the real transcript
# readers without discovering anything outside this world's private HOME.
def timestamp():
    return datetime.now(timezone.utc).isoformat()


home = Path.home() / ("." + harness)
if harness == "claude":
    transcript = home / "projects" / str(Path.cwd()).replace("/", "-") / (session_id + ".jsonl")
    records = []
elif harness == "codex":
    transcript = home / "sessions" / ("rollout-demo-" + session_id + ".jsonl")
    records = [{"type": "session_meta", "timestamp": timestamp(), "payload": {
        "id": session_id, "timestamp": timestamp(), "cwd": str(Path.cwd())}}]
else:
    transcript = home / "session-state" / session_id / "events.jsonl"
    records = [{"type": "session.start", "timestamp": timestamp(), "data": {
        "sessionId": session_id, "startTime": timestamp(), "context": {"cwd": str(Path.cwd())}}}]
transcript.parent.mkdir(parents=True, exist_ok=True)
transcript.write_text("".join(json.dumps(record) + "\n" for record in records))


def message(role, text):
    text = "Scripted demo data · " + text
    if harness == "claude":
        record = {"type": role, "timestamp": timestamp(), "cwd": str(Path.cwd()),
                  "sessionId": session_id, "message": {"role": role, "content": text}}
    elif harness == "codex":
        record = {"type": "response_item", "timestamp": timestamp(), "payload": {
            "type": "message", "role": role, "content": [{"type": "output_text", "text": text}]}}
    else:
        record = {"type": role + ".message", "timestamp": timestamp(), "data": {"content": text}}
    with transcript.open("a") as stream:
        stream.write(json.dumps(record) + "\n")


message("user", "Prepare this project for release.")
message("assistant", "Reviewed the release changes. The health endpoint and regression test are ready for checkout review."
        if harness == "claude" else "Reviewed the release changes; checks need approval.")
hook("SessionStart", source="startup")
hook("UserPromptSubmit", prompt="Prepare this project for release.")
heading()
if harness == "claude":
    print("> Add a health endpoint to the service.\n")
    print("  ✓ Reviewed the request handler")
    print("  ✓ Added GET /health")
    print("  → Checking the regression test…", flush=True)
    hook("PreToolUse", tool_name="Bash")
elif harness == "codex":
    print("> Polish the release page.\n")
    print("  ✓ Added installation instructions")
    print("  ✓ Simplified the feature list")
    print("  ✓ Checked the layout at narrow widths\n")
    print("Ready for review.", flush=True)
    hook("Stop")
else:
    waiting()

# Real answer panels send one key, without a following newline. Keep this
# stand-in in cbreak mode just like a terminal agent; restore it on exit.
previous = termios.tcgetattr(sys.stdin)
try:
    tty.setcbreak(sys.stdin.fileno())
    while True:
        command = sys.stdin.read(1)
        if not command:
            break
        if command.lower() in ("y", "1", "\n", "\r"):
            # Keep the prompt stable long enough for the key-delivery receipt;
            # then model the short check, rather than completing synchronously.
            time.sleep(1)
            hook("UserPromptSubmit", prompt="Run the checks.")
            heading()
            print("  ✓ Release checks passed\n  Ready for review.", flush=True)
            message("assistant", "Release checks passed. Ready for review.")
            hook("Stop")
        elif command == "r":
            waiting()
finally:
    termios.tcsetattr(sys.stdin, termios.TCSADRAIN, previous)
