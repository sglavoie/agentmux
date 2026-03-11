#!/usr/bin/env python3
"""Tests for validate_hooks() in lib/config_validator.py.

Run: python3 tests/test_hooks_validator.py
"""

import json
import os
import sys
import tempfile

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "lib"))

import config_validator as cv

PASS = 0
FAIL = 0

VALID_EVENTS = {"SessionStart", "UserPromptSubmit", "PostToolUse", "Stop",
                "PermissionRequest", "PostToolUseFailure", "Notification",
                "SubagentStart", "SubagentStop", "PreToolUse",
                "TeammateIdle", "TaskCompleted", "ConfigChange",
                "WorktreeCreate", "WorktreeRemove", "PreCompact", "SessionEnd"}


def check(label, condition):
    global PASS, FAIL
    if condition:
        PASS += 1
        print(f"  PASS  {label}")
    else:
        FAIL += 1
        print(f"  FAIL  {label}")


def _write_json(tmp_dir, data):
    path = os.path.join(tmp_dir, "hooks.json")
    with open(path, "w") as f:
        json.dump(data, f)
    return path


# ── valid hooks.json ─────────────────────────────────────────────────


def test_valid_hooks():
    """Minimal valid hooks.json produces no errors or warnings."""
    print("\n--- valid hooks ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, {
            "hooks": {
                "SessionStart": [{"hooks": [{"type": "command", "command": "echo hi"}]}],
                "Stop": [],
            }
        })
        v = cv.validate_hooks(path, VALID_EVENTS, {"work"})
        check("no errors", len(v.errors) == 0)
        check("no warnings", len(v.warnings) == 0)


def test_valid_with_account_overrides():
    """Valid hooks with account_overrides."""
    print("\n--- valid with overrides ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, {
            "hooks": {"SessionStart": []},
            "account_overrides": {
                "work": {
                    "disabled_events": ["SessionStart"],
                    "extra_commands": {
                        "Stop": [{"type": "command", "command": "echo bye"}]
                    }
                }
            }
        })
        v = cv.validate_hooks(path, VALID_EVENTS, {"work"})
        check("no errors", len(v.errors) == 0)
        check("no warnings", len(v.warnings) == 0)


