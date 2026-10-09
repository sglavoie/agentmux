"""Drive the Session 2 terminal compatibility matrix end to end.

One GhosttyHarness.app viewer at a time attaches, through `tmux attach`, to a
session on a private tmux server (its own socket and config under the run
directory). The deterministic cases run against scripts/probe.py; the live
cases run Claude, Codex, and Copilot, each in its own tmux session, ask each a
question, kill the viewer with SIGKILL, and reattach a new viewer to the same
agent process. Every observation is written to <run>/results.json, with the
captures and logs it names beside it. Nothing outside the run directory is
written.

    python3 -I scripts/run_matrix.py --tmux PATH [--agents claude,codex,copilot] [--skip-probe]
"""

from __future__ import annotations

import argparse
import datetime
import hashlib
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import time
from pathlib import Path

HARNESS_DIR = Path(__file__).resolve().parent.parent
MACOS_DIR = HARNESS_DIR.parent
CACHE = Path(os.environ.get("AMX_MACOS_CACHE", MACOS_DIR / ".cache")) / "ghostty-harness"
APP = CACHE / "build/Debug/GhosttyHarness.app/Contents/MacOS/GhosttyHarness"
PROBE = HARNESS_DIR / "scripts/probe.py"
TMUX_CONF = HARNESS_DIR / "tmux.conf"

# Variables that would tie a test agent to the session running this driver:
# the outer agent's identity, and Kitty's, which Agent Mux hooks report against.
DROPPED_ENV = re.compile(
    r"^(CLAUDECODE|CLAUDE_CODE_.*|CLAUDE_PID|CLAUDE_EFFORT|CODEX_CI|CODEX_SESSION_ID|CODEX_THREAD_ID"
    r"|ASL_.*|KITTY_.*|TMUX|TMUX_PANE|TERM|TERMINFO|TERM_PROGRAM.*|COLORTERM|AMX_.*)$"
)

AGENTS = {
    "claude": ["claude"],
    "codex": ["codex"],
    "copilot": ["copilot"],
}
# Asked so the answer never appears in the question.
QUESTION = "Reply with only the uppercase concatenation of the words amx, harness and ok. No other text."
ANSWER = "AMXHARNESSOK"
FOLLOW_UP = "Now reply with only the lowercase concatenation of the words amx, reattached and ok."
FOLLOW_UP_ANSWER = "amxreattachedok"


class Failure(Exception):
    pass


def clean_env() -> dict[str, str]:
    return {k: v for k, v in os.environ.items() if not DROPPED_ENV.match(k)}


def wait_for(predicate, timeout: float, what: str, interval: float = 0.25):
    deadline = time.monotonic() + timeout
    while True:
        value = predicate()
        if value:
            return value
        if time.monotonic() > deadline:
            raise Failure(f"timed out after {timeout:.0f}s waiting for {what}")
        time.sleep(interval)


class Tmux:
    def __init__(self, binary: str, run_dir: Path, env: dict[str, str]):
        self.binary = binary
        # A short socket path: sun_path is 104 bytes on macOS.
        self.socket = Path(f"/tmp/amx-gh-{os.getpid()}.sock")
        self.run_dir = run_dir
        self.env = env

    @property
    def base(self) -> list[str]:
        return [self.binary, "-S", str(self.socket), "-f", str(TMUX_CONF)]

    def run(self, *args: str, check: bool = True) -> str:
        proc = subprocess.run(self.base + list(args), capture_output=True, text=True, env=self.env)
        if check and proc.returncode != 0:
            raise Failure(f"tmux {' '.join(args)} failed: {proc.stderr.strip()}")
        return proc.stdout

    def version(self) -> str:
        return subprocess.run([self.binary, "-V"], capture_output=True, text=True).stdout.strip()

    def new_session(self, name: str, argv: list[str], cwd: Path) -> None:
        self.run("new-session", "-d", "-s", name, "-x", "120", "-y", "36", "-c", str(cwd), "--", *argv)

    def fmt(self, target: str, fmt: str) -> str:
        return self.run("display-message", "-p", "-t", target, fmt).strip()

    def capture(self, target: str, path: Path | None = None) -> str:
        text = self.run("capture-pane", "-p", "-t", target)
        if path:
            path.write_text(text)
        return text

    def has_session(self, name: str) -> bool:
        return subprocess.run(self.base + ["has-session", "-t", name], capture_output=True, env=self.env).returncode == 0

    def attached(self, name: str) -> int:
        return int(self.fmt(name, "#{session_attached}") or 0)

    def kill_server(self) -> None:
        subprocess.run(self.base + ["kill-server"], capture_output=True, env=self.env)
        self.socket.unlink(missing_ok=True)


