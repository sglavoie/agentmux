"""Shared TOML reader for cc-manager config files.

Uses ``tomllib`` (stdlib since Python 3.11).  Shared by the Python
dashboard code and the ``toml-helper.py`` CLI bridge for Bash.
"""

import sys
import tomllib


def read_header_comments(path):
    """Extract the leading comment block from a TOML file.

    Returns all consecutive ``#``-prefixed and blank lines at the top of
    *path* as a single string (newline-terminated).  Returns ``""`` if
    the file has no leading comments.
    """
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
    if not lines:
        return ""
    return "".join(lines)


def read_accounts(path):
    """Parse ``accounts.toml`` and return a list of account dicts.

    Each dict has keys: ``name``, ``config_dir``, ``color``.
    """
    try:
        with open(path, "rb") as f:
            data = tomllib.load(f)
    except FileNotFoundError:
        return []
    except tomllib.TOMLDecodeError as e:
        print(f"Error: failed to parse {path}: {e}", file=sys.stderr)
        return []
    result = []
    for entry in data.get("accounts", []):
        result.append(
            {
                "name": entry.get("name", ""),
                "config_dir": entry.get("config_dir", ""),
                "color": entry.get("color", "gray"),
            }
        )
    return result


def read_sessions(path):
    """Parse ``sessions.toml`` and return a list of session dicts.

    Each dict has keys: ``title``, ``dir``, ``account``, ``flags``,
    ``pw`` (project_window), ``env``, ``group``.
    """
    try:
        with open(path, "rb") as f:
            data = tomllib.load(f)
    except FileNotFoundError:
        return []
    except tomllib.TOMLDecodeError as e:
        print(f"Error: failed to parse {path}: {e}", file=sys.stderr)
        return []
    result = []
    for i, entry in enumerate(data.get("sessions", []), start=1):
        session = {
            "title": entry.get("title", ""),
            "dir": entry.get("directory", ""),
            "account": entry.get("account", ""),
            "flags": entry.get("flags", ""),
            "pw": entry.get("project_window", ""),
            "env": entry.get("env", ""),
            "group": entry.get("group", ""),
        }
        if not session["title"]:
            print(
                f"Warning: session entry {i} missing required field 'title'",
                file=sys.stderr,
            )
        if not session["dir"]:
            print(
                f"Warning: session entry {i} missing required field 'directory'",
                file=sys.stderr,
            )
        result.append(session)
    return result


def read_defaults(path):
    """Parse ``defaults.toml`` and return ``(defaults_dict, notifications_dict)``.

    *defaults_dict* contains string/int values for keys like
    ``claude_flags``, ``project_window``, ``sort_sessions``,
    ``dashboard_refresh``.

    *notifications_dict* contains notification settings with keys like
    ``error``, ``permission``, ``stop``, ``idle``, ``idle_alert_minutes``,
    ``min_secs``, ``ignore_patterns``.
    """
    try:
        with open(path, "rb") as f:
            data = tomllib.load(f)
    except FileNotFoundError:
        return ({}, {})
    except tomllib.TOMLDecodeError as e:
        print(f"Error: failed to parse {path}: {e}", file=sys.stderr)
        return ({}, {})
    return data.get("defaults", {}), data.get("notifications", {})
