#!/usr/bin/env python3
"""Tests for dashboard palette fallback behavior.

Verifies that the dashboard survives a missing or corrupted palette.json
by falling back to hardcoded defaults.

Run: python3 tests/test_palette_fallback.py
"""

import json
import os
import sys
import tempfile

# Ensure the kittens directory is importable.
KITTENS_DIR = os.path.join(os.path.dirname(__file__), "..", "kittens")
if KITTENS_DIR not in sys.path:
    sys.path.insert(0, KITTENS_DIR)

PASS = 0
FAIL = 0

EXPECTED_COLORS = {
    "green",
    "blue",
    "red",
    "orange",
    "purple",
    "teal",
    "pink",
    "yellow",
    "gray",
    "cyan",
}


def check(label, condition):
    global PASS, FAIL
    if condition:
        PASS += 1
        print(f"  PASS  {label}")
    else:
        FAIL += 1
        print(f"  FAIL  {label}")


def _reload_palette(monkeypatch_resolve):
    """Reload config_data with a patched _resolve_data_path and return the palette."""
    import cc_dashboard.config_data as mod

    original = mod._resolve_data_path
    mod._resolve_data_path = monkeypatch_resolve
    try:
        palette, active_fg, inactive_fg = mod._load_palette()
    finally:
        mod._resolve_data_path = original
    return palette, active_fg, inactive_fg


def test_fallback_on_missing_file():
    """_load_palette() returns full fallback when palette.json is missing."""
    print("\n--- fallback on missing palette.json ---")
    palette, active_fg, inactive_fg = _reload_palette(lambda _: None)

    check("palette is a dict", isinstance(palette, dict))
    check("palette has all 10 colors", set(palette.keys()) == EXPECTED_COLORS)
    check(
        "active_fg is RGB tuple", isinstance(active_fg, tuple) and len(active_fg) == 3
    )
    check(
        "inactive_fg is RGB tuple",
        isinstance(inactive_fg, tuple) and len(inactive_fg) == 3,
    )


def test_fallback_entries_are_valid_tuples():
    """Each fallback palette entry is (active_rgb, inactive_rgb, text_fg_rgb)."""
    print("\n--- fallback entry structure ---")
    palette, _, _ = _reload_palette(lambda _: None)

    for name, entry in palette.items():
        check(f"{name}: is 3-tuple", isinstance(entry, tuple) and len(entry) == 3)
        for i, label in enumerate(("active_bg", "inactive_bg", "text_fg")):
            rgb = entry[i]
            check(
                f"{name}: {label} is (int,int,int)",
                isinstance(rgb, tuple)
                and len(rgb) == 3
                and all(isinstance(v, int) and 0 <= v <= 255 for v in rgb),
            )


def test_fallback_on_corrupted_json():
    """_load_palette() returns fallback when palette.json is invalid JSON."""
    print("\n--- fallback on corrupted palette.json ---")
    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        f.write("{not valid json!!!")
        tmp_path = f.name
    try:
        palette, active_fg, _ = _reload_palette(lambda _: tmp_path)
        check("palette is a dict", isinstance(palette, dict))
        check("palette has all 10 colors", set(palette.keys()) == EXPECTED_COLORS)
        check(
            "active_fg is RGB tuple",
            isinstance(active_fg, tuple) and len(active_fg) == 3,
        )
    finally:
        os.unlink(tmp_path)


def test_fallback_on_malformed_structure():
    """_load_palette() falls back per-color on missing keys."""
    print("\n--- fallback on malformed palette structure ---")
    malformed = {
        "colors": {
            "green": {"active_bg": "#2d7d46"},  # missing inactive_bg, text_fg
        },
        "active_fg": "#ffffff",
        "inactive_fg": "#aaaaaa",
    }
    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        json.dump(malformed, f)
        tmp_path = f.name
    try:
        palette, _afg, _ifg = _reload_palette(lambda _: tmp_path)
        check("palette has all 10 colors", set(palette.keys()) == EXPECTED_COLORS)
        check(
            "malformed green used fallback",
            palette["green"] == ((45, 125, 70), (20, 60, 30), (255, 255, 255)),
        )
    finally:
        os.unlink(tmp_path)


if __name__ == "__main__":
    print("palette fallback tests")

    test_fallback_on_missing_file()
    test_fallback_entries_are_valid_tuples()
    test_fallback_on_corrupted_json()
    test_fallback_on_malformed_structure()

    print(f"\n{PASS} passed, {FAIL} failed")
    if FAIL:
        sys.exit(1)
