"""Mode handlers for core dashboard actions and confirmations."""

import curses

from cc_dashboard.loop_helpers import (
    _resolve_session_runtime_options,
)


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


def handle_confirm_modes(key, state):
    """Handle key events for confirmation modes (close/restart/save)."""
    mode = state["mode"]

    if mode == "confirm":
        if key in (ord("y"), ord("Y")) and state["filtered"]:
            target = state["filtered"][state["selected"]]
            target_socket = target.get("socket", state["home_socket"])
            if (
                state["host_tab_id"] is not None
                and target["tab_id"] == state["host_tab_id"]
            ):
                state["spawn_detached_close"](target_socket, target["tab_id"])
                state["should_exit"] = True
                return True

            ok = state["do_close"](target_socket, target["tab_id"])
            if ok:
                closed_id = target["tab_id"]
                state["sessions"] = [
                    s for s in state["sessions"] if s["tab_id"] != closed_id
                ]
                state["filtered"] = [
                    s for s in state["filtered"] if s["tab_id"] != closed_id
                ]
                state["marked"].discard(closed_id)
                state["selected"] = min(
                    state["selected"], max(0, len(state["filtered"]) - 1)
                )
            if ok:
                state["msg"] = "Session closed"
            else:
                inst = target.get("instance_label", target_socket)
                state["msg"] = f"Instance {inst} is no longer available"
            state["msg_frames"] = 20
        state["mode"] = "list"
        return True

    if mode == "confirm_restart":
        if key in (ord("y"), ord("Y")) and state["filtered"]:
            sess = state["filtered"][state["selected"]]
            is_host = (
                state["host_tab_id"] is not None
                and sess["tab_id"] == state["host_tab_id"]
            )
            r_flags, r_pw, r_env = _resolve_session_runtime_options(sess)
            sess_socket = sess.get("socket", state["home_socket"])
            ok = state["do_restart"](
                sess_socket,
                sess,
                flags=r_flags,
                pw_override=r_pw,
                host_tab_id=state["host_tab_id"],
                env_vars=r_env,
            )
            if is_host:
                state["should_exit"] = True
                return True

            state["sessions"] = state["query_all_sessions"](
                managed_only=(not state["show_all"])
            )
            state["refilter"]()
            if ok:
                state["msg"] = "Session restarted"
            else:
                inst = sess.get("instance_label", sess_socket)
                state["msg"] = f"Instance {inst} is no longer available"
            state["msg_frames"] = 20
        state["mode"] = "list"
        return True

    if mode == "confirm_batch_close":
        if key in (ord("y"), ord("Y")) and state["batch_targets"]:
            has_host = state["host_tab_id"] is not None
            targets_sorted = sorted(
                state["batch_targets"],
                key=lambda s: (
                    1 if has_host and s["tab_id"] == state["host_tab_id"] else 0
                ),
            )
            closed = 0
            closed_ids = set()
            for t in targets_sorted:
                t_socket = t.get("socket", state["home_socket"])
                if has_host and t["tab_id"] == state["host_tab_id"]:
                    state["spawn_detached_close"](t_socket, t["tab_id"])
                    state["should_exit"] = True
                    return True
                if state["do_close"](t_socket, t["tab_id"]):
                    closed += 1
                    closed_ids.add(t["tab_id"])

            state["marked"].clear()
            if closed_ids:
                state["sessions"] = [
                    s for s in state["sessions"] if s["tab_id"] not in closed_ids
                ]
                state["filtered"] = [
                    s for s in state["filtered"] if s["tab_id"] not in closed_ids
                ]
                state["selected"] = min(
                    state["selected"], max(0, len(state["filtered"]) - 1)
                )
            state["msg"] = f"Closed {closed} session(s)"
            state["msg_frames"] = 20
        state["mode"] = "list"
        return True

    if mode == "confirm_batch_restart":
        if key in (ord("y"), ord("Y")) and state["batch_targets"]:
            has_host = state["host_tab_id"] is not None
            targets_sorted = sorted(
                state["batch_targets"],
                key=lambda s: (
                    1 if has_host and s["tab_id"] == state["host_tab_id"] else 0
                ),
            )
            restarted = 0
            for t in targets_sorted:
                r_flags, r_pw, r_env = _resolve_session_runtime_options(t)
                t_socket = t.get("socket", state["home_socket"])
                is_host = has_host and t["tab_id"] == state["host_tab_id"]
                ok = state["do_restart"](
                    t_socket,
                    t,
                    flags=r_flags,
                    pw_override=r_pw,
                    host_tab_id=state["host_tab_id"],
                    env_vars=r_env,
                )
                if is_host:
                    state["should_exit"] = True
                    return True
                if ok:
                    restarted += 1

            state["marked"].clear()
            state["sessions"] = state["query_all_sessions"](
                managed_only=(not state["show_all"])
            )
            state["refilter"]()
            state["msg"] = f"Restarted {restarted} session(s)"
            state["msg_frames"] = 20
        state["mode"] = "list"
        return True

    if mode == "confirm_batch_clone":
        if key in (ord("y"), ord("Y")) and state["batch_targets"]:
            all_title_dicts = [{"title": s["title"]} for s in state["sessions"]]
            cloned = 0
            for t in state["batch_targets"]:
                new_title = state["generate_clone_title"](all_title_dicts, t["title"])
                all_title_dicts.append({"title": new_title})
                r_flags, r_pw, r_env = _resolve_session_runtime_options(t)
                t_socket = t.get("socket", state["home_socket"])
                loc = state["compute_insert_location"](
                    t_socket, t["account"], new_title
                )
                clone_group = t.get("group", "")
                ok = state["do_launch"](
                    t_socket,
                    new_title,
                    t["cwd"],
                    t["account"],
                    r_flags,
                    r_pw,
                    location=loc,
                    env_vars=r_env,
                    color_override=t.get("cc_color", ""),
                    group=clone_group,
                )
                if ok:
                    cloned += 1

            state["marked"].clear()
            state["sessions"] = state["query_all_sessions"](
                managed_only=(not state["show_all"])
            )
            state["refilter"]()
            failed = len(state["batch_targets"]) - cloned
            if failed > 0:
                state["msg"] = f"Cloned {cloned}, failed {failed} session(s)"
            else:
                state["msg"] = f"Cloned {cloned} session(s)"
            state["msg_frames"] = 20
        state["mode"] = "list"
        return True

    if mode == "confirm_save":
        if key in (ord("y"), ord("Y")):
            try:
                count, _ = state["cfg"].save_sessions(state["sessions"])
                fname = state["cfg"]._sessions_config_filename()
                state["msg"] = f"Saved {count} session(s) to {fname}"
                state["msg_frames"] = 25
            except OSError as e:
                state["msg"] = f"Save failed: {e}"
                state["msg_frames"] = 30
        state["mode"] = "list"
        return True

    return False


