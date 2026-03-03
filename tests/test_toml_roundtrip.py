#!/usr/bin/env python3
"""Round-trip verification for the TOML writer and reader modules.

Run: python3 tests/test_toml_roundtrip.py
"""

import os
import sys
import tempfile

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "lib"))

import toml_reader
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


def test_accounts_roundtrip():
    print("\n--- accounts roundtrip ---")
    header = "# Account definitions\n# For cc-manager\n"
    data = {
        "accounts": [
            {"name": "work", "config_dir": "~/.claude", "color": "green"},
            {"name": "personal", "config_dir": "~/.claude-personal", "color": "blue"},
        ]
    }
    toml_str = toml_writer.dumps(data, header=header)
    check("header preserved in output", "# Account definitions" in toml_str)
    check("[[accounts]] present", "[[accounts]]" in toml_str)

    with tempfile.NamedTemporaryFile(mode="w", suffix=".toml", delete=False) as f:
        f.write(toml_str)
        path = f.name
    try:
        accounts = toml_reader.read_accounts(path)
        check("two accounts read back", len(accounts) == 2)
        check("first name matches", accounts[0]["name"] == "work")
        check("first config_dir matches", accounts[0]["config_dir"] == "~/.claude")
        check("first color matches", accounts[0]["color"] == "green")
        check("second name matches", accounts[1]["name"] == "personal")

        hdr = toml_reader.read_header_comments(path)
        check("header comments round-tripped", "Account definitions" in hdr)
    finally:
        os.unlink(path)


def test_sessions_roundtrip():
    print("\n--- sessions roundtrip ---")
    data = {
        "sessions": [
            {"title": "W:alpha", "directory": "~/projects/alpha", "account": "work"},
            {
                "title": "W:beta",
                "directory": "~/projects/beta",
                "account": "work",
                "flags": "--dangerously-skip-permissions",
                "project_window": "vsplit",
                "env": "DB=postgres://localhost/dev",
            },
        ]
    }
    toml_str = toml_writer.dumps(data)

    with tempfile.NamedTemporaryFile(mode="w", suffix=".toml", delete=False) as f:
        f.write(toml_str)
        path = f.name
    try:
        sessions = toml_reader.read_sessions(path)
        check("two sessions read back", len(sessions) == 2)
        check("simple session title", sessions[0]["title"] == "W:alpha")
        check("simple session dir", sessions[0]["dir"] == "~/projects/alpha")
        check("simple session has no flags", sessions[0]["flags"] == "")
        check(
            "complex session flags",
            sessions[1]["flags"] == "--dangerously-skip-permissions",
        )
        check("complex session pw", sessions[1]["pw"] == "vsplit")
        check(
            "complex session env", sessions[1]["env"] == "DB=postgres://localhost/dev"
        )
    finally:
        os.unlink(path)


def test_defaults_roundtrip():
    print("\n--- defaults roundtrip ---")
    data = {
        "defaults": {
            "claude_flags": "--dangerously-skip-permissions",
            "project_window": "stack",
            "sort_sessions": "alpha",
            "dashboard_refresh": 3,
        },
        "notifications": {
            "error": True,
            "permission": True,
            "stop": False,
            "idle": True,
            "idle_alert_minutes": 30,
            "min_secs": 3,
            "ignore_patterns": ["Conversation cleared", "Context compacted"],
        },
    }
    toml_str = toml_writer.dumps(data)
    check("[defaults] present", "[defaults]" in toml_str)
    check("[notifications] present", "[notifications]" in toml_str)

    with tempfile.NamedTemporaryFile(mode="w", suffix=".toml", delete=False) as f:
        f.write(toml_str)
        path = f.name
    try:
        defaults, notif = toml_reader.read_defaults(path)
        check(
            "claude_flags matches",
            defaults["claude_flags"] == "--dangerously-skip-permissions",
        )
        check("dashboard_refresh is int 3", defaults["dashboard_refresh"] == 3)
        check("sort_sessions matches", defaults["sort_sessions"] == "alpha")
        check("notify error is True", notif["error"] is True)
        check("notify stop is False", notif["stop"] is False)
        check("idle_alert_minutes is 30", notif["idle_alert_minutes"] == 30)
        check(
            "ignore_patterns is list",
            notif["ignore_patterns"] == ["Conversation cleared", "Context compacted"],
        )
    finally:
        os.unlink(path)


def test_writer_escaping():
    print("\n--- writer escaping ---")
    data = {
        "sessions": [
            {"title": 'has "quotes"', "directory": "~/path\\with\\backslash"},
        ]
    }
    toml_str = toml_writer.dumps(data)
    check("quotes escaped", r"has \"quotes\"" in toml_str)
    check("backslashes escaped", r"~/path\\with\\backslash" in toml_str)

    with tempfile.NamedTemporaryFile(mode="w", suffix=".toml", delete=False) as f:
        f.write(toml_str)
        path = f.name
    try:
        sessions = toml_reader.read_sessions(path)
        check("quotes round-trip", sessions[0]["title"] == 'has "quotes"')
        check("backslash round-trip", sessions[0]["dir"] == "~/path\\with\\backslash")
    finally:
        os.unlink(path)


def main():
    test_accounts_roundtrip()
    test_sessions_roundtrip()
    test_defaults_roundtrip()
    test_writer_escaping()

    print(f"\n{'=' * 40}")
    print(f"Results: {PASS} passed, {FAIL} failed")
    if FAIL:
        sys.exit(1)
    print("All tests passed!")


if __name__ == "__main__":
    main()
