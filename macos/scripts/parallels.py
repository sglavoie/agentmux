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
import uuid

REPO = Path(__file__).resolve().parents[2]


def guest(vm, command):
    # Guest tools impose their own command parser/length limit. Pass a small
    # script path, with all shell quoting handled by the guest's actual shell.
    staging = Path.home() / ".cache/amx-prl"
    staging.mkdir(parents=True, exist_ok=True)
    fd, filename = tempfile.mkstemp(prefix="cmd-", suffix=".sh", dir=staging)
    shared = PurePosixPath("/Volumes/My Shared Files/Home") / Path(filename).relative_to(Path.home())
    if any(c in str(shared) for c in '\n\r"$`\\'):
        os.close(fd)
        Path(filename).unlink()
        raise ValueError("unsupported shell character in shared checkout path")
    with os.fdopen(fd, "w") as script:
        script.write("#!/bin/sh\ntrap " + shlex.quote("rm -f " + shlex.quote(str(shared))) + " EXIT\n" + command + "\n")
    # Replace the coordinator process so prlctl retains its original terminal
    # transport. The guest removes only this invocation's command file on exit.
    os.execvp("prlctl", ["prlctl", "exec", vm, f'/bin/sh "{shared}"'])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--vm", default=os.environ.get("AMX_PARALLELS_VM", "macOS"))
    parser.add_argument("--user", default=os.environ.get("AMX_PARALLELS_USER", "sglavoie"))
    commands = parser.add_subparsers(dest="operation", required=True)
    commands.add_parser("sync")
    run = commands.add_parser("run")
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
        # The guest's own tools and preferences; never inherit host session IDs
        # or route recursively to a different machine. Ad-hoc signing belongs
        # only to this isolated guest, not to the installed host app.
        shell = sync_shell + "cd " + quoted_checkout + " && " + shlex.join([
            "/usr/bin/env", "-u", "AMX_MAC_TEST_HOST", "-u", "AMX_UI_TEST_REMOTE_ONLY",
            "-u", "AMX_RUNTIME_SESSION_ID", "-u", "TMUX", "-u", "TMUX_PANE",
            "AMX_UI_REMOTE_EXECUTION=1", "AMX_CODESIGN_IDENTITY=-",
            "DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer",
            f"PATH=/usr/local/agentmux/bin:/usr/local/agentmux/go/bin:/opt/homebrew/bin:{checkout}/macos/Vendor/tmux/bin:/usr/bin:/bin:/usr/sbin:/sbin",
            *command])
    print(f"parallels: {args.operation} in {args.vm} as {args.user}: {checkout}", flush=True)
    return guest(args.vm, shlex.join(["/usr/bin/sudo", "-H", "-u", args.user, "/bin/sh", "-c", shell]))


if __name__ == "__main__":
    sys.exit(main())
