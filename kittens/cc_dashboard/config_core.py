"""Accounts/defaults/sessions configuration helpers for dashboard."""

import contextlib
import os
import shutil
from datetime import datetime

from cc_dashboard import config_state as _state
from cc_dashboard.config_data import ACTIVE_FG, INACTIVE_FG, PALETTE


def _accounts_config_path():
    env = os.environ.get("CC_MANAGER_ACCOUNTS", "")
    if env:
        return env
    xdg = os.environ.get(
        "XDG_CONFIG_HOME", os.path.join(os.path.expanduser("~"), ".config")
    )
    return os.path.join(xdg, "cc-manager", "accounts.conf")


def load_accounts():
    """Parse accounts.conf into the shared account state.

    Falls back to hardcoded work/personal defaults if the file is missing.
    """
    _state._accounts.clear()
    _state._account_names[:] = []

    path = _accounts_config_path()
    if os.path.isfile(path):
        with open(path) as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                parts = line.split("|")
                if len(parts) < 2:
                    continue
                name = parts[0].strip()
                config_dir = parts[1].strip() if len(parts) > 1 else ""
                color = parts[2].strip() if len(parts) > 2 else "gray"
                if not name:
                    continue
                config_dir = os.path.expanduser(config_dir) if config_dir else ""
                _state._accounts[name] = {"config_dir": config_dir, "color": color}
                _state._account_names.append(name)
    else:
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


def _defaults_config_path():
    env = os.environ.get("CC_MANAGER_DEFAULTS", "")
    if env:
        return env
    xdg = os.environ.get(
        "XDG_CONFIG_HOME", os.path.join(os.path.expanduser("~"), ".config")
    )
    return os.path.join(xdg, "cc-manager", "defaults.conf")


def load_defaults():
    """Parse defaults.conf into the shared default settings."""
    _state._default_claude_flags = ""
    _state._default_project_window = ""
    _state._default_sort_sessions = ""
    _state._default_dashboard_refresh = 3
    _state._default_idle_alert_minutes = 0
    _state._default_notify_idle = ""

    path = _defaults_config_path()
    if not os.path.isfile(path):
        return

    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            if "=" not in line:
                continue
            key, _, value = line.partition("=")
            key = key.strip()
            value = value.strip()
            if key == "claude_flags":
                _state._default_claude_flags = value
            elif key == "project_window":
                _state._default_project_window = value
            elif key == "sort_sessions":
                _state._default_sort_sessions = value
            elif key == "dashboard_refresh":
                with contextlib.suppress(ValueError):
                    _state._default_dashboard_refresh = max(0, int(value))
            elif key == "idle_alert_minutes":
                with contextlib.suppress(ValueError):
                    _state._default_idle_alert_minutes = max(0, int(value))
            elif key == "notify_idle":
                _state._default_notify_idle = value


def _sessions_config_path():
    env = os.environ.get("CC_MANAGER_CONFIG", "")
    if env:
        return os.path.join(env, "sessions.conf") if os.path.isdir(env) else env
    xdg = os.environ.get(
        "XDG_CONFIG_HOME", os.path.join(os.path.expanduser("~"), ".config")
    )
    return os.path.join(xdg, "cc-manager", "sessions.conf")


def _config_dir():
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


def _hooks_config_path():
    """Return the path to hooks.json in the config directory."""
    return os.path.join(_config_dir(), "hooks.json")


def _all_config_paths():
    """Return a list of (label, path) tuples for all config files."""
    return [
        ("accounts", _accounts_config_path()),
        ("sessions", _sessions_config_path()),
        ("defaults", _defaults_config_path()),
        ("hooks", _hooks_config_path()),
    ]


def load_sessions():
    """Parse sessions.conf into the shared sessions list."""
    _state._sessions[:] = []

    path = _sessions_config_path()
    if not os.path.isfile(path):
        return

    default_account = _state._account_names[0] if _state._account_names else "work"
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            parts = line.split("|")
            if len(parts) < 2:
                continue
            title = parts[0].strip()
            directory = parts[1].strip()
            account = (
                parts[2].strip()
                if len(parts) > 2 and parts[2].strip()
                else default_account
            )
            flags = parts[3].strip() if len(parts) > 3 else ""
            pw = parts[4].strip() if len(parts) > 4 else ""
            env = parts[5].strip() if len(parts) > 5 else ""
            if not title:
                continue
            _state._sessions.append(
                {
                    "title": title,
                    "dir": directory,
                    "account": account,
                    "flags": flags,
                    "pw": pw,
                    "env": env,
                }
            )


def _find_config_session(title):
    """Find a config session entry matching the given title, or None."""
    for entry in _state._sessions:
        if entry["title"] == title:
            return entry
    return None


def _resolve_claude_flags(override=""):
    """Return the effective claude flags for a session.

    Empty override inherits the global default, "-" disables all flags,
    and any other value replaces the global default entirely.
    """
    if not override:
        return _state._default_claude_flags
    if override == "-":
        return ""
    return override


def _resolve_project_window(override=""):
    """Return the effective project window layout for a session.

    Empty override inherits the global default, "-" disables the project window,
    and any other value replaces the global default entirely.
    """
    if not override:
        return _state._default_project_window
    if override == "-":
        return ""
    return override


def _resolve_account_by_config_dir(config_dir):
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


def _color_for_config_dir(config_dir):
    """Return the palette color name of the account that owns *config_dir*, or ""."""
    if not config_dir:
        return ""
    for info in _state._accounts.values():
        if info.get("config_dir") == config_dir:
            return info.get("color", "")
    return ""


