"""Mode handlers for dashboard action and restart flows."""

import curses


def handle_actions_mode(key, state, build_session_actions):
    """Handle key events for the session actions picker mode."""
    if state["mode"] != "actions":
        return False

    if key in (27, ord("q"), ord("Q")):
        state["mode"] = "list"
    elif key in (curses.KEY_UP, ord("k"), ord("K")) and state["filtered"]:
        actions = build_session_actions(
            state["filtered"][state["selected"]], state["actions_scope"]
        )
        if actions:
            state["actions_selected"] = (state["actions_selected"] - 1) % len(actions)
    elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and state["filtered"]:
        actions = build_session_actions(
            state["filtered"][state["selected"]], state["actions_scope"]
        )
        if actions:
            state["actions_selected"] = (state["actions_selected"] + 1) % len(actions)
    elif key in (10, 13, curses.KEY_ENTER) and state["filtered"]:
        sess = state["filtered"][state["selected"]]
        actions = build_session_actions(sess, state["actions_scope"])
        if not actions:
            state["mode"] = "list"
            return True

        action_id = actions[state["actions_selected"]][0]
        if action_id == "restart":
            if state["actions_scope"] == "marked" and state["actions_targets"]:
                state["batch_targets"] = list(state["actions_targets"])
                state["mode"] = "confirm_batch_restart"
            else:
                state["mode"] = "confirm_restart"
        elif action_id == "restart_options":
            restart_state = state["start_restart_options"](sess)
            state["restart_options_fields"] = restart_state["restart_options_fields"]
            state["restart_options_active"] = restart_state["restart_options_active"]
            state["restart_options_cursor"] = restart_state["restart_options_cursor"]
            state["restart_options_commands"] = restart_state.get(
                "restart_options_commands", []
            )
            state["restart_target_socket"] = restart_state["restart_target_socket"]
            state["restart_target_tab_id"] = restart_state["restart_target_tab_id"]
            state["mode"] = restart_state["mode"]
        elif action_id == "move":
            if not state["multi_instance"]:
                state["msg"] = "Only one Kitty instance running"
                state["msg_frames"] = 20
                state["mode"] = "list"
            else:
                state["mode"] = "move_picker"
                state["move_selected"] = 0
        elif action_id == "clone":
            if state["actions_scope"] == "marked" and state["actions_targets"]:
                state["batch_targets"] = list(state["actions_targets"])
                state["mode"] = "confirm_batch_clone"
            else:
                state["clone_title"] = state["generate_clone_title"](
                    state["sessions"], sess["title"]
                )
                state["clone_cursor"] = len(state["clone_title"])
                state["mode"] = "clone"
        elif action_id == "worktree":
            if not sess.get("git"):
                state["msg"] = "Not a git repository"
                state["msg_frames"] = 20
                state["mode"] = "list"
            else:
                state["mode"] = "worktree"
                state["worktree_branch"] = ""
                state["worktree_cursor"] = 0
        elif action_id == "override":
            if state["actions_scope"] == "marked" and state["actions_targets"]:
                state["batch_targets"] = list(state["actions_targets"])
                state["mode"] = "override"
                state["override_source"] = "batch"
                state["override_selected"] = 0
            else:
                state["mode"] = "override"
                state["override_source"] = "list"
                current_dir = sess.get("config_dir", "")
                if not current_dir:
                    current_dir = (
                        state["cfg"]
                        ._accounts.get(sess["account"], {})
                        .get("config_dir", "")
                    )
                dirs = state["cfg"]._unique_config_dirs()
                state["override_selected"] = 0
                for i, d in enumerate(dirs):
                    if d == current_dir:
                        state["override_selected"] = i
                        break
        elif action_id == "color":
            state["mode"] = "color_picker"
            if state["actions_scope"] == "marked":
                state["color_selected"] = 0
            else:
                acct_info = state["cfg"]._accounts.get(sess.get("account", ""), {})
                account_color = acct_info.get("color", "gray")
                current_color = sess.get("cc_color", "") or account_color
                state["color_selected"] = 0
                for i, name in enumerate(state["palette_names"]):
                    if name == current_color:
                        state["color_selected"] = i
                        break
        elif action_id == "group":
            if state["actions_scope"] == "marked" and state["actions_targets"]:
                state["batch_group_value"] = ""
                state["batch_group_cursor"] = 0
                state["mode"] = "batch_group"
            else:
                state["mode"] = "list"
        elif action_id == "edit_notes":
            sess_socket = sess.get("socket", state.get("home_socket", ""))
            ok = state["do_edit_notes"](sess_socket, sess["tab_id"], sess["title"])
            if ok:
                state["msg"] = "Notes editor opened"
            else:
                state["msg"] = "Failed to open notes editor"
            state["msg_frames"] = 20
            state["mode"] = "list"
        elif action_id == "mute":
            if state["actions_scope"] == "marked" and state["actions_targets"]:
                count = 0
                for t in state["actions_targets"]:
                    t_socket = t.get("socket", state["home_socket"])
                    if state["do_toggle_mute"](t_socket, t):
                        count += 1
                state["marked"].clear()
                state["msg"] = f"Toggled mute for {count} session(s)"
            else:
                sess_socket = sess.get("socket", state["home_socket"])
                ok = state["do_toggle_mute"](sess_socket, sess)
                was_muted = sess.get("muted", False)
                if ok:
                    state["msg"] = "Session unmuted" if was_muted else "Session muted"
                else:
                    state["msg"] = "Failed to toggle mute"
            state["msg_frames"] = 20
            state["sessions"] = state["query_all_sessions"](
                managed_only=(not state["show_all"])
            )
            state["refilter"]()
            state["mode"] = "list"
        elif action_id == "rename":
            state["mode"] = "rename"
            title = sess["title"]
            if len(title) >= 2 and title[0].isupper() and title[1] == ":":
                state["rename_value"] = title[2:]
            else:
                state["rename_value"] = title
            state["rename_value"] = state["strip_override_marker"](
                state["rename_value"]
            )
            state["rename_cursor"] = len(state["rename_value"])
        elif action_id == "close":
            if state["actions_scope"] == "marked" and state["actions_targets"]:
                state["batch_targets"] = list(state["actions_targets"])
                state["mode"] = "confirm_batch_close"
            else:
                state["mode"] = "confirm"

    return True


