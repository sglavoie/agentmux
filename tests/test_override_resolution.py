#!/usr/bin/env python3
"""Tests for the three-way override resolution model.

The override model works as follows:
- Omitted (empty) → use the global default
- "-" → disable (return empty string)
- Any other value → override (use the provided value)

This is implemented in lib/cc-common/defaults.sh as resolve_claude_flags()
and resolve_project_window(). We test by sourcing the shell functions.

Run: python3 tests/test_override_resolution.py
"""

import os
import subprocess
import sys

PASS = 0
FAIL = 0

REPO_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))


def check(label, condition):
    global PASS, FAIL
    if condition:
        PASS += 1
        print(f"  PASS  {label}")
    else:
        FAIL += 1
        print(f"  FAIL  {label}")


def _resolve_via_shell(func_name, default_value, override):
    """Call a resolve function from defaults.sh with given default and override.

    Sets the global default variable, then calls the resolve function with
    the override argument. Returns the resolved value.
    """
    if func_name == "resolve_claude_flags":
        default_var = "_DEFAULT_CLAUDE_FLAGS"
    elif func_name == "resolve_project_window":
        default_var = "_DEFAULT_PROJECT_WINDOW"
    else:
        raise ValueError(f"Unknown function: {func_name}")

    # Build a minimal bash script that sources only defaults.sh and calls the function
    script = f"""
        set -euo pipefail
        # Source only the defaults module (it has a source guard)
        _CC_COMMON_DEFAULTS_LOADED=""
        source "{REPO_DIR}/lib/cc-common/defaults.sh"
        # Set the default
        {default_var}='{default_value}'
        # Call the resolve function and print result
        result=$({func_name} '{override}')
        printf '%s' "$result"
    """

    result = subprocess.run(
        ["bash", "-c", script],
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        print(f"    Shell error: {result.stderr.strip()}")
        return None
    return result.stdout


# ── resolve_claude_flags ──────────────────────────────────────────────


def test_resolve_claude_flags():
    """Three-way override for claude_flags."""
    print("\n--- resolve_claude_flags ---")

    # Omitted → use default
    result = _resolve_via_shell("resolve_claude_flags", "--verbose", "")
    check("omitted → use default", result == "--verbose")

    # "-" → disable
    result = _resolve_via_shell("resolve_claude_flags", "--verbose", "-")
    check("dash → disable (empty)", result == "")

    # Override → use override value
    result = _resolve_via_shell("resolve_claude_flags", "--verbose", "--debug")
    check("override → use override", result == "--debug")

    # Both empty → empty
    result = _resolve_via_shell("resolve_claude_flags", "", "")
    check("both empty → empty", result == "")

    # Default empty, override provided → use override
    result = _resolve_via_shell("resolve_claude_flags", "", "--fast")
    check("no default, override → use override", result == "--fast")

    # Default set, dash → disable (not default)
    result = _resolve_via_shell("resolve_claude_flags", "--important", "-")
    check("default set, dash → empty (not default)", result == "")


# ── resolve_project_window ────────────────────────────────────────────


def test_resolve_project_window():
    """Three-way override for project_window."""
    print("\n--- resolve_project_window ---")

    # Omitted → use default
    result = _resolve_via_shell("resolve_project_window", "vsplit", "")
    check("omitted → use default", result == "vsplit")

    # "-" → disable
    result = _resolve_via_shell("resolve_project_window", "vsplit", "-")
    check("dash → disable (empty)", result == "")

    # Override → use override value
    result = _resolve_via_shell("resolve_project_window", "vsplit", "hsplit")
    check("override → use override", result == "hsplit")

    # "stack" override
    result = _resolve_via_shell("resolve_project_window", "vsplit", "stack")
    check("stack override", result == "stack")

    # "split" override
    result = _resolve_via_shell("resolve_project_window", "", "split")
    check("split override", result == "split")


# ── run all ───────────────────────────────────────────────────────────


if __name__ == "__main__":
    print("override resolution tests")

    test_resolve_claude_flags()
    test_resolve_project_window()

    print(f"\n{PASS} passed, {FAIL} failed")
    if FAIL:
        sys.exit(1)