class Harness:
    """One GhosttyHarness.app process attached to one tmux session."""

    count = 0

    def __init__(self, run_dir: Path, tmux: Tmux, session: str):
        Harness.count += 1
        self.name = f"viewer-{Harness.count:02d}-{session}"
        self.log = run_dir / f"{self.name}.log"
        self.control = run_dir / f"{self.name}.ctl"
        os.mkfifo(self.control)
        attach = " ".join(f"'{a}'" for a in tmux.base + ["attach-session", "-t", session])
        env = dict(tmux.env)
        env.update(
            AMX_HARNESS_COMMAND=f"exec {attach}",
            AMX_HARNESS_CONTROL=str(self.control),
            AMX_HARNESS_LOG=str(self.log),
        )
        self.stdout = open(run_dir / f"{self.name}.stdout", "w")
        self.proc = subprocess.Popen([str(APP)], env=env, stdout=self.stdout, stderr=subprocess.STDOUT)
        # O_RDWR never blocks on a FIFO, unlike O_WRONLY before the reader opens.
        self.fd = os.open(self.control, os.O_RDWR)
        self.seq = 0
        wait_for(lambda: self.log.exists() and "- ready" in self.log.read_text(), 30, f"{self.name} ready")

    @property
    def pid(self) -> int:
        return self.proc.pid

    def cmd(self, verb: str, args: str = "", timeout: float = 15) -> str:
        self.seq += 1
        line_id = f"c{self.seq}"
        os.write(self.fd, f"{line_id} {verb} {args}".rstrip().encode() + b"\n")

        def result():
            for line in self.log.read_text().splitlines():
                if line.startswith(line_id + " "):
                    return line
            return None

        line = wait_for(result, timeout, f"{self.name} {verb}")
        status, _, detail = line[len(line_id) + 1:].partition(" ")
        if status != "ok":
            raise Failure(f"{self.name} {verb} {args}: {detail}")
        return detail

    def size(self) -> tuple[int, int]:
        detail = self.cmd("size")
        fields = dict(f.split("=") for f in detail.split())
        return int(fields["cols"]), int(fields["rows"])

    def screen(self, path: Path) -> str:
        self.cmd("screen", str(path))
        return path.read_text()

    def kill(self) -> None:
        os.kill(self.proc.pid, signal.SIGKILL)
        self.proc.wait(10)
        self.close()

    def quit(self) -> None:
        if self.proc.poll() is None:
            try:
                self.cmd("quit", timeout=5)
            except Failure:
                pass
            try:
                self.proc.wait(10)
            except subprocess.TimeoutExpired:
                self.proc.kill()
        self.close()

    def close(self) -> None:
        if self.fd >= 0:
            os.close(self.fd)
            self.fd = -1
        self.stdout.close()


def rows(text: str) -> list[str]:
    lines = [line.rstrip() for line in text.split("\n")]
    while lines and not lines[-1]:
        lines.pop()
    return lines


def probe_events(path: Path) -> list[dict]:
    if not path.exists():
        return []
    return [json.loads(line) for line in path.read_text().splitlines() if line.strip()]


def probe_input_since(path: Path, start: int) -> bytes:
    return b"".join(bytes.fromhex(e["hex"]) for e in probe_events(path)[start:] if e["event"] == "input")


def process_start(pid: int) -> str:
    return subprocess.run(["ps", "-o", "lstart=", "-p", str(pid)], capture_output=True, text=True).stdout.strip()


