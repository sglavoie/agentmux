"""Shared helpers for dashboard form-mode handlers."""

from cc_dashboard.filters import (
    _filter_config_sessions,
    _filter_pick_by_groups,
    _sort_pick_entries,
)


def _get_pick_entry(state: dict) -> dict | None:
    """Return the currently selected pick entry, or None if list is empty."""
    filtered: list = state.get("pick_filtered", [])
    idx: int = state.get("pick_selected", 0)
    if not filtered or idx < 0 or idx >= len(filtered):
        return None
    result: dict = filtered[idx]
    return result


def _resolve_live_or_config_options(sess, cfg, strip_override_marker):
    orig_title = sess["title"]
    stripped = strip_override_marker(orig_title)
    src_cfg = next((s for s in cfg._sessions if s["title"] == stripped), None)
    has_prefix = len(stripped) >= 2 and stripped[0].isupper() and stripped[1] == ":"
    if src_cfg is None and has_prefix:
        bare = stripped[2:]
        src_cfg = next((s for s in cfg._sessions if s["title"] == bare), None)

    flags = sess.get("cc_flags", "") or (src_cfg["flags"] if src_cfg else "")
    pw_val = sess.get("cc_pw", "") or (src_cfg["pw"] if src_cfg else "")
    env_val = sess.get("cc_env", "") or (src_cfg["env"] if src_cfg else "")
    raw_cmds = sess.get("cc_commands", [])
    cfg_commands = src_cfg.get("commands", []) if src_cfg else []
    if not isinstance(cfg_commands, list):
        cfg_commands = []
    else:
        cfg_commands = [cmd for cmd in cfg_commands if isinstance(cmd, str)]
    commands = raw_cmds if raw_cmds else cfg_commands
    return flags, pw_val, env_val, commands


def _refilter_pick(state):
    """Re-apply group filter and text search to rebuild pick_filtered."""
    entries = list(state["cfg"]._sessions)
    entries = _filter_pick_by_groups(entries, state.get("pick_group_filter"))
    if state["pick_query"]:
        entries = _filter_config_sessions(entries, state["pick_query"])
    state["pick_filtered"], state["pick_running_count"] = _sort_pick_entries(
        entries, state["pick_running"], state["sort_mode"]
    )
    state["pick_selected"] = min(
        state["pick_selected"], max(0, len(state["pick_filtered"]) - 1)
    )
