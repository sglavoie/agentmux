import importlib.util
import json
import os
from pathlib import Path
import plistlib
import shlex
import shutil
import subprocess
import sys

if os.environ.get("AMX_UI_REMOTE_EXECUTION") != "1":
    raise SystemExit("Run this acceptance driver on the approved test Mac over SSH with AMX_UI_REMOTE_EXECUTION=1")

spec = importlib.util.spec_from_file_location("packaged", "/tmp/packaged-app.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
root = Path("/tmp/amx-firstsession-1030").resolve()
domain = "io.github.sglavoie.agentmux.firstsession1030"
archive = Path("/tmp/Agent-Mux-0.10.0-candidate-macos-arm64.zip")

if sys.argv[1] == "prepare":
    if root.exists():
        raise SystemExit("world already exists")
    if subprocess.run(["defaults", "read", domain], capture_output=True).returncode == 0:
        raise SystemExit("test defaults already exist")
    root.mkdir()
    report = {"checks": []}
    world = module.Acceptance(root, report)
    shutil.copytree(world.extract(archive), world.app, symlinks=True)
    info = world.app / "Contents/Info.plist"
    with info.open("rb") as stream:
        data = plistlib.load(stream)
    data["CFBundleIdentifier"] = domain
    with info.open("wb") as stream:
        plistlib.dump(data, stream)
    world.run("/usr/bin/codesign", "--force", "--sign", "-", world.app)
    shim = root / "shim/amx"
    shim.parent.mkdir()
    exports = "\n".join(f"export {key}={shlex.quote(value)}" for key, value in world.env.items())
    shim.write_text("#!/bin/sh\n" + exports + "\nexec " + shlex.quote(str(world.binary)) + ' "$@"\n')
    shim.chmod(0o755)
    for key, value in (("amxBinaryPath", str(shim)), ("amxHotKeyDashboard", ""), ("amxHotKeyNextAttention", "")):
        subprocess.run(["defaults", "write", domain, key, "-string", value], check=True)
    subprocess.run(["defaults", "write", domain, "amxUseAppNotifications", "-bool", "false"], check=True)
    (root / "world.json").write_text(json.dumps({"root": str(root), "domain": domain, "env": world.env}))
    print("prepared", root)
else:
    data = json.loads((root / "world.json").read_text())
    assert data["root"] == str(root) and data["domain"] == domain
    world = module.Acceptance.__new__(module.Acceptance)
    world.root, world.env = root, data["env"]
    world.app = root / "Applications/Agent Mux.app"
    world.binary = world.app / "Contents/Resources/bin/amx"
    world.agent = root / "bin/claude"
    if sys.argv[1] == "start":
        if (root / "app.pid").exists():
            old_pid = int((root / "app.pid").read_text())
            running = subprocess.run(["ps", "-p", str(old_pid), "-o", "comm="], capture_output=True, text=True)
            if running.stdout.strip() == str(world.app / "Contents/MacOS/Agent Mux"):
                raise SystemExit("the isolated app is already running")
        with (root / "app.log").open("a") as log:
            process = subprocess.Popen([str(world.app / "Contents/MacOS/Agent Mux")], env=world.env,
                                       stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True,
                                       cwd=root / "project")
        (root / "app.pid").write_text(str(process.pid))
        print("started", process.pid)
    elif sys.argv[1] == "fixture":
        world.install_fixture()
        print("installed agent fixture using generated hooks")
    elif sys.argv[1] == "facts":
        for args in (("daemon", "status", "--json"), ("backend", "status", "--json"), ("project", "list", "--json")):
            result = subprocess.run([str(world.binary), *args], env=world.env, capture_output=True, text=True)
            print(result.stdout.strip())
        registry = root / "home/.local/share/agent-mux/runtime-sessions.json"
        if registry.exists():
            print(registry.read_text())
    elif sys.argv[1] == "quit":
        subprocess.run(["osascript", "-e", f'tell application id "{domain}" to quit'], check=True)
    elif sys.argv[1] == "update":
        pid = int((root / "app.pid").read_text())
        module.wait_for("test app quit", lambda: subprocess.run(["ps", "-p", str(pid), "-o", "comm="], capture_output=True).returncode != 0)
        new = world.extract(archive)
        info = new / "Contents/Info.plist"
        with info.open("rb") as stream:
            plist = plistlib.load(stream)
        plist["CFBundleIdentifier"] = domain
        with info.open("wb") as stream:
            plistlib.dump(plist, stream)
        world.run("/usr/bin/codesign", "--force", "--sign", "-", new)
        staged = root / "Applications/.Agent Mux.app.installing"
        previous = root / "Applications/.Agent Mux.app.previous"
        shutil.copytree(new, staged, symlinks=True)
        world.app.rename(previous)
        staged.rename(world.app)
        shutil.rmtree(previous)
        print("replaced app; new bundled amx sha256", module.digest(world.binary))
    elif sys.argv[1] == "cleanup":
        if (root / "app.pid").exists():
            old_pid = int((root / "app.pid").read_text())
            running = subprocess.run(["ps", "-p", str(old_pid), "-o", "comm="], capture_output=True, text=True)
            if running.stdout.strip() == str(world.app / "Contents/MacOS/Agent Mux"):
                subprocess.run(["osascript", "-e", f'tell application id "{domain}" to quit'], capture_output=True)
        errors = world.cleanup()
        print("cleanup", errors)
        subprocess.run(["defaults", "delete", domain], capture_output=True)
        plist = Path.home() / f"Library/Preferences/{domain}.plist"
        if plist.exists():
            plist.unlink()
        if (root / "app.pid").exists():
            old_pid = int((root / "app.pid").read_text())
            module.wait_for("test app quit", lambda: subprocess.run(
                ["ps", "-p", str(old_pid), "-o", "comm="], capture_output=True).returncode != 0)
        report = {"result": "failed" if errors else "passed", "errors": errors,
                  "app_stopped": True, "own_preferences_removed": not plist.exists()}
        (root / "cleanup.json").write_text(json.dumps(report, indent=2) + "\n")
        if errors:
            raise SystemExit("isolated backend or daemon cleanup failed")
        print("world processes and preferences stopped; files retained for evidence")
