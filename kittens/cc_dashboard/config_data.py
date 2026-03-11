"""Shared data loading helpers for dashboard configuration."""

from __future__ import annotations

import json
import os
import sys
from typing import Any

# RGB tuple: (red, green, blue) with values 0-255
RGBTuple = tuple[int, int, int]

# Palette entry: (active_bg_rgb, inactive_bg_rgb, text_fg_rgb)
PaletteEntry = tuple[RGBTuple, RGBTuple, RGBTuple]


def _resolve_data_path(filename: str) -> str | None:
    """Resolve the path to a shared JSON data file.

    Mirrors the fallback order in ``lib/cc-common/data.sh`` so both the
    shell and Python code find data files in the same set of locations:

    1. ``../../data/``  — repo layout (kittens/cc_dashboard/ -> data/)
    2. ``../data/``     — alternate repo layout
    3. ``data/``        — flat layout (file next to this package)
    4. ``~/.local/lib/cc-manager/`` — installed layout (symlinked by just)
    """
    this_dir = os.path.dirname(os.path.realpath(__file__))
    candidates = [
        os.path.join(this_dir, "..", "..", "data", filename),
        os.path.join(this_dir, "..", "data", filename),
        os.path.join(this_dir, "data", filename),
        os.path.join(os.path.expanduser("~"), ".local", "lib", "cc-manager", filename),
    ]
    for path in candidates:
        if os.path.isfile(path):
            return os.path.realpath(path)
    return None


def _hex_to_rgb(hex_str: str) -> RGBTuple:
    """Convert a hex color string like '#2d7d46' to an (R, G, B) tuple."""
    h = hex_str.lstrip("#")
    if len(h) != 6:
        raise ValueError(f"invalid hex color: '{hex_str}' (expected 6 hex digits)")
    return (int(h[0:2], 16), int(h[2:4], 16), int(h[4:6], 16))


def _load_palette() -> tuple[dict[str, PaletteEntry], RGBTuple, RGBTuple]:
    """Load color palette from data/palette.json.

    Returns (palette_dict, active_fg_tuple, inactive_fg_tuple) where
    palette_dict maps color names to (active_bg_rgb, inactive_bg_rgb, text_fg_rgb).
    """
    path = _resolve_data_path("palette.json")
    if not path:
        print("Warning: palette.json not found in any data directory", file=sys.stderr)
        return {}, (255, 255, 255), (170, 170, 170)
    try:
        with open(path) as f:
            data = json.load(f)
    except (json.JSONDecodeError, OSError) as e:
        print(f"Warning: failed to load palette from {path}: {e}", file=sys.stderr)
        return {}, (255, 255, 255), (170, 170, 170)
    palette: dict[str, PaletteEntry] = {}
    for name, entry in data.get("colors", {}).items():
        try:
            palette[name] = (
                _hex_to_rgb(entry["active_bg"]),
                _hex_to_rgb(entry["inactive_bg"]),
                tuple(entry["text_fg"]),
            )
        except (KeyError, ValueError, TypeError) as e:
            print(
                f"Warning: skipping palette color '{name}': {e}",
                file=sys.stderr,
            )
    try:
        active_fg = _hex_to_rgb(data["active_fg"])
        inactive_fg = _hex_to_rgb(data["inactive_fg"])
    except (KeyError, ValueError, TypeError) as e:
        print(f"Warning: palette.json missing foreground colors: {e}", file=sys.stderr)
        active_fg = (255, 255, 255)
        inactive_fg = (170, 170, 170)
    return palette, active_fg, inactive_fg


PALETTE: dict[str, PaletteEntry]
ACTIVE_FG: RGBTuple
INACTIVE_FG: RGBTuple
PALETTE, ACTIVE_FG, INACTIVE_FG = _load_palette()
PALETTE_NAMES: list[str] = list(PALETTE.keys())


def load_palette_colors() -> frozenset[str] | None:
    """Return the set of valid palette color names.

    Uses the already-loaded PALETTE dict — avoids re-reading palette.json.
    """
    return frozenset(PALETTE.keys()) if PALETTE else None


def _load_icons_json() -> dict[str, Any] | None:
    """Load and return the parsed icons.json data, or None on failure."""
    path = _resolve_data_path("icons.json")
    if not path:
        print("Warning: icons.json not found in any data directory", file=sys.stderr)
        return None
    try:
        with open(path) as f:
            result: dict[str, Any] = json.load(f)
            return result
    except (json.JSONDecodeError, OSError) as e:
        print(f"Warning: failed to load icons from {path}: {e}", file=sys.stderr)
        return None


def load_hook_events() -> list[str]:
    """Load hook event names from icons.json.

    Returns a list of event name strings, or a hardcoded fallback list.
    """
    data = _load_icons_json()
    if data:
        events = data.get("hook_events")
        if isinstance(events, list):
            return [str(e) for e in events]
    return [
        "SessionStart",
        "UserPromptSubmit",
        "PreToolUse",
        "PermissionRequest",
        "PostToolUse",
        "PostToolUseFailure",
        "Notification",
        "SubagentStart",
        "SubagentStop",
        "Stop",
        "TeammateIdle",
        "TaskCompleted",
        "ConfigChange",
        "WorktreeCreate",
        "WorktreeRemove",
        "PreCompact",
        "SessionEnd",
    ]


def load_sort_modes() -> tuple[str, ...]:
    """Load sort modes from icons.json, excluding 'alpha' (legacy alias).

    Returns a tuple of sort mode strings for use in the dashboard UI.
    """
    data = _load_icons_json()
    if data:
        try:
            modes = data["sort_modes"]
            return tuple(m for m in modes if m != "alpha")
        except (KeyError, TypeError):
            pass
    return ("default", "name", "account", "group", "status", "idle", "activity")
