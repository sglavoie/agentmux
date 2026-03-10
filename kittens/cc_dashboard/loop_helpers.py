"""Helpers for the dashboard event loop.

This module intentionally contains pure or near-pure helpers used by
`cc_dashboard.loop` to keep the main event loop file smaller.
"""

import os

from cc_dashboard import config as _cfg
from cc_dashboard.filters import _apply_filters, _apply_sort
from cc_dashboard.remote.git_ops import detect_worktree
from cc_dashboard.remote.shared import _strip_override_marker


def _compute_peek_matches(lines, query):
    """Return sorted list of line indices containing query (case-insensitive)."""
    if not query:
        return []
    q = query.lower()
    return [i for i, line in enumerate(lines) if q in line.lower()]


def _compute_browse_matches(entries, query):
    """Return indices of browse entries whose title or session_id contains query."""
    if not query:
        return []
    q = query.lower()
    return [
        i
        for i, e in enumerate(entries)
        if q in (e.get("title", "") or "").lower()
        or q in e.get("session_id", "").lower()
    ]


def _rstrip_blank_lines(lines):
    """Remove trailing blank/whitespace-only lines from a list."""
    while lines and not lines[-1].strip():
        lines.pop()
    return lines


def _session_fingerprint(sessions):
    """Return a hashable fingerprint of session state for change detection."""
    return tuple(
        (
            s.get("socket", ""),
            s["tab_id"],
            s["title"],
            s["status"],
            s["cwd"],
            s["account"],
            s.get("window_count", 1),
            (s.get("activity") or {}).get("last_event_ts"),
            (s.get("activity") or {}).get("last_event_type"),
            s.get("model", ""),
            (s.get("activity") or {}).get("active_subagents"),
            (s.get("git") or {}).get("branch"),
            (s.get("git") or {}).get("dirty"),
            s.get("group", ""),
        )
        for s in sessions
    )


def _make_new_fields(default_account, default_pw, multi_instance, home_instance_label):
    fields = {
        "title": "",
        "dir": "~/",
        "account": default_account,
        "flags": "",
        "pw": default_pw,
        "env": "",
        "group": "",
        "save": "yes",
    }
    if multi_instance:
        fields["instance"] = home_instance_label
    return fields


def _reload_all_config():
    """Re-read all config files from disk and return refreshed defaults."""
    _cfg.load_accounts()
    _cfg.load_defaults()
    _cfg.load_sessions()
    default_account = _cfg._account_names[0] if _cfg._account_names else "work"
    default_pw = (
        _cfg._default_project_window if _cfg._default_project_window else "none"
    )
    refresh_interval = _cfg._default_dashboard_refresh
    refresh_threshold = refresh_interval * 10
    return default_account, default_pw, refresh_interval, refresh_threshold


def _refilter(
    sessions, search_query, account_filter, sort_mode, filtered, selected,
    group_filter=None,
):
    old_tab_id = (
        filtered[selected]["tab_id"] if filtered and selected < len(filtered) else None
    )
    new_filtered = _apply_sort(
        _apply_filters(sessions, search_query, account_filter, group_filter),
        sort_mode,
    )
    new_selected = selected
    if old_tab_id is not None:
        for i, sess in enumerate(new_filtered):
            if sess["tab_id"] == old_tab_id:
                new_selected = i
                break
    new_selected = min(new_selected, max(0, len(new_filtered) - 1))
    return new_filtered, new_selected


def _resolve_session_runtime_options(sess):
    r_flags = sess.get("cc_flags", "")
    r_pw = sess.get("cc_pw", "")
    r_env = sess.get("cc_env", "")
    if not r_flags and not r_pw and not r_env:
        cfg_entry = _cfg._find_config_session(sess["title"])
        if cfg_entry is None:
            cfg_entry = _cfg._find_config_session(_strip_override_marker(sess["title"]))
        r_flags = cfg_entry.get("flags", "") if cfg_entry else ""
        r_pw = cfg_entry.get("pw", "") if cfg_entry else ""
        r_env = cfg_entry.get("env", "") if cfg_entry else ""
    return r_flags, r_pw, r_env


def _build_session_actions(sess, scope, multi_instance):
    if scope == "marked":
        actions = [
            ("restart", "Restart marked sessions"),
            ("close", "Close marked sessions"),
        ]
        if multi_instance:
            actions.insert(1, ("move", "Move marked sessions"))
        return actions

    actions = [
        ("restart", "Restart now"),
        ("restart_options", "Restart with options"),
        ("clone", "Clone session"),
        ("override", "Override config dir"),
        ("rename", "Rename session"),
        ("color", "Set tab color"),
        ("edit_notes", "Edit notes"),
        ("close", "Close session"),
    ]
    if sess.get("git"):
        actions.insert(3, ("worktree", "Clone to git worktree"))
    if multi_instance:
        actions.insert(2, ("move", "Move to another instance"))
    return actions


def _begin_restart_options(
    sess,
    multi_instance,
    home_instance_label,
    restart_default_config_label,
    home_socket,
):
    flags, pw_override, env_vars = _resolve_session_runtime_options(sess)
    account = sess.get("account", "")
    default_dir = _cfg._accounts.get(account, {}).get("config_dir", "")
    current_dir = sess.get("config_dir", "") or default_dir
    config_choice = (
        restart_default_config_label
        if not current_dir or current_dir == default_dir
        else current_dir
    )
    restart_options_fields = {
        "flags": flags,
        "pw": pw_override if pw_override else "none",
        "env": env_vars,
        "config_dir": config_choice,
    }
    if multi_instance:
        restart_options_fields["instance"] = sess.get(
            "instance_label", home_instance_label
        )

    return {
        "restart_options_fields": restart_options_fields,
        "restart_options_active": 0,
        "restart_options_cursor": len(restart_options_fields["flags"]),
        "restart_target_socket": sess.get("socket", home_socket),
        "restart_target_tab_id": sess.get("tab_id", 0),
        "mode": "restart_options",
    }


def _build_delete_entries(config_sessions):
    entries = []
    for entry in config_sessions:
        e = dict(entry)
        expanded = e.get("expanded_dir", "")
        if not expanded:
            expanded = os.path.expanduser(e["dir"])
        if os.path.isdir(expanded):
            wt = detect_worktree(expanded)
            if wt["is_worktree"]:
                e["type_tag"] = "[worktree]"
                e["wt_main_repo"] = wt["main_repo"]
            elif os.path.isdir(os.path.join(expanded, ".git")):
                e["type_tag"] = "[repo]"
            else:
                e["type_tag"] = "[dir]"
        else:
            e["type_tag"] = "[n/a]"
        entries.append(e)
    return entries
