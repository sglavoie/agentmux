#!/usr/bin/env python3
"""Regression tests for the dashboard host-tab marker logic.

Run: python3 tests/test_dashboard_title.py
"""

import json
import os
import sys
from types import SimpleNamespace

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

sys.path.insert(0, os.path.join(REPO_ROOT, "kittens"))

import claude_dashboard  # noqa: E402
from cc_dashboard.remote import command  # noqa: E402
from cc_dashboard.remote.shared import _MANAGED_TITLE_RE, _strip_icons  # noqa: E402

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


def _with_window_id(window_id):
    old = os.environ.get("KITTY_WINDOW_ID")
    os.environ["KITTY_WINDOW_ID"] = str(window_id)
    return old


def _restore_window_id(old):
    if old is None:
        os.environ.pop("KITTY_WINDOW_ID", None)
    else:
        os.environ["KITTY_WINDOW_ID"] = old


def test_get_host_tab_info_managed_detection():
    print("\n--- host tab info: managed tab ---")
    old = _with_window_id(101)
    original = command.kitty_cmd
    try:
        payload = [
            {
                "tabs": [
                    {
                        "id": 17,
                        "title": "★⚡W:project",
                        "windows": [
                            {"id": 101, "user_vars": {}},
                            {"id": 55, "user_vars": {"cc_account": "work"}},
                        ],
                    }
                ]
            }
        ]
        command.kitty_cmd = lambda socket, *args, **kwargs: SimpleNamespace(
            returncode=0,
            stdout=json.dumps(payload),
        )
        info = command.get_host_tab_info("unix:/tmp/kitty-test")
        check(
            "returns tab id",
            info
            == {
                "tab_id": 17,
                "title": "★⚡W:project",
                "is_managed": True,
            },
        )
    finally:
        command.kitty_cmd = original
        _restore_window_id(old)


def test_get_host_tab_info_missing_window_id():
    print("\n--- host tab info: missing KITTY_WINDOW_ID ---")
    old = os.environ.pop("KITTY_WINDOW_ID", None)
    try:
        check("returns None", command.get_host_tab_info("unix:/tmp/kitty-test") is None)
    finally:
        _restore_window_id(old)


def test_dashboard_title_prefix_idempotent():
    print("\n--- dashboard marker prefix ---")
    title = "★⚡W:project"
    prefixed = claude_dashboard._dashboard_title(title)
    check("prefix added before existing icons", prefixed == "🚗★⚡W:project")
    check(
        "prefix not duplicated",
        claude_dashboard._dashboard_title(prefixed) == "🚗★⚡W:project",
    )


def test_restore_host_title_strips_only_dashboard_marker():
    print("\n--- restore host title ---")
    calls = []
    original = claude_dashboard.kitty_cmd
    try:
        payload = [
            {
                "tabs": [
                    {
                        "id": 23,
                        "title": "🚗★⚡W:project",
                        "windows": [],
                    }
                ]
            }
        ]

        def fake_kitty_cmd(socket, *args, **kwargs):
            calls.append(args)
            if args == ("ls",):
                return SimpleNamespace(returncode=0, stdout=json.dumps(payload))
            return SimpleNamespace(returncode=0, stdout="")

        claude_dashboard.kitty_cmd = fake_kitty_cmd
        restored = claude_dashboard._restore_host_title(
            "unix:/tmp/kitty-test", 23, True
        )
        check("restore succeeds", restored is True)
        check(
            "set-tab-title keeps managed icons",
            calls[1] == ("set-tab-title", "--match", "id:23", "★⚡W:project"),
        )
    finally:
        claude_dashboard.kitty_cmd = original


def test_managed_title_helpers_accept_dashboard_marker():
    print("\n--- managed title helpers ---")
    check(
        "regex matches normal managed title",
        bool(_MANAGED_TITLE_RE.match("★⚡W:project")),
    )
    check(
        "regex matches dashboard-prefixed managed title",
        bool(_MANAGED_TITLE_RE.match("🚗★⚡W:project")),
    )
    check("regex rejects plain shell title", not _MANAGED_TITLE_RE.match("zsh"))
    check(
        "strip_icons removes dashboard marker before parsing",
        _strip_icons("🚗★⚡W:project") == "W:project",
    )


if __name__ == "__main__":
    print("dashboard title tests\n")

    test_get_host_tab_info_managed_detection()
    test_get_host_tab_info_missing_window_id()
    test_dashboard_title_prefix_idempotent()
    test_restore_host_title_strips_only_dashboard_marker()
    test_managed_title_helpers_accept_dashboard_marker()

    print(f"\n{PASS} passed, {FAIL} failed")
    if FAIL:
        sys.exit(1)
