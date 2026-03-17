#!/usr/bin/env python3
"""Unit tests for kittens/cc_dashboard/drawing.py.

Tests _clip_to_width(), _truncate_to_width(), _display_width(),
_pad_to_width(), and _to_ascii() with ASCII, emoji, and edge cases.

Run: python3 tests/test_dashboard_drawing.py
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "kittens", "cc_dashboard"))

from drawing import (
    _clip_to_width,
    _display_width,
    _pad_to_width,
    _to_ascii,
    _truncate_to_width,
)

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


# ── _to_ascii ──────────────────────────────────────────────────────────


def test_to_ascii():
    """Strip non-ASCII characters, keeping only printable ASCII."""
    check("pure ascii unchanged", _to_ascii("hello world") == "hello world")
    check("empty string", _to_ascii("") == "")
    check("emoji stripped", _to_ascii("\U0001f431ccm") == "ccm")
    check("multiple emoji stripped", _to_ascii("\U0001f916sloppy-paste") == "sloppy-paste")
    check("bullet stripped", _to_ascii("\u2022dotfiles") == "dotfiles")
    check("cjk stripped", _to_ascii("\u4e16\u754c") == "")
    check("mixed ascii+cjk", _to_ascii("ab\u4e16c") == "abc")
    check("control chars stripped", _to_ascii("\x00\x01\x1f") == "")
    check("space preserved", _to_ascii(" a b ") == " a b ")
    check("tilde preserved", _to_ascii("~") == "~")
    check("all printable ascii", _to_ascii("!@#$%^&*()") == "!@#$%^&*()")


# ── _display_width ────────────────────────────────────────────────────


def test_display_width():
    """Display width after ASCII stripping."""
    check("ascii width", _display_width("hello") == 5)
    check("empty string", _display_width("") == 0)
    # Non-ASCII chars are stripped, so width is 0
    check("cjk stripped width", _display_width("\u4e16\u754c") == 0)
    check("mixed ascii+cjk", _display_width("ab\u4e16") == 2)
    # Emoji stripped, only ASCII counted
    check("emoji prefix stripped", _display_width("\U0001f431ccm") == 3)


# ── _clip_to_width ────────────────────────────────────────────────────


def test_clip_to_width_ascii():
    """Clip ASCII strings at various widths."""
    check("ascii shorter than limit", _clip_to_width("abc", 10) == "abc")
    check("ascii equal to limit", _clip_to_width("abc", 3) == "abc")
    check("ascii longer than limit", _clip_to_width("abcdef", 3) == "abc")
    check("ascii limit=0", _clip_to_width("abc", 0) == "")
    check("ascii limit=1", _clip_to_width("abc", 1) == "a")
    check("empty string", _clip_to_width("", 5) == "")


def test_clip_to_width_nonascii():
    """Non-ASCII characters are stripped before clipping."""
    # CJK chars stripped, result is empty
    check("cjk stripped to empty", _clip_to_width("\u4e16\u754c\u4f60", 6) == "")
    check("cjk stripped clip at 1", _clip_to_width("\u4e16\u754c\u4f60", 1) == "")
    # Mixed: only ASCII survives
    check("mixed keeps ascii", _clip_to_width("a\u4e16b", 4) == "ab")
    check("mixed clip at 1", _clip_to_width("a\u4e16b", 1) == "a")
    # Emoji prefix stripped
    check("emoji prefix stripped", _clip_to_width("\U0001f431ccm", 3) == "ccm")
    check("emoji prefix clipped", _clip_to_width("\U0001f431ccm", 2) == "cc")


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


def test_truncate_to_width_nonascii():
    """Non-ASCII characters are stripped before truncation."""
    # CJK stripped → empty string, fits any width
    check("cjk stripped fits", _truncate_to_width("\u4e16\u754c\u4f60", 6) == "")
    check("cjk stripped fits 2", _truncate_to_width("\u4e16\u754c\u4f60", 2) == "")
    # Emoji prefix stripped, then normal truncation
    check("emoji stripped fits", _truncate_to_width("\U0001f431ccm", 5) == "ccm")
    check("emoji stripped truncate", _truncate_to_width("\U0001f431abcdef", 5) == "abc..")


# ── _pad_to_width ─────────────────────────────────────────────────────


def test_pad_to_width():
    """Pad strings to target width with spaces."""
    check("pad ascii", _pad_to_width("abc", 6) == "abc   ")
    check("pad already at width", _pad_to_width("abc", 3) == "abc")
    check("pad wider than target", _pad_to_width("abcdef", 3) == "abcdef")
    # CJK stripped → empty string → 4 spaces
    check("pad cjk stripped", _pad_to_width("\u4e16", 4) == "    ")
    # Emoji stripped → pad remaining ASCII
    check("pad emoji stripped", _pad_to_width("\U0001f431cc", 5) == "cc   ")


# ── run all ───────────────────────────────────────────────────────────


if __name__ == "__main__":
    print("dashboard drawing tests\n")

    test_to_ascii()
    test_display_width()
    test_clip_to_width_ascii()
    test_clip_to_width_nonascii()
    test_truncate_to_width_ascii()
    test_truncate_to_width_edge_cases()
    test_truncate_to_width_nonascii()
    test_pad_to_width()

    print(f"\n{PASS} passed, {FAIL} failed")
    if FAIL:
        sys.exit(1)
