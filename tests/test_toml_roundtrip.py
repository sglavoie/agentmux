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
                "groups": ["backend", "api"],
                "commands": ["npm run dev", "shell"],
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
        check("simple session has no commands", sessions[0]["commands"] == [])
        check("simple session has empty groups", sessions[0]["groups"] == [])
        check(
            "complex session flags",
            sessions[1]["flags"] == "--dangerously-skip-permissions",
        )
        check("complex session pw", sessions[1]["pw"] == "vsplit")
        check(
            "complex session env", sessions[1]["env"] == "DB=postgres://localhost/dev"
        )
        check(
            "complex session commands",
            sessions[1]["commands"] == ["npm run dev", "shell"],
        )
        check(
            "complex session groups",
            sessions[1]["groups"] == ["backend", "api"],
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


def test_control_char_escaping():
    print("\n--- control character escaping ---")
    data = {
        "sessions": [
            {
                "title": "has\nnewline",
                "directory": "~/path\twith\ttabs",
                "account": "work",
            },
            {
                "title": "has\rcarriage\rreturn",
                "directory": "~/normal/path",
                "account": "personal",
            },
        ]
    }
    toml_str = toml_writer.dumps(data)
    check("newline escaped in output", "\\n" in toml_str)
    check("tab escaped in output", "\\t" in toml_str)
    check("cr escaped in output", "\\r" in toml_str)
    check("no literal newline in value", '\n"has\n' not in toml_str)

    with tempfile.NamedTemporaryFile(mode="w", suffix=".toml", delete=False) as f:
        f.write(toml_str)
        path = f.name
    try:
        sessions = toml_reader.read_sessions(path)
        check("two sessions read back", len(sessions) == 2)
        check("newline round-trip", sessions[0]["title"] == "has\nnewline")
        check("tab round-trip", sessions[0]["dir"] == "~/path\twith\ttabs")
        check("cr round-trip", sessions[1]["title"] == "has\rcarriage\rreturn")
    finally:
        os.unlink(path)


def test_mute_roundtrip():
    print("\n--- mute flag roundtrip ---")
    data = {
        "sessions": [
            {
                "title": "W:muted-session",
                "directory": "~/projects/muted",
                "account": "work",
                "mute": True,
            },
            {
                "title": "W:normal-session",
                "directory": "~/projects/normal",
                "account": "work",
            },
        ]
    }
    toml_str = toml_writer.dumps(data)
    check("mute = true in output", "mute = true" in toml_str)

    with tempfile.NamedTemporaryFile(mode="w", suffix=".toml", delete=False) as f:
        f.write(toml_str)
        path = f.name
    try:
        sessions = toml_reader.read_sessions(path)
        check("two sessions read back", len(sessions) == 2)
        check("muted session has mute=true", sessions[0]["mute"] == "true")
        check("normal session has mute=empty", sessions[1]["mute"] == "")
    finally:
        os.unlink(path)


def test_session_to_toml_entry_mute():
    print("\n--- _session_to_toml_entry mute parameter ---")
    kitten_path = os.path.join(
        os.path.dirname(__file__), "..", "kittens"
    )
    sys.path.insert(0, kitten_path)
    from cc_dashboard.config_sessions import _session_to_toml_entry

    entry_muted = _session_to_toml_entry(
        "W:test", "~/proj", "work", mute=True
    )
    check("muted entry has mute key", "mute" in entry_muted)
    check("muted entry mute is True", entry_muted["mute"] is True)

    entry_not_muted = _session_to_toml_entry(
        "W:test", "~/proj", "work", mute=False
    )
    check("non-muted entry omits mute key", "mute" not in entry_not_muted)

    entry_default = _session_to_toml_entry("W:test", "~/proj", "work")
    check("default entry omits mute key", "mute" not in entry_default)

    # Verify full round-trip: entry -> TOML -> parse
    full_data = {"sessions": [entry_muted, entry_not_muted]}
    toml_str = toml_writer.dumps(full_data)
    with tempfile.NamedTemporaryFile(mode="w", suffix=".toml", delete=False) as f:
        f.write(toml_str)
        path = f.name
    try:
        sessions = toml_reader.read_sessions(path)
        check("muted session round-trips mute=true", sessions[0]["mute"] == "true")
        check("non-muted session round-trips mute=empty", sessions[1]["mute"] == "")
    finally:
        os.unlink(path)


def test_update_session_preserves_mute():
    print("\n--- update_session preserves mute ---")
    kitten_path = os.path.join(
        os.path.dirname(__file__), "..", "kittens"
    )
    if kitten_path not in sys.path:
        sys.path.insert(0, kitten_path)
    from cc_dashboard.config_sessions import (
        _session_to_toml_entry,
    )
    from cc_dashboard import config_state as _state
    import cc_dashboard.config_sessions as _csess

    # Build a sessions.toml with one muted session
    entry_muted = _session_to_toml_entry(
        "W:muted-proj", "~/projects/muted", "work", mute=True
    )
    entry_normal = _session_to_toml_entry(
        "W:normal-proj", "~/projects/normal", "work", flags="--verbose"
    )
    with tempfile.NamedTemporaryFile(
        mode="w", suffix=".toml", delete=False, dir=tempfile.gettempdir()
    ) as f:
        f.write(toml_writer.dumps({"sessions": [entry_muted, entry_normal]}))
        path = f.name

    # Patch config path at the config_sessions module level (where it's imported)
    orig_path_fn = _csess._sessions_config_path
    _csess._sessions_config_path = lambda: path
    orig_sessions = list(_state._sessions)
    orig_account_names = list(_state._account_names)
    _state._sessions = []
    _state._account_names = ["work"]

    try:
        # Simulate an edit: change title and flags, but don't pass mute
        ok = _csess.update_session(
            "W:muted-proj",
            "W:muted-proj-renamed",
            "~/projects/muted",
            "work",
            flags="--new-flag",
        )
        check("update_session found the entry", ok)

        # Re-read the file and verify mute was preserved
        sessions = toml_reader.read_sessions(path)
        check("two sessions after update", len(sessions) == 2)
        check(
            "updated session title changed",
            sessions[0]["title"] == "W:muted-proj-renamed",
        )
        check(
            "updated session flags changed",
            sessions[0]["flags"] == "--new-flag",
        )
        check(
            "mute preserved through update",
            sessions[0]["mute"] == "true",
        )
        check(
            "normal session mute still empty",
            sessions[1]["mute"] == "",
        )

        # Verify explicit mute=False override works
        ok2 = _csess.update_session(
            "W:muted-proj-renamed",
            "W:muted-proj-renamed",
            "~/projects/muted",
            "work",
            mute=False,
        )
        check("update_session explicit mute=False", ok2)
        sessions2 = toml_reader.read_sessions(path)
        check(
            "explicit mute=False clears mute",
            sessions2[0]["mute"] == "",
        )
    finally:
        _csess._sessions_config_path = orig_path_fn
        _state._sessions = orig_sessions
        _state._account_names = orig_account_names
        os.unlink(path)
        # Clean up backup file if created
        bak = path + ".bak"
        if os.path.exists(bak):
            os.unlink(bak)


def test_float_values():
    print("\n--- float values ---")
    data = {
        "defaults": {
            "refresh_rate": 2.5,
            "timeout": 30.0,
            "label": "test",
        }
    }
    toml_str = toml_writer.dumps(data)
    check("float 2.5 in output", "2.5" in toml_str)
    check("float 30.0 in output", "30.0" in toml_str)

    with tempfile.NamedTemporaryFile(mode="w", suffix=".toml", delete=False) as f:
        f.write(toml_str)
        path = f.name
    try:
        import tomllib

        with open(path, "rb") as fh:
            parsed = tomllib.load(fh)
        check("float round-trip 2.5", parsed["defaults"]["refresh_rate"] == 2.5)
        check("float round-trip 30.0", parsed["defaults"]["timeout"] == 30.0)
    finally:
        os.unlink(path)


def main():
    test_accounts_roundtrip()
    test_sessions_roundtrip()
    test_defaults_roundtrip()
    test_writer_escaping()
    test_control_char_escaping()
    test_mute_roundtrip()
    test_session_to_toml_entry_mute()
    test_update_session_preserves_mute()
    test_float_values()

    print(f"\n{'=' * 40}")
    print(f"Results: {PASS} passed, {FAIL} failed")
    if FAIL:
        sys.exit(1)
    print("All tests passed!")


if __name__ == "__main__":
    main()
