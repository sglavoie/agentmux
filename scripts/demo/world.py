#!/usr/bin/env python3
"""Prepare and manage an isolated, local Agent Mux demo. Never used by CI."""

import argparse
import json
import os
from pathlib import Path
import plistlib
import shlex
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time

HERE = Path(__file__).resolve().parent
REPO = HERE.parent.parent
MARKER = "agent-mux-desktop-demo-v1"


def run(args, **kwargs):
    return subprocess.run([str(a) for a in args], check=True, text=True, **kwargs)


def environment(root):
    # Start from an allowlist, not the operator's agent/profile environment.
    home = root / "home"
    return {
        "HOME": str(home), "TMPDIR": str(root / "tmp"),
        "PATH": f"{root}/libexec:{root}/bin:/usr/bin:/bin:/usr/sbin:/sbin",
        "SHELL": "/bin/zsh", "TERM": "xterm-256color", "LANG": "en_US.UTF-8",
        "USER": os.environ.get("USER", "demo"),
        "XDG_CONFIG_HOME": str(home / ".config"),
        "XDG_DATA_HOME": str(home / ".local/share"),
        "XDG_STATE_HOME": str(home / ".local/state"),
        "AMX_CONFIG": str(home / ".config/agent-mux/config.toml"),
        "AMX_BIN": str(root / "bin/amx"),
        "AMX_TMUX": str(root / "tmux/bin/tmux"),
    }


def load(root):
    root = root.resolve()
    data = json.loads((root / "demo.json").read_text())
    if data.get("kind") != MARKER or data.get("root") != str(root):
        raise ValueError("Not an owned Agent Mux demo directory")
    return root, data


def amx(root, *args, **kwargs):
    return run([root / "bin/amx", *args], env=environment(root), **kwargs)


def snapshot(root):
    with socket.socket(socket.AF_UNIX) as client:
        client.settimeout(3)
        client.connect(str(root / "home/.local/share/agent-mux/daemon.sock"))
        request = {"type": "subscribe", "subscribe": {
            "protocol": 1, "client": "readme-demo", "features": ["runtime_sessions"]}}
        client.sendall((json.dumps(request) + "\n").encode())
        stream = client.makefile()
        json.loads(stream.readline())
        return json.loads(stream.readline())["data"]


