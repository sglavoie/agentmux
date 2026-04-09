"""Basic tab actions: focus, close, rename, and clone naming."""

import contextlib
import os
import re
import subprocess

from cc_dashboard.remote.command import kitty_cmd
from cc_dashboard.remote.shared import _strip_override_marker


def do_focus(socket, tab_id):
    """Focus a tab by its Kitty tab ID. Returns True on success."""
    result = kitty_cmd(socket, "focus-tab", "--match", f"id:{tab_id}")
    return result is not None and result.returncode == 0


def do_cross_instance_focus(socket, tab_id, home_socket):
    """Focus a tab and raise the OS window if it's in a different Kitty instance.

    When the target socket matches home_socket, this delegates to do_focus.
    When the target is a different instance, it additionally uses osascript
    to bring that instance's OS window to the foreground.
    """
    do_focus(socket, tab_id)
    if socket == home_socket:
        return
    bare = socket.removeprefix("unix:")
    base = os.path.basename(bare)
    match = re.match(r"kitty-(\d+)", base)
    if not match:
        return
    pid = match.group(1)
    with contextlib.suppress(subprocess.SubprocessError, OSError):
        subprocess.run(
            [
                "osascript",
                "-e",
                f'tell application "System Events" to set frontmost of '
                f"(first process whose unix id is {pid}) to true",
            ],
            capture_output=True,
            timeout=3,
        )


def do_close(socket, tab_id):
    """Close a tab by its Kitty tab ID. Returns True on success."""
    result = kitty_cmd(socket, "close-tab", "--match", f"id:{tab_id}")
    return result is not None and result.returncode == 0


def do_rename(socket, tab_id, new_title):
    """Rename a tab by its Kitty tab ID. Returns True on success."""
    result = kitty_cmd(socket, "set-tab-title", "--match", f"id:{tab_id}", new_title)
    return result is not None and result.returncode == 0


def generate_clone_title(sessions, original_title):
    """Generate a unique title for a cloned session.

    Strips the override marker and any trailing -N suffix to find the base
    title, then increments N until a unique title is found among existing
    sessions.
    """
    existing_titles = {session["title"] for session in sessions}
    stripped = _strip_override_marker(original_title)
    base = re.sub(r"-(\d+)$", "", stripped)
    n = 2
    max_clones = 100
    while n <= max_clones:
        candidate = f"{base}-{n}"
        if candidate not in existing_titles:
            return candidate
        n += 1
    return f"{base}-{n}"


__all__ = [
    "do_close",
    "do_cross_instance_focus",
    "do_focus",
    "do_rename",
    "generate_clone_title",
]
