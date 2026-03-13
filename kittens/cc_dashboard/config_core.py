"""Accounts/defaults/sessions configuration helpers for dashboard."""

from __future__ import annotations

import os
import re
import shutil
import sys
import tomllib
import types
from datetime import datetime
from typing import Any

from cc_dashboard import config_state as _state
from cc_dashboard.config_data import ACTIVE_FG, INACTIVE_FG, PALETTE


def _lib_dir() -> str:
    """Return the ``lib/`` directory containing ``toml_writer``/``toml_reader``.

    Resolves symlinks so this works both in-repo and when installed via
    ``just install`` (which symlinks ``cc_dashboard/`` into ``~/.config/kitty/``).
    """
    here = os.path.dirname(os.path.realpath(__file__))
    return os.path.join(here, "..", "..", "lib")


def _import_toml_writer() -> types.ModuleType:
    """Import and return the ``toml_writer`` module from ``lib/``."""
    lib = _lib_dir()
    if lib not in sys.path:
        sys.path.insert(0, lib)
    import toml_writer

    tw: types.ModuleType = toml_writer
    return tw


def _accounts_config_path() -> str:
    env = os.environ.get("CC_MANAGER_ACCOUNTS", "")
    if env:
        return env
    xdg = os.environ.get(
        "XDG_CONFIG_HOME", os.path.join(os.path.expanduser("~"), ".config")
    )
    return os.path.join(xdg, "cc-manager", "accounts.toml")


def load_accounts() -> None:
    """Parse accounts config into the shared account state."""
    _state._accounts.clear()
    _state._account_names[:] = []

    path = _accounts_config_path()
    if os.path.isfile(path):
        with open(path, "rb") as f:
            data = tomllib.load(f)
        for entry in data.get("accounts", []):
            name = entry.get("name", "")
            if not name:
                continue
            config_dir = entry.get("config_dir", "")
            config_dir = os.path.expanduser(config_dir) if config_dir else ""
            color = entry.get("color", "gray")
            _state._accounts[name] = {"config_dir": config_dir, "color": color}
            _state._account_names.append(name)
    else:
        print(
            "Note: accounts.toml not found, using default accounts (work, personal).",
            file=sys.stderr,
        )
        home = os.path.expanduser("~")
        _state._accounts["work"] = {
            "config_dir": os.path.join(home, ".claude"),
            "color": "green",
        }
        _state._accounts["personal"] = {
            "config_dir": os.path.join(home, ".claude-personal"),
            "color": "blue",
        }
        _state._account_names[:] = ["work", "personal"]


def _defaults_config_path() -> str:
    env = os.environ.get("CC_MANAGER_DEFAULTS", "")
    if env:
        return env
    xdg = os.environ.get(
        "XDG_CONFIG_HOME", os.path.join(os.path.expanduser("~"), ".config")
    )
    return os.path.join(xdg, "cc-manager", "defaults.toml")


def load_defaults() -> None:
    """Parse defaults config into the shared default settings."""
    _state._default_claude_flags = ""
    _state._default_project_window = ""
    _state._default_sort_sessions = ""
    _state._default_dashboard_refresh = 3
    _state._default_idle_alert_minutes = 0
    _state._default_notify_idle = ""
    _state._default_tab_statuses = []
    _state._default_notes_editor = ""
    _state._default_dim_after_minutes = 0

    path = _defaults_config_path()
    if not os.path.isfile(path):
        return

    with open(path, "rb") as f:
        data = tomllib.load(f)
    defaults = data.get("defaults", {})
    notifications = data.get("notifications", {})

    if "claude_flags" in defaults:
        _state._default_claude_flags = str(defaults["claude_flags"])
    if "project_window" in defaults:
        _state._default_project_window = str(defaults["project_window"])
    if "sort_sessions" in defaults:
        _state._default_sort_sessions = str(defaults["sort_sessions"])
    if "dashboard_refresh" in defaults:
        try:
            _state._default_dashboard_refresh = max(
                0, int(defaults["dashboard_refresh"])
            )
        except (ValueError, TypeError) as e:
            print(
                f"Warning: ignoring invalid config value for dashboard_refresh: {e}",
                file=sys.stderr,
            )
    if "idle_alert_minutes" in notifications:
        try:
            _state._default_idle_alert_minutes = max(
                0, int(notifications["idle_alert_minutes"])
            )
        except (ValueError, TypeError) as e:
            print(
                f"Warning: ignoring invalid config value for idle_alert_minutes: {e}",
                file=sys.stderr,
            )
    if "dim_after_minutes" in notifications:
        try:
            _state._default_dim_after_minutes = max(
                0, int(notifications["dim_after_minutes"])
            )
        except (ValueError, TypeError) as e:
            print(
                f"Warning: ignoring invalid config value for dim_after_minutes: {e}",
                file=sys.stderr,
            )
    if "idle" in notifications:
        val = notifications["idle"]
        _state._default_notify_idle = (
            "on" if val is True else ("" if val is False else str(val))
        )
    if "notes_editor" in defaults:
        _state._default_notes_editor = str(defaults["notes_editor"])
    if "kitty_timeout" in defaults:
        try:
            _state._default_kitty_timeout = max(1, int(defaults["kitty_timeout"]))
        except (ValueError, TypeError) as e:
            print(
                f"Warning: ignoring invalid config value for kitty_timeout: {e}",
                file=sys.stderr,
            )
    if "tab_statuses" in defaults:
        val = defaults["tab_statuses"]
        if isinstance(val, list):
            _state._default_tab_statuses = [str(s) for s in val]


