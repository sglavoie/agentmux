"""Sessions configuration helpers for dashboard."""

from __future__ import annotations

import contextlib
import os
import shutil
import tomllib
from datetime import datetime
from typing import Any

from cc_dashboard import config_state as _state
from cc_dashboard.config_notes import rename_notes
from cc_dashboard.config_paths import (
    _read_header_comments,
    _sessions_config_path,
    _tilde_directory,
    _import_toml_writer,
)


def load_sessions() -> None:
    """Parse sessions config into the shared sessions list."""
    _state._sessions[:] = []

    path = _sessions_config_path()
    if not os.path.isfile(path):
        return

    default_account = _state._account_names[0] if _state._account_names else "work"
    try:
        with open(path, "rb") as f:
            data = tomllib.load(f)
    except (tomllib.TOMLDecodeError, OSError) as e:
        print(
            f"Warning: failed to parse {path}: {e}"
            " (check syntax with: ccm config validate)",
            file=sys.stderr,
        )
        return
    for entry in data.get("sessions", []):
        title = entry.get("title", "")
        if not title:
            continue
        _state._sessions.append(
            {
                "title": title,
                "dir": entry.get("directory", ""),
                "account": entry.get("account", "") or default_account,
                "flags": entry.get("flags", ""),
                "pw": entry.get("project_window", ""),
                "env": entry.get("env", ""),
                "group": entry.get("group", ""),
                "commands": _normalize_commands(entry.get("commands")),
            }
        )


def _find_config_session(title: str) -> dict[str, Any] | None:
    """Find a config session entry matching the given title, or None."""
    for entry in _state._sessions:
        if entry["title"] == title:
            return entry
    return None


def _resolve_claude_flags(override: str = "") -> str:
    """Return the effective claude flags for a session.

    Empty override inherits the global default, "-" disables all flags,
    and any other value replaces the global default entirely.
    """
    if not override:
        return _state._default_claude_flags
    if override == "-":
        return ""
    return override


def _resolve_project_window(override: str = "") -> str:
    """Return the effective project window layout for a session.

    Empty override inherits the global default, "-" disables the project window,
    and any other value replaces the global default entirely.
    """
    if not override:
        return _state._default_project_window
    if override == "-":
        return ""
    return override


def _normalize_commands(raw_commands: Any) -> list[str]:
    """Return a sanitized list of command strings."""
    if raw_commands is None:
        return []
    if isinstance(raw_commands, str):
        return raw_commands.split("\x1f") if raw_commands else []
    if not isinstance(raw_commands, list):
        return []
    result: list[str] = []
    for command in raw_commands:
        if (
            isinstance(command, str)
            and "\t" not in command
            and "\n" not in command
            and "\r" not in command
        ):
            result.append(command)
    return result


def _session_to_toml_entry(
    title: str,
    directory: str,
    account: str,
    flags: str = "",
    pw: str = "",
    env: str = "",
    group: str = "",
    commands: list[str] | None = None,
) -> dict[str, Any]:
    """Build a TOML-serializable dict for one ``[[sessions]]`` entry."""
    entry: dict[str, Any] = {"title": title, "directory": directory}
    if account:
        entry["account"] = account
    if flags:
        entry["flags"] = flags
    if pw:
        entry["project_window"] = pw
    if env:
        entry["env"] = env
    if group:
        entry["group"] = group
    if commands:
        entry["commands"] = commands
    return entry


def _write_sessions_toml(
    path: str, sessions_data: list[dict[str, Any]], header: str = ""
) -> None:
    """Serialize *sessions_data* (list of TOML entry dicts) to *path* atomically."""
    toml_writer = _import_toml_writer()
    content = toml_writer.dumps({"sessions": sessions_data}, header=header)
    tmp_path = path + ".tmp"
    try:
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(tmp_path, "w") as f:
            f.write(content)
        os.replace(tmp_path, path)
    except OSError:
        with contextlib.suppress(OSError):
            os.remove(tmp_path)
        raise


