"""Shared TOML reader for cc-manager config files.

Uses ``tomllib`` (stdlib since Python 3.11).  Shared by the Python
dashboard code and the ``toml-helper.py`` CLI bridge for Bash.
"""

import json
import os
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
    for i, entry in enumerate(data.get("accounts", []), start=1):
        name = entry.get("name", "")
        if not name:
            print(
                f"Warning: skipping account entry {i} with empty name",
                file=sys.stderr,
            )
            continue
        color = entry.get("color", "gray")
        if color not in _VALID_PALETTE_COLORS:
            print(
                f"Warning: account '{name}' has unknown color "
                f"'{color}', falling back to 'gray'.",
                file=sys.stderr,
            )
            color = "gray"
        result.append(
            {
                "name": name,
                "config_dir": os.path.expanduser(entry.get("config_dir", "")),
                "color": color,
            }
        )
    return result


def _load_palette_colors():
    """Load valid palette color names from palette.json."""
    hardcoded = frozenset({
        "green", "blue", "red", "orange", "purple",
        "teal", "pink", "yellow", "gray", "cyan",
    })
    # Try repo-relative path first (lib/ -> repo root -> data/)
    repo_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    candidates = [
        os.path.join(repo_root, "data", "palette.json"),
        os.path.expanduser("~/.local/lib/cc-manager/palette.json"),
    ]
    for path in candidates:
        try:
            with open(path) as f:
                data = json.load(f)
            return frozenset(data["colors"].keys())
        except (FileNotFoundError, OSError, json.JSONDecodeError, KeyError, TypeError):
            continue
    return hardcoded


_VALID_PALETTE_COLORS = _load_palette_colors()


def read_sessions(path, known_accounts=None):
    """Parse ``sessions.toml`` and return a list of session dicts.

    Each dict has keys: ``title``, ``dir``, ``account``, ``flags``,
    ``pw`` (project_window), ``env``, ``group``.

    If *known_accounts* is provided (a set or list of account name strings),
    sessions referencing unknown accounts will emit a warning.
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
        if not session["title"] or not session["dir"]:
            missing = []
            if not session["title"]:
                missing.append("title")
            if not session["dir"]:
                missing.append("directory")
            print(
                f"Warning: skipping session entry {i} missing required field(s): {', '.join(missing)}",
                file=sys.stderr,
            )
            continue
        account = session["account"]
        if known_accounts is not None and account and account not in known_accounts:
            print(
                f"Warning: session '{session['title']}' references "
                f"unknown account '{account}'.",
                file=sys.stderr,
            )
        pw = session["pw"]
        valid_pw = {"", "-", "stack", "vsplit", "hsplit", "split"}
        if pw and pw not in valid_pw:
            print(
                f"Warning: session '{session['title']}' has unknown project_window '{pw}', ignoring.",
                file=sys.stderr,
            )
            session["pw"] = ""
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
