#!/usr/bin/env python3
"""Tests for lib/config_validator.py.

Run: python3 tests/test_config_validator.py
"""

import os
import sys
import tempfile

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "lib"))

import config_validator as cv

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


# ── helpers ───────────────────────────────────────────────────────────


def _write(tmp_dir, filename, content):
    path = os.path.join(tmp_dir, filename)
    with open(path, "w") as f:
        f.write(content)
    return path


# ── accounts.toml ────────────────────────────────────────────────────


def test_valid_accounts():
    """Valid accounts.toml produces no errors or warnings."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "accounts.toml", """\
[[accounts]]
name = "work"
config_dir = "~"
color = "blue"
""")
        # Use a palette that includes "blue"
        v, names = cv.validate_accounts(path, {"blue", "green", "red"})
        check("valid accounts: no errors", len(v.errors) == 0)
        check("valid accounts: no warnings", len(v.warnings) == 0)
        check("valid accounts: name captured", "work" in names)


def test_accounts_missing_name():
    """Account without a name produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "accounts.toml", """\
[[accounts]]
config_dir = "~"
color = "blue"
""")
        v, _ = cv.validate_accounts(path, {"blue"})
        check("missing name: error", len(v.errors) == 1)
        check("missing name: mentions 'name'", "name" in v.errors[0])


def test_accounts_invalid_color():
    """Unknown color produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "accounts.toml", """\
[[accounts]]
name = "test"
color = "neon"
""")
        v, _ = cv.validate_accounts(path, {"blue", "green"})
        check("invalid color: error", len(v.errors) == 1)
        check("invalid color: mentions 'neon'", "neon" in v.errors[0])


def test_accounts_unknown_key():
    """Unknown key in account produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "accounts.toml", """\
[[accounts]]
name = "test"
color = "blue"
bogus_key = "oops"
""")
        v, _ = cv.validate_accounts(path, {"blue"})
        check("unknown key: warning", len(v.warnings) == 1)
        check("unknown key: mentions 'bogus_key'", "bogus_key" in v.warnings[0])


def test_accounts_nonexistent_config_dir():
    """Non-existent config_dir produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "accounts.toml", """\
[[accounts]]
name = "test"
config_dir = "/no/such/path/xyz"
""")
        v, _ = cv.validate_accounts(path, None)
        check("nonexistent config_dir: warning", len(v.warnings) == 1)
        check("nonexistent config_dir: mentions path",
              "/no/such/path/xyz" in v.warnings[0])


def test_accounts_duplicate():
    """Duplicate account name produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "accounts.toml", """\
[[accounts]]
name = "work"
[[accounts]]
name = "work"
""")
        v, _ = cv.validate_accounts(path, None)
        check("duplicate name: error", any("duplicate" in e for e in v.errors))


def test_accounts_missing_file():
    """Missing file produces a warning, not a crash."""
    v, names = cv.validate_accounts("/no/such/file.toml", None)
    check("missing file: no errors", len(v.errors) == 0)
    check("missing file: warning", len(v.warnings) == 1)
    check("missing file: no names", len(names) == 0)


def test_accounts_bad_toml():
    """Invalid TOML produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "accounts.toml", "this is [[[bad toml")
        v, _ = cv.validate_accounts(path, None)
        check("bad toml: error", len(v.errors) == 1)


# ── sessions.toml ────────────────────────────────────────────────────


def test_valid_sessions():
    """Valid sessions.toml produces no errors."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "sessions.toml", """\
[[sessions]]
title = "myproj"
directory = "~"
account = "work"
""")
        v = cv.validate_sessions(path, {"work"})
        check("valid sessions: no errors", len(v.errors) == 0)


def test_sessions_missing_title():
    """Session without title produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "sessions.toml", """\
[[sessions]]
directory = "~"
""")
        v = cv.validate_sessions(path, set())
        check("missing title: error", len(v.errors) >= 1)
        check("missing title: mentions 'title'",
              any("title" in e for e in v.errors))


def test_sessions_missing_directory():
    """Session without directory produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "sessions.toml", """\
[[sessions]]
title = "test"
""")
        v = cv.validate_sessions(path, set())
        check("missing directory: error",
              any("directory" in e for e in v.errors))


def test_sessions_unknown_account():
    """Session referencing unknown account produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "sessions.toml", """\
[[sessions]]
title = "test"
directory = "~"
account = "ghost"
""")
        v = cv.validate_sessions(path, {"work"})
        check("unknown account: error",
              any("ghost" in e for e in v.errors))


def test_sessions_nonexistent_dir():
    """Non-existent directory produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "sessions.toml", """\
[[sessions]]
title = "test"
directory = "/no/such/dir/xyz"
""")
        v = cv.validate_sessions(path, set())
        check("nonexistent dir: warning", len(v.warnings) >= 1)


def test_sessions_unknown_key():
    """Unknown key in session produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "sessions.toml", """\
[[sessions]]
title = "test"
directory = "~"
bogus = "oops"
""")
        v = cv.validate_sessions(path, set())
        check("unknown session key: warning",
              any("bogus" in w for w in v.warnings))


def test_sessions_invalid_project_window():
    """Invalid project_window produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "sessions.toml", """\