def process_command(pid: int) -> str:
    return subprocess.run(["ps", "-o", "command=", "-p", str(pid)], capture_output=True, text=True).stdout.strip()


# --- Deterministic cases --------------------------------------------------------


def run_probe_cases(run_dir: Path, tmux: Tmux, results: dict) -> None:
    events = run_dir / "probe-events.jsonl"
    tmux.new_session("probe", ["python3", "-I", str(PROBE), str(events)], run_dir)
    wait_for(lambda: any(e["event"] == "start" for e in probe_events(events)), 15, "probe start")
    viewer = Harness(run_dir, tmux, "probe")
    try:
        wait_for(lambda: tmux.attached("probe") == 1, 15, "probe attach")
        time.sleep(1)
        results["capture"] = case_capture(run_dir, tmux, viewer)
        results["resize"] = case_resize(run_dir, tmux, viewer, events)
        results["modified-keys"] = case_modified_keys(tmux, viewer, events)
        results["paste"] = case_paste(viewer, events)
        results["scrolling"] = case_scrolling(run_dir, tmux, viewer)
        # Ctrl+] ends the probe; its session ends, so tmux detaches the viewer
        # and the viewer exits on its own.
        viewer.cmd("key", "ctrl+]")
        wait_for(lambda: viewer.proc.poll() is not None, 15, "viewer exit after session end")
        viewer.close()
    finally:
        if viewer.proc.poll() is None:
            viewer.quit()


def case_capture(run_dir: Path, tmux: Tmux, viewer: Harness) -> dict:
    pane = tmux.capture("probe", run_dir / "capture-tmux.txt")
    seen = viewer.screen(run_dir / "capture-ghostty.txt")
    marker = "PROBE-READY capture marker ✓ éè 中文"
    pane_rows, seen_rows = rows(pane), rows(seen)
    passed = marker in pane and marker in seen and pane_rows == seen_rows
    return {
        "status": "passed" if passed else "failed",
        "marker_in_tmux": marker in pane,
        "marker_in_ghostty": marker in seen,
        "rows_compared": len(pane_rows),
        "rows_equal": pane_rows == seen_rows,
        "artifacts": ["capture-tmux.txt", "capture-ghostty.txt"],
    }


def case_resize(run_dir: Path, tmux: Tmux, viewer: Harness, events: Path) -> dict:
    observations = []
    passed = True
    for width, height in [(1000, 640), (700, 450), (1200, 800)]:
        start = len(probe_events(events))
        detail = viewer.cmd("resize", f"{width} {height}")
        cols, rows_ = viewer.size()

        def tmux_matches():
            return tmux.fmt("probe", "#{window_width}x#{window_height}") == f"{cols}x{rows_}"

        def winch():
            return [e for e in probe_events(events)[start:] if e["event"] == "winch"]

        ok = True
        try:
            wait_for(tmux_matches, 10, "tmux window size")
        except Failure:
            ok = False
        tmux_size = tmux.fmt("probe", "#{window_width}x#{window_height}")
        last = None
        try:
            last = wait_for(winch, 10, "probe SIGWINCH")[-1]
        except Failure:
            # A resize to the size already in effect raises no SIGWINCH.
            last = None
        probe_size = f"{last['cols']}x{last['rows']}" if last else None
        if probe_size is not None and probe_size != f"{cols}x{rows_}":
            ok = False
        time.sleep(0.5)
        same_screen = rows(tmux.capture("probe")) == rows(viewer.screen(run_dir / "resize-ghostty.txt"))
        ok = ok and same_screen
        passed = passed and ok
        observations.append({
            "window_points": f"{width}x{height}",
            "harness": detail,
            "ghostty_grid": f"{cols}x{rows_}",
            "tmux_window": tmux_size,
            "probe_winch": probe_size,
            "screens_equal": same_screen,
            "ok": ok,
        })
    changed = len({o["ghostty_grid"] for o in observations}) == len(observations)
    return {"status": "passed" if passed and changed else "failed", "steps": observations}


