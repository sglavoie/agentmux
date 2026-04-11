#!/usr/bin/env python3
"""Edge case tests for lib/toml_writer.py.

Run: python3 tests/test_toml_writer.py
"""

import os
import sys
import tempfile
import tomllib

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "lib"))

import toml_writer

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


def _roundtrip(data, header=""):
    """Write data to TOML string, parse it back, return parsed dict."""
    toml_str = toml_writer.dumps(data, header=header)
    with tempfile.NamedTemporaryFile(mode="w", suffix=".toml", delete=False) as f:
        f.write(toml_str)
        path = f.name
    try:
        with open(path, "rb") as fh:
            return tomllib.load(fh)
    finally:
        os.unlink(path)


# ── empty structures ─────────────────────────────────────────────────


def test_empty_list():
    """Empty array serializes and round-trips correctly."""
    print("\n--- empty list ---")
    data = {"defaults": {"ignore_patterns": []}}
    toml_str = toml_writer.dumps(data)
    check("empty list in output", "[]" in toml_str)
    parsed = _roundtrip(data)
    check("empty list round-trip", parsed["defaults"]["ignore_patterns"] == [])


def test_empty_array_of_tables():
    """Empty [[sessions]] list produces no entries."""
    print("\n--- empty array of tables ---")
    data = {"sessions": []}
    toml_str = toml_writer.dumps(data)
    check("no [[sessions]] header for empty list", "[[sessions]]" not in toml_str)


def test_empty_table():
    """Empty [defaults] section produces just the header."""
    print("\n--- empty table ---")
    data = {"defaults": {}}
    toml_str = toml_writer.dumps(data)
    check("[defaults] header present", "[defaults]" in toml_str)
    parsed = _roundtrip(data)
    check("empty table round-trip", parsed["defaults"] == {})


# ── special characters in strings ────────────────────────────────────


def test_special_chars_in_keys():
    """Keys with hyphens and underscores work correctly."""
    print("\n--- special chars in keys ---")
    data = {"defaults": {"my-key": "value1", "my_key2": "value2"}}
    parsed = _roundtrip(data)
    check("hyphen key round-trip", parsed["defaults"]["my-key"] == "value1")
    check("underscore key round-trip", parsed["defaults"]["my_key2"] == "value2")


def test_string_with_all_escape_chars():
    """String containing all escapable chars round-trips."""
    print("\n--- all escape chars ---")
    value = 'tab\there\nnewline\rreturn\\backslash"quote'
    data = {"defaults": {"messy": value}}
    parsed = _roundtrip(data)
    check("all escapes round-trip", parsed["defaults"]["messy"] == value)


def test_backspace_and_formfeed():
    """Backspace and form feed are escaped correctly (TOML spec requirement)."""
    print("\n--- backspace and formfeed ---")
    value = "before\bafter\fend"
    data = {"defaults": {"ctrl": value}}
    toml_str = toml_writer.dumps(data)
    check("backspace escaped as \\b", "\\b" in toml_str)
    check("formfeed escaped as \\f", "\\f" in toml_str)
    parsed = _roundtrip(data)
    check("backspace+formfeed round-trip", parsed["defaults"]["ctrl"] == value)


def test_nul_and_other_control_chars():
    """Control characters outside the named escapes use \\uXXXX."""
    print("\n--- NUL and other control chars ---")
    value = "bell\x07here\x00nul"
    data = {"defaults": {"ctrl": value}}
    toml_str = toml_writer.dumps(data)
    check("BEL escaped as \\u0007", "\\u0007" in toml_str)
    check("NUL escaped as \\u0000", "\\u0000" in toml_str)
    check("no literal control chars", all(ord(c) > 0x1F or c == "\n" for c in toml_str))
    parsed = _roundtrip(data)
    check("control chars round-trip", parsed["defaults"]["ctrl"] == value)


def test_empty_string_value():
    """Empty string value serializes correctly."""
    print("\n--- empty string ---")
    data = {"defaults": {"empty": ""}}
    toml_str = toml_writer.dumps(data)
    check('empty string as ""', 'empty = ""' in toml_str)
    parsed = _roundtrip(data)
    check("empty string round-trip", parsed["defaults"]["empty"] == "")


def test_string_with_unicode():
    """Unicode characters in values are preserved (not escaped)."""
    print("\n--- unicode strings ---")
    data = {"sessions": [{"title": "project-cafe", "icon": "★⚡🛑"}]}
    parsed = _roundtrip(data)
    check("unicode round-trip", parsed["sessions"][0]["icon"] == "★⚡🛑")


# ── nested structures ────────────────────────────────────────────────


def test_list_of_strings():
    """Array of strings serializes inline."""
    print("\n--- list of strings ---")
    data = {"defaults": {"patterns": ["foo", "bar", "baz"]}}
    toml_str = toml_writer.dumps(data)
    check("inline array", '["foo", "bar", "baz"]' in toml_str)
    parsed = _roundtrip(data)
    check("list round-trip", parsed["defaults"]["patterns"] == ["foo", "bar", "baz"])


