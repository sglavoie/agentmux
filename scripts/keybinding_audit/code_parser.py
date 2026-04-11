"""Code parsing for handled dashboard keybindings."""

from __future__ import annotations

from pathlib import Path

from .constants import (
    CURSES_KEY_MAP,
    FUNCTION_MODES,
    HANDLER_FILES,
    NUMERIC_KEY_MAP,
    RE_CURSES_KEY,
    RE_FUNCTION_DEF,
    RE_KEY_RANGE,
    RE_NUMERIC,
    RE_ORD,
    RE_PRINTABLE_RANGE,
)


def extract_all_code_keys() -> dict[str, set[str]]:
    """Extract handled keys per mode from all handler files."""
    code_keys: dict[str, set[str]] = {}

    for filepath in HANDLER_FILES:
        if not filepath.exists():
            continue

        functions = _parse_handler_functions(filepath)
        for func_name, func_lines in functions.items():
            if func_name not in FUNCTION_MODES:
                continue

            modes = FUNCTION_MODES[func_name]
            for mode, block_lines in _split_mode_blocks(func_lines, modes).items():
                code_keys.setdefault(mode, set()).update(
                    _extract_keys_for_mode_block(block_lines)
                )

    return code_keys


def _extract_keys_from_line(line: str) -> set[str]:
    """Extract normalized key identifiers from a single line of code."""
    keys: set[str] = set()

    for match in RE_ORD.finditer(line):
        character = match.group(1)
        if character:
            keys.add(character.lower())

    for match in RE_CURSES_KEY.finditer(line):
        name = match.group(1)
        if name in CURSES_KEY_MAP:
            keys.add(CURSES_KEY_MAP[name])

    if "key" in line and ("==" in line or "in (" in line or "in(" in line):
        for match in RE_NUMERIC.finditer(line):
            value = int(match.group(1))
            if value in NUMERIC_KEY_MAP:
                keys.add(NUMERIC_KEY_MAP[value])

    return keys


def _extract_keys_for_mode_block(lines: list[str]) -> set[str]:
    """Extract all key identifiers from a block of handler code."""
    keys: set[str] = set()
    has_printable_range = False

    for line in lines:
        keys.update(_extract_keys_from_line(line))
        if RE_PRINTABLE_RANGE.search(line):
            has_printable_range = True

        for match in RE_KEY_RANGE.finditer(line):
            lower, upper = match.groups()
            for value in range(int(lower), int(upper) + 1):
                keys.add(str(value))

    if has_printable_range:
        keys.add("_printable_")

    return keys


def _parse_handler_functions(filepath: Path) -> dict[str, list[str]]:
    """Parse a handler file and return `{function_name: [lines]}`."""
    functions: dict[str, list[str]] = {}
    with filepath.open(encoding="utf-8") as handle:
        source_lines = handle.readlines()

    current_func: str | None = None
    current_lines: list[str] = []

    for line in source_lines:
        stripped = line.rstrip()
        func_match = RE_FUNCTION_DEF.match(stripped)
        if func_match:
            if current_func:
                functions[current_func] = current_lines
            current_func = func_match.group(2)
            current_lines = [stripped]
            continue

        if not current_func:
            continue

        if stripped and not stripped.startswith((" ", "#")):
            functions[current_func] = current_lines
            current_func = None
            current_lines = []
            continue

        current_lines.append(stripped)

    if current_func:
        functions[current_func] = current_lines

    return functions


def _split_mode_blocks(lines: list[str], modes: list[str]) -> dict[str, list[str]]:
    """Split function lines into per-mode blocks based on mode string checks."""
    if len(modes) == 1:
        return {modes[0]: lines}

    blocks: dict[str, list[str]] = {mode: [] for mode in modes}
    current_mode: str | None = None

    for line in lines:
        for mode in modes:
            mode_mentioned = f'"{mode}"' in line or f"'{mode}'" in line
            mode_check = "mode ==" in line or "mode !=" in line or 'mode"] ==' in line
            if mode_mentioned and mode_check:
                current_mode = mode
                break

        if current_mode in blocks:
            blocks[current_mode].append(line)

    return blocks
