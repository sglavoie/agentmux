#!/usr/bin/env python3
"""Tests for the toml-helper.py CLI bridge.

Verifies that the subprocess-based CLI produces correct TSV output for
read commands and correct TOML files for write commands.  This is the
critical path between Bash scripts and TOML config files.

Run: python3 tests/test_toml_helper.py
"""

import json
import os
import subprocess
import sys
import tempfile
import tomllib

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
HELPER = os.path.join(REPO_ROOT, "lib", "toml-helper.py")

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


def run_helper(*args, stdin_data=None):
    """Run toml-helper.py and return (stdout, stderr, returncode)."""
    result = subprocess.run(
        [sys.executable, HELPER, *args],
        input=stdin_data,
        capture_output=True,
        text=True,
        cwd=REPO_ROOT,
    )
    return result.stdout, result.stderr, result.returncode


def tsv_lines(stdout):
    """Split TSV output into lines, preserving trailing empty fields.

    Using str.strip() would eat trailing tab characters that represent
    empty TSV columns, so we only strip the final newline.
    """
    return stdout.rstrip("\n").split("\n") if stdout.rstrip("\n") else []


# ── read-accounts ────────────────────────────────────────────────


def test_read_accounts():
    """read-accounts outputs correct TSV columns."""
    print("\n--- read-accounts ---")
    with tempfile.NamedTemporaryFile(
        mode="w", suffix=".toml", delete=False
    ) as f:
        f.write(
            '[[accounts]]\nname = "work"\n'
            'config_dir = "~/.claude-work"\ncolor = "blue"\n\n'
            '[[accounts]]\nname = "personal"\n'
            'config_dir = "~/.claude-personal"\ncolor = "green"\n'
        )
        f.flush()
        path = f.name

    stdout, stderr, rc = run_helper("read-accounts", path)
    os.unlink(path)

    lines = tsv_lines(stdout)
    check("exit code 0", rc == 0)
    check("two lines of output", len(lines) == 2)

    cols1 = lines[0].split("\t")
    check("first account: 3 columns", len(cols1) == 3)
    check("first account: name=work", cols1[0] == "work")
    check("first account: config_dir", cols1[1] == "~/.claude-work")
    check("first account: color=blue", cols1[2] == "blue")

    cols2 = lines[1].split("\t")
    check("second account: name=personal", cols2[0] == "personal")


# ── read-sessions ────────────────────────────────────────────────


def test_read_sessions():
    """read-sessions outputs correct 7-column TSV."""
    print("\n--- read-sessions ---")
    with tempfile.NamedTemporaryFile(
        mode="w", suffix=".toml", delete=False
    ) as f:
        f.write(
            '[[sessions]]\ntitle = "myproject"\n'
            'directory = "~/code/myproject"\naccount = "work"\n'
            'flags = "--verbose"\nproject_window = "vsplit"\n'
            'env = "FOO=bar"\ngroup = "dev"\n\n'
            '[[sessions]]\ntitle = "notes"\n'
            'directory = "~/notes"\naccount = "personal"\n'
        )
        f.flush()
        path = f.name

    stdout, stderr, rc = run_helper("read-sessions", path)
    os.unlink(path)

    lines = tsv_lines(stdout)
    check("exit code 0", rc == 0)
    check("two lines of output", len(lines) == 2)

    cols1 = lines[0].split("\t")
    check("full session: 7 columns", len(cols1) == 7)
    check("title=myproject", cols1[0] == "myproject")
    check("dir=~/code/myproject", cols1[1] == "~/code/myproject")
    check("account=work", cols1[2] == "work")
    check("flags=--verbose", cols1[3] == "--verbose")
    check("pw=vsplit", cols1[4] == "vsplit")
    check("env=FOO=bar", cols1[5] == "FOO=bar")
    check("group=dev", cols1[6] == "dev")

    # Minimal session: optional fields are empty strings
    cols2 = lines[1].split("\t")
    check("minimal session: 7 columns", len(cols2) == 7)
    check("minimal: title=notes", cols2[0] == "notes")
    check("minimal: empty flags", cols2[3] == "")
    check("minimal: empty pw", cols2[4] == "")
    check("minimal: empty group", cols2[6] == "")


# ── read-defaults ────────────────────────────────────────────────


def test_read_defaults():
    """read-defaults outputs TSV for defaults and notifications."""
    print("\n--- read-defaults ---")
    with tempfile.NamedTemporaryFile(
        mode="w", suffix=".toml", delete=False
    ) as f:
        f.write(
            '[defaults]\nclaude_flags = "--verbose"\n'
            'sort_sessions = "alpha"\ndashboard_refresh = 3\n\n'
            '[notifications]\nerror = true\nstop = false\n'
            'idle_alert_minutes = 30\n'
            'ignore_patterns = ["test", "debug"]\n'
        )
        f.flush()
        path = f.name

    stdout, stderr, rc = run_helper("read-defaults", path)
    os.unlink(path)

    check("exit code 0", rc == 0)

    # Parse TSV output into a dict
    kv = {}
    for line in tsv_lines(stdout):
        parts = line.split("\t", 1)
        if len(parts) == 2:
            kv[parts[0]] = parts[1]

    check("claude_flags present", "claude_flags" in kv)
    check("claude_flags value", kv.get("claude_flags") == "--verbose")
    check("sort_sessions=alpha", kv.get("sort_sessions") == "alpha")
    check("dashboard_refresh=3", kv.get("dashboard_refresh") == "3")
    # Boolean true → "on", false → ""
    check("notify_error=on", kv.get("notify_error") == "on")
    check("notify_stop=empty", kv.get("notify_stop") == "")
    check("idle_alert_minutes=30", kv.get("idle_alert_minutes") == "30")
    # List → pipe-separated
    check("ignore_patterns pipe-separated",
          kv.get("notify_ignore_patterns") == "test|debug")


