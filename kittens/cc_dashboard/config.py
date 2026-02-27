"""Account and defaults configuration for the Claude Code Dashboard."""

import contextlib
import json
import os
import shutil
from datetime import datetime

# ── Shared data loading ──────────────────────────────────────────────────────


def _resolve_data_path(filename):
    """Resolve the path to a shared JSON data file.

    Checks the repo layout first (../../data/ relative to this file), then
    the installed layout (~/.local/lib/cc-manager/).
    """
    this_dir = os.path.dirname(os.path.realpath(__file__))
    repo_path = os.path.join(this_dir, "..", "..", "data", filename)
    if os.path.isfile(repo_path):
        return os.path.realpath(repo_path)
    installed_path = os.path.join(
        os.path.expanduser("~"), ".local", "lib", "cc-manager", filename
    )
    if os.path.isfile(installed_path):
        return installed_path
    return None


def _hex_to_rgb(hex_str):
    """Convert a hex color string like '#2d7d46' to an (R, G, B) tuple."""
    h = hex_str.lstrip("#")
    return (int(h[0:2], 16), int(h[2:4], 16), int(h[4:6], 16))


def _load_palette():
    """Load color palette from data/palette.json.

    Returns (palette_dict, active_fg_tuple, inactive_fg_tuple) where
    palette_dict maps color names to (active_bg_rgb, inactive_bg_rgb, text_fg_rgb).
    """
    path = _resolve_data_path("palette.json")
    if not path:
        return {}, (255, 255, 255), (170, 170, 170)
    with open(path) as f:
        data = json.load(f)
    palette = {}
    for name, entry in data.get("colors", {}).items():
        palette[name] = (
            _hex_to_rgb(entry["active_bg"]),
            _hex_to_rgb(entry["inactive_bg"]),
            tuple(entry["text_fg"]),
        )
    return palette, _hex_to_rgb(data["active_fg"]), _hex_to_rgb(data["inactive_fg"])


# ── Color palette ────────────────────────────────────────────────────────────
# Loaded from data/palette.json. Each entry maps a color name to
# (active_bg_rgb, inactive_bg_rgb, text_fg_rgb).

PALETTE, ACTIVE_FG, INACTIVE_FG = _load_palette()


# ── Account configuration ───────────────────────────────────────────────────

_accounts: dict[str, dict[str, str]] = {}  # name -> {"config_dir": str, "color": str}
_account_names: list[str] = []  # ordered list of account names

# Global defaults loaded from defaults.conf
_default_claude_flags = ""
_default_project_window = ""
_default_sort_sessions = ""
_default_dashboard_refresh = 3
_default_idle_alert_minutes = 0
_default_notify_idle = ""


def _accounts_config_path():
    env = os.environ.get("CC_MANAGER_ACCOUNTS", "")
    if env:
        return env
    xdg = os.environ.get(
        "XDG_CONFIG_HOME", os.path.join(os.path.expanduser("~"), ".config")
    )
    return os.path.join(xdg, "cc-manager", "accounts.conf")


def load_accounts():
    """Parse accounts.conf into the module-level _accounts dict.

    Falls back to hardcoded work/personal defaults if the file is missing.
    """
    global _accounts, _account_names
    _accounts = {}
    _account_names = []

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
                _accounts[name] = {"config_dir": config_dir, "color": color}
                _account_names.append(name)
    else:
        home = os.path.expanduser("~")
        _accounts["work"] = {
            "config_dir": os.path.join(home, ".claude"),
            "color": "green",
        }
        _accounts["personal"] = {
            "config_dir": os.path.join(home, ".claude-personal"),
            "color": "blue",
        }
        _account_names = ["work", "personal"]


def _defaults_config_path():
    env = os.environ.get("CC_MANAGER_DEFAULTS", "")
    if env:
        return env
    xdg = os.environ.get(
        "XDG_CONFIG_HOME", os.path.join(os.path.expanduser("~"), ".config")
    )
    return os.path.join(xdg, "cc-manager", "defaults.conf")


def load_defaults():
    """Parse defaults.conf into the module-level default settings."""
    global \
        _default_claude_flags, \
        _default_project_window, \
        _default_sort_sessions, \
        _default_dashboard_refresh, \
        _default_idle_alert_minutes, \
        _default_notify_idle
    _default_claude_flags = ""
    _default_project_window = ""
    _default_sort_sessions = ""
    _default_dashboard_refresh = 3
    _default_idle_alert_minutes = 0
    _default_notify_idle = ""

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
                _default_claude_flags = value
            elif key == "project_window":
                _default_project_window = value
            elif key == "sort_sessions":
                _default_sort_sessions = value
            elif key == "dashboard_refresh":
                with contextlib.suppress(ValueError):
                    _default_dashboard_refresh = max(0, int(value))
            elif key == "idle_alert_minutes":
                with contextlib.suppress(ValueError):
                    _default_idle_alert_minutes = max(0, int(value))
            elif key == "notify_idle":
                _default_notify_idle = value