# The pane asked for modifyOtherKeys 2; tmux (extended-keys-format csi-u)
# forwards these. Level 2 encodes every modified key, so Ctrl+C and Shift+Tab
# arrive as CSI u rather than as 0x03 and CSI Z.
MODIFIED_KEYS = [
    ("enter", b"\r"),
    ("shift+enter", b"\x1b[13;2u"),
    ("ctrl+enter", b"\x1b[13;5u"),
    ("alt+enter", b"\x1b[13;3u"),
    ("shift+tab", b"\x1b[9;2u"),
    ("ctrl+c", b"\x1b[99;5u"),
    ("ctrl+j", b"\x1b[106;5u"),
    ("escape", b"\x1b"),
    ("up", b"\x1b[A"),
    ("shift+up", b"\x1b[1;2A"),
    ("ctrl+left", b"\x1b[1;5D"),
]


def case_modified_keys(tmux: Tmux, viewer: Harness, events: Path) -> dict:
    steps = []
    for spec, expected in MODIFIED_KEYS:
        start = len(probe_events(events))
        viewer.cmd("key", spec)
        try:
            got = wait_for(lambda: probe_input_since(events, start), 5, f"probe input for {spec}")
        except Failure:
            got = b""
        time.sleep(0.15)
        got = probe_input_since(events, start)
        steps.append({"key": spec, "expected_hex": expected.hex(), "received_hex": got.hex(), "ok": got == expected})
    distinct = len({s["received_hex"] for s in steps}) == len(steps)
    passed = all(s["ok"] for s in steps) and distinct
    return {
        "status": "passed" if passed else "failed",
        "extended_keys": tmux.run("show-options", "-s", "extended-keys").strip(),
        "all_distinct": distinct,
        "steps": steps,
    }


def case_paste(viewer: Harness, events: Path) -> dict:
    text = "amx paste line one\nline two ✓ 'quoted' $HOME"
    start = len(probe_events(events))
    viewer.cmd("paste", text.replace("\n", "\\n"))
    try:
        wait_for(lambda: b"\x1b[201~" in probe_input_since(events, start), 10, "bracketed paste end")
    except Failure:
        pass
    got = probe_input_since(events, start)
    # Ghostty sends a pasted newline as CR, as a terminal's Enter would.
    expected = b"\x1b[200~" + text.replace("\n", "\r").encode() + b"\x1b[201~"
    alt_expected = b"\x1b[200~" + text.encode() + b"\x1b[201~"
    ok = got in (expected, alt_expected)
    return {
        "status": "passed" if ok else "failed",
        "pasted": text,
        "received_hex": got.hex(),
        "received_text": got.decode("utf-8", "backslashreplace"),
        "bracketed": got.startswith(b"\x1b[200~") and got.endswith(b"\x1b[201~"),
    }


def case_scrolling(run_dir: Path, tmux: Tmux, viewer: Harness) -> dict:
    before_mode = tmux.fmt("probe", "#{pane_in_mode}")
    viewer.cmd("scroll", "10")
    time.sleep(0.8)
    in_mode = tmux.fmt("probe", "#{pane_in_mode}")
    mode_name = tmux.fmt("probe", "#{pane_mode}")
    position = tmux.fmt("probe", "#{scroll_position}")
    seen = viewer.screen(run_dir / "scroll-ghostty.txt")
    numbers = [int(n) for n in re.findall(r"probe history line (\d{4})", seen)]
    viewer.cmd("scroll", "-100")
    time.sleep(0.8)
    # Scrolling back to the bottom leaves copy mode by itself; q only if not.
    if tmux.fmt("probe", "#{pane_in_mode}") == "1":
        viewer.cmd("key", "q")
        time.sleep(0.5)
    after_mode = tmux.fmt("probe", "#{pane_in_mode}")
    bottom = viewer.screen(run_dir / "scroll-ghostty-bottom.txt")
    passed = (
        before_mode == "0"
        and in_mode == "1"
        and mode_name == "copy-mode"
        and position.isdigit() and int(position) > 0
        and "PROBE-READY" not in seen.split("\n")[-3:]
        and after_mode == "0"
        and "PROBE-READY" in bottom
    )
    return {
        "status": "passed" if passed else "failed",
        "pane_in_mode_before": before_mode,
        "pane_in_mode_after_wheel": in_mode,
        "pane_mode": mode_name,
        "scroll_position": position,
        "visible_history_lines": [min(numbers), max(numbers)] if numbers else [],
        "pane_in_mode_after_exit": after_mode,
        "artifacts": ["scroll-ghostty.txt", "scroll-ghostty-bottom.txt"],
    }


