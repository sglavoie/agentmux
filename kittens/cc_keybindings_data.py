"""Static binding data and search helpers for the keybindings overlay."""

from __future__ import annotations

from cc_dashboard.views_shared import _FOOTER_HINTS

KITTY_GLOBAL_SECTION = (
    "Kitty Global",
    [
        ("F1", "Keybinding reference (this overlay)"),
        ("F2", "Toggle star / pin on current tab"),
        ("F3", "Toggle error / warning markers"),
        ("F4", "Clear markers and state icons"),
        ("F5", "Toggle flag on current tab"),
        ("F6", "Set tab color (color picker overlay)"),
        ("F7", "Set tab status (status overlay)"),
        ("F8", "Session info overlay"),
        ("F9", "Edit session notes"),
        ("F10", "Toggle dim on current tab"),
        ("F11", "Toggle focus mode (dim all other tabs)"),
        ("F12", "Broadcast input to all tab windows"),
        ("Cmd+1..0", "Switch to tab 1-10"),
        ("Cmd+L", "Toggle between last two tabs"),
        ("Ctrl+Shift+D", "Open session dashboard"),
        ("Ctrl+Shift+P", "Quick-switch overlay"),
    ],
)

MODE_DISPLAY_NAMES: dict[str, str] = {
    "list": "Session List",
    "filter": "Account Filter",
    "group_filter": "Group Filter",
    "actions": "Session Actions",
    "group_actions": "Group Actions",
    "sort_pick": "Sort Picker",
    "color_picker": "Color Picker",
    "peek": "Session Peek",
    "actlog": "Activity Log",
    "actlog_detail": "Activity Detail",
    "actlog_browse": "Log Browser",
    "actlog_gsearch": "Global Log Search",
    "config_pick": "Config Browser",
    "config_action": "Config Action",
    "new": "New / Edit Session",
    "new_dir_browse": "Directory Browser",
    "batch_group": "Batch Group",
    "pick": "Launch from Config",
    "pick_group_filter": "Launch Group Filter",
    "rename": "Rename Session",
    "clone": "Clone Session",
    "worktree": "Create Worktree",
    "override": "Config Dir Override",
    "move": "Move to Instance",
    "restart_options": "Restart Options",
    "delete": "Delete Session",
    "prefs": "Preferences",
    "prefs_array": "Preferences Array",
    "hooks": "Hooks Manager",
    "hooks_event": "Hook Events",
    "hooks_add": "Add / Edit Hook",
    "hooks_matcher_pick": "Matcher Picker",
    "hooks_info": "Hook Info",
    "hooks_example": "Hook Example",
}

MODE_ORDER: list[str] = [
    "list",
    "filter",
    "group_filter",
    "actions",
    "group_actions",
    "sort_pick",
    "color_picker",
    "peek",
    "actlog",
    "actlog_detail",
    "actlog_browse",
    "actlog_gsearch",
    "config_pick",
    "config_action",
    "new",
    "new_dir_browse",
    "batch_group",
    "pick",
    "pick_group_filter",
    "rename",
    "clone",
    "worktree",
    "override",
    "move",
    "restart_options",
    "delete",
    "prefs",
    "prefs_array",
    "hooks",
    "hooks_event",
    "hooks_add",
    "hooks_matcher_pick",
    "hooks_info",
    "hooks_example",
]

SKIP_F1_MODES: set[str] = {
    "confirm",
    "confirm_restart",
    "confirm_batch_close",
    "confirm_batch_restart",
    "confirm_batch_clone",
    "confirm_save",
    "confirm_mkdir",
    "delete_confirm",
    "hooks_delete_confirm",
    "hooks_sync_confirm",
    "peek_search",
    "actlog_search",
    "actlog_browse_search",
    "actlog_gsearch_search",
}

DESCRIPTION_OVERRIDES: dict[tuple[str, str], str] = {
    ("list", "Enter"): "Focus & switch to selected tab",
    ("list", "Ctrl+F"): "Search across all activity logs",
}


def parse_hint(hint: str) -> tuple[str | None, str]:
    """Parse a footer hint into a key/description pair."""
    colon = hint.find(":")
    space = hint.find(" ")
    if colon != -1 and (space == -1 or colon < space):
        return (None, hint)
    if space == -1:
        return (hint, "")
    key = hint[:space]
    desc = hint[space + 1 :]
    if key.startswith("^") and "/" not in key and len(key) == 2:
        key = f"Ctrl+{key[1]}"
    return (key, desc)


def build_sections() -> list[tuple[str, list[tuple[str, str]]]]:
    """Build F1 overlay sections from dashboard footer hints."""
    sections: list[tuple[str, list[tuple[str, str]]]] = [KITTY_GLOBAL_SECTION]
    for mode in MODE_ORDER:
        hints = _FOOTER_HINTS.get(mode, [])
        bindings: list[tuple[str, str]] = []
        for hint in hints:
            raw_key, desc = parse_hint(hint)
            override = DESCRIPTION_OVERRIDES.get((mode, raw_key or ""))
            if override:
                desc = override
            bindings.append((raw_key or "", desc))
        display_name = MODE_DISPLAY_NAMES.get(mode, mode.replace("_", " ").title())
        sections.append((display_name, bindings))
    return sections


def build_lines(
    sections: list[tuple[str, list[tuple[str, str]]]],
) -> list[tuple[str, str, str]]:
    """Flatten sections into line tuples for rendering."""
    lines: list[tuple[str, str, str]] = []
    for section_name, bindings in sections:
        lines.append(("header", section_name, ""))
        for key, description in bindings:
            lines.append(("binding", key, description))
        lines.append(("blank", "", ""))
    if lines and lines[-1][0] == "blank":
        lines.pop()
    return lines


def compute_matches(lines: list[tuple[str, str, str]], query: str) -> list[int]:
    """Return sorted line indices whose text contains the query."""
    if not query:
        return []
    lowered_query = query.lower()
    return [
        index
        for index, (_kind, text1, text2) in enumerate(lines)
        if lowered_query in text1.lower() or lowered_query in text2.lower()
    ]


def scroll_to_line(
    target: int,
    scroll: int,
    content_height: int,
    max_scroll: int,
) -> int:
    """Return a scroll offset that keeps the target line visible."""
    if target < scroll:
        return max(0, target - content_height // 3)
    if target >= scroll + content_height:
        return min(max_scroll, target - content_height // 3)
    return scroll


UNREGISTERED_FOOTER_MODES = set(_FOOTER_HINTS) - set(MODE_ORDER) - SKIP_F1_MODES
assert not UNREGISTERED_FOOTER_MODES, (
    "Footer hint modes not registered in MODE_ORDER or SKIP_F1_MODES: "
    f"{sorted(UNREGISTERED_FOOTER_MODES)}"
)

SECTIONS = build_sections()
LINES = build_lines(SECTIONS)