def prepare(args):
    if sys.platform != "darwin":
        raise ValueError("The desktop demo requires macOS")
    binary = args.amx.resolve()
    runtime = REPO / "macos/Vendor/tmux"
    if not binary.is_file() or not (runtime / "bin/tmux").is_file():
        raise ValueError("Run just build and just mac-tmux first")
    # Short paths also leave room for macOS Unix socket names.
    root = Path(tempfile.mkdtemp(prefix="amxd-", dir="/tmp")).resolve()
    data = {"kind": MARKER, "root": str(root),
            "bundle_id": "io.github.sglavoie.agentmux.demo." + root.name.replace("_", "-"),
            "source_app": str(args.app.resolve())}
    (root / "demo.json").write_text(json.dumps(data, indent=2) + "\n")
    for name in ("bin", "libexec", "tmp", "logs", "home/.config/agent-mux", "projects"):
        (root / name).mkdir(parents=True, exist_ok=True)
    shutil.copy2(binary, root / "libexec/amx")
    shutil.copytree(runtime, root / "tmux", symlinks=True)
    env = environment(root)
    # Ghostty may start commands through login(1). Restore the isolated home
    # at the executable boundary as well as in the app's child environment.
    exports = "\n".join(f"export {key}={shlex.quote(value)}" for key, value in env.items())
    wrapper = root / "bin/amx"
    wrapper.write_text(f'#!/bin/sh\n{exports}\nexec {shlex.quote(str(root / "libexec/amx"))} "$@"\n')
    wrapper.chmod(0o755)
    # The launcher uses an interactive login shell. Never let it find the
    # operator's actual agent CLI or load the operator's shell configuration.
    (root / "home/.zshenv").write_text(exports + "\n")
    (root / "home/.zshrc").write_text(exports + "\n")
    agent = root / "bin/demo-agent.py"
    agent.write_text(f"#!{sys.executable}\n" + (HERE / "agent.py").read_text().split("\n", 1)[1])
    agent.chmod(0o755)
    config = ['[defaults]', 'launch_backend = "tmux"', 'sort = "alpha"',
              '[defaults.asl]', 'enabled = false', '[defaults.notifications]']
    config += [f'{key} = false' for key in (
        'error', 'permission', 'elicitation', 'stop', 'stop_failure', 'stale_active', 'hook_silence')]
    config += ['[harness_emojis]', 'claude = "🟠"', 'codex = "🔵"', 'copilot = "🟣"']
    for harness, project, color in (
        ("claude", "api-service", "yellow"),
        ("codex", "release-page", "blue"),
        ("copilot", "documentation", "purple"),
    ):
        (root / "bin" / harness).symlink_to(agent.name)
        profile_home = root / "home" / ("." + harness)
        profile_home.mkdir()
        checkout = root / "projects" / project
        checkout.mkdir()
        (checkout / "README.md").write_text(f"# {project}\n\nA synthetic project for the Agent Mux demo.\n")
        source = checkout / "server.py"
        source.write_text('def handle(path):\n    return {"status": 404}\n')
        git_env = {**env, "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null"}
        run(["/usr/bin/git", "init", "-q", "-b", "main", checkout], env=git_env)
        run(["/usr/bin/git", "-C", checkout, "add", "."], env=git_env)
        run(["/usr/bin/git", "-C", checkout, "-c", "user.name=Demo",
             "-c", "user.email=demo@example.invalid", "-c", "commit.gpgsign=false",
             "commit", "-qm", "Initial demo project"], env=git_env)
        source.write_text('def handle(path):\n    if path == "/health":\n        return {"status": 200}\n    return {"status": 404}\n')
        if project == "api-service":
            (checkout / "test_server.py").write_text(
                'from server import handle\n\n\n'
                'def test_health_endpoint():\n'
                '    assert handle("/health") == {"status": 200}\n\n\n'
                'def test_unknown_route():\n'
                '    assert handle("/missing") == {"status": 404}\n'
            )
        config += ['[[profiles]]', f'name = "{harness}"', f'harness = "{harness}"',
                   f'home_dir = {json.dumps(str(profile_home))}', f'color = "{color}"',
                   '[[projects]]', f'title = "{project}"',
                   f'directory = {json.dumps(str(checkout))}', f'profile = "{harness}"']
    Path(env["AMX_CONFIG"]).write_text("\n".join(config) + "\n")
    amx(root, "config", "validate", stdout=subprocess.DEVNULL)
    # The demo profiles have real managed hook files as well as the stand-ins'
    # direct hook events, so configuration diagnostics remain meaningful.
    amx(root, "config", "hooks-sync", stdout=subprocess.DEVNULL)
    print(root)


def start(root):
    status = subprocess.run([str(root / "bin/amx"), "daemon", "status"],
                            env=environment(root), capture_output=True, text=True)
    if "ready: true" in status.stdout:
        raise ValueError("Demo already running; stop it before starting again")
    with (root / "logs/daemon.log").open("a") as log:
        subprocess.Popen([str(root / "bin/amx"), "daemon", "start"],
                         env=environment(root), stdin=subprocess.DEVNULL,
                         stdout=log, stderr=log, start_new_session=True)
    try:
        deadline = time.monotonic() + 20
        while True:
            try:
                snapshot(root)
                break
            except (OSError, ValueError, KeyError):
                if time.monotonic() > deadline:
                    raise TimeoutError("Demo daemon did not become ready; see logs/daemon.log")
                time.sleep(0.25)
        for project in ("api-service", "release-page", "documentation"):
            amx(root, "project", "launch", project, "--backend", "tmux", "--json",
                stdout=subprocess.DEVNULL)
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            state = snapshot(root)
            states = {session.get("project"): session.get("icon_state")
                      for session in state.get("sessions", [])}
            if states == {"api-service": "stop", "release-page": "bolt", "documentation": "key"}:
                print(json.dumps(state, indent=2))
                return
            time.sleep(0.25)
        raise TimeoutError("Expected stopped, working, and waiting demo sessions; inspect logs and pane output")
    except BaseException:
        stop(root)
        raise


