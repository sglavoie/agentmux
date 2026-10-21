#!/usr/bin/env python3
"""Deterministic demo agent. No model calls, credentials, or network access."""

import json
import os
from pathlib import Path
import subprocess
import sys

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


hook("SessionStart", source="startup")
hook("UserPromptSubmit", prompt="Prepare this project for release.")
print("\033[2J\033[H", end="")
print(f"\033[1;36m{harness.title()}  /  {project}\033[0m")
print("\033[2mScripted demo data · no live AI requests\033[0m\n")

if harness == "claude":
    print("> Add a health endpoint to the service.\n")
    print("  ✓ Reviewed the request handler")
    print("  ✓ Added GET /health")
    print("  ✓ Added a regression test\n")
    print("Ready for review. Open Checkout Changes to inspect the patch.")
    hook("Stop")
elif harness == "codex":
    print("> Polish the release page.\n")
    print("  ✓ Added installation instructions")
    print("  ✓ Simplified the feature list")
    print("  → Checking the layout at narrow widths…")
else:
    print("> Review the documentation before release.\n")
    print("  ✓ Checked links and command examples")
    print("  ✓ Added a troubleshooting section\n")
    print("May I run the documentation checks? Type approve to continue.")
    hook("PermissionRequest", tool_name="Bash", tool_input={"command": "make check-docs"})

print("\n\033[2mDemo commands: approve · review · work\033[0m", flush=True)
for line in sys.stdin:
    command = line.strip().lower()
    if command == "approve":
        hook("UserPromptSubmit", prompt="Run the checks.")
        print("\n  ✓ Documentation checks passed\n  Ready for review.", flush=True)
        hook("Stop")
    elif command == "review":
        hook("PermissionRequest", tool_name="Bash", tool_input={"command": "make test"})
        print("\nMay I run the checks? Type approve to continue.", flush=True)
    elif command == "work":
        hook("UserPromptSubmit", prompt="Continue the review.")
        print("\n  → Reviewing the next change…", flush=True)
    else:
        print("Use approve, review, or work (scripted demo).", flush=True)
