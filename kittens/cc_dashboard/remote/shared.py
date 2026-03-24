"""Shared constants and helpers for kitty remote integration."""

import re

from cc_dashboard.config_data import load_icons, load_override_emojis

_ICONS = load_icons()
_ICON_DASHBOARD = _ICONS.get("dashboard", "🚗")
_ICON_STAR = _ICONS["star"]
_ICON_FLAG = _ICONS.get("flag", "❗")
_ICON_MUTE = _ICONS.get("mute", "🔇")
_ICON_STATES = _ICONS["states"]
_STATE_NAMES = {
    "bolt": "active",
    "stop": "stopped",
    "key": "permission",
    "error": "error",
}

_state_icons_alt = "|".join(re.escape(_ICON_STATES[k]) for k in _ICONS["state_order"])

_COLOR_TO_EMOJI, OVERRIDE_EMOJIS = load_override_emojis()


def _build_managed_title_re():
    """Build the compiled regex for matching managed tab titles.

    The actual title order produced by build_title() in cc-title-icons.sh is:
    star? flag? mute? state_icon? override_emoji? X:title
    """
    override_alt = "|".join(re.escape(e) for e in sorted(OVERRIDE_EMOJIS))
    return re.compile(
        r"^(?:"
        + re.escape(_ICON_DASHBOARD)
        + r")?("
        + re.escape(_ICON_STAR)
        + r")?("
        + re.escape(_ICON_FLAG)
        + r")?("
        + re.escape(_ICON_MUTE)
        + r")?("
        + _state_icons_alt
        + r")?(?:"
        + override_alt
        + r")?[A-Z]:"
    )


_MANAGED_TITLE_RE = _build_managed_title_re()


def _strip_dashboard_marker(title):
    """Remove the leading dashboard marker from a tab title."""
    if title.startswith(_ICON_DASHBOARD):
        return title[len(_ICON_DASHBOARD) :]
    return title


def _strip_icons(title):
    """Remove leading star, flag, and state icon prefixes from a tab title."""
    title = _strip_dashboard_marker(title)
    if title.startswith(_ICON_STAR):
        title = title[len(_ICON_STAR) :]
    if title.startswith(_ICON_FLAG):
        title = title[len(_ICON_FLAG) :]
    if title.startswith(_ICON_MUTE):
        title = title[len(_ICON_MUTE) :]
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


def _strip_override_marker(title, return_emoji=False):
    """Remove override emoji prefix (and legacy trailing overline) from a title.

    When *return_emoji* is True, returns a ``(stripped_title, emoji)`` tuple
    so the caller can re-apply the emoji later.
    """
    # Legacy: strip trailing U+203E OVERLINE
    title = title.rstrip("\u203e")
    # New: strip leading override emoji
    emoji_found = ""
    for emoji in OVERRIDE_EMOJIS:
        if title.startswith(emoji):
            emoji_found = emoji
            title = title[len(emoji) :]
            break
    if return_emoji:
        return title, emoji_found
    return title


def _append_override_marker(title, color_name=""):
    """Prepend a colored override emoji to a title (idempotent).

    If *color_name* is empty or unknown, the title is returned with any
    existing emoji stripped (i.e. no override indicator).
    """
    title = _strip_override_marker(title)
    emoji = _COLOR_TO_EMOJI.get(color_name, "")
    if emoji:
        return emoji + title
    return title


def _append_override_for_dir(title, config_dir_override):
    """Prepend override emoji matching the account that owns *config_dir_override*.

    Looks up which account has *config_dir_override* as its ``config_dir``
    and uses that account's color to select the emoji.
    """
    from cc_dashboard import config as _cfg

    color_name = ""
    for _acct_name, acct_info in _cfg._accounts.items():
        if acct_info.get("config_dir", "") == config_dir_override:
            color_name = acct_info.get("color", "")
            break
    return _append_override_marker(title, color_name)


def _has_override_emoji(title):
    """Return True if *title* starts with an override emoji."""
    return any(title.startswith(e) for e in OVERRIDE_EMOJIS)


def _detect_state(title):
    """Detect the state icon in a tab title, returning a status string.

    Returns one of: "active", "stopped", "permission", "error", or "".
    The star and flag prefixes are skipped before checking.
    """
    title = _strip_dashboard_marker(title)
    if title.startswith(_ICON_STAR):
        title = title[len(_ICON_STAR) :]
    if title.startswith(_ICON_FLAG):
        title = title[len(_ICON_FLAG) :]
    if title.startswith(_ICON_MUTE):
        title = title[len(_ICON_MUTE) :]
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
