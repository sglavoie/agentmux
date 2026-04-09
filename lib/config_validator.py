#!/usr/bin/env python3
"""Validate cc-manager config files.

Checks accounts.toml, sessions.toml, defaults.toml, and hooks.json for
syntax errors, missing required fields, invalid values, and cross-file
consistency (e.g. sessions referencing non-existent accounts).

Exit codes: 0 = all OK (warnings are fine), 1 = errors found.
"""

from __future__ import annotations

import sys

from config_validator_accounts import validate_accounts
from config_validator_defaults import validate_defaults
from config_validator_hooks import validate_hooks
from config_validator_runner import collect_validators, quick_validate
from config_validator_sessions import validate_sessions
from config_validator_shared import Validator, green, red, yellow
from data_loader import load_palette_colors


def main() -> None:
    if len(sys.argv) < 3:
        print(f"Usage: {sys.argv[0]} CONFIG_DIR DATA_DIR", file=sys.stderr)
        sys.exit(2)

    config_dir = sys.argv[1]
    data_dir = sys.argv[2]

    if load_palette_colors(data_dir) is None:
        print(
            yellow("  WARN")
            + ": could not load palette.json; skipping color validation",
            file=sys.stderr,
        )

    validators = collect_validators(config_dir, data_dir)

    print("Validating cc-manager configuration:\n")
    for validator in validators:
        print(validator.summary_line())
        for line in validator.detail_lines():
            print(line)

    total_errors = sum(len(validator.errors) for validator in validators)
    total_warnings = sum(len(validator.warnings) for validator in validators)

    print()
    if total_errors:
        print(
            red(f"Found {total_errors} error(s)")
            + (f" and {total_warnings} warning(s)." if total_warnings else ".")
        )
        sys.exit(1)
    if total_warnings:
        print(yellow(f"All clear with {total_warnings} warning(s)."))
    else:
        print(green("All configuration files are valid."))


__all__ = [
    "Validator",
    "main",
    "quick_validate",
    "validate_accounts",
    "validate_defaults",
    "validate_hooks",
    "validate_sessions",
]


if __name__ == "__main__":
    main()
