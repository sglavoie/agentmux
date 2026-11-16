#!/usr/bin/env python3
"""Sync/run Agent Mux commands in a Parallels macOS guest; never fall back locally.

Requires guest tools, a guest account, and Parallels' Home shared folder.
Provision the guest toolchain before use; see scripts/demo/README.md.
"""

import argparse
import os
from pathlib import Path, PurePosixPath
import shlex
import subprocess
import sys
import tempfile
import time
import uuid

REPO = Path(__file__).resolve().parents[2]


def write_guest_script(command, directory, shared):
    """Claim an invocation before any side effect, and publish its real result."""
    q = shlex.quote
    script = directory / "command.sh"
    script.write_text("\n".join([
        "#!/bin/sh",
        "cd " + q(str(shared)) + " || exit 125",
        # mkdir is atomic even if an ambiguous transport failure launches the
        # retained script late. A second invocation cannot execute the payload.
        "mkdir claimed 2>/dev/null || exit 125",
        "echo $$ > claimed/pid",
        "trap 'code=$?; printf \"%s\\n\" \"$code\" > result.tmp; mv result.tmp result' EXIT",
        "(" + command + "\n) > stdout 2> stderr",
        "exit $?",
        "",
    ]))
    return script


def execute_guest(vm, directory, shared, attempts=5):
    """Retry only a proven pre-start transport error, never a command failure."""
    transport_errors = ("Invalid argument", "PRL_ERR_IO_STOPPED")
    command = ["prlctl", "exec", vm, f'/bin/sh "{shared}/command.sh"']
    for attempt in range(attempts):
        result = subprocess.run(command, capture_output=True)
        # Keep transport diagnostics separate from guest command output.
        prefix = directory / f"transport-{attempt + 1}"
        prefix.with_suffix(".stdout").write_bytes(result.stdout)
        prefix.with_suffix(".stderr").write_bytes(result.stderr)
        prefix.with_suffix(".status").write_text(f"{result.returncode}\n")
        for name, stream in (("stdout", sys.stdout), ("stderr", sys.stderr)):
            path = directory / name
            if path.exists():
                with path.open("rb") as output:
                    while chunk := output.read(65536):
                        stream.buffer.write(chunk)
                stream.flush()
        receipt = directory / "result"
        if receipt.exists():
            # The transport may fail after the command finishes. The atomic
            # guest receipt, including nonzero test exits, is authoritative.
            return int(receipt.read_text().strip())
        diagnostic = result.stderr.decode(errors="replace")
        if (directory / "claimed").exists():
            raise RuntimeError(
                f"Guest command started but completion is unknown; NOT retried. "
                f"Inspect {directory} and guest PID in claimed/pid. {diagnostic}")
        if (result.returncode == 0 or
                not any(error in diagnostic for error in transport_errors) or
                attempt == attempts - 1):
            sys.stdout.buffer.write(result.stdout)
            sys.stderr.buffer.write(result.stderr)
            raise RuntimeError(f"Guest command did not publish a completion receipt; retained {directory}")
        print(f"parallels: transport failed before guest start; retry {attempt + 2}/{attempts}",
              file=sys.stderr, flush=True)
        time.sleep(1)
    raise AssertionError("unreachable")


