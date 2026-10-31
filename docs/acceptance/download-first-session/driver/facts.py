import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys

if os.environ.get("AMX_UI_REMOTE_EXECUTION") != "1":
    raise SystemExit("Run this driver on the approved test Mac with AMX_UI_REMOTE_EXECUTION=1")

spec = importlib.util.spec_from_file_location("packaged", "/tmp/packaged-app.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
root = Path("/tmp/amx-firstsession-1030").resolve()
metadata = json.loads((root / "world.json").read_text())
assert metadata["root"] == str(root)
world = module.Acceptance.__new__(module.Acceptance)
world.root, world.env = root, metadata["env"]
world.app = root / "Applications/Agent Mux.app"
world.binary = world.app / "Contents/Resources/bin/amx"

def facts():
    status = world.amx("backend", "status")
    registry = json.loads((root / "home/.local/share/agent-mux/runtime-sessions.json").read_text())
    snapshot = world.snapshot()
    current = [entry for entry in registry["sessions"] if entry["state"] == "live"]
    rows = [{key: row.get(key) for key in ("project", "icon_state", "attention", "runtime", "conversation")} for row in snapshot["sessions"]]
    captures = {}
    for entry in current:
        pane = entry["target"]["ref"]
        captures[entry["id"]] = world.tmux("capture-pane", "-p", "-J", "-t", pane)
    return {"app_pid": int((root / "app.pid").read_text()),
            "bundled_amx_sha256": module.digest(world.binary),
            "server": status["server"], "sessions": current, "rows": rows,
            "clients": world.tmux("list-clients", "-F", "#{client_pid} #{client_termname} #{pane_id}").splitlines(),
            "captures": captures}

verb = sys.argv[1]
if verb == "resumed":
    data = module.wait_for("native History resume", facts, lambda value: any(
        "--resume " + module.SESSION in capture for capture in value["captures"].values()))
    before = json.loads((root / "before-reopen.json").read_text())
    assert len(data["sessions"]) == 1 and data["sessions"][0]["id"] != before["sessions"][0]["id"]
    (root / "resumed-from-history.json").write_text(json.dumps(data, indent=2) + "\n")
    print("PASS: native History resumed the exact conversation in a new runtime session")
    sys.exit(0)
data = facts()
if verb == "record":
    label = sys.argv[2]
    assert label.replace("-", "").isalnum()
    (root / (label + ".json")).write_text(json.dumps(data, indent=2) + "\n")
    print(json.dumps(data, indent=2))
elif verb == "verify":
    before = json.loads((root / (sys.argv[2] + ".json")).read_text())
    assert data["server"]["generation"] == before["server"]["generation"], "server generation changed"
    assert [(x["id"], x["process"]) for x in data["sessions"]] == [(x["id"], x["process"]) for x in before["sessions"]], "agent identity changed"
    assert len(data["clients"]) == 1, "no single embedded terminal attached"
    client_pid = int(data["clients"][0].split()[0])
    ancestry = []
    while client_pid > 1 and client_pid not in ancestry:
        ancestry.append(client_pid)
        if client_pid == data["app_pid"]:
            break
        client_pid = int(subprocess.check_output(["ps", "-p", str(client_pid), "-o", "ppid="], text=True).strip())
    assert data["app_pid"] in ancestry, ("terminal is not descended from the test app", ancestry)
    print("PASS: server generation, agent PID/start identity, and embedded terminal preserved")
elif verb == "state":
    expected = sys.argv[2]
    data = module.wait_for("native session state " + expected, facts, lambda value:
        any(row["icon_state"] == expected for row in value["rows"]))
    print("PASS: native session state", expected)
elif verb == "input":
    assert any(sys.argv[2] in text for text in data["captures"].values()), data["captures"]
    print("PASS: terminal input reached the retained agent:", sys.argv[2])
