#!/usr/bin/env python3
"""Carries a program's terminal between two Macs.

The acceptance run shows and drives everything on one Mac (the app, tmux, the
daemon, every key and pixel) while the three real harnesses are signed in on
another. So each harness process runs headless where it is signed in, in a
pseudo-terminal of its own, and its terminal is carried over SSH into the tmux
pane that AgentMux launched for it. No credential moves between machines.

  bridge.py pane SOCKET
      Runs in the tmux pane. Listens on SOCKET, then relays this terminal:
      bytes typed go out, bytes received are written, and a resize is sent on.
      Exits with the far program's exit status.

  bridge.py connect SOCKET
      Run over SSH on the pane's Mac: joins standard input and output to
      SOCKET, byte for byte.

  bridge.py serve HOST REMOTE_BRIDGE SOCKET -- COMMAND ARG...
      Run where the harness is signed in. Starts COMMAND in a pseudo-terminal
      and joins it to the pane through `ssh HOST REMOTE_BRIDGE connect SOCKET`.

Frames between pane and serve: one type byte (D data, R resize as rows and
columns, E the pane's terminal environment, X exit status), a four-byte
length, the payload.
"""
import fcntl
import os
import select
import signal
import socket
import struct
import subprocess
import sys
import termios
import tty


TERMINAL_ENVIRONMENT = ("TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "COLORTERM", "TMUX", "TMUX_PANE")


def frame(kind, payload=b""):
    return kind + struct.pack(">I", len(payload)) + payload


class Reader:
    """Splits a byte stream back into frames."""

    def __init__(self):
        self.buffer = b""

    def feed(self, data):
        self.buffer += data
        while len(self.buffer) >= 5:
            size = struct.unpack(">I", self.buffer[1:5])[0]
            if len(self.buffer) < 5 + size:
                return
            kind, payload = self.buffer[:1], self.buffer[5:5 + size]
            self.buffer = self.buffer[5 + size:]
            yield kind, payload


def window_size(fd):
    rows, cols, _, _ = struct.unpack("HHHH", fcntl.ioctl(fd, termios.TIOCGWINSZ, b"\0" * 8))
    return rows, cols


def write_all(fd, data):
    while data:
        data = data[os.write(fd, data):]


def pane(path):
    if os.path.exists(path):
        os.unlink(path)
    server = socket.socket(socket.AF_UNIX)
    server.bind(path)
    os.chmod(path, 0o600)
    server.listen(1)
    sys.stdout.write(f"waiting for the harness to be joined to this pane (pid {os.getpid()})\r\n")
    sys.stdout.flush()
    conn, _ = server.accept()
    server.close()
    os.unlink(path)
    saved = termios.tcgetattr(0)
    status = 1
    resized = [True]
    signal.signal(signal.SIGWINCH, lambda *_: resized.__setitem__(0, True))
    try:
        tty.setraw(0)
        # What tells a program which terminal it is in. A harness decides
        # from these whether to ask for extended keys, so it must see the
        # pane's own values, tmux's included.
        carried = {k: os.environ[k] for k in TERMINAL_ENVIRONMENT if k in os.environ}
        conn.sendall(frame(b"E", "\0".join(f"{k}={v}" for k, v in carried.items()).encode()))
        reader = Reader()
        while True:
            if resized[0]:
                resized[0] = False
                conn.sendall(frame(b"R", struct.pack(">HH", *window_size(0))))
            try:
                ready, _, _ = select.select([0, conn], [], [], 0.5)
            except InterruptedError:
                continue
            if 0 in ready:
                data = os.read(0, 65536)
                if not data:
                    break
                conn.sendall(frame(b"D", data))
            if conn in ready:
                data = conn.recv(65536)
                if not data:
                    break
                done = False
                for kind, payload in reader.feed(data):
                    if kind == b"D":
                        write_all(1, payload)
                    elif kind == b"X":
                        status = struct.unpack(">i", payload)[0]
                        done = True
                if done:
                    break
    finally:
        termios.tcsetattr(0, termios.TCSADRAIN, saved)
    return status


def connect(path):
    conn = socket.socket(socket.AF_UNIX)
    conn.connect(path)
    while True:
        ready, _, _ = select.select([0, conn], [], [])
        if 0 in ready:
            data = os.read(0, 65536)
            if not data:
                break
            conn.sendall(data)
        if conn in ready:
            data = conn.recv(65536)
            if not data:
                break
            write_all(1, data)
    return 0


def serve(host, remote_bridge, path, command):
    link = subprocess.Popen(
        ["ssh", "-o", "StrictHostKeyChecking=yes", "-o", "ServerAliveInterval=30", host,
         f"exec python3 '{remote_bridge}' connect '{path}'"],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, bufsize=0)
    to_pane, from_pane = link.stdin.fileno(), link.stdout.fileno()
    reader = Reader()
    child, master = None, None
    pending_size = None
    terminal = {"TERM": "xterm-256color"}
    status = 1
    while True:
        fds = [from_pane] + ([master] if master is not None else [])
        ready, _, _ = select.select(fds, [], [], 0.5)
        if from_pane in ready:
            data = os.read(from_pane, 65536)
            if not data:
                break
            for kind, payload in reader.feed(data):
                if kind == b"E":
                    terminal = dict(item.split("=", 1) for item in payload.decode().split("\0") if "=" in item)
                elif kind == b"R":
                    rows, cols = struct.unpack(">HH", payload)
                    size = struct.pack("HHHH", rows, cols, 0, 0)
                    if master is None:
                        # The harness starts only once the pane's size is known.
                        child, master = os.forkpty()
                        if child == 0:
                            for name in TERMINAL_ENVIRONMENT:
                                os.environ.pop(name, None)
                            os.environ.update(terminal)
                            fcntl.ioctl(0, termios.TIOCSWINSZ, size)
                            os.execvp(command[0], command)
                        fcntl.ioctl(master, termios.TIOCSWINSZ, size)
                        print(f"harness pid {child}", file=sys.stderr, flush=True)
                    else:
                        fcntl.ioctl(master, termios.TIOCSWINSZ, size)
                        os.kill(child, signal.SIGWINCH)
                elif kind == b"D" and master is not None:
                    write_all(master, payload)
        if master is not None and master in ready:
            try:
                data = os.read(master, 65536)
            except OSError:
                data = b""
            if not data:
                _, raw = os.waitpid(child, 0)
                status = os.waitstatus_to_exitcode(raw)
                write_all(to_pane, frame(b"X", struct.pack(">i", status)))
                child = None
                break
            write_all(to_pane, frame(b"D", data))
    if child:
        # The pane went away: the harness has no terminal left.
        os.kill(child, signal.SIGHUP)
        os.waitpid(child, 0)
    link.stdin.close()
    link.wait()
    return status


def main(argv):
    if len(argv) == 2 and argv[0] == "pane":
        return pane(argv[1])
    if len(argv) == 2 and argv[0] == "connect":
        return connect(argv[1])
    if len(argv) >= 6 and argv[0] == "serve" and argv[4] == "--":
        return serve(argv[1], argv[2], argv[3], argv[5:])
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
