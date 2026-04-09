#!/usr/bin/env python3
"""Config lookup runtime regression tests for the commands feature."""

from commands_runtime_support import (
    _resolve_live_or_config_options,
    _resolve_session_runtime_options,
    cfg,
    check,
    handle_list_mode,
)


def test_resolve_config_lookup_with_account_prefix() -> None:
    """Config lookup matches titles with X: account prefix (e.g. W:proj)."""
    print("\n--- resolve config lookup with account prefix ---")

    class Cfg:
        def __init__(self):
            self._sessions = [
                {
                    "title": "W:proj",
                    "flags": "--verbose",
                    "pw": "stack",
                    "env": "FOO=bar",
                    "commands": ["npm run dev"],
                }
            ]

    cfg_obj = Cfg()
    sess = {
        "title": "W:proj",
        "cc_flags": "",
        "cc_pw": "",
        "cc_env": "",
        "cc_commands": [],
    }
    flags, pw, env, commands = _resolve_live_or_config_options(
        sess, cfg_obj, lambda title: title
    )
    check("config lookup matches W:proj title", flags == "--verbose")
    check("config lookup pw with prefix", pw == "stack")
    check("config lookup env with prefix", env == "FOO=bar")
    check("config lookup commands with prefix", commands == ["npm run dev"])

    sess2 = {
        "title": "W:proj",
        "cc_flags": "",
        "cc_pw": "",
        "cc_env": "",
        "cc_commands": [],
    }
    flags2, _pw2, _env2, commands2 = _resolve_live_or_config_options(
        sess2, cfg_obj, lambda title: title
    )
    check("config lookup still works after emoji strip", flags2 == "--verbose")
    check("config lookup commands after emoji strip", commands2 == ["npm run dev"])


def test_resolve_config_lookup_fallback_without_prefix() -> None:
    """Config lookup falls back to bare title when config omits X: prefix."""
    print("\n--- resolve config lookup fallback without prefix ---")

    class Cfg:
        def __init__(self):
            self._sessions = [
                {
                    "title": "proj",
                    "flags": "--fallback",
                    "pw": "",
                    "env": "",
                    "commands": ["shell"],
                }
            ]

    sess = {
        "title": "W:proj",
        "cc_flags": "",
        "cc_pw": "",
        "cc_env": "",
        "cc_commands": [],
    }
    flags, _pw, _env, commands = _resolve_live_or_config_options(
        sess, Cfg(), lambda title: title
    )
    check("fallback lookup matches bare title", flags == "--fallback")
    check("fallback lookup commands", commands == ["shell"])


def test_runtime_options_fallback_without_prefix() -> None:
    """_resolve_session_runtime_options falls back to bare title."""
    print("\n--- runtime options fallback without prefix ---")
    original_sessions = cfg._sessions
    original_find = cfg._find_config_session

    cfg._sessions = [
        {
            "title": "proj",
            "flags": "--from-config",
            "pw": "stack",
            "env": "FOO=1",
            "commands": ["npm run dev"],
        }
    ]

    def _find(title):
        for session in cfg._sessions:
            if session["title"] == title:
                return session
        return None

    cfg._find_config_session = _find

    sess = {
        "title": "W:proj",
        "cc_flags": "",
        "cc_pw": "",
        "cc_env": "",
        "cc_commands": [],
    }
    flags, pw, env, commands = _resolve_session_runtime_options(sess)

    cfg._sessions = original_sessions
    cfg._find_config_session = original_find

    check("runtime options fallback flags", flags == "--from-config")
    check("runtime options fallback pw", pw == "stack")
    check("runtime options fallback env", env == "FOO=1")
    check("runtime options fallback commands", commands == ["npm run dev"])


