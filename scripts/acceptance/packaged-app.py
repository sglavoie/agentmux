#!/usr/bin/env python3
"""Headless acceptance of an extracted macOS release, without developer tools.

Runs only the bundle's amx and tmux in a disposable HOME. The scripted Claude
fixture executes the hook commands setup actually installed. No app window,
model, credentials, network request, or operator configuration is used.
Native first-launch/reopen checks are separate and require the test Mac.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import socket
import subprocess
import sys
import tempfile
import time


SESSION = "11111111-1111-4111-8111-111111111111"


def digest(path):
    checksum = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            checksum.update(chunk)
    return checksum.hexdigest()


def wait_for(description, read, accepts=bool, timeout=25):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            last = read()
            if accepts(last):
                return last
        except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
            last = str(error)
        time.sleep(0.2)
    raise RuntimeError(f"{description}: timed out; last observation: {last}")


class Acceptance:
    def __init__(self, root, report, apple_git_shim=False, workspace_setup=False):
        self.root, self.report = root, report
        self.workspace_setup = workspace_setup
        self.app = root / "Applications/Agent Mux.app"
        self.binary = self.app / "Contents/Resources/bin/amx"
        self.env = {
            "HOME": str(root / "home"), "TMPDIR": str(root / "tmp"),
            "PATH": f"{root}/bin:/usr/bin:/bin:/usr/sbin:/sbin",
            "SHELL": "/bin/zsh", "ZDOTDIR": str(root / "home"),
            "TERM": "xterm-256color", "LANG": "en_US.UTF-8",
        }
        self.checks = report["checks"]
        for name in ("home", "tmp", "bin", "project", "Applications"):
            (root / name).mkdir()
        # Git integration must degrade without triggering Apple's git shim
        # (which would ask to install Command Line Tools on a fresh Mac).
        for tool in ("git", "go", "just", "brew", "xcodebuild", "kitten"):
            if tool == "git" and apple_git_shim:
                continue
            stub = root / "bin" / tool
            stub.write_text("#!/bin/sh\nexit 127\n")
            stub.chmod(0o755)
        if apple_git_shim:
            # Exercise the real system Git selection without changing the
            # machine's selected Xcode or uninstalling any developer tools.
            self.env["DEVELOPER_DIR"] = str(root / "absent-developer-tools")
        # Login shells must neither restore Homebrew nor read real profiles.
        exports = "\n".join(f"export {k}={shlex.quote(v)}" for k, v in self.env.items())
        (root / "home/.zshenv").write_text(exports + "\n")
        self.agent = root / "bin/claude"
        self.agent.write_text("#!/bin/sh\necho '2.1.296 (acceptance fixture)'\n")
        self.agent.chmod(0o755)

    def run(self, *args, **kwargs):
        result = subprocess.run([str(a) for a in args], env=self.env, text=True,
                                capture_output=True, timeout=35, **kwargs)
        if result.returncode:
            raise RuntimeError(f"{args}: exit {result.returncode}\n{result.stdout}\n{result.stderr}")
        return result.stdout

    def amx(self, *args):
        return json.loads(self.run(self.binary, *args, "--json"))["data"]

    def passed(self, description, evidence=None):
        self.checks.append({"check": description, "evidence": evidence})
        print(f"PASS: {description}", flush=True)

    def extract(self, archive):
        destination = self.root / "extracted"
        if destination.exists():
            shutil.rmtree(destination)
        self.run("/usr/bin/ditto", "-x", "-k", archive, destination)
        app = destination / "Agent Mux.app"
        self.run("/usr/bin/codesign", "--verify", "--strict", "--deep", app)
        if not (app / "Contents/Resources/bin/amx").is_file():
            raise RuntimeError("The archive has no bundled amx")
        return app

    def snapshot(self):
        with socket.socket(socket.AF_UNIX) as client:
            client.settimeout(3)
            client.connect(str(self.root / "home/.local/share/agent-mux/daemon.sock"))
            request = {"type": "subscribe", "subscribe": {
                "protocol": 1, "client": "packaged-app-acceptance", "features": ["runtime_sessions"]}}
            client.sendall((json.dumps(request) + "\n").encode())
            stream = client.makefile()
            json.loads(stream.readline())
            return json.loads(stream.readline())["data"]

    def tmux(self, *args):
        status = self.amx("backend", "status")
        return self.run(status["tmux"]["path"], "-S",
                        self.root / f"tmp/agent-mux-{os.getuid()}/tmux.sock", *args)

    def send(self, pane, command):
        self.tmux("send-keys", "-t", pane, "-l", command)
        self.tmux("send-keys", "-t", pane, "Enter")

    def install_fixture(self):
        settings = json.loads((self.root / "home/.claude/settings.json").read_text())
        hook_cases = []
        for event in ("SessionStart", "UserPromptSubmit", "PermissionRequest", "Stop", "SessionEnd"):
            entries = settings["hooks"][event]
            command = next(h["command"] for group in entries for h in group["hooks"]
                           if "hook-indicator" in h.get("command", ""))
            if str(self.binary) not in command:
                raise RuntimeError(f"Hook {event} does not name bundled amx: {command}")
            payload = json.dumps({"session_id": SESSION, "hook_event_name": event,
                                  "cwd": str(self.root / "project"), "source": "startup",
                                  "tool_name": "Bash", "prompt": "fixture work"})
            hook_cases.append(f"{event}) printf '%s' {shlex.quote(payload)} | {command};;")
        self.agent.write_text("#!/bin/sh\n"
                              "case \"$1\" in --version) echo '2.1.296 (acceptance fixture)'; exit 0;; esac\n"
                              "hook() { case \"$1\" in\n" + "\n".join(hook_cases) + "\nesac; }\n"
                              "hook SessionStart\nprintf 'fixture agent pid=%s args=%s\\n' \"$$\" \"$*\"\n"
                              "while IFS= read -r command; do\ncase \"$command\" in\n"
                              "ask) hook PermissionRequest;; work) hook UserPromptSubmit;;\n"
                              "done) hook Stop;; quit) hook SessionEnd; exit 0;;\n"
                              "*) printf 'echo: %s\\n' \"$command\";; esac\ndone\n")
        # A transcript fixture stands in for the harness's own durable output.
        folder = self.root / "home/.claude/projects/acceptance-project"
        folder.mkdir(parents=True)
        records = [{"type": kind, "entrypoint": "cli", "sessionId": SESSION, "cwd": str(self.root / "project"),
                    "timestamp": "2026-10-30T12:00:00.000Z", "uuid": f"fixture-{kind}",
                    "message": {"role": kind, "content": f"Scripted {kind} message"}}
                   for kind in ("user", "assistant")]
        (folder / f"{SESSION}.jsonl").write_text("".join(json.dumps(row) + "\n" for row in records))

    def exercise(self, archive, update):
        shutil.copytree(self.extract(archive), self.app, symlinks=True)
        self.passed("archive extracted and bundle signature verified", digest(archive))
        for tool in ("amx", "tmux"):
            assert shutil.which(tool, path=self.env["PATH"]) is None, f"unexpected {tool} on PATH"
        missing = subprocess.run([str(self.binary), "project", "list", "--json"], env=self.env,
                                 capture_output=True, text=True, timeout=15)
        assert missing.returncode != 0 and json.loads(missing.stdout)["error"]["code"] == "config_missing"
        self.passed("fresh HOME reports config_missing; no separate amx or tmux on PATH")
        answers = f"claude\n\n\n{self.root}/project\nFirst project\n\nyes\n"
        setup_arguments = ("workspace-view", "setup") if self.workspace_setup else ("config", "setup")
        summary = self.run(self.binary, *setup_arguments, input=answers + ("\n" if self.workspace_setup else ""))
        assert "Hooks: synced" in summary
        if self.workspace_setup:
            assert "choose Start Daemon if offered" in summary and "then choose Launch Project" in summary, summary
            assert "run amx, press l" not in summary, summary
            self.passed("workspace setup establishes hosted context and returns to native launch controls")
        self.install_fixture()
        self.passed("guided setup creates config and hooks naming the bundled amx")
        if "DEVELOPER_DIR" in self.env:
            review = subprocess.run([str(self.binary), "session", "changes", "--directory",
                                     str(self.root / "project"), "--json"], env=self.env,
                                    capture_output=True, text=True, timeout=15)
            error = json.loads(review.stdout)["error"]
            assert review.returncode and error["code"] == "review_unavailable" and "git is unavailable" in error["message"], error
            self.passed("Apple Git without a usable developer directory reports unavailable without executing its installer shim")
        runtime_status = self.amx("backend", "status")["tmux"]
        assert runtime_status["verified"] and runtime_status["source"] == "bundled", runtime_status
        assert runtime_status["path"].startswith(str(self.root / "home/.local/share/agent-mux/runtime") + "/")
        self.passed("verified bundled tmux runs from an installed copy outside the app")
        launch = self.amx("project", "launch", "First project")
        runtime = launch["runtime_session_id"]
        attached = wait_for("agent live", lambda: self.amx("attach", runtime), lambda a: a["state"] == "live")
        pane = attached["pane"]
        wait_for("SessionStart delivered", self.snapshot, lambda s: any(
            x.get("runtime", {}).get("session_id") == runtime for x in s.get("sessions", [])))
        self.passed("project launches with bundled tmux and SessionStart reaches the daemon", runtime)
        self.send(pane, "ask")
        wait_for("permission attention", self.snapshot, lambda s: any(
            x.get("icon_state") == "key" and x.get("attention") for x in s.get("sessions", [])))
        self.passed("installed permission hook produces attention")
        self.send(pane, "done")
        wait_for("completion", self.snapshot, lambda s: any(
            x.get("icon_state") == "stop" for x in s.get("sessions", [])))
        self.passed("installed completion hook produces completed state")
        if update:
            before = self.amx("backend", "status")["server"]["generation"]
            pid = self.tmux("display-message", "-p", "-t", pane, "#{pane_pid}").strip()
            new = self.extract(update)
            assert digest(new / "Contents/MacOS/Agent Mux") != digest(self.app / "Contents/MacOS/Agent Mux"), "update must be a different app build"
            previous = self.root / "previous.app"
            self.app.rename(previous)
            shutil.copytree(new, self.app, symlinks=True)
            shutil.rmtree(previous)
            assert self.amx("backend", "status")["server"]["generation"] == before
            assert self.tmux("display-message", "-p", "-t", pane, "#{pane_pid}").strip() == pid
            self.send(pane, "still here")
            wait_for("input after update", lambda: self.tmux("capture-pane", "-p", "-t", pane),
                     lambda text: "echo: still here" in text)
            self.passed("replacing the app preserves server generation, agent PID, and working input")
        self.send(pane, "quit")
        wait_for("agent exit", lambda: self.tmux("display-message", "-p", "-t", pane, "#{pane_dead}"),
                 lambda text: text.strip() == "1")
        continued = self.amx("session", "continue", f"claude:{SESSION}")
        wait_for("native resume argv", lambda: self.tmux("capture-pane", "-p", "-J", "-t", ":"),
                 lambda text: "--resume" in text and SESSION in text)
        self.passed("closed conversation resumes with its recorded identity", continued.get("action"))

    def cleanup(self):
        failures = []
        for args in (("backend", "stop", "--yes"), ("daemon", "stop")):
            try:
                self.run(self.binary, *args)
            except (OSError, subprocess.SubprocessError, RuntimeError):
                pass
        for args, key in ((("backend", "status"), "running"), (("daemon", "status"), "alive")):
            try:
                status = subprocess.run([str(self.binary), *args, "--json"], env=self.env,
                                        capture_output=True, text=True, timeout=15)
                data = json.loads(status.stdout)["data"]
                value = data["server"][key] if key == "running" else data[key]
                if value:
                    failures.append(f"{args[0]} is still running")
            except (OSError, subprocess.SubprocessError, ValueError, KeyError) as error:
                failures.append(f"could not verify {args[0]} cleanup: {error}")
        return failures


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path, help="release ZIP containing Agent Mux.app")
    parser.add_argument("--update", type=Path, help="ZIP of a different build to replace it with")
    parser.add_argument("--apple-git-shim", action="store_true", help="use the system Git with an absent DEVELOPER_DIR instead of a Git fixture")
    parser.add_argument("--workspace-setup", action="store_true", help="exercise the actual workspace-view setup command and app-specific summary")
    parser.add_argument("--output", type=Path, required=True, help="JSON acceptance evidence")
    args = parser.parse_args()
    if sys.platform != "darwin":
        parser.error("requires macOS")
    report = {"schema_version": 1, "result": "failed", "fixture": "scripted Claude; seeded transcript",
              "scope": "headless packaged CLI; not native UI or a physically toolchain-free Mac",
              "archive_sha256": digest(args.archive),
              "update_archive_sha256": digest(args.update) if args.update else None,
              "apple_git_shim": args.apple_git_shim,
              "workspace_setup": args.workspace_setup,
              "checks": []}
    with tempfile.TemporaryDirectory(prefix="amxi-", dir="/tmp") as temporary:
        acceptance = Acceptance(Path(temporary).resolve(), report, apple_git_shim=args.apple_git_shim,
                                workspace_setup=args.workspace_setup)
        try:
            acceptance.exercise(args.archive.resolve(), args.update.resolve() if args.update else None)
            report["result"] = "passed"
        except Exception as error:
            report["error"] = str(error)
            raise
        finally:
            cleanup_failures = acceptance.cleanup()
            report["cleanup"] = {"result": "failed" if cleanup_failures else "passed", "errors": cleanup_failures}
            if cleanup_failures:
                report["result"] = "failed"
            args.output.parent.mkdir(parents=True, exist_ok=True)
            args.output.write_text(json.dumps(report, indent=2) + "\n")
    if report["result"] != "passed":
        raise RuntimeError(f"Acceptance did not pass: {report}")


if __name__ == "__main__":
    main()
