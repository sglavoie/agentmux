"""Accounts config validator tests."""

from __future__ import annotations

import tempfile

from .shared import check, cv, write_file


def test_valid_accounts() -> None:
    """Valid accounts.toml produces no errors or warnings."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "accounts.toml",
            """\
[[accounts]]
name = "work"
config_dir = "~"
color = "blue"
""",
        )
        v, names = cv.validate_accounts(path, {"blue", "green", "red"})
        check("valid accounts: no errors", len(v.errors) == 0)
        check("valid accounts: no warnings", len(v.warnings) == 0)
        check("valid accounts: name captured", "work" in names)


def test_accounts_missing_name() -> None:
    """Account without a name produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "accounts.toml",
            """\
[[accounts]]
config_dir = "~"
color = "blue"
""",
        )
        v, _ = cv.validate_accounts(path, {"blue"})
        check("missing name: error", len(v.errors) == 1)
        check("missing name: mentions 'name'", "name" in v.errors[0])


def test_accounts_invalid_color() -> None:
    """Unknown color produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "accounts.toml",
            """\
[[accounts]]
name = "test"
color = "neon"
""",
        )
        v, _ = cv.validate_accounts(path, {"blue", "green"})
        check("invalid color: error", len(v.errors) == 1)
        check("invalid color: mentions 'neon'", "neon" in v.errors[0])


def test_accounts_unknown_key() -> None:
    """Unknown key in account produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "accounts.toml",
            """\
[[accounts]]
name = "test"
color = "blue"
bogus_key = "oops"
""",
        )
        v, _ = cv.validate_accounts(path, {"blue"})
        check("unknown key: warning", len(v.warnings) == 1)
        check("unknown key: mentions 'bogus_key'", "bogus_key" in v.warnings[0])


def test_accounts_nonexistent_config_dir() -> None:
    """Non-existent config_dir produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "accounts.toml",
            """\
[[accounts]]
name = "test"
config_dir = "/no/such/path/xyz"
""",
        )
        v, _ = cv.validate_accounts(path, None)
        check("nonexistent config_dir: warning", len(v.warnings) == 1)
        check(
            "nonexistent config_dir: mentions path",
            "/no/such/path/xyz" in v.warnings[0],
        )


def test_accounts_duplicate() -> None:
    """Duplicate account name produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "accounts.toml",
            """\
[[accounts]]
name = "work"
[[accounts]]
name = "work"
""",
        )
        v, _ = cv.validate_accounts(path, None)
        check("duplicate name: error", any("duplicate" in e for e in v.errors))


def test_accounts_missing_file() -> None:
    """Missing file produces a warning, not a crash."""
    v, names = cv.validate_accounts("/no/such/file.toml", None)
    check("missing file: no errors", len(v.errors) == 0)
    check("missing file: warning", len(v.warnings) == 1)
    check("missing file: no names", len(names) == 0)


def test_accounts_bad_toml() -> None:
    """Invalid TOML produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(d, "accounts.toml", "this is [[[bad toml")
        v, _ = cv.validate_accounts(path, None)
        check("bad toml: error", len(v.errors) == 1)


def run() -> None:
    test_valid_accounts()
    test_accounts_missing_name()
    test_accounts_invalid_color()
    test_accounts_unknown_key()
    test_accounts_nonexistent_config_dir()
    test_accounts_duplicate()
    test_accounts_missing_file()
    test_accounts_bad_toml()
