"""Sessions config validator tests."""

from __future__ import annotations

import tempfile

from .shared import check, cv, write_file


def test_valid_sessions() -> None:
    """Valid sessions.toml produces no errors."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "myproj"
directory = "~"
account = "work"
""",
        )
        v = cv.validate_sessions(path, {"work"})
        check("valid sessions: no errors", len(v.errors) == 0)


def test_sessions_missing_title() -> None:
    """Session without title produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
directory = "~"
""",
        )
        v = cv.validate_sessions(path, set())
        check("missing title: error", len(v.errors) >= 1)
        check("missing title: mentions 'title'", any("title" in e for e in v.errors))


def test_sessions_missing_directory() -> None:
    """Session without directory produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "test"
""",
        )
        v = cv.validate_sessions(path, set())
        check("missing directory: error", any("directory" in e for e in v.errors))


def test_sessions_unknown_account() -> None:
    """Session referencing unknown account produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "test"
directory = "~"
account = "ghost"
""",
        )
        v = cv.validate_sessions(path, {"work"})
        check("unknown account: error", any("ghost" in e for e in v.errors))


def test_sessions_nonexistent_dir() -> None:
    """Non-existent directory produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "test"
directory = "/no/such/dir/xyz"
""",
        )
        v = cv.validate_sessions(path, set())
        check("nonexistent dir: warning", len(v.warnings) >= 1)


def test_sessions_unknown_key() -> None:
    """Unknown key in session produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "test"
directory = "~"
bogus = "oops"
""",
        )
        v = cv.validate_sessions(path, set())
        check("unknown session key: warning", any("bogus" in w for w in v.warnings))


def test_sessions_invalid_project_window() -> None:
    """Invalid project_window produces an error."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "test"
directory = "~"
project_window = "invalid"
""",
        )
        v = cv.validate_sessions(path, set())
        check(
            "invalid project_window: error",
            any("project_window" in e for e in v.errors),
        )


def test_sessions_env_with_spaces() -> None:
    """Env value that splits into non-KEY=VALUE tokens produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "test"
directory = "~"
env = "FOO=hello world BAR=ok"
""",
        )
        v = cv.validate_sessions(path, set())
        check(
            "env with spaces: warning",
            any("world" in w and "KEY=VALUE" in w for w in v.warnings),
        )

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "test"
directory = "~"
env = "FOO=hello BAR=ok"
""",
        )
        v = cv.validate_sessions(path, set())
        check(
            "valid env: no warnings about env",
            not any("KEY=VALUE" in w for w in v.warnings),
        )

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "test"
directory = "~"
env = "-"
""",
        )
        v = cv.validate_sessions(path, set())
        check(
            "env disabled: no warnings about env",
            not any("KEY=VALUE" in w for w in v.warnings),
        )

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "test"
directory = "~"
env = "KEY= MULTI=a,b,c"
""",
        )
        v = cv.validate_sessions(path, set())
        check(
            "empty value env: no warnings about env",
            not any("KEY=VALUE" in w for w in v.warnings),
        )


def test_sessions_commands_valid() -> None:
    """Valid commands array produces no errors or warnings."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "test"
directory = "~"
commands = ["npm run dev", "shell"]
""",
        )
        v = cv.validate_sessions(path, set())
        check("valid commands: no errors", len(v.errors) == 0)
        check(
            "valid commands: no unknown-key warning",
            not any("commands" in w for w in v.warnings),
        )


def test_sessions_commands_not_list() -> None:
    """Non-list commands value produces a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "test"
directory = "~"
commands = "npm run dev"
""",
        )
        v = cv.validate_sessions(path, set())
        check("non-list commands: warning", len(v.warnings) >= 1)
        check(
            "non-list commands: mentions 'commands'",
            any("commands" in w for w in v.warnings),
        )


def test_sessions_commands_mixed_types() -> None:
    """Commands list with non-string entries produces a warning per bad entry."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "test"
directory = "~"
commands = ["shell", 123]
""",
        )
        v = cv.validate_sessions(path, set())
        check("mixed-type commands: warning", len(v.warnings) >= 1)
        check(
            "mixed-type commands: mentions 'commands'",
            any("commands" in w for w in v.warnings),
        )


def test_sessions_commands_with_pw_disabled() -> None:
    """commands + project_window='-' produces a contradictory-config warning."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "test"
directory = "~"
project_window = "-"
commands = ["npm run dev"]
""",
        )
        v = cv.validate_sessions(path, set())
        check("commands + pw='-': warning", len(v.warnings) >= 1)
        check(
            "commands + pw='-': mentions 'commands' or 'project_window'",
            any("commands" in w or "project_window" in w for w in v.warnings),
        )


def test_sessions_commands_transport_unsafe() -> None:
    """Commands with tabs/newlines produce a warning."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "test"
directory = "~"
commands = ["safe", "bad\\tcmd", "bad\\ncmd"]
""",
        )
        v = cv.validate_sessions(path, set())
        check("transport-unsafe commands: warning", len(v.warnings) >= 1)
        check(
            "transport-unsafe commands: mentions tab or newline",
            any("tab or newline" in w for w in v.warnings),
        )


def test_sessions_single_use_group() -> None:
    """Group name used once gets a typo warning when multiple groups exist."""
    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "a"
directory = "~"
group = "backend"

[[sessions]]
title = "b"
directory = "~"
group = "backend"

[[sessions]]
title = "c"
directory = "~"
group = "bckend"
""",
        )
        v = cv.validate_sessions(path, set())
        check(
            "single-use group: warning about 'bckend'",
            any("bckend" in w and "typo" in w for w in v.warnings),
        )
        check(
            "single-use group: no warning about 'backend'",
            not any("backend" in w and "typo" in w for w in v.warnings),
        )

    with tempfile.TemporaryDirectory() as d:
        path = write_file(
            d,
            "sessions.toml",
            """\
[[sessions]]
title = "a"
directory = "~"
group = "solo"
""",
        )
        v = cv.validate_sessions(path, set())
        check(
            "single group total: no typo warning",
            not any("typo" in w for w in v.warnings),
        )


def run() -> None:
    test_valid_sessions()
    test_sessions_missing_title()
    test_sessions_missing_directory()
    test_sessions_unknown_account()
    test_sessions_nonexistent_dir()
    test_sessions_unknown_key()
    test_sessions_invalid_project_window()
    test_sessions_env_with_spaces()
    test_sessions_commands_valid()
    test_sessions_commands_not_list()
    test_sessions_commands_mixed_types()
    test_sessions_commands_with_pw_disabled()
    test_sessions_commands_transport_unsafe()
    test_sessions_single_use_group()