def _save_tab_statuses(statuses: list[str]) -> None:
    """Write tab_statuses back to defaults.toml, preserving other settings."""
    path = _defaults_config_path()
    toml_writer = _import_toml_writer()

    # Read existing data
    header = ""
    defaults = {}
    notifications = {}
    if os.path.isfile(path):
        from toml_reader import read_header_comments

        header = read_header_comments(path)
        try:
            with open(path, "rb") as f:
                data = tomllib.load(f)
            defaults = data.get("defaults", {})
            notifications = data.get("notifications", {})
        except tomllib.TOMLDecodeError as e:
            print(f"Warning: failed to parse {path}: {e}", file=sys.stderr)

    defaults["tab_statuses"] = statuses

    out = {}
    if defaults:
        out["defaults"] = defaults
    if notifications:
        out["notifications"] = notifications
    tmp_path = path + ".tmp"
    try:
        with open(tmp_path, "w") as f:
            f.write(toml_writer.dumps(out, header=header))
            f.write("\n")
        os.replace(tmp_path, path)
    except OSError as e:
        print(f"Warning: failed to save {path}: {e}", file=sys.stderr)
        return

    _state._default_tab_statuses = statuses


def _sessions_config_path() -> str:
    env = os.environ.get("CC_MANAGER_CONFIG", "")
    if env:
        if os.path.isdir(env):
            return os.path.join(env, "sessions.toml")
        return env
    xdg = os.environ.get(
        "XDG_CONFIG_HOME", os.path.join(os.path.expanduser("~"), ".config")
    )
    return os.path.join(xdg, "cc-manager", "sessions.toml")


def _config_dir() -> str:
    """Return the base config directory, mirroring resolve_config_dir() from bin/ccm."""
    env_config = os.environ.get("CC_MANAGER_CONFIG", "")
    if env_config:
        return os.path.dirname(env_config) if os.path.isfile(env_config) else env_config
    env_accounts = os.environ.get("CC_MANAGER_ACCOUNTS", "")
    if env_accounts:
        return os.path.dirname(env_accounts)
    xdg = os.environ.get(
        "XDG_CONFIG_HOME", os.path.join(os.path.expanduser("~"), ".config")
    )
    return os.path.join(xdg, "cc-manager")


def _notes_dir() -> str:
    """Return the notes directory, creating it if necessary."""
    d = os.path.join(_config_dir(), "notes")
    os.makedirs(d, exist_ok=True)
    return d


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
    if os.path.isfile(path):
        with open(path) as f:
            return f.read()
    return ""


def rename_notes(old_title: str, new_title: str) -> None:
    """Rename a notes file when a session title changes."""
    old_path = notes_file_for_title(old_title)
    if os.path.isfile(old_path):
        new_path = notes_file_for_title(new_title)
        os.rename(old_path, new_path)


def _resolve_notes_editor() -> str:
    """Return the editor command to use for session notes."""
    return (
        _state._default_notes_editor
        or os.environ.get("EDITOR")
        or os.environ.get("VISUAL")
        or "vim"
    )


def _sessions_config_filename() -> str:
    """Return the basename of the sessions config file (e.g. ``sessions.toml``)."""
    return os.path.basename(_sessions_config_path())


def _hooks_config_path() -> str:
    """Return the path to hooks.json in the config directory."""
    return os.path.join(_config_dir(), "hooks.json")


def _all_config_paths() -> list[tuple[str, str]]:
    """Return a list of (label, path) tuples for all config files."""
    return [
        ("accounts", _accounts_config_path()),
        ("sessions", _sessions_config_path()),
        ("defaults", _defaults_config_path()),
        ("hooks", _hooks_config_path()),
    ]


def load_sessions() -> None:
    """Parse sessions config into the shared sessions list."""
    _state._sessions[:] = []

    path = _sessions_config_path()
    if not os.path.isfile(path):
        return

    default_account = _state._account_names[0] if _state._account_names else "work"
    with open(path, "rb") as f:
        data = tomllib.load(f)
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
            }
        )


