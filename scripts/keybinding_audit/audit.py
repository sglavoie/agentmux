"""CLI and comparison logic for the keybinding audit."""

from __future__ import annotations

import re

from .code_parser import extract_all_code_keys
from .constants import (
    EMBEDDED_SUBMODES,
    EQUIV_LOOKUP,
    HINT_KEY_NORMALIZE,
    MODE_EXTRA_IGNORE,
    MODE_NAME_MAP,
    MODES_WITH_SEARCH_SUBMODE,
    SEARCH_SUBMODE_IMPLICIT_KEYS,
    TEXT_INPUT_KEYS,
    TEXT_INPUT_MODES,
)
from .footer_hints import extract_footer_hints


def _normalize_hint_key(key: str) -> str:
    """Normalize a hint key to match code key format."""
    key_lower = key.lower()
    if key_lower in HINT_KEY_NORMALIZE:
        return HINT_KEY_NORMALIZE[key_lower]
    if key_lower.startswith("ctrl+"):
        return key_lower
    if key_lower.startswith("^") and len(key_lower) == 2:
        return f"ctrl+{key_lower[1]}"
    return key_lower


def _expand_hint_keys(hints: list[str]) -> set[str]:
    """Expand footer hint keys into normalized code-comparable keys."""
    raw_keys: set[str] = set()
    for hint in hints:
        colon = hint.find(":")
        space = hint.find(" ")
        if colon != -1 and (space == -1 or colon < space):
            continue

        token = hint if space == -1 else hint[:space]
        if "Any" in token:
            raw_keys.add("_any_")
            continue

        range_match = re.match(r"(\d)-[N\d]", token)
        if range_match:
            for digit in range(1, 10):
                raw_keys.add(str(digit))
            continue

        for part in token.split("/"):
            raw_keys.add(_normalize_hint_key(part))

    expanded = set(raw_keys)
    for key in raw_keys:
        if key in EQUIV_LOOKUP:
            expanded.update(EQUIV_LOOKUP[key])

    return expanded


def _format_key(key: str) -> str:
    """Format a normalized key for display."""
    if key.startswith("ctrl+"):
        return f"Ctrl+{key[5:]}"
    return key


def main() -> int:
    """Run the audit and print a human-readable report."""
    footer_hints = extract_footer_hints()
    code_keys = extract_all_code_keys()

    print("=== Keybinding Documentation Audit ===")
    print()
    print("Checking code handlers against footer hints...")
    print()

    modes_with_gaps = 0
    modes_checked = 0

    for mode, hints in sorted(footer_hints.items()):
        if mode in EMBEDDED_SUBMODES:
            continue

        code_mode = MODE_NAME_MAP.get(mode, mode)
        if code_mode not in code_keys:
            continue

        modes_checked += 1
        hint_keys = _expand_hint_keys(hints)
        handled_keys = code_keys[code_mode]

        ignore = {"_printable_", "_any_"}
        if mode in TEXT_INPUT_MODES:
            ignore.update(TEXT_INPUT_KEYS)
        if mode in MODES_WITH_SEARCH_SUBMODE:
            ignore.update(TEXT_INPUT_KEYS)
            ignore.update(SEARCH_SUBMODE_IMPLICIT_KEYS)
        if mode in MODE_EXTRA_IGNORE:
            ignore.update(MODE_EXTRA_IGNORE[mode])

        undocumented = handled_keys - hint_keys - ignore
        if not undocumented:
            print(f"  {mode}: OK")
            continue

        modes_with_gaps += 1
        display_keys = sorted(_format_key(key) for key in undocumented)
        print(f"  {mode}: {len(undocumented)} key(s) in code but not in footer hints:")
        for display_key in display_keys:
            print(f"    {display_key}")

    no_handler = {
        mode
        for mode in footer_hints
        if MODE_NAME_MAP.get(mode, mode) not in code_keys
        and mode not in EMBEDDED_SUBMODES
    }
    if no_handler:
        print()
        print(f"  Note: {len(no_handler)} mode(s) with no code handler found:")
        for mode in sorted(no_handler):
            print(f"    {mode}")

    print()
    if modes_with_gaps:
        print(f"Summary: {modes_with_gaps} mode(s) with undocumented keys")
        return 1

    print(f"Summary: All {modes_checked} checked mode(s) clean")
    return 0
