"""Mode handlers for dashboard color, move, and group operations."""

from __future__ import annotations

import curses
from typing import Any

from cc_dashboard.filters import _session_groups
from cc_dashboard.loop_helpers import (
    _resolve_session_runtime_options,
)


def handle_color_picker_mode(key: int, state: dict[str, Any]) -> bool:
    """Handle key events for the color picker mode."""
    if state["mode"] != "color_picker":
        return False

    total = len(state["palette_names"]) + 1

    if key in (27, ord("q"), ord("Q")):
        state["mode"] = "list"
    elif key in (curses.KEY_UP, ord("k"), ord("K")):
        state["color_selected"] = (state["color_selected"] - 1) % total
    elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
        state["color_selected"] = (state["color_selected"] + 1) % total
    elif key in (10, 13, curses.KEY_ENTER):
        palette_names = state["palette_names"]

        if state["color_selected"] < len(palette_names):
            chosen = palette_names[state["color_selected"]]
        else:
            chosen = ""

        if state.get("actions_scope") == "marked":
            count = 0
            for t in state["actions_targets"]:
                acct_color = (
                    state["cfg"]
                    ._accounts.get(t.get("account", ""), {})
                    .get("color", "gray")
                )
                color_val = "" if chosen == acct_color else chosen
                t_socket = t.get("socket", state["home_socket"])
                if state["do_set_color"](t_socket, t, color_val):
                    count += 1
            label = chosen if chosen else "default"
            state["msg"] = f"Set color to {label} for {count} session(s)"
            state["marked"] = set()
        else:
            sess = state["filtered"][state["selected"]]
            acct_color = (
                state["cfg"]
                ._accounts.get(sess.get("account", ""), {})
                .get("color", "gray")
            )

            if chosen == acct_color:
                chosen = ""

            sess_socket = sess.get("socket", state["home_socket"])
            ok = state["do_set_color"](sess_socket, sess, chosen)

            if ok:
                label = chosen if chosen else f"default ({acct_color})"
                state["msg"] = f"Tab color: {label}"
            else:
                state["msg"] = "Failed to set tab color"

        state["msg_frames"] = 20
        state["sessions"] = state["query_all_sessions"](
            managed_only=(not state["show_all"])
        )
        state["refilter"]()
        state["mode"] = "list"

    return True


def handle_move_picker_mode(key: int, state: dict[str, Any]) -> bool:
    """Handle key events for move picker mode."""
    if state["mode"] != "move_picker":
        return False

    sess = state["filtered"][state["selected"]] if state["filtered"] else None
    current_inst = sess.get("instance_label", "") if sess else ""
    targets = [lbl for lbl in state["instance_labels"] if lbl != current_inst]

    if key in (27, ord("q"), ord("Q")):
        state["mode"] = "list"
    elif key in (curses.KEY_UP, ord("k"), ord("K")):
        if targets:
            state["move_selected"] = (state["move_selected"] - 1) % len(targets)
    elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
        if targets:
            state["move_selected"] = (state["move_selected"] + 1) % len(targets)
    elif key in (10, 13, curses.KEY_ENTER) and sess and targets:
        target_label = targets[state["move_selected"]]
        target_socket = state["instance_socket_map"].get(target_label, "")
        if not target_socket:
            state["msg"] = f"Instance {target_label} is no longer available"
            state["msg_frames"] = 20
            state["mode"] = "list"
            return True

        if state["actions_scope"] == "marked" and state["actions_targets"]:
            to_move = [
                s
                for s in state["actions_targets"]
                if s.get("instance_label", "") != target_label
            ]
            if not to_move:
                state["msg"] = "All marked sessions already on target instance"
                state["msg_frames"] = 20
            else:
                has_host = state["host_tab_id"] is not None
                targets_sorted = sorted(
                    to_move,
                    key=lambda s: (
                        1 if has_host and s["tab_id"] == state["host_tab_id"] else 0
                    ),
                )
                moved = 0
                for t in targets_sorted:
                    r_flags, r_pw, r_env, r_commands = _resolve_session_runtime_options(
                        t
                    )
                    t_socket = t.get("socket", state["home_socket"])
                    is_host = has_host and t["tab_id"] == state["host_tab_id"]
                    ok = state["do_move"](
                        t_socket,
                        target_socket,
                        t,
                        flags=r_flags,
                        pw_override=r_pw,
                        host_tab_id=state["host_tab_id"],
                        env_vars=r_env,
                        commands=r_commands,
                    )
                    if is_host:
                        state["should_exit"] = True
                        return True
                    if ok:
                        moved += 1

                state["marked"].clear()
                state["sessions"] = state["query_all_sessions"](
                    managed_only=(not state["show_all"])
                )
                state["refilter"]()
                state["msg"] = f"Moved {moved} session(s) to {target_label}"
                state["msg_frames"] = 20
            state["mode"] = "list"
            return True

        is_host = (
            state["host_tab_id"] is not None and sess["tab_id"] == state["host_tab_id"]
        )
        sess_socket = sess.get("socket", state["home_socket"])
        r_flags, r_pw, r_env, r_commands = _resolve_session_runtime_options(sess)
        ok = state["do_move"](
            sess_socket,
            target_socket,
            sess,
            flags=r_flags,
            pw_override=r_pw,
            host_tab_id=state["host_tab_id"],
            env_vars=r_env,
            commands=r_commands,
        )
        if is_host:
            state["should_exit"] = True
            return True

        state["sessions"] = state["query_all_sessions"](
            managed_only=(not state["show_all"])
        )
        state["refilter"]()
        if ok:
            state["msg"] = f"Session moved to {target_label}"
        else:
            inst = sess.get("instance_label", sess_socket)
            state["msg"] = f"Instance {inst} is no longer available"
        state["msg_frames"] = 20
        state["mode"] = "list"

    return True