def _unique_config_dirs():
    """Return a deduplicated, ordered list of config_dir paths from all accounts."""
    seen = set()
    result = []
    for name in _state._account_names:
        d = _state._accounts.get(name, {}).get("config_dir", "")
        if d and d not in seen:
            seen.add(d)
            result.append(d)
    return result


def save_sessions(live_sessions):
    """Save live sessions to sessions.conf, mirroring cmd_save from cc-launch.

    Accepts the sessions list from query_all_sessions (dicts with 'title',
    'cwd', 'account', 'cc_flags', 'cc_pw' keys).
    Returns (count, backup_created).
    """
    path = _sessions_config_path()
    home = os.path.expanduser("~")

    header_lines = []
    if os.path.isfile(path):
        with open(path) as f:
            for line in f:
                stripped = line.strip()
                if stripped.startswith("#") or not stripped:
                    header_lines.append(line)
                else:
                    break

    backup_created = False
    if os.path.isfile(path):
        shutil.copy2(path, path + ".bak")
        backup_created = True

    with open(path, "w") as f:
        if header_lines:
            for line in header_lines:
                f.write(line)
        else:
            saved_at = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
            f.write("# cc-manager session definitions\n")
            f.write(f"# Saved by dashboard on {saved_at}\n")
            f.write("#\n")
            f.write(
                "# Format: title | directory | account"
                " [| claude_flags [| project_window [| env_vars]]]\n"
            )
            f.write("\n")
        for s in live_sessions:
            title = s.get("title", "")
            directory = s.get("cwd", "~/")
            if directory.startswith(home):
                directory = "~" + directory[len(home) :]
            account = s.get("account", "unknown")
            flags = s.get("cc_flags", "")
            pw = s.get("cc_pw", "")
            env = s.get("cc_env", "")
            base = f"{title:<20s} | {directory:<40s} | {account:<10s}"
            if env:
                f.write(f"{base} | {flags:<30s} | {pw:<10s} | {env}\n")
            elif pw:
                f.write(f"{base} | {flags:<30s} | {pw}\n")
            elif flags:
                f.write(f"{base} | {flags}\n")
            else:
                f.write(f"{title:<20s} | {directory:<40s} | {account}\n")

    load_sessions()
    return len(live_sessions), backup_created


def append_session(title, directory, account, flags="", pw="", env=""):
    """Append a single session entry to sessions.conf."""
    path = _sessions_config_path()
    home = os.path.expanduser("~")
    if directory.startswith(home):
        directory = "~" + directory[len(home) :]

    needs_newline = False
    if os.path.isfile(path) and os.path.getsize(path) > 0:
        with open(path, "rb") as f:
            f.seek(-1, 2)
            needs_newline = f.read(1) != b"\n"

    with open(path, "a") as f:
        if needs_newline:
            f.write("\n")
        line = f"{title:<20s} | {directory:<40s} | {account}"
        if env:
            line += f" | {flags:<30s} | {pw:<10s} | {env}"
        elif pw:
            line += f" | {flags} | {pw}"
        elif flags:
            line += f" | {flags}"
        f.write(line + "\n")

    load_sessions()


def update_session(original_title, title, directory, account, flags="", pw="", env=""):
    """Update an existing session entry in sessions.conf by original title.

    Returns True if the entry was found and updated, False otherwise.
    """
    path = _sessions_config_path()
    if not os.path.isfile(path):
        return False

    home = os.path.expanduser("~")
    if directory.startswith(home):
        directory = "~" + directory[len(home) :]

    with open(path) as f:
        lines = f.readlines()

    found = False
    updated = []
    for line in lines:
        stripped = line.strip()
        if stripped and not stripped.startswith("#"):
            parts = stripped.split("|")
            if len(parts) >= 2 and parts[0].strip() == original_title:
                found = True
                new_line = f"{title:<20s} | {directory:<40s} | {account}"
                if env:
                    new_line += f" | {flags:<30s} | {pw:<10s} | {env}"
                elif pw:
                    new_line += f" | {flags:<30s} | {pw}"
                elif flags:
                    new_line += f" | {flags}"
                updated.append(new_line + "\n")
                continue
        updated.append(line)

    if not found:
        return False

    shutil.copy2(path, path + ".bak")
    with open(path, "w") as f:
        f.writelines(updated)

    load_sessions()
    return True


def remove_session(title):
    """Remove a session entry from sessions.conf by title. Returns True if found."""
    path = _sessions_config_path()
    if not os.path.isfile(path):
        return False

    with open(path) as f:
        lines = f.readlines()

    found = False
    kept = []
    for line in lines:
        stripped = line.strip()
        if stripped and not stripped.startswith("#"):
            parts = stripped.split("|")
            if len(parts) >= 2 and parts[0].strip() == title:
                found = True
                continue
        kept.append(line)

    if not found:
        return False

    shutil.copy2(path, path + ".bak")
    with open(path, "w") as f:
        f.writelines(kept)

    load_sessions()
    return True


def _palette_tab_colors(account, color_override="", config_dir_override=""):
    """Return a list of kitty set-tab-color arguments for the given account."""
    if color_override and color_override in PALETTE:
        color_name = color_override
    elif config_dir_override:
        color_name = _color_for_config_dir(config_dir_override) or _state._accounts.get(account, {}).get("color", "gray")
    else:
        color_name = _state._accounts.get(account, {}).get("color", "gray")
    active_bg, inactive_bg, _ = PALETTE.get(color_name, PALETTE["gray"])

    def _hex(rgb):
        return f"#{rgb[0]:02x}{rgb[1]:02x}{rgb[2]:02x}"

    return [
        f"active_bg={_hex(active_bg)}",
        f"active_fg={_hex(ACTIVE_FG)}",
        f"inactive_bg={_hex(inactive_bg)}",
        f"inactive_fg={_hex(INACTIVE_FG)}",
    ]
