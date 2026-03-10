"""Low-level kitty command and socket discovery helpers."""

import concurrent.futures
import glob
import json
import os
import re
import subprocess

_DEBUG_LOG = os.environ.get("CCM_DEBUG_LOG", "")


def get_kitty_socket():
    """Discover the Kitty remote control socket."""
    sock = os.environ.get("KITTY_LISTEN_ON", "")
    if sock:
        return sock
    candidates = sorted(glob.glob("/tmp/kitty-*"), key=os.path.getmtime, reverse=True)
    return f"unix:{candidates[0]}" if candidates else ""


def _socket_label(socket_path):
    """Derive a short instance label from a socket path.

    Extracts the PID from filenames like /tmp/kitty-12345-... and returns
    "K:12345". Falls back to the full socket path when the pattern doesn't
    match.
    """
    bare = socket_path.removeprefix("unix:")
    base = os.path.basename(bare)
    m = re.match(r"kitty-(\d+)", base)
    if m:
        return f"K:{m.group(1)}"
    return socket_path


def get_all_kitty_sockets():
    """Discover all active Kitty remote control sockets.

    Collects all /tmp/kitty-* sockets plus KITTY_LISTEN_ON, probes each
    with ``kitty @ ls`` (2-second timeout), and returns a list of
    ``(socket, label)`` tuples for the ones that respond.
    """
    seen = set()
    candidates = []

    env_sock = os.environ.get("KITTY_LISTEN_ON", "")
    if env_sock:
        candidates.append(env_sock)
        seen.add(env_sock)

    for path in sorted(glob.glob("/tmp/kitty-*"), key=os.path.getmtime, reverse=True):
        sock = f"unix:{path}"
        if sock not in seen:
            candidates.append(sock)
            seen.add(sock)

    def _probe(sock):
        try:
            result = subprocess.run(
                ["kitty", "@", "--to", sock, "ls"],
                capture_output=True,
                text=True,
                timeout=2,
            )
            if result.returncode == 0:
                return (sock, _socket_label(sock))
        except (subprocess.SubprocessError, OSError):
            pass
        return None

    active = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=len(candidates) or 1) as pool:
        futures = {pool.submit(_probe, sock): sock for sock in candidates}
        for future in concurrent.futures.as_completed(futures):
            result = future.result()
            if result is not None:
                active.append(result)

    # Preserve original candidate order (env socket first, then by mtime)
    order = {sock: i for i, sock in enumerate(candidates)}
    active.sort(key=lambda x: order.get(x[0], 0))

    return active


def kitty_cmd(socket, *args):
    """Run a kitty @ remote control command. Returns the CompletedProcess or None."""
    try:
        result = subprocess.run(
            ["kitty", "@", "--to", socket, *list(args)],
            capture_output=True,
            text=True,
            timeout=5,
        )
        if _DEBUG_LOG and result.returncode != 0:
            with open(_DEBUG_LOG, "a") as f:
                f.write(
                    f"FAIL rc={result.returncode} cmd={list(args)}\n"
                    f"  stdout={result.stdout!r}\n"
                    f"  stderr={result.stderr!r}\n"
                )
        return result
    except (subprocess.SubprocessError, OSError) as exc:
        if _DEBUG_LOG:
            with open(_DEBUG_LOG, "a") as f:
                f.write(f"EXCEPTION cmd={list(args)} exc={exc}\n")
        return None


def get_window_text(socket, window_id, extent="screen"):
    """Capture text from a Kitty window. Returns text string or None."""
    result = kitty_cmd(
        socket, "get-text", "--extent", extent, "--match", f"id:{window_id}"
    )
    if result and result.returncode == 0:
        return result.stdout
    return None


def get_host_tab_id(socket):
    """Return the tab ID of the tab hosting the current dashboard process.

    Reads KITTY_WINDOW_ID (set by Kitty for all child processes) and maps it
    to a tab ID by walking the kitty @ ls output. Returns None when the
    window ID is unavailable or the lookup fails.
    """
    wid_str = os.environ.get("KITTY_WINDOW_ID")
    if not wid_str:
        return None
    try:
        wid = int(wid_str)
    except ValueError:
        return None
    result = kitty_cmd(socket, "ls")
    if not result or result.returncode != 0:
        return None
    try:
        data = json.loads(result.stdout)
    except json.JSONDecodeError:
        return None
    for os_win in data:
        for tab in os_win.get("tabs", []):
            for win in tab.get("windows", []):
                if win.get("id") == wid:
                    return tab.get("id")
    return None
