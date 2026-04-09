#!/usr/bin/env python3
"""Shared types and formatting helpers for config validation."""

from __future__ import annotations

import sys


def _use_color() -> bool:
    """Return True if stderr is a terminal (for colored output)."""
    return hasattr(sys.stderr, "isatty") and sys.stderr.isatty()


_COLOR: bool = _use_color()


def green(text: str) -> str:
    return f"\033[32m{text}\033[0m" if _COLOR else text


def yellow(text: str) -> str:
    return f"\033[33m{text}\033[0m" if _COLOR else text


def red(text: str) -> str:
    return f"\033[31m{text}\033[0m" if _COLOR else text


class Validator:
    """Accumulates errors and warnings for a single config file."""

    def __init__(self, label: str) -> None:
        self.label: str = label
        self.errors: list[str] = []
        self.warnings: list[str] = []

    def error(self, msg: str) -> None:
        self.errors.append(msg)

    def warn(self, msg: str) -> None:
        self.warnings.append(msg)

    def summary_line(self) -> str:
        parts: list[str] = []
        if self.errors:
            parts.append(red(f"{len(self.errors)} error(s)"))
        if self.warnings:
            parts.append(yellow(f"{len(self.warnings)} warning(s)"))
        status = ", ".join(parts) if parts else green("OK")
        return f"  {self.label} ... {status}"

    def detail_lines(self) -> list[str]:
        lines: list[str] = []
        for error in self.errors:
            lines.append(f"    {red('ERROR')}: {error}")
        for warning in self.warnings:
            lines.append(f"    {yellow('WARN')}:  {warning}")
        return lines
