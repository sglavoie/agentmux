#!/usr/bin/env python3
"""Smoke tests for CLI tools and config infrastructure.

These tests verify basic functionality without requiring Kitty or
a running terminal. They test config validation, data loading, and
TOML infrastructure.

Run: python3 tests/test_smoke.py
"""

import importlib
import json
import os
import subprocess
import sys
import tempfile
import tomllib

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

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


# ── config_validator catches known-bad configs ──────────────────────


def test_validator_rejects_bad_accounts():
    """config_validator catches missing name in accounts."""
    print("\n--- validator: bad accounts ---")
    sys.path.insert(0, os.path.join(REPO_ROOT, "lib"))
    import config_validator as cv

    with tempfile.NamedTemporaryFile(
        mode="w", suffix=".toml", delete=False
    ) as f:
        f.write('[[accounts]]\nconfig_dir = "~/.claude"\n')
        f.flush()
        v, names = cv.validate_accounts(f.name, None)
    os.unlink(f.name)
    check("missing name -> error", len(v.errors) > 0)
    check("error mentions 'name'",
          any("name" in e for e in v.errors))


def test_validator_rejects_bad_sessions():
    """config_validator catches missing title in sessions."""
    print("\n--- validator: bad sessions ---")
    sys.path.insert(0, os.path.join(REPO_ROOT, "lib"))
    import config_validator as cv

    with tempfile.NamedTemporaryFile(
        mode="w", suffix=".toml", delete=False
    ) as f:
        f.write('[[sessions]]\ndirectory = "~/code"\n')
        f.flush()
        v = cv.validate_sessions(f.name, set())
    os.unlink(f.name)
    check("missing title -> error", len(v.errors) > 0)
    check("error mentions 'title'",
          any("title" in e for e in v.errors))


def test_validator_rejects_bad_defaults():
    """config_validator catches invalid sort_sessions value."""
    print("\n--- validator: bad defaults ---")
    sys.path.insert(0, os.path.join(REPO_ROOT, "lib"))
    import config_validator as cv

    with tempfile.NamedTemporaryFile(
        mode="w", suffix=".toml", delete=False
    ) as f:
        f.write('[defaults]\nsort_sessions = "bogus"\n')
        f.flush()
        v = cv.validate_defaults(f.name)
    os.unlink(f.name)
    check("invalid sort -> error", len(v.errors) > 0)
    check("error mentions sort_sessions",
          any("sort_sessions" in e for e in v.errors))


def test_validator_rejects_bad_hooks():
    """config_validator catches invalid hooks.json."""
    print("\n--- validator: bad hooks ---")
    sys.path.insert(0, os.path.join(REPO_ROOT, "lib"))
    import config_validator as cv

    with tempfile.NamedTemporaryFile(
        mode="w", suffix=".json", delete=False
    ) as f:
        json.dump({"not_hooks": {}}, f)
        f.flush()
        v = cv.validate_hooks(f.name, set(), set())
    os.unlink(f.name)
    check("missing hooks key -> error", len(v.errors) > 0)


def test_validator_accepts_good_config():
    """config_validator passes valid minimal config."""
    print("\n--- validator: good config ---")
    sys.path.insert(0, os.path.join(REPO_ROOT, "lib"))
    import config_validator as cv

    with tempfile.NamedTemporaryFile(
        mode="w", suffix=".toml", delete=False
    ) as f:
        f.write(
            '[[accounts]]\nname = "test"\n'
            'config_dir = "/tmp"\ncolor = "green"\n'
        )
        f.flush()
        palette = frozenset(["green", "blue", "red", "orange", "purple",
                             "teal", "pink", "yellow", "gray", "cyan"])
        v, names = cv.validate_accounts(f.name, palette)
    os.unlink(f.name)
    check("valid accounts -> no errors", len(v.errors) == 0)
    check("account name captured", "test" in names)


# ── data_loader module ──────────────────────────────────────────────


def test_data_loader_palette():
    """data_loader.load_palette_colors returns valid colors."""
    print("\n--- data_loader: palette ---")
    sys.path.insert(0, os.path.join(REPO_ROOT, "lib"))
    from data_loader import load_palette_colors

    colors = load_palette_colors(os.path.join(REPO_ROOT, "data"))
    check("returns frozenset", isinstance(colors, frozenset))
    check("has 10 colors", colors is not None and len(colors) == 10)
    check("green in palette", colors is not None and "green" in colors)