def _find_config_session(title: str) -> dict[str, str] | None:
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


def _resolve_account_by_config_dir(config_dir: str) -> str:
    """Reverse-lookup: find an account name matching the given config directory."""
    home = os.path.expanduser("~")
    default_dir = os.path.join(home, ".claude")

    for name, info in _state._accounts.items():
        d = info["config_dir"]
        if config_dir and d == config_dir:
            return name
        if not config_dir and (not d or d == default_dir):
            return name

    if not config_dir:
        for name, info in _state._accounts.items():
            d = info["config_dir"]
            if not d or d == default_dir:
                return name

    return _state._account_names[0] if _state._account_names else "unknown"


def _color_for_config_dir(config_dir: str) -> str:
    """Return the palette color name of the account that owns *config_dir*, or ""."""
    if not config_dir:
        return ""
    for info in _state._accounts.values():
        if info.get("config_dir") == config_dir:
            return info.get("color", "")
    return ""


def _unique_config_dirs() -> list[str]:
    """Return a deduplicated, ordered list of config_dir paths from all accounts."""
    seen = set()
    result = []
    for name in _state._account_names:
        d = _state._accounts.get(name, {}).get("config_dir", "")
        if d and d not in seen:
            seen.add(d)
            result.append(d)
    return result


def _read_header_comments(path: str) -> str:
    """Extract leading ``#`` comment / blank lines from a config file."""
    lines = []
    with open(path) as f:
        for line in f:
            stripped = line.strip()
            if stripped.startswith("#") or not stripped:
                lines.append(line)
            else:
                break
    return "".join(lines) if lines else ""


def _tilde_directory(directory: str) -> str:
    """Replace ``$HOME`` prefix with ``~`` in *directory*."""
    home = os.path.expanduser("~")
    if directory.startswith(home):
        return "~" + directory[len(home) :]
    return directory


def _session_to_toml_entry(
    title: str,
    directory: str,
    account: str,
    flags: str = "",
    pw: str = "",
    env: str = "",
    group: str = "",
) -> dict[str, str]:
    """Build a TOML-serializable dict for one ``[[sessions]]`` entry."""
    d = {"title": title, "directory": directory}
    if account:
        d["account"] = account
    if flags:
        d["flags"] = flags
    if pw:
        d["project_window"] = pw
    if env:
        d["env"] = env
    if group:
        d["group"] = group
    return d


def _write_sessions_toml(
    path: str, sessions_data: list[dict[str, str]], header: str = ""
) -> None:
    """Serialize *sessions_data* (list of TOML entry dicts) to *path*."""
    tw = _import_toml_writer()
    with open(path, "w") as f:
        f.write(tw.dumps({"sessions": sessions_data}, header=header))


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
    for s in live_sessions:
        directory = _tilde_directory(s.get("cwd", "~/"))
        entries.append(
            _session_to_toml_entry(
                s.get("title", ""),
                directory,
                s.get("account", "unknown"),
                s.get("cc_flags", ""),
                s.get("cc_pw", ""),
                s.get("cc_env", ""),
                s.get("group", ""),
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
        _session_to_toml_entry(title, directory, account, flags, pw, env, group)
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
    for i, entry in enumerate(entries):
        if entry.get("title") == original_title:
            entries[i] = _session_to_toml_entry(
                title, directory, account, flags, pw, env, group
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
    entries = [e for e in entries if e.get("title") != title]

    if len(entries) == original_len:
        return False

    shutil.copy2(path, path + ".bak")
    _write_sessions_toml(path, entries, header=header.rstrip("\n") if header else "")
    load_sessions()
    return True


def _palette_tab_colors(
    account: str, color_override: str = "", config_dir_override: str = ""
) -> list[str]:
    """Return a list of kitty set-tab-color arguments for the given account."""
    if color_override and color_override in PALETTE:
        color_name = color_override
    elif account and account in _state._accounts:
        color_name = _state._accounts[account].get("color", "gray")
    elif config_dir_override:
        color_name = _color_for_config_dir(config_dir_override) or "gray"
    else:
        color_name = "gray"
    _FALLBACK_COLOR = ((128, 128, 128), (80, 80, 80), (255, 255, 255))
    active_bg, inactive_bg, _ = PALETTE.get(
        color_name, PALETTE.get("gray", _FALLBACK_COLOR)
    )

    def _hex(rgb):
        return f"#{rgb[0]:02x}{rgb[1]:02x}{rgb[2]:02x}"

    return [
        f"active_bg={_hex(active_bg)}",
        f"active_fg={_hex(ACTIVE_FG)}",
        f"inactive_bg={_hex(inactive_bg)}",
        f"inactive_fg={_hex(INACTIVE_FG)}",
    ]
