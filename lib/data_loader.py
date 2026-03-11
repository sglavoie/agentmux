"""Canonical loaders for shared JSON data files (palette.json, icons.json).

This module is the single source of truth for loading data files in the
``lib/`` layer.  Both ``toml_reader.py`` and ``config_validator.py`` import
from here instead of duplicating the loading logic.

Path resolution order:
1. ``<repo_root>/data/``  — repo layout (lib/ is one level below root)
2. ``~/.local/lib/cc-manager/`` — installed layout (symlinked by just)
"""

from __future__ import annotations

import json
import os
from typing import Any


def _data_candidates(filename: str, data_dir: str | None = None) -> list[str]:
    """Return candidate paths for *filename*, optionally anchored at *data_dir*."""
    paths: list[str] = []
    if data_dir:
        paths.append(os.path.join(data_dir, filename))
    # Repo layout: lib/ -> repo root -> data/
    repo_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    paths.append(os.path.join(repo_root, "data", filename))
    # Installed layout
    paths.append(os.path.expanduser(f"~/.local/lib/cc-manager/{filename}"))
    return paths


def _load_json(filename: str, data_dir: str | None = None) -> dict[str, Any] | None:
    """Load and return parsed JSON from the first readable candidate path."""
    for path in _data_candidates(filename, data_dir):
        try:
            with open(path) as f:
                return json.load(f)
        except (FileNotFoundError, OSError, json.JSONDecodeError):
            continue
    return None


def load_palette_colors(data_dir: str | None = None) -> frozenset[str] | None:
    """Load valid palette color names from palette.json.

    Returns a frozenset of color name strings, or None if palette.json
    cannot be loaded (caller should fall back or skip color validation).
    """
    data = _load_json("palette.json", data_dir)
    if data:
        try:
            return frozenset(data["colors"].keys())
        except (KeyError, TypeError):
            pass
    return None


def load_icons_json(data_dir: str | None = None) -> dict[str, Any] | None:
    """Load and return the parsed icons.json data, or None on failure."""
    return _load_json("icons.json", data_dir)


def load_hook_events(data_dir: str | None = None) -> set[str]:
    """Load valid hook event names from icons.json.

    Returns a set of event name strings, or an empty set on failure.
    """
    data = load_icons_json(data_dir)
    if data:
        try:
            return set(data["hook_events"])
        except (KeyError, TypeError):
            pass
    return set()


def load_sort_modes(data_dir: str | None = None) -> set[str]:
    """Load valid sort mode names from icons.json.

    Returns a set of sort mode strings, falling back to a hardcoded set.
    """
    data = load_icons_json(data_dir)
    if data:
        try:
            return set(data["sort_modes"])
        except (KeyError, TypeError):
            pass
    return {"default", "alpha", "name", "account",
            "group", "status", "idle", "activity"}
