#!/usr/bin/env python3
"""Tests for lib/config_validator.py.

Run: python3 tests/test_config_validator.py
"""

import sys

from config_validator_cases import shared, test_accounts, test_defaults, test_sessions


def main() -> None:
    print("config_validator tests\n")

    test_accounts.run()
    test_sessions.run()
    test_defaults.run()

    print(f"\n{shared.PASS} passed, {shared.FAIL} failed")
    if shared.FAIL:
        sys.exit(1)


if __name__ == "__main__":
    main()