# ── write-sessions ───────────────────────────────────────────────


def test_write_sessions():
    """write-sessions creates valid TOML from JSON stdin."""
    print("\n--- write-sessions ---")
    with tempfile.NamedTemporaryFile(
        mode="w", suffix=".toml", delete=False
    ) as f:
        f.write("# My sessions config\n")
        f.flush()
        path = f.name

    sessions_json = json.dumps([
        {
            "title": "myproject",
            "dir": "~/code/myproject",
            "account": "work",
            "flags": "--verbose",
            "pw": "vsplit",
            "env": "FOO=bar",
            "group": "dev",
        },
        {
            "title": "notes",
            "dir": "~/notes",
            "account": "",
            "flags": "",
            "pw": "",
            "env": "",
            "group": "",
        },
    ])

    stdout, stderr, rc = run_helper(
        "write-sessions", path, stdin_data=sessions_json
    )

    check("exit code 0", rc == 0)

    # Verify the file is valid TOML
    with open(path, "rb") as f:
        data = tomllib.load(f)

    check("has sessions key", "sessions" in data)
    check("two sessions written", len(data["sessions"]) == 2)

    s1 = data["sessions"][0]
    check("title preserved", s1["title"] == "myproject")
    check("directory preserved", s1["directory"] == "~/code/myproject")
    check("account preserved", s1["account"] == "work")
    check("flags preserved", s1["flags"] == "--verbose")
    check("project_window preserved", s1["project_window"] == "vsplit")
    check("env preserved", s1["env"] == "FOO=bar")
    check("group preserved", s1["group"] == "dev")

    # Minimal session: empty optional fields should be omitted
    s2 = data["sessions"][1]
    check("minimal: title preserved", s2["title"] == "notes")
    check("minimal: directory preserved", s2["directory"] == "~/notes")
    check("minimal: no account key", "account" not in s2)
    check("minimal: no flags key", "flags" not in s2)
    check("minimal: no pw key", "project_window" not in s2)
    check("minimal: no env key", "env" not in s2)
    check("minimal: no group key", "group" not in s2)

    # Header comment preserved
    with open(path) as f:
        first_line = f.readline()
    check("header comment preserved", first_line.startswith("# My sessions"))

    os.unlink(path)


# ── write-then-read roundtrip ────────────────────────────────────


def test_write_read_roundtrip():
    """write-sessions followed by read-sessions preserves all data."""
    print("\n--- write-then-read roundtrip ---")
    path = tempfile.mktemp(suffix=".toml")

    original = [
        {
            "title": "project-a",
            "dir": "~/code/a",
            "account": "work",
            "flags": "--model opus",
            "pw": "hsplit",
            "env": "DEBUG=1",
            "group": "backend",
        },
        {
            "title": "project-b",
            "dir": "~/code/b",
            "account": "personal",
            "flags": "",
            "pw": "",
            "env": "",
            "group": "",
        },
    ]

    # Write
    _, _, rc = run_helper(
        "write-sessions", path, stdin_data=json.dumps(original)
    )
    check("write exit code 0", rc == 0)

    # Read back
    stdout, _, rc = run_helper("read-sessions", path)
    check("read exit code 0", rc == 0)

    lines = tsv_lines(stdout)
    check("roundtrip: two sessions", len(lines) == 2)

    cols1 = lines[0].split("\t")
    check("roundtrip: title matches", cols1[0] == "project-a")
    check("roundtrip: dir matches", cols1[1] == "~/code/a")
    check("roundtrip: account matches", cols1[2] == "work")
    check("roundtrip: flags matches", cols1[3] == "--model opus")
    check("roundtrip: pw matches", cols1[4] == "hsplit")
    check("roundtrip: env matches", cols1[5] == "DEBUG=1")
    check("roundtrip: group matches", cols1[6] == "backend")

    cols2 = lines[1].split("\t")
    check("roundtrip: minimal title", cols2[0] == "project-b")
    check("roundtrip: minimal empty flags", cols2[3] == "")

    os.unlink(path)


# ── error handling ───────────────────────────────────────────────


def test_error_handling():
    """CLI reports errors correctly for bad inputs."""
    print("\n--- error handling ---")

    # Unknown command
    _, stderr, rc = run_helper("bogus-command", "/tmp/x")
    check("unknown command: non-zero exit", rc != 0)
    check("unknown command: error message", "unknown command" in stderr.lower())

    # Missing arguments
    _, stderr, rc = run_helper("read-accounts")
    check("missing arg: non-zero exit", rc != 0)
    check("missing arg: error message", "requires" in stderr.lower())

    # No arguments at all
    _, stderr, rc = run_helper()
    check("no args: non-zero exit", rc != 0)

    # write-sessions with invalid JSON
    _, stderr, rc = run_helper(
        "write-sessions", "/tmp/test-out.toml",
        stdin_data="not json"
    )
    check("bad JSON: non-zero exit", rc != 0)
    check("bad JSON: error message", "json" in stderr.lower())

    # Nonexistent file for read (should return empty, not crash)
    stdout, _, rc = run_helper("read-accounts", "/tmp/nonexistent-12345.toml")
    check("missing file: exit 0", rc == 0)
    check("missing file: empty output", stdout.strip() == "")


# ── run all ──────────────────────────────────────────────────────


if __name__ == "__main__":
    print("toml-helper CLI bridge tests")

    test_read_accounts()
    test_read_sessions()
    test_read_defaults()
    test_write_sessions()
    test_write_read_roundtrip()
    test_error_handling()

    print(f"\n{PASS} passed, {FAIL} failed")
    if FAIL:
        sys.exit(1)
