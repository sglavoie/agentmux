"""Path and file utility helpers for dashboard configuration."""

from __future__ import annotations

import os
import sys
import types


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
        return os.path.expanduser(env)
    xdg = os.environ.get(
        "XDG_CONFIG_HOME", os.path.join(os.path.expanduser("~"), ".config")
    )
    return os.path.join(xdg, "cc-manager", "accounts.toml")


def _defaults_config_path() -> str:
    env = os.environ.get("CC_MANAGER_DEFAULTS", "")
    if env:
        return os.path.expanduser(env)
    xdg = os.environ.get(
        "XDG_CONFIG_HOME", os.path.join(os.path.expanduser("~"), ".config")
    )
    return os.path.join(xdg, "cc-manager", "defaults.toml")


def _sessions_config_path() -> str:
    env = os.environ.get("CC_MANAGER_CONFIG", "")
    if env:
        env = os.path.expanduser(env)
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
    directory = os.path.join(_config_dir(), "notes")
    os.makedirs(directory, exist_ok=True)
    return directory


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


def _read_header_comments(path: str) -> str:
    """Extract leading ``#`` comment / blank lines from a config file."""
    lines = []
    try:
        with open(path) as f:
            for line in f:
                stripped = line.strip()
                if stripped.startswith("#") or not stripped:
                    lines.append(line)
                else:
                    break
    except (FileNotFoundError, OSError):
        return ""
    return "".join(lines) if lines else ""


def _tilde_directory(directory: str) -> str:
    """Replace ``$HOME`` prefix with ``~`` in *directory*."""
    home = os.path.expanduser("~")
    if directory.startswith(home):
        return "~" + directory[len(home) :]
    return directory