def handle_restart_options_mode(key, state):
    """Handle key events for restart options form mode."""
    if state["mode"] != "restart_options":
        return False

    text_fields = {"flags", "env"}
    cycle_fields = ["pw", "config_dir"]
    field_keys = ["flags", "pw", "env", "config_dir"]
    if state["multi_instance"] and "instance" in state["restart_options_fields"]:
        field_keys.append("instance")
        cycle_fields.append("instance")
    fk = field_keys[state["restart_options_active"]]
    last_field = len(field_keys) - 1

    if key == 27:
        state["mode"] = "list"
    elif key == curses.KEY_UP:
        state["restart_options_active"] = max(0, state["restart_options_active"] - 1)
        active_key = field_keys[state["restart_options_active"]]
        if active_key in text_fields:
            state["restart_options_cursor"] = len(
                state["restart_options_fields"][active_key]
            )
    elif key == curses.KEY_DOWN:
        state["restart_options_active"] = min(
            last_field, state["restart_options_active"] + 1
        )
        active_key = field_keys[state["restart_options_active"]]
        if active_key in text_fields:
            state["restart_options_cursor"] = len(
                state["restart_options_fields"][active_key]
            )
    elif key == 9:
        if fk == "pw":
            state["restart_options_fields"]["pw"] = state["cycle_pw"](
                state["restart_options_fields"]["pw"]
            )
        elif fk == "config_dir":
            config_choices = [
                state["restart_default_config_label"],
                *state["cfg"]._unique_config_dirs(),
            ]
            try:
                idx = config_choices.index(
                    state["restart_options_fields"]["config_dir"]
                )
            except ValueError:
                idx = 0
            state["restart_options_fields"]["config_dir"] = config_choices[
                (idx + 1) % len(config_choices)
            ]
        elif fk == "instance":
            state["restart_options_fields"]["instance"] = state["cycle_instance"](
                state["restart_options_fields"]["instance"], state["instance_labels"]
            )
        else:
            state["restart_options_active"] = min(
                last_field, state["restart_options_active"] + 1
            )
            active_key = field_keys[state["restart_options_active"]]
            if active_key in text_fields:
                state["restart_options_cursor"] = len(
                    state["restart_options_fields"][active_key]
                )
    elif key in (10, 13, curses.KEY_ENTER):
        if state["restart_options_active"] < last_field:
            state["restart_options_active"] += 1
            active_key = field_keys[state["restart_options_active"]]
            if active_key in text_fields:
                state["restart_options_cursor"] = len(
                    state["restart_options_fields"][active_key]
                )
        else:
            sess = next(
                (
                    s
                    for s in state["sessions"]
                    if s.get("tab_id") == state["restart_target_tab_id"]
                    and s.get("socket", state["home_socket"])
                    == state["restart_target_socket"]
                ),
                None,
            )
            if sess is None:
                state["msg"] = "Session no longer available"
                state["msg_frames"] = 20
                state["mode"] = "list"
                return True

            sess_socket = sess.get("socket", state["home_socket"])
            target_socket = sess_socket
            if (
                state["multi_instance"]
                and "instance" in state["restart_options_fields"]
            ):
                target_socket = state["instance_socket_map"].get(
                    state["restart_options_fields"]["instance"], sess_socket
                )

            flags = state["restart_options_fields"]["flags"].strip()
            pw_override = state["restart_options_fields"]["pw"]
            pw_override = "" if pw_override == "none" else pw_override
            env_vars = state["restart_options_fields"]["env"].strip()
            config_dir_override = state["restart_options_fields"]["config_dir"]
            if config_dir_override == state["restart_default_config_label"]:
                config_dir_override = None

            is_host = (
                state["host_tab_id"] is not None
                and sess["tab_id"] == state["host_tab_id"]
            )
            commands = state.get("restart_options_commands", [])
            if target_socket != sess_socket:
                ok = state["do_move"](
                    sess_socket,
                    target_socket,
                    sess,
                    flags=flags,
                    pw_override=pw_override,
                    config_dir_override=config_dir_override,
                    host_tab_id=state["host_tab_id"],
                    env_vars=env_vars,
                    commands=commands,
                )
                success_msg = (
                    "Session restarted in "
                    f"{state['restart_options_fields'].get('instance', '')}"
                ).strip()
            else:
                ok = state["do_restart"](
                    sess_socket,
                    sess,
                    flags=flags,
                    pw_override=pw_override,
                    config_dir_override=config_dir_override,
                    host_tab_id=state["host_tab_id"],
                    env_vars=env_vars,
                    commands=commands,
                )
                success_msg = "Session restarted"

            if is_host:
                state["should_exit"] = True
                return True

            state["sessions"] = state["query_all_sessions"](
                managed_only=(not state["show_all"])
            )
            state["refilter"]()
            if ok:
                state["msg"] = success_msg
            else:
                inst = sess.get("instance_label", sess_socket)
                state["msg"] = f"Instance {inst} is no longer available"
            state["msg_frames"] = 20
            state["mode"] = "list"
    elif fk in text_fields:
        val = state["restart_options_fields"][fk]
        if key in (curses.KEY_BACKSPACE, 127, 8):
            if state["restart_options_cursor"] > 0:
                state["restart_options_fields"][fk] = (
                    val[: state["restart_options_cursor"] - 1]
                    + val[state["restart_options_cursor"] :]
                )
                state["restart_options_cursor"] -= 1
        elif key == curses.KEY_LEFT:
            state["restart_options_cursor"] = max(
                0, state["restart_options_cursor"] - 1
            )
        elif key == curses.KEY_RIGHT:
            state["restart_options_cursor"] = min(
                len(val), state["restart_options_cursor"] + 1
            )
        elif key == curses.KEY_HOME or key == 1:
            state["restart_options_cursor"] = 0
        elif key == curses.KEY_END or key == 5:
            state["restart_options_cursor"] = len(val)
        elif 32 <= key < 127:
            state["restart_options_fields"][fk] = (
                val[: state["restart_options_cursor"]]
                + chr(key)
                + val[state["restart_options_cursor"] :]
            )
            state["restart_options_cursor"] += 1

    return True


def handle_sort_pick_mode(key, state):
    """Handle key events for sort picker mode."""
    if state["mode"] != "sort_pick":
        return False

    if key in (27, ord("q"), ord("Q")):
        state["mode"] = "list"
    elif key in (curses.KEY_UP, ord("k"), ord("K")):
        state["sort_selected"] = (state["sort_selected"] - 1) % len(state["sort_modes"])
    elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
        state["sort_selected"] = (state["sort_selected"] + 1) % len(state["sort_modes"])
    elif key in (10, 13, curses.KEY_ENTER):
        state["sort_mode"] = state["sort_modes"][state["sort_selected"]]
        state["refilter"]()
        if "_save_ui_state" in state:
            state["_save_ui_state"]()
        label = state["sort_labels"][state["sort_mode"]]
        state["msg"] = f"Sort: {label}" if label else "Sort: default (tab order)"
        state["msg_frames"] = 15
        state["mode"] = "list"

    return True


__all__ = [
    "handle_actions_mode",
    "handle_restart_options_mode",
    "handle_sort_pick_mode",
]
