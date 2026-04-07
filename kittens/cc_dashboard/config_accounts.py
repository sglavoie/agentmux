"""Account and tab-color helpers for dashboard configuration."""

from __future__ import annotations

import os
import sys
import tomllib

from cc_dashboard import config_state as _state
from cc_dashboard.config_data import ACTIVE_FG, INACTIVE_FG, PALETTE
from cc_dashboard.config_paths import _accounts_config_path


def load_accounts() -> None:
    """Parse accounts config into the shared account state."""
    _state._accounts.clear()
    _state._account_names[:] = []

    path = _accounts_config_path()
    if os.path.isfile(path):
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
        for entry in data.get("accounts", []):
            name = entry.get("name", "")
            if not name:
                continue
            config_dir = entry.get("config_dir", "")
            config_dir = os.path.expanduser(config_dir) if config_dir else ""
            color = entry.get("color", "gray")
            if color not in PALETTE:
                print(
                    f"Warning: unknown color '{color}' for account '{name}',"
                    " falling back to gray.",
                    file=sys.stderr,
                )
                color = "gray"
            _state._accounts[name] = {"config_dir": config_dir, "color": color}
            _state._account_names.append(name)
        return

    print(
        f"Note: accounts.toml not found at {path}, using defaults."
        " Run 'ccm config init' to create.",
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


def _resolve_account_by_config_dir(config_dir: str) -> str:
    """Reverse-lookup: find an account name matching the given config directory."""
    home = os.path.expanduser("~")
    default_dir = os.path.join(home, ".claude")

    for name, info in _state._accounts.items():
        directory = info["config_dir"]
        if config_dir and directory == config_dir:
            return name
        if not config_dir and (not directory or directory == default_dir):
            return name

    if not config_dir:
        for name, info in _state._accounts.items():
            directory = info["config_dir"]
            if not directory or directory == default_dir:
                return name

    return _state._account_names[0] if _state._account_names else "unknown"


def _color_for_config_dir(config_dir: str) -> str:
    """Return the palette color name of the account that owns *config_dir*, or ''."""
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
        directory = _state._accounts.get(name, {}).get("config_dir", "")
        if directory and directory not in seen:
            seen.add(directory)
            result.append(directory)
    return result


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
    fallback_color = ((128, 128, 128), (80, 80, 80), (255, 255, 255))
    active_bg, inactive_bg, _ = PALETTE.get(
        color_name, PALETTE.get("gray", fallback_color)
    )

    def _hex(rgb):
        return f"#{rgb[0]:02x}{rgb[1]:02x}{rgb[2]:02x}"

    return [
        f"active_bg={_hex(active_bg)}",
        f"active_fg={_hex(ACTIVE_FG)}",
        f"inactive_bg={_hex(inactive_bg)}",
        f"inactive_fg={_hex(INACTIVE_FG)}",
    ]


__all__ = [
    "_accounts_config_path",
    "_color_for_config_dir",
    "_palette_tab_colors",
    "_resolve_account_by_config_dir",
    "_unique_config_dirs",
    "load_accounts",
]