def _read_sessions_toml_raw(path: str) -> list[dict[str, Any]]:
    """Return the raw ``[[sessions]]`` list from a TOML file."""
    with open(path, "rb") as f:
        data = tomllib.load(f)
    result: list[dict[str, Any]] = data.get("sessions", [])
    return result


def save_sessions(live_sessions: list[dict[str, Any]]) -> tuple[int, bool]:
    """Save live sessions to the sessions config file.

    Accepts the sessions list from query_all_sessions (dicts with 'title',
    'cwd', 'account', 'cc_flags', 'cc_pw' keys).
    Returns (count, backup_created).
    """
    path = _sessions_config_path()

    header = ""
    if os.path.isfile(path):
        header = _read_header_comments(path)

    backup_created = False
    if os.path.isfile(path):
        shutil.copy2(path, path + ".bak")
        backup_created = True

    entries = []
    for session in live_sessions:
        directory = _tilde_directory(session.get("cwd", "~/"))
        commands = _normalize_commands(session.get("cc_commands"))
        entries.append(
            _session_to_toml_entry(
                session.get("title", ""),
                directory,
                session.get("account", "unknown"),
                session.get("cc_flags", ""),
                session.get("cc_pw", ""),
                session.get("cc_env", ""),
                session.get("group", ""),
                commands or None,
            )
        )
    if not header:
        saved_at = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
        header = (
            f"# cc-manager session definitions\n# Saved by dashboard on {saved_at}\n"
        )
    _write_sessions_toml(path, entries, header=header.rstrip("\n"))

    load_sessions()
    return len(live_sessions), backup_created


def append_session(
    title: str,
    directory: str,
    account: str,
    flags: str = "",
    pw: str = "",
    env: str = "",
    group: str = "",
    commands: list[str] | None = None,
) -> None:
    """Append a single session entry to the sessions config file."""
    path = _sessions_config_path()
    directory = _tilde_directory(directory)

    header = ""
    entries = []
    if os.path.isfile(path):
        header = _read_header_comments(path)
        entries = _read_sessions_toml_raw(path)
    entries.append(
        _session_to_toml_entry(
            title, directory, account, flags, pw, env, group, commands
        )
    )
    _write_sessions_toml(path, entries, header=header.rstrip("\n") if header else "")

    load_sessions()


def update_session(
    original_title: str,
    title: str,
    directory: str,
    account: str,
    flags: str = "",
    pw: str = "",
    env: str = "",
    group: str = "",
    commands: list[str] | None = None,
) -> bool:
    """Update an existing session entry by original title.

    Returns True if the entry was found and updated, False otherwise.
    """
    path = _sessions_config_path()
    if not os.path.isfile(path):
        return False

    directory = _tilde_directory(directory)

    header = _read_header_comments(path)
    entries = _read_sessions_toml_raw(path)

    found = False
    for index, entry in enumerate(entries):
        if entry.get("title") == original_title:
            entries[index] = _session_to_toml_entry(
                title, directory, account, flags, pw, env, group, commands
            )
            found = True
            if original_title != title:
                rename_notes(original_title, title)
            break

    if not found:
        return False

    shutil.copy2(path, path + ".bak")
    _write_sessions_toml(path, entries, header=header.rstrip("\n") if header else "")
    load_sessions()
    return True


def remove_session(title: str) -> bool:
    """Remove a session entry by title. Returns True if found."""
    path = _sessions_config_path()
    if not os.path.isfile(path):
        return False

    header = _read_header_comments(path)
    entries = _read_sessions_toml_raw(path)

    original_len = len(entries)
    entries = [entry for entry in entries if entry.get("title") != title]

    if len(entries) == original_len:
        return False

    shutil.copy2(path, path + ".bak")
    _write_sessions_toml(path, entries, header=header.rstrip("\n") if header else "")
    load_sessions()
    return True


__all__ = [
    "_find_config_session",
    "_normalize_commands",
    "_read_sessions_toml_raw",
    "_resolve_claude_flags",
    "_resolve_project_window",
    "_session_to_toml_entry",
    "_write_sessions_toml",
    "append_session",
    "load_sessions",
    "remove_session",
    "save_sessions",
    "update_session",
]