# ── Session configuration ──────────────────────────────────────────────────

# list of {"title": str, "dir": str, "account": str, "flags": str, "pw": str}
_sessions: list[dict[str, str]] = []


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
    """Parse sessions.conf into the module-level _sessions list."""
    global _sessions
    _sessions = []

    path = _sessions_config_path()
    if not os.path.isfile(path):
        return

    default_account = _account_names[0] if _account_names else "work"
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
            if not title:
                continue
            _sessions.append(
                {
                    "title": title,
                    "dir": directory,
                    "account": account,
                    "flags": flags,
                    "pw": pw,
                }
            )


def _find_config_session(title):
    """Find a config session entry matching the given title, or None."""
    for entry in _sessions:
        if entry["title"] == title:
            return entry
    return None


def _resolve_claude_flags(override=""):
    """Return the effective claude flags for a session.

    Empty override inherits the global default, "-" disables all flags,
    and any other value replaces the global default entirely.
    """
    if not override:
        return _default_claude_flags
    if override == "-":
        return ""
    return override


def _resolve_project_window(override=""):
    """Return the effective project window layout for a session.

    Empty override inherits the global default, "-" disables the project window,
    and any other value replaces the global default entirely.
    """
    if not override:
        return _default_project_window
    if override == "-":
        return ""
    return override


def _resolve_account_by_config_dir(config_dir):
    """Reverse-lookup: find an account name matching the given config directory."""
    home = os.path.expanduser("~")
    default_dir = os.path.join(home, ".claude")

    for name, info in _accounts.items():
        d = info["config_dir"]
        if config_dir and d == config_dir:
            return name
        if not config_dir and (not d or d == default_dir):
            return name

    # No config dir detected — return the account that uses the default dir
    if not config_dir:
        for name, info in _accounts.items():
            d = info["config_dir"]
            if not d or d == default_dir:
                return name

    return _account_names[0] if _account_names else "unknown"


def _unique_config_dirs():
    """Return a deduplicated, ordered list of config_dir paths from all accounts."""
    seen = set()
    result = []
    for name in _account_names:
        d = _accounts.get(name, {}).get("config_dir", "")
        if d and d not in seen:
            seen.add(d)
            result.append(d)
    return result


def save_sessions(live_sessions):
    """Save live sessions to sessions.conf, mirroring cmd_save from cc-launch.

    Accepts the sessions list from query_all_sessions (dicts with 'title',
    'cwd', 'account' keys).  Returns (count, backup_created).
    """
    path = _sessions_config_path()
    home = os.path.expanduser("~")

    backup_created = False
    if os.path.isfile(path):
        shutil.copy2(path, path + ".bak")
        backup_created = True

    with open(path, "w") as f:
        f.write("# cc-manager session definitions\n")
        f.write(
            f"# Saved by dashboard on {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}\n"
        )
        f.write("#\n")
        f.write("# Format: title | directory | account\n")
        f.write("\n")
        for s in live_sessions:
            title = s.get("title", "")
            directory = s.get("cwd", "~/")
            if directory.startswith(home):
                directory = "~" + directory[len(home):]
            account = s.get("account", "unknown")
            f.write(f"{title:<20s} | {directory:<40s} | {account}\n")

    load_sessions()
    return len(live_sessions), backup_created


def append_session(title, directory, account, flags="", pw=""):
    """Append a single session entry to sessions.conf."""
    path = _sessions_config_path()
    home = os.path.expanduser("~")
    if directory.startswith(home):
        directory = "~" + directory[len(home):]

    # Ensure the file ends with a newline before appending
    needs_newline = False
    if os.path.isfile(path) and os.path.getsize(path) > 0:
        with open(path, "rb") as f:
            f.seek(-1, 2)
            needs_newline = f.read(1) != b"\n"

    with open(path, "a") as f:
        if needs_newline:
            f.write("\n")
        line = f"{title:<20s} | {directory:<40s} | {account}"
        if pw:
            line += f" | {flags} | {pw}"
        elif flags:
            line += f" | {flags}"
        f.write(line + "\n")

    load_sessions()


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


def _palette_tab_colors(account):
    """Return a list of kitty set-tab-color arguments for the given account."""
    color_name = _accounts.get(account, {}).get("color", "gray")
    active_bg, inactive_bg, _ = PALETTE.get(color_name, PALETTE["gray"])

    def _hex(rgb):
        return f"#{rgb[0]:02x}{rgb[1]:02x}{rgb[2]:02x}"

    return [
        f"active_bg={_hex(active_bg)}",
        f"active_fg={_hex(ACTIVE_FG)}",
        f"inactive_bg={_hex(inactive_bg)}",
        f"inactive_fg={_hex(INACTIVE_FG)}",
    ]
