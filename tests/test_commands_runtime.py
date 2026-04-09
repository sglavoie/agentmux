#!/usr/bin/env python3
"""Runtime regression tests for the commands feature.

Run: python3 tests/test_commands_runtime.py
"""

import sys

import commands_runtime_support as support
from test_commands_runtime_launch import run_tests as run_launch_tests
from test_commands_runtime_lookup import run_tests as run_lookup_tests
from test_commands_runtime_override import run_tests as run_override_tests


def main() -> int:
    print("commands runtime tests")
    run_launch_tests()
    run_override_tests()
    run_lookup_tests()
    print(f"\n{support.PASS} passed, {support.FAIL} failed")
    return 1 if support.FAIL else 0


if __name__ == "__main__":
    sys.exit(main())
