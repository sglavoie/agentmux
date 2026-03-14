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


_FALLBACK_PALETTE: dict[str, PaletteEntry] = {
    "green": ((45, 125, 70), (20, 60, 30), (255, 255, 255)),
    "blue": ((30, 80, 160), (15, 40, 80), (255, 255, 255)),
    "red": ((160, 40, 40), (80, 20, 20), (255, 255, 255)),
    "orange": ((180, 100, 20), (90, 50, 10), (255, 255, 255)),
    "purple": ((100, 40, 160), (50, 20, 80), (255, 255, 255)),
    "teal": ((20, 130, 130), (10, 65, 65), (255, 255, 255)),
    "pink": ((160, 50, 120), (80, 25, 60), (255, 255, 255)),
    "yellow": ((160, 140, 20), (80, 70, 10), (255, 255, 255)),
    "gray": ((128, 128, 128), (64, 64, 64), (255, 255, 255)),
    "cyan": ((20, 120, 180), (10, 60, 90), (255, 255, 255)),
}


def _load_palette() -> tuple[dict[str, PaletteEntry], RGBTuple, RGBTuple]:
    """Load color palette from data/palette.json.

    Returns (palette_dict, active_fg_tuple, inactive_fg_tuple) where
    palette_dict maps color names to (active_bg_rgb, inactive_bg_rgb, text_fg_rgb).
    Falls back to hardcoded defaults for missing colors so the dashboard never crashes.
    """
    path = _resolve_data_path("palette.json")
    if not path:
        print(
            "Warning: palette.json not found, using fallback palette",
            file=sys.stderr,
        )
        return dict(_FALLBACK_PALETTE), (255, 255, 255), (170, 170, 170)
    try:
        with open(path) as f:
            data = json.load(f)
    except (json.JSONDecodeError, OSError) as e:
        print(f"Warning: failed to load palette from {path}: {e}", file=sys.stderr)
        return dict(_FALLBACK_PALETTE), (255, 255, 255), (170, 170, 170)
    palette: dict[str, PaletteEntry] = {}
    for name, entry in data.get("colors", {}).items():
        try:
            fg_raw = entry["text_fg"]
            if not isinstance(fg_raw, list) or len(fg_raw) != 3:
                raise ValueError(f"text_fg must be a 3-element list, got {fg_raw!r}")
            fg_tuple: RGBTuple = (int(fg_raw[0]), int(fg_raw[1]), int(fg_raw[2]))
            palette[name] = (
                _hex_to_rgb(entry["active_bg"]),
                _hex_to_rgb(entry["inactive_bg"]),
                fg_tuple,
            )
        except (KeyError, ValueError, TypeError) as e:
            print(
                f"Warning: skipping palette color '{name}': {e}",
                file=sys.stderr,
            )
    # Fill in any missing colors from the fallback palette
    for name, fallback_entry in _FALLBACK_PALETTE.items():
        if name not in palette:
            palette[name] = fallback_entry
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


_ICONS_FALLBACK: dict[str, Any] = {
    "star": "★",
    "flag": "❗",
    "states": {"bolt": "⚡", "stop": "🛑", "key": "🔑", "error": "⚠️"},
    "state_order": ["bolt", "stop", "key", "error"],
}

_cached_icons: dict[str, Any] | None = None


def _load_icons_json() -> dict[str, Any] | None:
    """Load and return the parsed icons.json data, or None on failure.

    Results are cached after the first successful load.
    """
    global _cached_icons
    if _cached_icons is not None:
        return _cached_icons
    path = _resolve_data_path("icons.json")
    if not path:
        print("Warning: icons.json not found in any data directory", file=sys.stderr)
        return None
    try:
        with open(path) as f:
            _cached_icons = json.load(f)
            return _cached_icons
    except (json.JSONDecodeError, OSError) as e:
        print(f"Warning: failed to load icons from {path}: {e}", file=sys.stderr)
        return None


def load_icons() -> dict[str, Any]:
    """Load icon definitions from icons.json with fallback defaults.

    Always returns a complete icon dict, using hardcoded fallbacks if
    icons.json is missing or malformed.  Validates that required keys
    (star, states, state_order) are present and well-typed, falling
    back per-key so a partially valid file still works.
    """
    data = _load_icons_json()
    if data is None:
        return dict(_ICONS_FALLBACK)
    result = dict(data)
    if not isinstance(result.get("star"), str):
        result["star"] = _ICONS_FALLBACK["star"]
    if not isinstance(result.get("states"), dict):
        result["states"] = dict(_ICONS_FALLBACK["states"])
    if not isinstance(result.get("state_order"), list):
        result["state_order"] = list(_ICONS_FALLBACK["state_order"])
    return result


def load_override_emojis() -> tuple[dict[str, str], set[str]]:
    """Load color-to-emoji mapping from palette.json.

    Returns (color_to_emoji_dict, emoji_set).  Uses the already-loaded
    PALETTE data to avoid re-reading palette.json.
    """
    path = _resolve_data_path("palette.json")
    mapping: dict[str, str] = {}
    if path:
        try:
            with open(path) as f:
                data = json.load(f)
            for color, info in data.get("colors", {}).items():
                if "emoji" in info:
                    mapping[color] = info["emoji"]
        except (json.JSONDecodeError, OSError):
            pass
    if not mapping:
        mapping = {
            "green": "🟩",
            "blue": "🟦",
            "red": "🟥",
            "orange": "🟧",
            "purple": "🟪",
            "teal": "🟢",
            "pink": "🟣",
            "yellow": "🟨",
            "gray": "⬜",
            "cyan": "🔵",
        }
    return mapping, set(mapping.values())


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


def dim_hex(hex_str: str) -> str:
    """Halve the RGB components of a hex color string.

    Python equivalent of ``_dim_hex`` in ``lib/cc-common/dim.sh``.
    Example: ``dim_hex('#2d7d46')`` returns ``'#163e23'``.
    """
    h = hex_str.lstrip("#")
    if len(h) != 6:
        return hex_str
    r, g, b = int(h[0:2], 16) // 2, int(h[2:4], 16) // 2, int(h[4:6], 16) // 2
    return f"#{r:02x}{g:02x}{b:02x}"


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