def handle_color_picker_mode(key, state):
    """Handle key events for the color picker mode."""
    if state["mode"] != "color_picker":
        return False

    total = len(state["palette_names"]) + 1  # +1 for reset option

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
            chosen = ""  # reset

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

            # If chosen matches account default, treat as reset
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


def handle_move_picker_mode(key, state):
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
                    r_flags, r_pw, r_env = _resolve_session_runtime_options(t)
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
        r_flags, r_pw, r_env = _resolve_session_runtime_options(sess)
        ok = state["do_move"](
            sess_socket,
            target_socket,
            sess,
            flags=r_flags,
            pw_override=r_pw,
            host_tab_id=state["host_tab_id"],
            env_vars=r_env,
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


def handle_group_actions_mode(key, state):
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


def _ga_build_actions(state, group_name):
    """Build available actions for a group with counts."""
    running_titles = {s["title"] for s in state["sessions"]}
    to_launch = [
        s
        for s in state["cfg"]._sessions
        if (s.get("group", "") or "(ungrouped)") == group_name
        and s["title"] not in running_titles
    ]
    to_close = [
        s
        for s in state["sessions"]
        if (s.get("group", "") or "(ungrouped)") == group_name
    ]
    actions = []
    if to_launch:
        actions.append(("launch", f"Launch ({len(to_launch)} session(s))"))
    if to_close:
        actions.append(("close", f"Close ({len(to_close)} session(s))"))
    return actions


def _do_group_launch(state, group_name):
    """Launch all config sessions in a group that are not already running."""
    state["reload_all_config"]()
    running_titles = {s["title"] for s in state["sessions"]}
    to_launch = [
        s
        for s in state["cfg"]._sessions
        if (s.get("group", "") or "(ungrouped)") == group_name
        and s["title"] not in running_titles
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
            group=entry.get("group", ""),
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


def _do_group_close(state, group_name):
    """Transition to batch close confirmation for all running sessions in a group."""
    targets = [
        s
        for s in state["sessions"]
        if (s.get("group", "") or "(ungrouped)") == group_name
    ]
    if not targets:
        state["msg"] = f"No running sessions in '{group_name}'"
        state["msg_frames"] = 20
        state["mode"] = "list"
        return

    state["batch_targets"] = targets
    state["mode"] = "confirm_batch_close"