def guest(vm, command):
    # Guest tools impose their own command parser/length limit. Keep a short
    # script path and durable launch/result evidence on the shared filesystem.
    staging = Path.home() / ".cache/amx-prl"
    staging.mkdir(parents=True, exist_ok=True)
    directory = Path(tempfile.mkdtemp(prefix="run-", dir=staging))
    shared = PurePosixPath("/Volumes/My Shared Files/Home") / directory.relative_to(Path.home())
    if any(c in str(shared) for c in '\n\r"$`\\'):
        raise ValueError("unsupported shell character in shared checkout path")
    write_guest_script(command, directory, shared)
    print(f"parallels: invocation evidence: {directory}", flush=True)
    return execute_guest(vm, directory, shared)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--vm", default=os.environ.get("AMX_PARALLELS_VM", "macOS"))
    parser.add_argument("--user", default=os.environ.get("AMX_PARALLELS_USER", "sglavoie"))
    commands = parser.add_subparsers(dest="operation", required=True)
    commands.add_parser("sync")
    run = commands.add_parser("run")
    run.add_argument("--gui", action="store_true", help="Use the guest GUI session (required for clipboard tests)")
    run.add_argument("command", nargs=argparse.REMAINDER)
    get = commands.add_parser("get")
    get.add_argument("source", help="Path relative to the guest checkout")
    get.add_argument("destination", type=Path, help="Local directory under the shared Home")
    args = parser.parse_args()
    if not args.user or any(c not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-" for c in args.user):
        parser.error("user must be a local account name")
    try:
        shared_repo = PurePosixPath("/Volumes/My Shared Files/Home") / REPO.relative_to(Path.home())
    except ValueError:
        parser.error("checkout must be within the host Home shared folder")
    checkout = PurePosixPath("/Users") / args.user / "Library/Caches/agent-mux-parallels" / REPO.name
    quoted_checkout = shlex.quote(str(checkout))
    sync_shell = ""
    if args.operation in {"sync", "run"}:
        # Archive on the host, then walk only guest-local files. Parallels shared
        # folders can turn framework symlinks into loops when rsync walks them.
        staging = REPO / "macos/build"
        staging.mkdir(parents=True, exist_ok=True)
        archive = staging / f"parallels-source-{uuid.uuid4()}.tar"
        excludes = [".claude", "amx", "macos/build", "macos/DerivedData",
                    "macos/AgentMux.xcodeproj", "macos/.cache"]
        if args.operation == "run":
            excludes.append("macos/Vendor")
        subprocess.run(["/usr/bin/tar", "-cf", str(archive),
                        *[f"--exclude=./{path}" for path in excludes],
                        "-C", str(REPO), "."], check=True,
                       env={**os.environ, "COPYFILE_DISABLE": "1"})
        shared_archive = shared_repo / archive.relative_to(REPO)
        sync_shell = "\n".join([
            "set -eu",
            'stage=$(mktemp -d /private/tmp/agentmux-sync.XXXXXX)',
            "trap " + shlex.quote('rm -rf "$stage"; rm -f ' + shlex.quote(str(shared_archive))) + " EXIT",
            "cp " + shlex.quote(str(shared_archive)) + ' "$stage/source.tar"',
            'mkdir "$stage/source"',
            'tar -xf "$stage/source.tar" -C "$stage/source"',
            "mkdir -p " + quoted_checkout,
            shlex.join(["/usr/bin/rsync", "-a", "--delete",
                        *[f"--exclude=/{path}" for path in excludes]])
                + ' "$stage/source/" ' + quoted_checkout + "/",
        ]) + "\n"
    if args.operation == "sync":
        shell = sync_shell
    elif args.operation == "get":
        source = PurePosixPath(args.source)
        if source.is_absolute() or ".." in source.parts:
            parser.error("source must stay within the guest checkout")
        destination = args.destination.resolve()
        try:
            shared_destination = PurePosixPath("/Volumes/My Shared Files/Home") / destination.relative_to(Path.home())
        except ValueError:
            parser.error("destination must be within the host Home shared folder")
        destination.mkdir(parents=True, exist_ok=True)
        shell = shlex.join(["/usr/bin/rsync", "-a", str(checkout / source), str(shared_destination) + "/"])
    else:
        command = args.command
        if command[:1] == ["--"]:
            command = command[1:]
        if not command:
            parser.error("run requires a command")
        if args.gui:
            command = ["python3", "macos/scripts/run-in-gui.py", "/usr/bin/env", *command]
        # The guest's own tools and preferences; never inherit host session IDs
        # or route recursively to a different machine. Ad-hoc signing belongs
        # only to this isolated guest, not to the installed host app.
        shell = sync_shell + "cd " + quoted_checkout + " && " + shlex.join([
            "/usr/bin/env", "-u", "AMX_MAC_TEST_HOST", "-u", "AMX_UI_TEST_REMOTE_ONLY",
            "-u", "AMX_RUNTIME_SESSION_ID", "-u", "TMUX", "-u", "TMUX_PANE",
            "AMX_UI_REMOTE_EXECUTION=1", "AMX_CODESIGN_IDENTITY=-",
            "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8",
            "GOMAXPROCS=2", "GOFLAGS=-p=1",
            # xcodebuild forwards TEST_RUNNER_ variables to the hosted process
            # with that prefix stripped; ordinary shell variables are filtered.
            "TEST_RUNNER_AMX_TEST_OCR_MODE=cpu-fast",
            "DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer",
            f"PATH=/usr/local/agentmux/bin:/usr/local/agentmux/go/bin:/opt/homebrew/bin:{checkout}/macos/Vendor/tmux/bin:/usr/bin:/bin:/usr/sbin:/sbin",
            *command])
    print(f"parallels: {args.operation} in {args.vm} as {args.user}: {checkout}", flush=True)
    return guest(args.vm, shlex.join(["/usr/bin/sudo", "-H", "-u", args.user, "/bin/sh", "-c", shell]))


if __name__ == "__main__":
    sys.exit(main())