# --- Live agents ----------------------------------------------------------------

TRUST = re.compile(r"trust|Do you want to work in this folder|allow .* to work", re.I)
UPDATE = re.compile(r"update available|new version", re.I)


def settle(tmux: Tmux, session: str, timeout: float, log: list[str]) -> str:
    """Wait for the agent's screen to stop changing, answering folder-trust
    prompts with Enter (their first, accepting choice) on the way."""
    deadline = time.monotonic() + timeout
    last, stable_since = "", time.monotonic()
    while time.monotonic() < deadline:
        screen = tmux.capture(session)
        if screen != last:
            last, stable_since = screen, time.monotonic()
        elif time.monotonic() - stable_since > 3 and screen.strip():
            if UPDATE.search(screen) and not TRUST.search(screen):
                raise Failure(f"{session} shows an update prompt; not answering it automatically")
            if TRUST.search(screen):
                log.append("answered a folder-trust prompt with enter")
                tmux.run("send-keys", "-t", session, "Enter")
                last = ""
                time.sleep(2)
                continue
            return screen
        time.sleep(0.5)
    raise Failure(f"{session} screen never settled")


def ask(viewer: Harness, tmux: Tmux, session: str, question: str, answer: str, timeout: float) -> str:
    viewer.cmd("type", question)
    time.sleep(1)
    viewer.cmd("key", "enter")

    def answered():
        screen = tmux.capture(session)
        return screen if re.search(rf"(?<![A-Za-z]){answer}(?![A-Za-z])", screen) else None

    return wait_for(answered, timeout, f"{session} answer {answer}", interval=1)


def run_agent(name: str, argv: list[str], run_dir: Path, tmux: Tmux) -> dict:
    work = run_dir / f"work-{name}"
    work.mkdir()
    notes: list[str] = []
    tmux.new_session(name, argv, work)
    pane_pid = int(tmux.fmt(name, "#{pane_pid}"))
    started = process_start(pane_pid)
    record: dict = {"argv": argv, "pane_pid": pane_pid, "process_command": process_command(pane_pid), "notes": notes}
    viewer = Harness(run_dir, tmux, name)
    try:
        wait_for(lambda: tmux.attached(name) == 1, 15, f"{name} attach")
        record["viewer_1_pid"] = viewer.pid
        settle(tmux, name, 120, notes)
        viewer.screen(run_dir / f"{name}-1-start-ghostty.txt")
        tmux.capture(name, run_dir / f"{name}-1-start-tmux.txt")
        ask(viewer, tmux, name, QUESTION, ANSWER, 240)
        time.sleep(2)
        tmux.capture(name, run_dir / f"{name}-2-answer-tmux.txt")
        seen = viewer.screen(run_dir / f"{name}-2-answer-ghostty.txt")
        record["answer_in_ghostty"] = ANSWER in seen

        # Viewer crash: SIGKILL, never a clean detach.
        viewer.kill()
        wait_for(lambda: tmux.attached(name) == 0, 15, f"{name} client gone after SIGKILL")
        record["after_kill"] = {
            "session_alive": tmux.has_session(name),
            "pane_pid": int(tmux.fmt(name, "#{pane_pid}")),
            "process_start": process_start(pane_pid),
            "attached_clients": tmux.attached(name),
        }

        viewer = Harness(run_dir, tmux, name)
        wait_for(lambda: tmux.attached(name) == 1, 15, f"{name} reattach")
        record["viewer_2_pid"] = viewer.pid
        time.sleep(2)
        seen = viewer.screen(run_dir / f"{name}-3-reattach-ghostty.txt")
        record["answer_visible_after_reattach"] = ANSWER in seen
        ask(viewer, tmux, name, FOLLOW_UP, FOLLOW_UP_ANSWER, 240)
        time.sleep(2)
        tmux.capture(name, run_dir / f"{name}-4-followup-tmux.txt")
        seen = viewer.screen(run_dir / f"{name}-4-followup-ghostty.txt")
        record["follow_up_in_ghostty"] = FOLLOW_UP_ANSWER in seen
        record["after_reattach"] = {
            "pane_pid": int(tmux.fmt(name, "#{pane_pid}")),
            "process_start": process_start(pane_pid),
        }
        same = (
            record["after_kill"]["session_alive"]
            and record["after_kill"]["pane_pid"] == pane_pid
            and record["after_reattach"]["pane_pid"] == pane_pid
            and record["after_kill"]["process_start"] == started
            and record["after_reattach"]["process_start"] == started
        )
        record["same_process"] = same
        record["status"] = (
            "passed"
            if same and record["answer_in_ghostty"] and record["answer_visible_after_reattach"]
            and record["follow_up_in_ghostty"] and viewer.pid != record["viewer_1_pid"]
            else "failed"
        )
    except Failure as err:
        record["status"] = "failed"
        record["error"] = str(err)
        try:
            tmux.capture(name, run_dir / f"{name}-failure-tmux.txt")
        except Failure:
            pass
    finally:
        viewer.quit()
        # Ending the agent is an explicit act, separate from closing the viewer.
        tmux.run("kill-session", "-t", name, check=False)
    record["process_start"] = started
    return record


