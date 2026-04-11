"""Session notes helpers for dashboard configuration."""

from __future__ import annotations

import os
import re
import shlex
import shutil

from cc_dashboard import config_state as _state
from cc_dashboard.config_paths import _config_dir, _notes_dir


def _sanitize_title(title: str) -> str:
    """Sanitize a session title for use as a filename."""
    name = title.replace(":", "_")
    name = re.sub(r"[/\x00]", "", name)
    return name


def notes_file_for_title(title: str) -> str:
    """Return the notes file path for a given session title."""
    notes_dir = os.path.realpath(_notes_dir())
    filename = f"{_sanitize_title(title)}.md"
    path = os.path.realpath(os.path.join(notes_dir, filename))
    if not path.startswith(notes_dir + os.sep):
        raise ValueError(f"Title would escape notes directory: {title!r}")
    return path


def read_notes(title: str) -> str:
    """Read notes for a session title. Returns '' if no notes file exists."""
    path = notes_file_for_title(title)
    try:
        with open(path) as f:
            return f.read()
    except OSError:
        return ""


def rename_notes(old_title: str, new_title: str) -> None:
    """Rename a notes file when a session title changes."""
    old_path = notes_file_for_title(old_title)
    if os.path.isfile(old_path):
        new_path = notes_file_for_title(new_title)
        os.rename(old_path, new_path)


def _resolve_notes_editor() -> str:
    """Return the editor command to use for session notes.

    Resolves the binary to an absolute path so Kitty's ``launch`` command
    (which executes directly, without a shell) can find editors installed
    outside Kitty's restricted PATH (e.g. /opt/homebrew/bin).
    """
    editor = (
        _state._default_notes_editor
        or os.environ.get("EDITOR")
        or os.environ.get("VISUAL")
        or "vim"
    )
    parts = shlex.split(editor)
    # Kitty's process has a restricted PATH that excludes common install
    # locations like /opt/homebrew/bin.  Augment the search path so
    # shutil.which() can find editors installed there.
    search_path = os.pathsep.join(
        filter(
            None,
            [
                os.environ.get("PATH", ""),
                "/opt/homebrew/bin",
                "/usr/local/bin",
                os.path.expanduser("~/.local/bin"),
            ],
        )
    )
    resolved = shutil.which(parts[0], path=search_path)
    if resolved:
        parts[0] = resolved
    return shlex.join(parts)


__all__ = [
    "_config_dir",
    "_notes_dir",
    "_resolve_notes_editor",
    "_sanitize_title",
    "notes_file_for_title",
    "read_notes",
    "rename_notes",
]
