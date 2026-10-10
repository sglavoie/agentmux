#!/usr/bin/env python3
"""Asks the world's daemon to have the workspace show something, exactly as
the Dashboard does when a row is chosen: one workspace_view request on the
daemon's socket. It is how the driver moves the app without a key or a click.

usage: dview.py session RUNTIME_SESSION_ID | dview.py overview | dview.py dashboard
"""
import json, os, socket, sys

request = {"kind": sys.argv[1]}
if len(sys.argv) > 2:
    request["runtime_session_id"] = sys.argv[2]
s = socket.socket(socket.AF_UNIX)
s.settimeout(5)
s.connect(os.path.expanduser("~/.local/share/agent-mux/daemon.sock"))
s.sendall((json.dumps({"type": "workspace_view", "workspace_view": request}) + "\n").encode())
answer = json.loads(s.makefile().readline())
print(json.dumps({k: answer.get(k) for k in ("ok", "error", "workspace_view") if answer.get(k) is not None}))
sys.exit(0 if answer.get("ok") else 1)