def handle_group_actions_mode(key: int, state: dict[str, Any]) -> bool:
    """Handle key events for the group actions overlay (G key).

    Two-step flow: pick_group -> pick_action (Launch / Close).
    """
    if state["mode"] != "group_actions":
        return False

    step = state["ga_step"]

    if step == "pick_group":
        groups = state["ga_groups"]
        if key in (27, ord("q"), ord("Q")):
            state["mode"] = "list"
        elif key in (curses.KEY_UP, ord("k"), ord("K")) and groups:
            state["ga_selected"] = (state["ga_selected"] - 1) % len(groups)
        elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and groups:
            state["ga_selected"] = (state["ga_selected"] + 1) % len(groups)
        elif key in (10, 13, curses.KEY_ENTER) and groups:
            state["ga_step"] = "pick_action"
            state["ga_action_selected"] = 0

    elif step == "pick_action":
        group_name = state["ga_groups"][state["ga_selected"]]
        actions = _ga_build_actions(state, group_name)
        if key == 27:
            state["ga_step"] = "pick_group"
        elif key in (ord("q"), ord("Q")):
            state["mode"] = "list"
        elif key in (curses.KEY_UP, ord("k"), ord("K")) and actions:
            state["ga_action_selected"] = (state["ga_action_selected"] - 1) % len(
                actions
            )
        elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and actions:
            state["ga_action_selected"] = (state["ga_action_selected"] + 1) % len(
                actions
            )
        elif key in (10, 13, curses.KEY_ENTER) and actions:
            action_id = actions[state["ga_action_selected"]][0]
            if action_id == "launch":
                _do_group_launch(state, group_name)
            elif action_id == "close":
                _do_group_close(state, group_name)

    return True


def _ga_build_actions(state: dict[str, Any], group_name: str) -> list[tuple[str, str]]:
    """Build available actions for a group with counts."""
    running_titles = {s["title"] for s in state["sessions"]}
    to_launch = [
        s
        for s in state["cfg"]._sessions
        if group_name in _session_groups(s) and s["title"] not in running_titles
    ]
    to_close = [s for s in state["sessions"] if group_name in _session_groups(s)]
    actions = []
    if to_launch:
        actions.append(("launch", f"Launch ({len(to_launch)} session(s))"))
    if to_close:
        actions.append(("close", f"Close ({len(to_close)} session(s))"))
    return actions


def _do_group_launch(state: dict[str, Any], group_name: str) -> None:
    """Launch all config sessions in a group that are not already running."""
    state["reload_all_config"]()
    running_titles = {s["title"] for s in state["sessions"]}
    to_launch = [
        s
        for s in state["cfg"]._sessions
        if group_name in _session_groups(s) and s["title"] not in running_titles
    ]
    if not to_launch:
        state["msg"] = f"All sessions in '{group_name}' already running"
        state["msg_frames"] = 20
        state["mode"] = "list"
        return

    launched = 0
    for entry in to_launch:
        loc = state["compute_insert_location"](
            state["home_socket"], entry["account"], entry["title"]
        )
        ok = state["do_launch"](
            state["home_socket"],
            entry["title"],
            entry["dir"],
            entry["account"],
            entry.get("flags", ""),
            entry.get("pw", ""),
            location=loc,
            env_vars=entry.get("env", ""),
            groups=entry.get("groups", []),
            commands=entry.get("commands", []),
        )
        if ok:
            launched += 1

    state["sessions"] = state["query_all_sessions"](
        managed_only=(not state["show_all"])
    )
    state["refilter"]()
    failed = len(to_launch) - launched
    if failed > 0:
        state["msg"] = (
            f"Launched {launched}, failed {failed} session(s) in '{group_name}'"
        )
    else:
        state["msg"] = f"Launched {launched} session(s) in '{group_name}'"
    state["msg_frames"] = 20
    state["mode"] = "list"


def _do_group_close(state: dict[str, Any], group_name: str) -> None:
    """Transition to batch close confirmation for all running sessions in a group."""
    targets = [s for s in state["sessions"] if group_name in _session_groups(s)]
    if not targets:
        state["msg"] = f"No running sessions in '{group_name}'"
        state["msg_frames"] = 20
        state["mode"] = "list"
        return

    state["batch_targets"] = targets
    state["mode"] = "confirm_batch_close"


__all__ = [
    "handle_color_picker_mode",
    "handle_group_actions_mode",
    "handle_move_picker_mode",
]
