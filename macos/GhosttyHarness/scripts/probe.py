"""The pane program for the harness's deterministic terminal cases.

Run inside the private tmux pane, it prints a numbered history for the
scrolling case, asks for bracketed paste and modifyOtherKeys (as Claude, Codex,
and Copilot do), and records every input byte and every size change, as JSON
lines, to the log named by its one argument. Ctrl+] ends it.
"""

import json
import os
import shutil
import signal
import sys
import termios
import tty

HISTORY_LINES = 300


def main() -> None:
    log = open(sys.argv[1], "a", buffering=1)

    def record(**fields: object) -> None:
        log.write(json.dumps(fields) + "\n")

    def size() -> tuple[int, int]:
        s = shutil.get_terminal_size()
        return s.columns, s.lines

    def on_winch(_sig: int, _frame: object) -> None:
        cols, rows = size()
        record(event="winch", cols=cols, rows=rows)

    for i in range(1, HISTORY_LINES + 1):
        sys.stdout.write(f"probe history line {i:04d}\r\n")
    sys.stdout.write("PROBE-READY capture marker ✓ éè 中文\r\n")
    # Bracketed paste on, then modifyOtherKeys level 2.
    sys.stdout.write("\x1b[?2004h\x1b[>4;2m")
    sys.stdout.flush()

    fd = sys.stdin.fileno()
    saved = termios.tcgetattr(fd)
    tty.setraw(fd)
    signal.signal(signal.SIGWINCH, on_winch)
    cols, rows = size()
    record(event="start", pid=os.getpid(), cols=cols, rows=rows)
    try:
        while True:
            try:
                data = os.read(fd, 4096)
            except InterruptedError:
                continue
            if not data:
                break
            record(event="input", hex=data.hex(), text=data.decode("utf-8", "backslashreplace"))
            if b"\x1d" in data:
                break
    finally:
        sys.stdout.write("\x1b[>4;0m\x1b[?2004l")
        sys.stdout.flush()
        termios.tcsetattr(fd, termios.TCSADRAIN, saved)
        record(event="exit")


if __name__ == "__main__":
    main()
