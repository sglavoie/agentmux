"""Low-level curses utilities and shared drawing primitives."""

from __future__ import annotations

import contextlib
import curses
import os
import unicodedata


def _to_ascii(text: str) -> str:
    """Strip non-ASCII characters for safe curses rendering."""
    return "".join(ch for ch in text if "\x20" <= ch <= "\x7e")


def _clip_to_width(text: str, max_width: int) -> str:
    """Clip text so its display width does not exceed *max_width*.

    Unlike a simple slice (``text[:n]``), this respects wide characters
    that occupy two terminal columns.
    """
    text = _to_ascii(text)
    w = 0
    for i, ch in enumerate(text):
        eaw = unicodedata.east_asian_width(ch)
        cw = 2 if eaw in ("W", "F") else 1
        if w + cw > max_width:
            return text[:i]
        w += cw
    return text


def safe_addstr(win: curses.window, y: int, x: int, text: str, attr: int = 0) -> None:
    """Write text to the curses window, silently clipping at window boundaries."""
    rows, cols = win.getmaxyx()
    if y < 0 or y >= rows or x < 0 or x >= cols:
        return
    text = _to_ascii(text)
    text = _clip_to_width(text, cols - x)
    with contextlib.suppress(curses.error, UnicodeEncodeError):
        win.addstr(y, x, text, attr)


def draw_border(
    win: curses.window, y: int, x: int, w: int, h: int, attr: int = 0
) -> None:
    """Draw an ASCII box border one cell outside the content area.

    The border wraps around a rectangle at (y, x) of size (w, h).
    Total footprint: (w+2) columns x (h+2) rows starting at (y-1, x-1).
    Off-screen edges are silently clipped by safe_addstr.
    """
    TL, TR, BL, BR, H, V = "+", "+", "+", "+", "-", "|"
    top, bot, left, right = y - 1, y + h, x - 1, x + w
    # Clear 1-cell padding region outside the border
    pad_top, pad_bot = y - 2, y + h + 1
    pad_left, pad_right = x - 2, x + w + 1
    pad_width = w + 4
    safe_addstr(win, pad_top, pad_left, " " * pad_width, attr)
    safe_addstr(win, pad_bot, pad_left, " " * pad_width, attr)
    for row in range(y - 1, y + h + 1):
        safe_addstr(win, row, pad_left, " ", attr)
        safe_addstr(win, row, pad_right, " ", attr)
    # Clear the border cells so characters aren't lost in the background
    safe_addstr(win, top, left, " " * (w + 2), attr)
    safe_addstr(win, bot, left, " " * (w + 2), attr)
    for row in range(y, y + h):
        safe_addstr(win, row, left, " ", attr)
        safe_addstr(win, row, right, " ", attr)
    # Draw the border characters
    safe_addstr(win, top, left, TL + H * w + TR, attr)
    safe_addstr(win, bot, left, BL + H * w + BR, attr)
    for row in range(y, y + h):
        safe_addstr(win, row, left, V, attr)
        safe_addstr(win, row, right, V, attr)


def _display_width(text: str) -> int:
    """Return the visual display width of text, accounting for wide characters."""
    text = _to_ascii(text)
    w = 0
    for ch in text:
        eaw = unicodedata.east_asian_width(ch)
        w += 2 if eaw in ("W", "F") else 1
    return w


def _truncate_to_width(text: str, width: int) -> str:
    """Truncate text with a trailing '..' when it exceeds the target width."""
    text = _to_ascii(text)
    if width < 2 or _display_width(text) <= width:
        return text
    result = []
    w = 0
    for ch in text:
        eaw = unicodedata.east_asian_width(ch)
        cw = 2 if eaw in ("W", "F") else 1
        if w + cw > width - 2:
            break
        result.append(ch)
        w += cw
    padding = width - w - 2  # space after content, before ".."
    return "".join(result) + " " * max(0, padding) + ".."


def _pad_to_width(text: str, width: int) -> str:
    """Pad text with spaces to reach the target visual width."""
    text = _to_ascii(text)
    current = _display_width(text)
    return text + " " * max(0, width - current)


def shorten_path(path: str, max_len: int) -> str:
    """Collapse home prefix to ~ and truncate long paths with a leading ellipsis.

    After collapsing the home prefix, keeps only the last 2 path segments
    to save horizontal space (e.g. ~/a/b/c -> b/c).
    """
    home = os.path.expanduser("~")
    if path.startswith(home):
        path = "~" + path[len(home) :]
    # Keep only the last 2 segments (drop ~ prefix for splitting)
    if path.startswith("~/"):
        inner = path[2:]
        segments = inner.split("/")
        path = "/".join(segments[-2:]) if len(segments) > 2 else inner
    if max_len < 4:
        return path[:max_len]
    if len(path) <= max_len:
        return path
    return "..." + path[-(max_len - 3) :]