def test_list_with_special_strings():
    """Array with strings needing escaping."""
    print("\n--- list with special strings ---")
    data = {
        "defaults": {"patterns": ['has "quotes"', "has\\backslash", "has\nnewline"]}
    }
    parsed = _roundtrip(data)
    check(
        "list special strings round-trip",
        parsed["defaults"]["patterns"]
        == ['has "quotes"', "has\\backslash", "has\nnewline"],
    )


def test_mixed_type_array_of_tables():
    """Multiple [[entries]] with varying optional keys."""
    print("\n--- mixed optional keys ---")
    data = {
        "sessions": [
            {"title": "minimal", "directory": "~"},
            {
                "title": "full",
                "directory": "~/proj",
                "flags": "--verbose",
                "env": "FOO=bar",
                "group": "dev",
            },
        ]
    }
    parsed = _roundtrip(data)
    check("minimal entry has 2 keys", len(parsed["sessions"][0]) == 2)
    check("full entry has 5 keys", len(parsed["sessions"][1]) == 5)
    check("group round-trip", parsed["sessions"][1]["group"] == "dev")


# ── type handling ────────────────────────────────────────────────────


def test_boolean_values():
    """Booleans serialize as true/false (not True/False)."""
    print("\n--- booleans ---")
    data = {"notifications": {"error": True, "idle": False}}
    toml_str = toml_writer.dumps(data)
    check("true not True", "true" in toml_str and "True" not in toml_str)
    check("false not False", "false" in toml_str and "False" not in toml_str)
    parsed = _roundtrip(data)
    check("bool True round-trip", parsed["notifications"]["error"] is True)
    check("bool False round-trip", parsed["notifications"]["idle"] is False)


def test_integer_values():
    """Integers including zero and negative."""
    print("\n--- integers ---")
    data = {"defaults": {"zero": 0, "negative": -5, "large": 99999}}
    parsed = _roundtrip(data)
    check("zero round-trip", parsed["defaults"]["zero"] == 0)
    check("negative round-trip", parsed["defaults"]["negative"] == -5)
    check("large round-trip", parsed["defaults"]["large"] == 99999)


def test_unsupported_type_raises():
    """Unsupported types (None, dict in value position) raise TypeError."""
    print("\n--- unsupported types ---")
    try:
        toml_writer.dumps({"defaults": {"bad": None}})
        check("None raises TypeError", False)
    except TypeError:
        check("None raises TypeError", True)

    try:
        toml_writer.dumps({"defaults": {"nested": {"a": 1}}})
        check("nested dict raises TypeError", False)
    except TypeError:
        check("nested dict raises TypeError", True)


def test_top_level_non_dict_raises():
    """Non-dict/list top-level value raises TypeError."""
    print("\n--- top-level non-dict ---")
    try:
        toml_writer.dumps({"bad": "string"})
        check("string top-level raises TypeError", False)
    except TypeError:
        check("string top-level raises TypeError", True)


def test_float_nan_inf_raises():
    """Float nan/inf values raise ValueError (invalid in TOML)."""
    print("\n--- float nan/inf ---")
    bad_floats = [
        ("nan", float("nan")),
        ("inf", float("inf")),
        ("-inf", float("-inf")),
    ]
    for label, val in bad_floats:
        try:
            toml_writer.dumps({"defaults": {"bad": val}})
            check(f"{label} raises ValueError", False)
        except ValueError:
            check(f"{label} raises ValueError", True)

    # Normal floats still work
    data = {"defaults": {"ok": 3.14}}
    parsed = _roundtrip(data)
    check("normal float round-trip", abs(parsed["defaults"]["ok"] - 3.14) < 1e-9)


# ── header handling ──────────────────────────────────────────────────


def test_header_with_trailing_newlines():
    """Header trailing newlines are normalized."""
    print("\n--- header normalization ---")
    data = {"defaults": {"key": "val"}}
    toml_str = toml_writer.dumps(data, header="# Comment\n\n\n")
    lines = toml_str.split("\n")
    check("header is first line", lines[0] == "# Comment")
    # Should have exactly one blank line between header and content
    check("single blank line after header", lines[1] == "" and lines[2] == "[defaults]")


def test_no_header():
    """No header starts directly with section."""
    print("\n--- no header ---")
    data = {"defaults": {"key": "val"}}
    toml_str = toml_writer.dumps(data)
    check("starts with [defaults]", toml_str.startswith("[defaults]"))


# ── run all ──────────────────────────────────────────────────────────


if __name__ == "__main__":
    print("toml_writer edge case tests")

    test_empty_list()
    test_empty_array_of_tables()
    test_empty_table()
    test_special_chars_in_keys()
    test_string_with_all_escape_chars()
    test_backspace_and_formfeed()
    test_nul_and_other_control_chars()
    test_empty_string_value()
    test_string_with_unicode()
    test_list_of_strings()
    test_list_with_special_strings()
    test_mixed_type_array_of_tables()
    test_boolean_values()
    test_integer_values()
    test_unsupported_type_raises()
    test_top_level_non_dict_raises()
    test_float_nan_inf_raises()
    test_header_with_trailing_newlines()
    test_no_header()

    print(f"\n{PASS} passed, {FAIL} failed")
    if FAIL:
        sys.exit(1)