def test_valid_empty_overrides():
    """Empty account_overrides is valid."""
    print("\n--- empty overrides ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, {"hooks": {}, "account_overrides": {}})
        v = cv.validate_hooks(path, VALID_EVENTS, set())
        check("no errors", len(v.errors) == 0)
        check("no warnings", len(v.warnings) == 0)


# ── missing/invalid file ─────────────────────────────────────────────


def test_missing_file():
    """Missing file produces a warning."""
    print("\n--- missing file ---")
    v = cv.validate_hooks("/no/such/hooks.json", VALID_EVENTS, set())
    check("no errors", len(v.errors) == 0)
    check("warning present", len(v.warnings) == 1)


def test_invalid_json():
    """Invalid JSON produces an error."""
    print("\n--- invalid json ---")
    with tempfile.TemporaryDirectory() as d:
        path = os.path.join(d, "hooks.json")
        with open(path, "w") as f:
            f.write("{bad json")
        v = cv.validate_hooks(path, VALID_EVENTS, set())
        check("error present", len(v.errors) == 1)
        check("mentions JSON", "JSON" in v.errors[0])


def test_top_level_not_object():
    """Non-object top level produces an error."""
    print("\n--- top level not object ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, [1, 2, 3])
        v = cv.validate_hooks(path, VALID_EVENTS, set())
        check("error present", len(v.errors) == 1)


# ── missing/invalid hooks key ────────────────────────────────────────


def test_missing_hooks_key():
    """Missing 'hooks' key produces an error."""
    print("\n--- missing hooks key ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, {"account_overrides": {}})
        v = cv.validate_hooks(path, VALID_EVENTS, set())
        check("error present", len(v.errors) == 1)
        check("mentions 'hooks'", "hooks" in v.errors[0])


def test_hooks_not_object():
    """Non-object 'hooks' produces an error."""
    print("\n--- hooks not object ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, {"hooks": "not_a_dict"})
        v = cv.validate_hooks(path, VALID_EVENTS, set())
        check("error present", len(v.errors) == 1)


def test_unknown_hook_event():
    """Unknown event name in hooks produces a warning."""
    print("\n--- unknown hook event ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, {"hooks": {"FakeEvent": []}})
        v = cv.validate_hooks(path, VALID_EVENTS, set())
        check("warning present", len(v.warnings) == 1)
        check("mentions FakeEvent", "FakeEvent" in v.warnings[0])


# ── account_overrides validation ─────────────────────────────────────


def test_overrides_not_object():
    """Non-object account_overrides produces an error."""
    print("\n--- overrides not object ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, {"hooks": {}, "account_overrides": "bad"})
        v = cv.validate_hooks(path, VALID_EVENTS, set())
        check("error present", len(v.errors) == 1)


def test_override_unknown_account():
    """Override for unknown account produces a warning."""
    print("\n--- unknown account in overrides ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, {
            "hooks": {},
            "account_overrides": {"ghost": {"disabled_events": []}}
        })
        v = cv.validate_hooks(path, VALID_EVENTS, {"work"})
        check("warning present", len(v.warnings) == 1)
        check("mentions ghost", "ghost" in v.warnings[0])


def test_override_entry_not_object():
    """Non-object override entry produces an error."""
    print("\n--- override entry not object ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, {
            "hooks": {},
            "account_overrides": {"work": "bad"}
        })
        v = cv.validate_hooks(path, VALID_EVENTS, {"work"})
        check("error present", len(v.errors) == 1)


def test_disabled_events_not_array():
    """Non-array disabled_events produces an error."""
    print("\n--- disabled_events not array ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, {
            "hooks": {},
            "account_overrides": {"work": {"disabled_events": "SessionStart"}}
        })
        v = cv.validate_hooks(path, VALID_EVENTS, {"work"})
        check("error present", len(v.errors) == 1)


def test_disabled_events_unknown_event():
    """Unknown event in disabled_events produces a warning."""
    print("\n--- disabled unknown event ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, {
            "hooks": {},
            "account_overrides": {"work": {"disabled_events": ["FakeEvent"]}}
        })
        v = cv.validate_hooks(path, VALID_EVENTS, {"work"})
        check("warning present", len(v.warnings) == 1)
        check("mentions FakeEvent", "FakeEvent" in v.warnings[0])


def test_extra_commands_not_object():
    """Non-object extra_commands produces an error."""
    print("\n--- extra_commands not object ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, {
            "hooks": {},
            "account_overrides": {"work": {"extra_commands": [1, 2]}}
        })
        v = cv.validate_hooks(path, VALID_EVENTS, {"work"})
        check("error present", len(v.errors) == 1)


def test_extra_commands_unknown_event():
    """Unknown event in extra_commands produces a warning."""
    print("\n--- extra_commands unknown event ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, {
            "hooks": {},
            "account_overrides": {
                "work": {"extra_commands": {"BogusEvent": []}}
            }
        })
        v = cv.validate_hooks(path, VALID_EVENTS, {"work"})
        check("warning present", len(v.warnings) == 1)
        check("mentions BogusEvent", "BogusEvent" in v.warnings[0])


def test_no_account_names_skips_account_check():
    """When account_names is empty, unknown accounts are not flagged."""
    print("\n--- no account names ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, {
            "hooks": {},
            "account_overrides": {"anything": {"disabled_events": []}}
        })
        v = cv.validate_hooks(path, VALID_EVENTS, set())
        check("no warnings", len(v.warnings) == 0)


def test_no_valid_events_skips_event_check():
    """When valid_events is empty, unknown events are not flagged."""
    print("\n--- no valid events ---")
    with tempfile.TemporaryDirectory() as d:
        path = _write_json(d, {"hooks": {"FakeEvent": []}})
        v = cv.validate_hooks(path, set(), set())
        check("no warnings", len(v.warnings) == 0)


# ── run all ──────────────────────────────────────────────────────────


if __name__ == "__main__":
    print("validate_hooks() tests")

    test_valid_hooks()
    test_valid_with_account_overrides()
    test_valid_empty_overrides()
    test_missing_file()
    test_invalid_json()
    test_top_level_not_object()
    test_missing_hooks_key()
    test_hooks_not_object()
    test_unknown_hook_event()
    test_overrides_not_object()
    test_override_unknown_account()
    test_override_entry_not_object()
    test_disabled_events_not_array()
    test_disabled_events_unknown_event()
    test_extra_commands_not_object()
    test_extra_commands_unknown_event()
    test_no_account_names_skips_account_check()
    test_no_valid_events_skips_event_check()

    print(f"\n{PASS} passed, {FAIL} failed")
    if FAIL:
        sys.exit(1)