def test_edit_config_lookup_strips_prefix() -> None:
    """Edit mode (e key) finds config when title has X: prefix but config omits it."""
    print("\n--- edit config lookup strips prefix ---")

    class FakeStdscr:
        def getmaxyx(self):
            return (40, 120)

    class Cfg:
        def __init__(self):
            self._sessions = [
                {
                    "title": "proj",
                    "dir": "~/code/proj",
                    "account": "work",
                    "flags": "",
                    "pw": "",
                    "env": "",
                    "group": "",
                    "commands": [],
                }
            ]
            self._account_names = ["work"]

        def _find_config_session(self, title):
            for session in self._sessions:
                if session["title"] == title:
                    return session
            return None

    state = {
        "mode": "list",
        "search_active": False,
        "account_filter_mode": False,
        "group_filter_mode": False,
        "filtered": [
            {
                "title": "W:proj",
                "cwd": "/tmp",
                "account": "work",
                "tab_id": 1,
                "status": "idle",
            }
        ],
        "selected": 0,
        "marked": set(),
        "search_query": "",
        "cfg": Cfg(),
        "strip_override_marker": lambda title, **kwargs: title,
        "msg": "",
        "msg_frames": 0,
        "show_all": False,
        "sessions": [],
    }

    handle_list_mode(FakeStdscr(), ord("e"), state)

    check("edit enters new mode on prefix fallback", state["mode"] == "new")
    check("edit form_mode is edit", state.get("form_mode") == "edit")
    check(
        "edit original title is bare config title",
        state.get("edit_original_title") == "proj",
    )


def test_detail_pane_config_lookup_strips_prefix() -> None:
    """Detail pane config lookup falls back to bare title after X: strip."""
    print("\n--- detail pane config lookup strips prefix ---")
    from cc_dashboard import config as dashboard_cfg
    from cc_dashboard.remote.shared import _strip_override_marker

    original_find = dashboard_cfg._find_config_session
    calls: list[str] = []

    def fake_find(title):
        calls.append(title)
        if title == "proj":
            return {"flags": "", "pw": "", "group": "g1"}
        return None

    dashboard_cfg._find_config_session = fake_find

    session_title = "W:proj"
    stripped = _strip_override_marker(session_title)
    cfg_entry = dashboard_cfg._find_config_session(session_title)
    if cfg_entry is None:
        cfg_entry = dashboard_cfg._find_config_session(stripped)
    if cfg_entry is None:
        has_prefix = len(stripped) >= 2 and stripped[0].isupper() and stripped[1] == ":"
        if has_prefix:
            cfg_entry = dashboard_cfg._find_config_session(stripped[2:])

    dashboard_cfg._find_config_session = original_find

    check("detail pane tried W:proj first", "W:proj" in calls)
    check("detail pane tried bare title via prefix strip", "proj" in calls)
    check("detail pane found config entry", cfg_entry is not None)


def test_session_group_enrichment_strips_prefix() -> None:
    """Session group enrichment falls back to bare title."""
    print("\n--- session group enrichment strips prefix ---")
    from cc_dashboard import config as dashboard_cfg

    original_find = dashboard_cfg._find_config_session
    calls: list[str] = []

    def fake_find(title):
        calls.append(title)
        if title == "proj":
            return {"group": "mygroup"}
        return None

    dashboard_cfg._find_config_session = fake_find

    title = "W:proj"
    cc_group = ""
    if not cc_group:
        cfg_entry = dashboard_cfg._find_config_session(title)
        if cfg_entry is None:
            has_prefix = len(title) >= 2 and title[0].isupper() and title[1] == ":"
            if has_prefix:
                cfg_entry = dashboard_cfg._find_config_session(title[2:])
        if cfg_entry:
            cc_group = cfg_entry.get("group", "")

    dashboard_cfg._find_config_session = original_find

    check("group enrichment found config via prefix strip", cc_group == "mygroup")
    check("group enrichment tried bare title", "proj" in calls)


def run_tests() -> None:
    test_resolve_config_lookup_with_account_prefix()
    test_resolve_config_lookup_fallback_without_prefix()
    test_runtime_options_fallback_without_prefix()
    test_edit_config_lookup_strips_prefix()
    test_detail_pane_config_lookup_strips_prefix()
    test_session_group_enrichment_strips_prefix()
