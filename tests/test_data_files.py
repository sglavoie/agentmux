#!/usr/bin/env python3
"""Validation tests for shared data files (palette.json, icons.json).

Ensures the JSON data files maintain their required structure and
internal consistency.

Run: python3 tests/test_data_files.py
"""

import json
import os
import sys

DATA_DIR = os.path.join(os.path.dirname(__file__), "..", "data")

PASS = 0
FAIL = 0


def check(label, condition):
    global PASS, FAIL
    if condition:
        PASS += 1
        print(f"  PASS  {label}")
    else:
        FAIL += 1
        print(f"  FAIL  {label}")


def _load_json(filename):
    path = os.path.join(DATA_DIR, filename)
    with open(path) as f:
        return json.load(f)


# ── palette.json ─────────────────────────────────────────────────────


def test_palette_structure():
    """palette.json has the expected top-level keys."""
    print("\n--- palette structure ---")
    data = _load_json("palette.json")
    check("has 'colors' key", "colors" in data)
    check("has 'active_fg' key", "active_fg" in data)
    check("has 'inactive_fg' key", "inactive_fg" in data)


def test_palette_has_10_colors():
    """palette.json defines exactly 10 named colors."""
    print("\n--- palette color count ---")
    data = _load_json("palette.json")
    colors = data["colors"]
    expected = {"green", "blue", "red", "orange", "purple",
                "teal", "pink", "yellow", "gray", "cyan"}
    check("exactly 10 colors", len(colors) == 10)
    check("all expected names present", set(colors.keys()) == expected)


def test_palette_color_fields():
    """Each color has active_bg, inactive_bg, and text_fg."""
    print("\n--- palette color fields ---")
    data = _load_json("palette.json")
    for name, color in data["colors"].items():
        check(f"{name}: has active_bg", "active_bg" in color)
        check(f"{name}: has inactive_bg", "inactive_bg" in color)
        check(f"{name}: has text_fg", "text_fg" in color)


def test_palette_hex_format():
    """Background colors are valid hex strings (#RRGGBB)."""
    print("\n--- palette hex format ---")
    data = _load_json("palette.json")
    import re
    hex_re = re.compile(r"^#[0-9a-fA-F]{6}$")
    for name, color in data["colors"].items():
        check(f"{name}: active_bg is hex",
              bool(hex_re.match(color["active_bg"])))
        check(f"{name}: inactive_bg is hex",
              bool(hex_re.match(color["inactive_bg"])))


def test_palette_text_fg_rgb():
    """text_fg is a 3-element array of ints 0-255."""
    print("\n--- palette text_fg RGB ---")
    data = _load_json("palette.json")
    for name, color in data["colors"].items():
        fg = color["text_fg"]
        check(f"{name}: text_fg is list of 3", isinstance(fg, list) and len(fg) == 3)
        check(f"{name}: text_fg values 0-255",
              all(isinstance(v, int) and 0 <= v <= 255 for v in fg))


def test_palette_fg_strings():
    """Top-level fg values are hex strings."""
    print("\n--- palette fg strings ---")
    data = _load_json("palette.json")
    import re
    hex_re = re.compile(r"^#[0-9a-fA-F]{6}$")
    check("active_fg is hex", bool(hex_re.match(data["active_fg"])))
    check("inactive_fg is hex", bool(hex_re.match(data["inactive_fg"])))


# ── icons.json ───────────────────────────────────────────────────────


def test_icons_structure():
    """icons.json has the expected top-level keys."""
    print("\n--- icons structure ---")
    data = _load_json("icons.json")
    check("has 'star' key", "star" in data)
    check("has 'states' key", "states" in data)
    check("has 'state_order' key", "state_order" in data)
    check("has 'hook_events' key", "hook_events" in data)
    check("has 'sort_modes' key", "sort_modes" in data)


def test_icons_state_order_consistency():
    """state_order entries match the keys in states."""
    print("\n--- state_order consistency ---")
    data = _load_json("icons.json")
    state_keys = set(data["states"].keys())
    order_set = set(data["state_order"])
    check("state_order matches states keys", state_keys == order_set)
    check("no duplicates in state_order",
          len(data["state_order"]) == len(order_set))


def test_icons_states_are_strings():
    """All state icon values are non-empty strings."""
    print("\n--- state icon values ---")
    data = _load_json("icons.json")
    for name, icon in data["states"].items():
        check(f"state '{name}' is string", isinstance(icon, str) and len(icon) > 0)
    check("star is string", isinstance(data["star"], str) and len(data["star"]) > 0)


def test_icons_hook_events_are_strings():
    """hook_events is a non-empty array of strings."""
    print("\n--- hook_events ---")
    data = _load_json("icons.json")
    events = data["hook_events"]
    check("hook_events is non-empty list",
          isinstance(events, list) and len(events) > 0)
    check("all events are strings",
          all(isinstance(e, str) for e in events))
    check("no duplicate events", len(events) == len(set(events)))


def test_icons_sort_modes_are_strings():
    """sort_modes is a non-empty array of strings."""
    print("\n--- sort_modes ---")
    data = _load_json("icons.json")
    modes = data["sort_modes"]
    check("sort_modes is non-empty list",
          isinstance(modes, list) and len(modes) > 0)
    check("all modes are strings",
          all(isinstance(m, str) for m in modes))


# ── cross-file consistency ───────────────────────────────────────────


def test_hook_events_match_example():
    """hook_events in icons.json match the keys in hooks.example.json."""
    print("\n--- hook_events vs hooks.example ---")
    icons = _load_json("icons.json")
    config_dir = os.path.join(os.path.dirname(__file__), "..", "config")
    example_path = os.path.join(config_dir, "hooks.example.json")
    with open(example_path) as f:
        example = json.load(f)

    icon_events = set(icons["hook_events"])
    example_events = set(example["hooks"].keys())
    check("icons hook_events == example hooks keys",
          icon_events == example_events)

    if icon_events != example_events:
        missing_from_example = icon_events - example_events
        extra_in_example = example_events - icon_events
        if missing_from_example:
            print(f"    Missing from example: {missing_from_example}")
        if extra_in_example:
            print(f"    Extra in example: {extra_in_example}")


# ── run all ──────────────────────────────────────────────────────────


if __name__ == "__main__":
    print("data file validation tests")

    test_palette_structure()
    test_palette_has_10_colors()
    test_palette_color_fields()
    test_palette_hex_format()
    test_palette_text_fg_rgb()
    test_palette_fg_strings()
    test_icons_structure()
    test_icons_state_order_consistency()
    test_icons_states_are_strings()
    test_icons_hook_events_are_strings()
    test_icons_sort_modes_are_strings()
    test_hook_events_match_example()

    print(f"\n{PASS} passed, {FAIL} failed")
    if FAIL:
        sys.exit(1)