def test_data_loader_hook_events():
    """data_loader.load_hook_events returns event names."""
    print("\n--- data_loader: hook events ---")
    sys.path.insert(0, os.path.join(REPO_ROOT, "lib"))
    from data_loader import load_hook_events

    events = load_hook_events(os.path.join(REPO_ROOT, "data"))
    check("returns set", isinstance(events, set))
    check("has events", len(events) > 0)
    check("Stop in events", "Stop" in events)


def test_data_loader_sort_modes():
    """data_loader.load_sort_modes returns mode names."""
    print("\n--- data_loader: sort modes ---")
    sys.path.insert(0, os.path.join(REPO_ROOT, "lib"))
    from data_loader import load_sort_modes

    modes = load_sort_modes(os.path.join(REPO_ROOT, "data"))
    check("returns set", isinstance(modes, set))
    check("has modes", len(modes) > 0)
    check("default in modes", "default" in modes)


# ── TOML infrastructure ─────────────────────────────────────────────


def test_toml_writer_import():
    """toml_writer module can be imported."""
    print("\n--- toml_writer import ---")
    sys.path.insert(0, os.path.join(REPO_ROOT, "lib"))
    import toml_writer

    check("toml_writer has dumps", hasattr(toml_writer, "dumps"))


def test_toml_reader_import():
    """toml_reader module can be imported."""
    print("\n--- toml_reader import ---")
    sys.path.insert(0, os.path.join(REPO_ROOT, "lib"))
    import toml_reader

    check("toml_reader has read_accounts",
          hasattr(toml_reader, "read_accounts"))
    check("toml_reader has read_sessions",
          hasattr(toml_reader, "read_sessions"))


def test_toml_roundtrip_basic():
    """Basic TOML write-then-read roundtrip."""
    print("\n--- TOML roundtrip ---")
    sys.path.insert(0, os.path.join(REPO_ROOT, "lib"))
    import toml_writer

    data = {
        "accounts": [
            {"name": "work", "config_dir": "~/.claude", "color": "green"},
        ]
    }
    output = toml_writer.dumps(data)
    parsed = tomllib.loads(output)
    check("roundtrip preserves structure",
          parsed["accounts"][0]["name"] == "work")
    check("roundtrip preserves color",
          parsed["accounts"][0]["color"] == "green")


# ── quick_validate programmatic API ─────────────────────────────────


def test_quick_validate():
    """quick_validate returns (level, message) tuples."""
    print("\n--- quick_validate ---")
    sys.path.insert(0, os.path.join(REPO_ROOT, "lib"))
    import config_validator as cv

    with tempfile.TemporaryDirectory() as tmpdir:
        # Create minimal valid config
        accounts_path = os.path.join(tmpdir, "accounts.toml")
        with open(accounts_path, "w") as f:
            f.write(
                '[[accounts]]\nname = "test"\n'
                'config_dir = "/tmp"\ncolor = "green"\n'
            )
        sessions_path = os.path.join(tmpdir, "sessions.toml")
        with open(sessions_path, "w") as f:
            f.write(
                '[[sessions]]\ntitle = "test"\n'
                'directory = "/tmp"\naccount = "test"\n'
            )
        defaults_path = os.path.join(tmpdir, "defaults.toml")
        with open(defaults_path, "w") as f:
            f.write('[defaults]\nsort_sessions = "default"\n')
        hooks_path = os.path.join(tmpdir, "hooks.json")
        with open(hooks_path, "w") as f:
            json.dump({"hooks": {}, "account_overrides": {}}, f)

        data_dir = os.path.join(REPO_ROOT, "data")
        issues = cv.quick_validate(tmpdir, data_dir)
        check("returns list", isinstance(issues, list))
        check("all tuples of (level, msg)",
              all(isinstance(i, tuple) and len(i) == 2 for i in issues))
        error_count = sum(1 for level, _ in issues if level == "error")
        check("no errors for valid config", error_count == 0)


# ── run all ──────────────────────────────────────────────────────────


if __name__ == "__main__":
    print("smoke tests")

    test_validator_rejects_bad_accounts()
    test_validator_rejects_bad_sessions()
    test_validator_rejects_bad_defaults()
    test_validator_rejects_bad_hooks()
    test_validator_accepts_good_config()
    test_data_loader_palette()
    test_data_loader_hook_events()
    test_data_loader_sort_modes()
    test_toml_writer_import()
    test_toml_reader_import()
    test_toml_roundtrip_basic()
    test_quick_validate()

    print(f"\n{PASS} passed, {FAIL} failed")
    if FAIL:
        sys.exit(1)
