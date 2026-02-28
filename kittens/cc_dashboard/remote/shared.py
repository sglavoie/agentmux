"""Shared constants and helpers for kitty remote integration."""

import json
import re

from cc_dashboard.config import _resolve_data_path


def _load_icons():
    """Load icon definitions from data/icons.json."""
    path = _resolve_data_path("icons.json")
    if not path:
        return {
            "star": "★",
            "states": {"bolt": "⚡", "stop": "🛑", "key": "🔑", "error": "⚠️"},
            "state_order": ["bolt", "stop", "key", "error"],
        }
    with open(path) as f:
        return json.load(f)


_ICONS = _load_icons()
_ICON_STAR = _ICONS["star"]
_ICON_FLAG = _ICONS.get("flag", "❗")
_ICON_STATES = _ICONS["states"]
_STATE_NAMES = {
    "bolt": "active",
    "stop": "stopped",
    "key": "permission",
    "error": "error",
}

_state_icons_alt = "|".join(re.escape(_ICON_STATES[k]) for k in _ICONS["state_order"])
_MANAGED_TITLE_RE = re.compile(
    r"^("
    + re.escape(_ICON_STAR)
    + r")?("
    + re.escape(_ICON_FLAG)
    + r")?("
    + _state_icons_alt
    + r")?[A-Z]:"
)


def _strip_icons(title):
    """Remove leading star, flag, and state icon prefixes from a tab title."""
    if title.startswith(_ICON_STAR):
        title = title[len(_ICON_STAR) :]
    if title.startswith(_ICON_FLAG):
        title = title[len(_ICON_FLAG) :]
    for key in _ICONS["state_order"]:
        icon = _ICON_STATES[key]
        if title.startswith(icon):
            title = title[len(icon) :]
            break
    return title


def _strip_account_prefix(title):
    """Remove the leading 'X:' account initial prefix from a stripped title.

    E.g. 'P:ccm' -> 'ccm', 'T:Excel' -> 'Excel'. Titles without the prefix
    are returned as-is.
    """
    if len(title) >= 2 and title[0].isupper() and title[1] == ":":
        return title[2:]
    return title


OVERRIDE_MARKER = "\u203e"  # U+203E OVERLINE


def _strip_override_marker(title):
    """Remove trailing override marker(s) from a title."""
    return title.rstrip(OVERRIDE_MARKER)


def _append_override_marker(title):
    """Append a single override marker to a title (idempotent)."""
    return _strip_override_marker(title) + OVERRIDE_MARKER


def _detect_state(title):
    """Detect the state icon in a tab title, returning a status string.

    Returns one of: "active", "stopped", "permission", "error", or "".
    The star and flag prefixes are skipped before checking.
    """
    if title.startswith(_ICON_STAR):
        title = title[len(_ICON_STAR) :]
    if title.startswith(_ICON_FLAG):
        title = title[len(_ICON_FLAG) :]
    for key in _ICONS["state_order"]:
        if title.startswith(_ICON_STATES[key]):
            return _STATE_NAMES[key]
    return ""


def format_duration(seconds):
    """Format a duration in seconds into a compact human-readable string."""
    seconds = max(0, int(seconds))
    if seconds < 60:
        return f"{seconds}s"
    minutes = seconds // 60
    if minutes < 60:
        return f"{minutes}m"
    hours = minutes // 60
    remaining_min = minutes % 60
    if remaining_min:
        return f"{hours}h {remaining_min}m"
    return f"{hours}h"
