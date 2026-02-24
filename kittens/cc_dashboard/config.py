"""Account and defaults configuration for the Claude Code Dashboard."""

import os


# ── Color palette ────────────────────────────────────────────────────────────
# Each entry maps a color name to (active_bg_rgb, inactive_bg_rgb) tuples.
# active_fg is always (255,255,255) and inactive_fg is always (170,170,170).

PALETTE = {
    "green":  ((0x2d, 0x7d, 0x46), (0x1a, 0x4a, 0x2a)),
    "blue":   ((0x2d, 0x5a, 0x88), (0x1a, 0x36, 0x55)),
    "red":    ((0x8b, 0x2d, 0x2d), (0x55, 0x1a, 0x1a)),
    "orange": ((0x8b, 0x5a, 0x2d), (0x55, 0x3a, 0x1a)),
    "purple": ((0x5a, 0x2d, 0x8b), (0x3a, 0x1a, 0x55)),
    "teal":   ((0x2d, 0x7d, 0x7d), (0x1a, 0x4a, 0x4a)),
    "pink":   ((0x8b, 0x2d, 0x6a), (0x55, 0x1a, 0x42)),
    "yellow": ((0x7d, 0x7d, 0x2d), (0x4a, 0x4a, 0x1a)),
    "gray":   ((0x55, 0x55, 0x55), (0x33, 0x33, 0x33)),
    "cyan":   ((0x2d, 0x6a, 0x8b), (0x1a, 0x42, 0x55)),
}

ACTIVE_FG = (255, 255, 255)
INACTIVE_FG = (170, 170, 170)


# ── Account configuration ───────────────────────────────────────────────────

_accounts = {}       # name -> {"config_dir": str, "color": str}
_account_names = []  # ordered list of account names

# Global defaults loaded from defaults.conf
_default_claude_flags = ""
_default_project_window = ""


def _accounts_config_path():
    env = os.environ.get("CC_MANAGER_ACCOUNTS", "")
    if env:
        return env
    xdg = os.environ.get("XDG_CONFIG_HOME", os.path.join(os.path.expanduser("~"), ".config"))
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
        _accounts["work"] = {"config_dir": os.path.join(home, ".claude"), "color": "green"}
        _accounts["personal"] = {"config_dir": os.path.join(home, ".claude-personal"), "color": "blue"}
        _account_names = ["work", "personal"]


def _defaults_config_path():
    env = os.environ.get("CC_MANAGER_DEFAULTS", "")
    if env:
        return env
    xdg = os.environ.get("XDG_CONFIG_HOME", os.path.join(os.path.expanduser("~"), ".config"))
    return os.path.join(xdg, "cc-manager", "defaults.conf")


def load_defaults():
    """Parse defaults.conf into the module-level default settings."""
    global _default_claude_flags, _default_project_window
    _default_claude_flags = ""
    _default_project_window = ""

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


def _palette_tab_colors(account):
    """Return a list of kitty set-tab-color arguments for the given account."""
    color_name = _accounts.get(account, {}).get("color", "gray")
    active_bg, inactive_bg = PALETTE.get(color_name, PALETTE["gray"])

    def _hex(rgb):
        return f"#{rgb[0]:02x}{rgb[1]:02x}{rgb[2]:02x}"

    return [
        f"active_bg={_hex(active_bg)}",
        "active_fg=#ffffff",
        f"inactive_bg={_hex(inactive_bg)}",
        "inactive_fg=#aaaaaa",
    ]
