"""Defaults config validator tests."""

from __future__ import annotations

import tempfile

from .shared import check, cv, write_file


def test_valid_defaults() -> None:
    """Valid defaults.toml produces no errors or warnings."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[defaults]
sort_sessions = "alpha"
dashboard_refresh = 5

[notifications]
error = true
idle_alert_minutes = 10
dim_after_minutes = 10
idle_poll_interval = 30
""",
        )
        v = cv.validate_defaults(path)
        check("valid defaults: no errors", len(v.errors) == 0)
        check("valid defaults: no warnings", len(v.warnings) == 0)


def test_defaults_unknown_key() -> None:
    """Unknown key in [defaults] produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[defaults]
mystery = "value"
""",
        )
        v = cv.validate_defaults(path)
        check("unknown defaults key: warning", any("mystery" in w for w in v.warnings))


def test_defaults_invalid_sort() -> None:
    """Invalid sort_sessions produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[defaults]
sort_sessions = "random"
""",
        )
        v = cv.validate_defaults(path)
        check("invalid sort: error", any("sort_sessions" in e for e in v.errors))


def test_defaults_negative_refresh() -> None:
    """Negative dashboard_refresh produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[defaults]
dashboard_refresh = -1
""",
        )
        v = cv.validate_defaults(path)
        check("negative refresh: error", len(v.errors) >= 1)

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[defaults]
dashboard_refresh = 0
""",
        )
        v = cv.validate_defaults(path)
        check("zero refresh: error", len(v.errors) >= 1)


def test_defaults_notification_types() -> None:
    """Non-boolean notification key produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
error = "yes"
""",
        )
        v = cv.validate_defaults(path)
        check("non-bool notification: error", any("boolean" in e for e in v.errors))


def test_defaults_unknown_notification_key() -> None:
    """Unknown key in [notifications] produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
bogus = true
""",
        )
        v = cv.validate_defaults(path)
        check(
            "unknown notification key: warning",
            any("bogus" in w for w in v.warnings),
        )


def test_defaults_unknown_section() -> None:
    """Unknown top-level section produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[defaults]
[alien_section]
foo = "bar"
""",
        )
        v = cv.validate_defaults(path)
        check("unknown section: warning", any("alien_section" in w for w in v.warnings))


def test_defaults_missing_file() -> None:
    """Missing defaults file produces a warning."""
    v = cv.validate_defaults("/no/such/defaults.toml")
    check("missing defaults: warning", len(v.warnings) == 1)
    check("missing defaults: no errors", len(v.errors) == 0)


def test_defaults_empty_file() -> None:
    """Empty defaults.toml is valid (no required keys)."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(d, "defaults.toml", "")
        v = cv.validate_defaults(path)
        check("empty defaults: no errors", len(v.errors) == 0)


def test_defaults_kitty_timeout() -> None:
    """kitty_timeout validation."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[defaults]
kitty_timeout = 0
""",
        )
        v = cv.validate_defaults(path)
        check("kitty_timeout=0: error", len(v.errors) >= 1)

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[defaults]
kitty_timeout = 10
""",
        )
        v = cv.validate_defaults(path)
        check("kitty_timeout=10: no errors", len(v.errors) == 0)

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[defaults]
kitty_timeout = 500
""",
        )
        v = cv.validate_defaults(path)
        check("kitty_timeout=500: error (exceeds 300)", len(v.errors) >= 1)
        check("kitty_timeout=500: mentions 300", any("300" in e for e in v.errors))


def test_defaults_idle_alert_minutes() -> None:
    """idle_alert_minutes validation."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
idle_alert_minutes = 10
""",
        )
        v = cv.validate_defaults(path)
        check("idle_alert_minutes=10: no errors", len(v.errors) == 0)

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
idle_alert_minutes = 0
""",
        )
        v = cv.validate_defaults(path)
        check("idle_alert_minutes=0: no errors", len(v.errors) == 0)

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
idle_alert_minutes = -5
""",
        )
        v = cv.validate_defaults(path)
        check("idle_alert_minutes=-5: error", len(v.errors) >= 1)

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
idle_alert_minutes = "ten"
""",
        )
        v = cv.validate_defaults(path)
        check("idle_alert_minutes=string: error", len(v.errors) >= 1)


def test_defaults_min_secs() -> None:
    """min_secs validation."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
min_secs = 0
""",
        )
        v = cv.validate_defaults(path)
        check("min_secs=0: no errors", len(v.errors) == 0)

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
min_secs = -1
""",
        )
        v = cv.validate_defaults(path)
        check("min_secs=-1: error", len(v.errors) >= 1)


def test_defaults_ignore_patterns_valid_regex() -> None:
    """Valid regex patterns in ignore_patterns produce no errors."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
ignore_patterns = ["Conversation cleared", "Context compacted.*", "^foo\\\\d+$"]
""",
        )
        v = cv.validate_defaults(path)
        check("valid regex patterns: no errors", len(v.errors) == 0)


def test_defaults_ignore_patterns_invalid_regex() -> None:
    """Invalid regex in ignore_patterns produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
ignore_patterns = ["valid", "[unclosed"]
""",
        )
        v = cv.validate_defaults(path)
        check("invalid regex: error", len(v.errors) >= 1)
        check(
            "invalid regex: mentions pattern",
            any("[unclosed" in e for e in v.errors),
        )


def test_defaults_dim_after_minutes() -> None:
    """dim_after_minutes validation (in [notifications])."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
dim_after_minutes = 10
""",
        )
        v = cv.validate_defaults(path)
        check("dim_after_minutes=10: no errors", len(v.errors) == 0)

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
dim_after_minutes = 0
""",
        )
        v = cv.validate_defaults(path)
        check("dim_after_minutes=0: no errors", len(v.errors) == 0)

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
dim_after_minutes = -5
""",
        )
        v = cv.validate_defaults(path)
        check("dim_after_minutes=-5: error", len(v.errors) >= 1)

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
dim_after_minutes = "ten"
""",
        )
        v = cv.validate_defaults(path)
        check("dim_after_minutes=string: error", len(v.errors) >= 1)


def test_defaults_idle_poll_interval() -> None:
    """idle_poll_interval validation (in [notifications])."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
idle_poll_interval = 30
""",
        )
        v = cv.validate_defaults(path)
        check("idle_poll_interval=30: no errors", len(v.errors) == 0)

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
idle_poll_interval = 3
""",
        )
        v = cv.validate_defaults(path)
        check("idle_poll_interval=3: error (below 5)", len(v.errors) >= 1)

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[notifications]
idle_poll_interval = 5000
""",
        )
        v = cv.validate_defaults(path)
        check("idle_poll_interval=5000: error (above 3600)", len(v.errors) >= 1)

    # idle_poll_interval under [defaults] should warn as unknown key
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "defaults.toml",
            """\
[defaults]
idle_poll_interval = 30
""",
        )
        v = cv.validate_defaults(path)
        check(
            "idle_poll_interval under [defaults]: unknown key warning",
            any("idle_poll_interval" in w for w in v.warnings),
        )


def run() -> None:
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
    test_defaults_ignore_patterns_valid_regex()
    test_defaults_ignore_patterns_invalid_regex()
    test_defaults_dim_after_minutes()
    test_defaults_idle_poll_interval()