# --- Evidence -------------------------------------------------------------------


def source_digest() -> str:
    h = hashlib.sha256()
    files = sorted(p for p in HARNESS_DIR.rglob("*") if p.is_file() and ".xcodeproj" not in str(p))
    for p in files:
        h.update(str(p.relative_to(HARNESS_DIR)).encode())
        h.update(p.read_bytes())
    return h.hexdigest()[:16]


def agent_versions() -> dict[str, str]:
    out = {}
    for name, argv in AGENTS.items():
        proc = subprocess.run(argv + ["--version"], capture_output=True, text=True, env=clean_env())
        out[name] = (proc.stdout or proc.stderr).strip().splitlines()[0] if (proc.stdout or proc.stderr) else "unknown"
    return out


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--tmux", default=shutil.which("tmux"))
    parser.add_argument("--agents", default="claude,codex,copilot")
    parser.add_argument("--skip-probe", action="store_true")
    args = parser.parse_args()
    if not args.tmux:
        print("no tmux: pass --tmux", file=sys.stderr)
        return 2
    if not APP.exists():
        print(f"{APP} missing: run scripts/build.sh", file=sys.stderr)
        return 2

    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    run_dir = CACHE / "runs" / stamp
    run_dir.mkdir(parents=True)
    tmux = Tmux(args.tmux, run_dir, clean_env())
    vendor_stamp = (CACHE / "Vendor/.stamp").read_text().strip()
    results: dict = {
        "run": stamp,
        "run_dir": str(run_dir),
        "harness_source_sha256_16": source_digest(),
        "ghosttykit": vendor_stamp,
        "tmux": tmux.version(),
        "tmux_binary": args.tmux,
        "agents": agent_versions(),
        "macos": subprocess.run(["sw_vers", "-productVersion"], capture_output=True, text=True).stdout.strip(),
        "cases": {},
    }
    # The server starts with the first session; its environment is the
    # sanitized one, so agents inherit nothing of the driver's session.
    try:
        if not args.skip_probe:
            run_probe_cases(run_dir, tmux, results["cases"])
        for name in [a for a in args.agents.split(",") if a]:
            results["cases"][f"{name}-live"] = run_agent(name, AGENTS[name], run_dir, tmux)
    finally:
        tmux.kill_server()
        (run_dir / "results.json").write_text(json.dumps(results, indent=2, ensure_ascii=False) + "\n")
    print(json.dumps({k: v.get("status") for k, v in results["cases"].items()}, indent=2))
    print(run_dir / "results.json")
    return 0 if all(v.get("status") == "passed" for v in results["cases"].values()) else 1


if __name__ == "__main__":
    sys.exit(main())