def launch_app(root, data):
    # Explicit command: prepare/start do not open windows or change focus.
    if (REPO / "macos/build/.demo-remote-only").exists() or (
        os.environ.get("AMX_UI_TEST_REMOTE_ONLY") == "1"
        and os.environ.get("AMX_UI_REMOTE_EXECUTION") != "1"
    ):
        raise ValueError("UI is disabled on this Mac; launch the demo on the remote test Mac")
    app = root / "Agent Mux Demo.app"
    if (root / "app.pid").exists():
        raise ValueError("Stop the previous demo app before launching again")
    if len(snapshot(root).get("sessions", [])) != 3:
        raise ValueError("Start the demo sessions before launching the app")
    if not app.exists():
        run(["/usr/bin/ditto", data["source_app"], app])
        info = app / "Contents/Info.plist"
        with info.open("rb") as stream:
            plist = plistlib.load(stream)
        plist["CFBundleIdentifier"] = data["bundle_id"]
        with info.open("wb") as stream:
            plistlib.dump(plist, stream)
        # Only the outer bundle changed. Preserve nested executable signatures
        # and the bundled runtime's checksums.
        run(["/usr/bin/codesign", "--force", "--sign", "-", app])
    for key, value in {"amxBinaryPath": str(root / "bin/amx"),
                       "amxHotKeyDashboard": "", "amxHotKeyNextAttention": ""}.items():
        run(["defaults", "write", data["bundle_id"], key, "-string", value])
    for key, value in {"amxUseAppNotifications": "false", "amxOpenWorkspaceAtLaunch": "true"}.items():
        run(["defaults", "write", data["bundle_id"], key, "-bool", value])
    run(["defaults", "write", data["bundle_id"], "amxEnvironment", "-dict",
         *[item for pair in environment(root).items() for item in pair]])
    with (root / "logs/app.log").open("a") as log:
        process = subprocess.Popen([str(app / "Contents/MacOS/Agent Mux")],
                                   env=environment(root), stdin=subprocess.DEVNULL,
                                   stdout=log, stderr=log, start_new_session=True)
    (root / "app.pid").write_text(str(process.pid))
    print(f"Demo app launched: {process.pid}")


def stop(root):
    # Every control command uses the demo's HOME and explicit executable.
    # Never kill processes by a generic name or touch the installed app.
    pid_file = root / "app.pid"
    if pid_file.exists():
        pid = int(pid_file.read_text())
        process = subprocess.run(["/bin/ps", "-p", str(pid), "-o", "command="],
                                 capture_output=True, text=True)
        expected = str(root / "Agent Mux Demo.app/Contents/MacOS/Agent Mux")
        if process.returncode == 0:
            if process.stdout.strip() != expected:
                raise ValueError("Saved app PID belongs to another process; refusing to signal it")
            os.kill(pid, signal.SIGTERM)
            deadline = time.monotonic() + 10
            while subprocess.run(["/bin/ps", "-p", str(pid)], capture_output=True).returncode == 0:
                if time.monotonic() > deadline:
                    raise TimeoutError("Demo app did not stop; session cleanup deferred")
                time.sleep(0.1)
        pid_file.unlink()
    amx(root, "backend", "stop", "--yes", stdout=subprocess.DEVNULL)
    result = subprocess.run([str(root / "bin/amx"), "daemon", "stop"],
                            env=environment(root), capture_output=True, text=True, timeout=20)
    if result.returncode and "not running" not in result.stderr + result.stdout:
        raise ValueError(f"Could not stop demo daemon: {result.stderr.strip()}")
    deadline = time.monotonic() + 10
    while True:
        try:
            snapshot(root)
        except (OSError, ValueError):
            break
        if time.monotonic() > deadline:
            raise TimeoutError("Demo daemon still answers after stop")
        time.sleep(0.1)
    if (root / "Agent Mux Demo.app").exists():
        _, data = load(root)
        subprocess.run(["defaults", "delete", data["bundle_id"]], capture_output=True)
    print(f"Demo sessions and daemon stopped: {root}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    prep = sub.add_parser("prepare", help="Create a fresh world; starts no processes or UI")
    prep.add_argument("--amx", type=Path, default=REPO / "amx")
    prep.add_argument("--app", type=Path, default=REPO / "macos/build/Agent Mux.app")
    for name in ("start", "status", "stop", "launch-app"):
        sub.add_parser(name).add_argument("world", type=Path)
    args = parser.parse_args()
    if args.command == "prepare":
        prepare(args)
        return
    root, data = load(args.world)
    if args.command == "start":
        start(root)
    elif args.command == "status":
        print(json.dumps(snapshot(root), indent=2))
    elif args.command == "launch-app":
        launch_app(root, data)
    else:
        stop(root)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        print(f"demo: {error}", file=sys.stderr)
        raise SystemExit(1)
