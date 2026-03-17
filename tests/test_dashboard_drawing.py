#!/usr/bin/env python3
"""Unit tests for kittens/cc_dashboard/drawing.py.

Tests _clip_to_width(), _truncate_to_width(), _display_width(), and
_pad_to_width() with ASCII, wide characters (CJK), and edge cases.

Run: python3 tests/test_dashboard_drawing.py
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "kittens", "cc_dashboard"))

from drawing import _clip_to_width, _display_width, _pad_to_width, _truncate_to_width

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


# ── _display_width ────────────────────────────────────────────────────


def test_display_width():
    """Display width accounts for wide characters."""
    check("ascii width", _display_width("hello") == 5)
    check("empty string", _display_width("") == 0)
    # CJK characters are width 2 each
    check("cjk width", _display_width("\u4e16\u754c") == 4)  # 世界
    check("mixed ascii+cjk", _display_width("ab\u4e16") == 4)  # ab世


# ── _clip_to_width ────────────────────────────────────────────────────


def test_clip_to_width_ascii():
    """Clip ASCII strings at various widths."""
    check("ascii shorter than limit", _clip_to_width("abc", 10) == "abc")
    check("ascii equal to limit", _clip_to_width("abc", 3) == "abc")
    check("ascii longer than limit", _clip_to_width("abcdef", 3) == "abc")
    check("ascii limit=0", _clip_to_width("abc", 0) == "")
    check("ascii limit=1", _clip_to_width("abc", 1) == "a")
    check("empty string", _clip_to_width("", 5) == "")


def test_clip_to_width_wide():
    """Clip wide (CJK) characters correctly."""
    # Each CJK char is width 2
    cjk = "\u4e16\u754c\u4f60"  # 世界你 = width 6
    check("cjk fits", _clip_to_width(cjk, 6) == cjk)
    check("cjk clip at 4", _clip_to_width(cjk, 4) == "\u4e16\u754c")  # 世界
    check("cjk clip at 3", _clip_to_width(cjk, 3) == "\u4e16")  # 世 (width 2, next would be 4)
    check("cjk clip at 1", _clip_to_width(cjk, 1) == "")  # Can't fit a width-2 char


def test_clip_to_width_mixed():
    """Clip mixed ASCII + wide characters."""
    mixed = "a\u4e16b"  # a世b = width 4
    check("mixed fits", _clip_to_width(mixed, 4) == mixed)
    check("mixed clip at 3", _clip_to_width(mixed, 3) == "a\u4e16")  # a世
    check("mixed clip at 2", _clip_to_width(mixed, 2) == "a")  # can't fit 世 (width 2) after a
    check("mixed clip at 1", _clip_to_width(mixed, 1) == "a")


# ── _truncate_to_width ────────────────────────────────────────────────


def test_truncate_to_width_ascii():
    """Truncate ASCII strings with '..' suffix."""
    check("ascii shorter than limit", _truncate_to_width("abc", 10) == "abc")
    check("ascii equal to limit", _truncate_to_width("abc", 3) == "abc")
    check("ascii longer: 6 chars, limit 5",
          _truncate_to_width("abcdef", 5) == "abc..")
    check("ascii longer: 6 chars, limit 4",
          _truncate_to_width("abcdef", 4) == "ab..")
    check("ascii longer: 6 chars, limit 3",
          _truncate_to_width("abcdef", 3) == "a..")
    check("ascii longer: 6 chars, limit 2",
          _truncate_to_width("abcdef", 2) == "..")


def test_truncate_to_width_edge_cases():
    """Truncate edge cases: limit=0, limit=1."""
    # When limit < 2, function returns original text (can't fit "..")
    check("limit=0 returns original", _truncate_to_width("abc", 0) == "abc")
    check("limit=1 returns original", _truncate_to_width("abc", 1) == "abc")
    # Empty string never needs truncation
    check("empty string", _truncate_to_width("", 5) == "")


def test_truncate_to_width_wide():
    """Truncate wide characters with '..' suffix."""
    cjk = "\u4e16\u754c\u4f60"  # 世界你 = width 6
    check("cjk fits in 6", _truncate_to_width(cjk, 6) == cjk)
    # limit 5: need 2 for "..", leaves 3 columns → 世 (width 2) fits, 界 (would be 4) doesn't
    check("cjk truncate at 5", _truncate_to_width(cjk, 5) == "\u4e16 ..")
    # limit 4: need 2 for "..", leaves 2 columns → 世 (width 2) fits
    check("cjk truncate at 4", _truncate_to_width(cjk, 4) == "\u4e16..")
    # limit 3: need 2 for "..", leaves 1 column → no CJK char fits (all width 2)
    check("cjk truncate at 3", _truncate_to_width(cjk, 3) == " ..")
    check("cjk truncate at 2", _truncate_to_width(cjk, 2) == "..")


def test_truncate_to_width_mixed():
    """Truncate mixed ASCII + wide characters."""
    mixed = "ab\u4e16\u754c"  # ab世界 = width 6
    check("mixed fits in 6", _truncate_to_width(mixed, 6) == mixed)
    # limit 5: 2 for "..", 3 left → "ab" (2) + 世 doesn't fit (would be 4)
    check("mixed truncate at 5", _truncate_to_width(mixed, 5) == "ab ..")
    check("mixed truncate at 4", _truncate_to_width(mixed, 4) == "ab..")


# ── _pad_to_width ─────────────────────────────────────────────────────


def test_pad_to_width():
    """Pad strings to target width with spaces."""
    check("pad ascii", _pad_to_width("abc", 6) == "abc   ")
    check("pad already at width", _pad_to_width("abc", 3) == "abc")
    check("pad wider than target", _pad_to_width("abcdef", 3) == "abcdef")
    check("pad cjk", _pad_to_width("\u4e16", 4) == "\u4e16  ")  # 世 + 2 spaces


# ── run all ───────────────────────────────────────────────────────────


if __name__ == "__main__":
    print("dashboard drawing tests\n")

    test_display_width()
    test_clip_to_width_ascii()
    test_clip_to_width_wide()
    test_clip_to_width_mixed()
    test_truncate_to_width_ascii()
    test_truncate_to_width_edge_cases()
    test_truncate_to_width_wide()
    test_truncate_to_width_mixed()
    test_pad_to_width()

    print(f"\n{PASS} passed, {FAIL} failed")
    if FAIL:
        sys.exit(1)