[[sessions]]
title = "test"
directory = "~"
project_window = "invalid"
""")
        v = cv.validate_sessions(path, set())
        check("invalid project_window: error",
              any("project_window" in e for e in v.errors))


# ── defaults.toml ────────────────────────────────────────────────────


def test_valid_defaults():
    """Valid defaults.toml produces no errors or warnings."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", """\
[defaults]
sort_sessions = "alpha"
dashboard_refresh = 5

[notifications]
error = true
idle_alert_minutes = 10
""")
        v = cv.validate_defaults(path)
        check("valid defaults: no errors", len(v.errors) == 0)
        check("valid defaults: no warnings", len(v.warnings) == 0)


def test_defaults_unknown_key():
    """Unknown key in [defaults] produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", """\
[defaults]
mystery = "value"
""")
        v = cv.validate_defaults(path)
        check("unknown defaults key: warning",
              any("mystery" in w for w in v.warnings))


def test_defaults_invalid_sort():
    """Invalid sort_sessions produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", """\
[defaults]
sort_sessions = "random"
""")
        v = cv.validate_defaults(path)
        check("invalid sort: error",
              any("sort_sessions" in e for e in v.errors))


def test_defaults_negative_refresh():
    """Negative dashboard_refresh produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", """\
[defaults]
dashboard_refresh = -1
""")
        v = cv.validate_defaults(path)
        check("negative refresh: error", len(v.errors) >= 1)


def test_defaults_notification_types():
    """Non-boolean notification key produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", """\
[notifications]
error = "yes"
""")
        v = cv.validate_defaults(path)
        check("non-bool notification: error",
              any("boolean" in e for e in v.errors))


def test_defaults_unknown_notification_key():
    """Unknown key in [notifications] produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", """\
[notifications]
bogus = true
""")
        v = cv.validate_defaults(path)
        check("unknown notification key: warning",
              any("bogus" in w for w in v.warnings))


def test_defaults_unknown_section():
    """Unknown top-level section produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", """\
[defaults]
[alien_section]
foo = "bar"
""")
        v = cv.validate_defaults(path)
        check("unknown section: warning",
              any("alien_section" in w for w in v.warnings))


def test_defaults_missing_file():
    """Missing defaults file produces a warning."""
    v = cv.validate_defaults("/no/such/defaults.toml")
    check("missing defaults: warning", len(v.warnings) == 1)
    check("missing defaults: no errors", len(v.errors) == 0)


def test_defaults_empty_file():
    """Empty defaults.toml is valid (no required keys)."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", "")
        v = cv.validate_defaults(path)
        check("empty defaults: no errors", len(v.errors) == 0)


def test_defaults_kitty_timeout():
    """kitty_timeout validation."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", """\
[defaults]
kitty_timeout = 0
""")
        v = cv.validate_defaults(path)
        check("kitty_timeout=0: error", len(v.errors) >= 1)

    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", """\
[defaults]
kitty_timeout = 10
""")
        v = cv.validate_defaults(path)
        check("kitty_timeout=10: no errors", len(v.errors) == 0)

    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", """\
[defaults]
kitty_timeout = 500
""")
        v = cv.validate_defaults(path)
        check("kitty_timeout=500: error (exceeds 300)", len(v.errors) >= 1)
        check("kitty_timeout=500: mentions 300",
              any("300" in e for e in v.errors))


def test_defaults_idle_alert_minutes():
    """idle_alert_minutes validation."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", """\
[notifications]
idle_alert_minutes = 10
""")
        v = cv.validate_defaults(path)
        check("idle_alert_minutes=10: no errors", len(v.errors) == 0)

    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", """\
[notifications]
idle_alert_minutes = -5
""")
        v = cv.validate_defaults(path)
        check("idle_alert_minutes=-5: error", len(v.errors) >= 1)

    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", """\
[notifications]
idle_alert_minutes = "ten"
""")
        v = cv.validate_defaults(path)
        check("idle_alert_minutes=string: error", len(v.errors) >= 1)


def test_defaults_min_secs():
    """min_secs validation."""
    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", """\
[notifications]
min_secs = 0
""")
        v = cv.validate_defaults(path)
        check("min_secs=0: no errors", len(v.errors) == 0)

    with tempfile.TemporaryDirectory() as d:
        path = _write(d, "defaults.toml", """\
[notifications]
min_secs = -1
""")
        v = cv.validate_defaults(path)
        check("min_secs=-1: error", len(v.errors) >= 1)


# ── run all ──────────────────────────────────────────────────────────


if __name__ == "__main__":
    print("config_validator tests\n")

    test_valid_accounts()
    test_accounts_missing_name()
    test_accounts_invalid_color()
    test_accounts_unknown_key()
    test_accounts_nonexistent_config_dir()
    test_accounts_duplicate()
    test_accounts_missing_file()
    test_accounts_bad_toml()

    test_valid_sessions()
    test_sessions_missing_title()
    test_sessions_missing_directory()
    test_sessions_unknown_account()
    test_sessions_nonexistent_dir()
    test_sessions_unknown_key()
    test_sessions_invalid_project_window()

    test_valid_defaults()
    test_defaults_unknown_key()
    test_defaults_invalid_sort()
    test_defaults_negative_refresh()
    test_defaults_notification_types()
    test_defaults_unknown_notification_key()
    test_defaults_unknown_section()
    test_defaults_missing_file()
    test_defaults_empty_file()
    test_defaults_kitty_timeout()
    test_defaults_idle_alert_minutes()
    test_defaults_min_secs()

    print(f"\n{PASS} passed, {FAIL} failed")
    if FAIL:
        sys.exit(1)
