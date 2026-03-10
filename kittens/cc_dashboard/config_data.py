"""Shared data loading helpers for dashboard configuration."""

import json
import os
import sys


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
    try:
        with open(path) as f:
            data = json.load(f)
    except (json.JSONDecodeError, OSError) as e:
        print(f"Warning: failed to load palette from {path}: {e}", file=sys.stderr)
        return {}, (255, 255, 255), (170, 170, 170)
    palette = {}
    for name, entry in data.get("colors", {}).items():
        palette[name] = (
            _hex_to_rgb(entry["active_bg"]),
            _hex_to_rgb(entry["inactive_bg"]),
            tuple(entry["text_fg"]),
        )
    return palette, _hex_to_rgb(data["active_fg"]), _hex_to_rgb(data["inactive_fg"])


PALETTE, ACTIVE_FG, INACTIVE_FG = _load_palette()
PALETTE_NAMES = list(PALETTE.keys())
