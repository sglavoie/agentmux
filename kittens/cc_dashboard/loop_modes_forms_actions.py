"""Session action handlers for dashboard form modes."""

import curses
import os
import time

from cc_dashboard.loop_modes_forms_shared import (
    _get_pick_entry,
    _resolve_live_or_config_options,
)


def handle_override_mode(key, state):
    """Handle key events for account override picker mode."""
    if state["mode"] != "override":
        return False

    cancel_mode = "pick" if state["override_source"] == "pick" else "list"
    if state["override_source"] == "batch":
        cancel_mode = "list"
    if key in (27, ord("q"), ord("Q")):
        state["mode"] = cancel_mode
    elif key in (curses.KEY_UP, ord("k"), ord("K")):
        dirs = state["cfg"]._unique_config_dirs()
        if dirs:
            state["override_selected"] = (state["override_selected"] - 1) % len(dirs)
    elif key in (curses.KEY_DOWN, ord("j"), ord("J")):
        dirs = state["cfg"]._unique_config_dirs()
        if dirs:
            state["override_selected"] = (state["override_selected"] + 1) % len(dirs)
    elif key in (10, 13, curses.KEY_ENTER):
        dirs = state["cfg"]._unique_config_dirs()
        selected_path = dirs[state["override_selected"]] if dirs else ""

        if state["override_source"] == "pick" and state["pick_filtered"]:
            entry = _get_pick_entry(state)
            if entry is None:
                state["mode"] = cancel_mode
                return True
            account = entry["account"]
            title = entry["title"]
            current_dir = state["cfg"]._accounts.get(account, {}).get("config_dir", "")
            cdo = None
            if selected_path != current_dir:
                title = state["append_override_for_dir"](title, selected_path)
                cdo = selected_path
            pw_val = entry["pw"]
            target_socket = (
                state["instance_socket_map"].get(
                    state["pick_target_instance"], state["home_socket"]
                )
                if state["multi_instance"]
                else state["home_socket"]
            )
            loc = state["compute_insert_location"](target_socket, account, title)
            state["do_launch"](
                target_socket,
                title,
                entry["dir"],
                account,
                entry["flags"],
                pw_val,
                location=loc,
                config_dir_override=cdo,
                env_vars=entry.get("env", ""),
                color_override=entry.get("cc_color", ""),
                groups=entry.get("groups", []),
                commands=entry.get("commands", []),
            )
            state["should_exit"] = True
            return True

        if state["override_source"] == "batch" and state.get("batch_targets"):
            has_host = state["host_tab_id"] is not None
            targets_sorted = sorted(
                state["batch_targets"],
                key=lambda s: (
                    1 if has_host and s["tab_id"] == state["host_tab_id"] else 0
                ),
            )
            overridden = 0
            skipped = 0
            for t in targets_sorted:
                t_socket = t.get("socket", state["home_socket"])
                r_flags, r_pw, r_env, r_commands = state[
                    "resolve_live_or_config_options"
                ](
                    t,
                    state["cfg"],
                    state["strip_override_marker"],
                )
                account_default_dir = (
                    state["cfg"]._accounts.get(t["account"], {}).get("config_dir", "")
                )
                session_dir = t.get("config_dir", "") or account_default_dir
                if selected_path == session_dir:
                    skipped += 1
                    continue
                is_revert = selected_path == account_default_dir
                config_dir_override = None if is_revert else selected_path
                is_host = has_host and t["tab_id"] == state["host_tab_id"]
                state["do_restart"](
                    t_socket,
                    t,
                    flags=r_flags,
                    pw_override=r_pw,
                    config_dir_override=config_dir_override,
                    host_tab_id=state["host_tab_id"],
                    env_vars=r_env,
                    commands=r_commands,
                )
                if is_host:
                    state["should_exit"] = True
                    return True
                overridden += 1
            state["marked"].clear()
            state["sessions"] = state["query_all_sessions"](
                managed_only=(not state["show_all"])
            )
            state["refilter"]()
            state["msg"] = f"Overridden {overridden} session(s), {skipped} skipped"
            state["msg_frames"] = 20
            state["mode"] = "list"
        elif state["override_source"] == "list" and state["filtered"]:
            sess = state["filtered"][state["selected"]]
            sess_socket = sess.get("socket", state["home_socket"])
            account_default_dir = (
                state["cfg"]._accounts.get(sess["account"], {}).get("config_dir", "")
            )
            session_dir = sess.get("config_dir", "") or account_default_dir
            if selected_path == session_dir:
                state["msg"] = "Same config dir selected, no change"
                state["msg_frames"] = 20
            else:
                is_revert = selected_path == account_default_dir
                config_dir_override = None if is_revert else selected_path
                if sess["status"] == "stopped":
                    ok = state["do_close"](sess_socket, sess["tab_id"])
                    if not ok:
                        inst = sess.get("instance_label", sess_socket)
                        state["msg"] = f"Instance {inst} is no longer available"
                        state["msg_frames"] = 20
                        state["mode"] = "list"
                        return True
                    time.sleep(0.3)
                    title = sess["title"]
                    if is_revert:
                        title = state["strip_override_marker"](title)
                    else:
                        title = state["append_override_for_dir"](title, selected_path)
                    account = sess["account"]
                    r_flags, r_pw, r_env, r_commands = state[
                        "resolve_live_or_config_options"
                    ](
                        sess,
                        state["cfg"],
                        state["strip_override_marker"],
                    )
                    location = state["compute_insert_location"](
                        sess_socket, account, title
                    )
                    state["do_launch"](
                        sess_socket,
                        title,
                        sess["cwd"],
                        account,
                        r_flags,
                        r_pw,
                        location=location,
                        config_dir_override=config_dir_override,
                        env_vars=r_env,
                        color_override=sess.get("cc_color", ""),
                        groups=sess.get("groups", []),
                        commands=r_commands,
                    )
                    state["should_exit"] = True
                    return True
                is_host = (
                    state["host_tab_id"] is not None
                    and sess["tab_id"] == state["host_tab_id"]
                )
                r_flags, r_pw, r_env, r_commands = state[
                    "resolve_live_or_config_options"
                ](
                    sess,
                    state["cfg"],
                    state["strip_override_marker"],
                )
                state["do_restart"](
                    sess_socket,
                    sess,
                    flags=r_flags,
                    pw_override=r_pw,
                    config_dir_override=config_dir_override,
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
                if is_revert:
                    state["msg"] = "Config dir reverted to account default"
                else:
                    home = os.path.expanduser("~")
                    display_path = selected_path
                    if display_path.startswith(home):
                        display_path = "~" + display_path[len(home) :]
                    state["msg"] = f'Config dir overridden to "{display_path}"'
                state["msg_frames"] = 20
            state["mode"] = "list"
        else:
            state["mode"] = cancel_mode

    return True


def handle_worktree_mode(key, state):
    """Handle key events for worktree mode."""
    if state["mode"] != "worktree":
        return False

    if key == 27:
        state["mode"] = "list"
    elif key in (10, 13, curses.KEY_ENTER):
        branch = state["worktree_branch"].strip()
        if not branch:
            state["msg"] = "Branch name cannot be empty"
            state["msg_frames"] = 20
            state["mode"] = "list"
        else:
            sess = state["filtered"][state["selected"]]
            result = state["do_create_worktree"](sess["cwd"], branch)
            if isinstance(result, str):
                state["msg"] = result
                state["msg_frames"] = 40
                state["mode"] = "list"
            else:
                wt_path = result["worktree_path"]
                proj = result["project_name"]
                orig_title = sess["title"]
                if (
                    len(orig_title) >= 2
                    and orig_title[0].isupper()
                    and orig_title[1] == ":"
                ):
                    new_title = orig_title[:2] + f"{proj}-{branch}"
                else:
                    new_title = f"{proj}-{branch}"
                flags, pw_val, env_val, commands = _resolve_live_or_config_options(
                    sess, state["cfg"], state["strip_override_marker"]
                )
                target_socket = sess.get("socket", state["home_socket"])
                loc = state["compute_insert_location"](
                    target_socket, sess["account"], new_title
                )
                sess_groups = sess.get("groups", [])
                state["do_launch"](
                    target_socket,
                    new_title,
                    wt_path,
                    sess["account"],
                    flags,
                    pw_val,
                    location=loc,
                    env_vars=env_val,
                    color_override=sess.get("cc_color", ""),
                    groups=sess_groups,
                    commands=commands,
                )
                state["cfg"].append_session(
                    new_title,
                    wt_path,
                    sess["account"],
                    flags,
                    pw_val,
                    env=env_val,
                    groups=sess_groups,
                    commands=commands,
                )
                state["sessions"] = state["query_all_sessions"](
                    managed_only=(not state["show_all"])
                )
                state["refilter"]()
                state["msg"] = f"Worktree created: {os.path.basename(wt_path)}"
                state["msg_frames"] = 40
                state["mode"] = "list"
    elif key in (curses.KEY_BACKSPACE, 127, 8):
        if state["worktree_cursor"] > 0:
            state["worktree_branch"] = (
                state["worktree_branch"][: state["worktree_cursor"] - 1]
                + state["worktree_branch"][state["worktree_cursor"] :]
            )
            state["worktree_cursor"] -= 1
    elif key == curses.KEY_LEFT:
        state["worktree_cursor"] = max(0, state["worktree_cursor"] - 1)
    elif key == curses.KEY_RIGHT:
        state["worktree_cursor"] = min(
            len(state["worktree_branch"]), state["worktree_cursor"] + 1
        )
    elif key == curses.KEY_HOME or key == 1:
        state["worktree_cursor"] = 0
    elif key == curses.KEY_END or key == 5:
        state["worktree_cursor"] = len(state["worktree_branch"])
    elif 32 <= key < 127:
        before = state["worktree_branch"][: state["worktree_cursor"]]
        after = state["worktree_branch"][state["worktree_cursor"] :]
        state["worktree_branch"] = before + chr(key) + after
        state["worktree_cursor"] += 1

    return True


def handle_clone_mode(key, state):
    """Handle key events for clone mode."""
    if state["mode"] != "clone":
        return False

    if key == 27:
        state["mode"] = "list"
    elif key in (10, 13, curses.KEY_ENTER):
        title = state["clone_title"].strip()
        if not title:
            state["msg"] = "Title cannot be empty"
            state["msg_frames"] = 20
            state["mode"] = "list"
        elif any(s["title"] == title for s in state["sessions"]):
            state["msg"] = "A session with that title is already running"
            state["msg_frames"] = 20
        elif state["cfg"]._find_config_session(title):
            state["msg"] = "A session with that title already exists in config"
            state["msg_frames"] = 20
        else:
            sess = state["filtered"][state["selected"]]
            flags, pw_val, env_val, commands = _resolve_live_or_config_options(
                sess, state["cfg"], state["strip_override_marker"]
            )
            target_socket = sess.get("socket", state["home_socket"])
            loc = state["compute_insert_location"](
                target_socket, sess["account"], title
            )
            clone_groups = sess.get("groups", [])
            state["do_launch"](
                target_socket,
                title,
                sess["cwd"],
                sess["account"],
                flags,
                pw_val,
                location=loc,
                env_vars=env_val,
                color_override=sess.get("cc_color", ""),
                groups=clone_groups,
                commands=commands,
            )
            state["cfg"].append_session(
                title,
                sess["cwd"],
                sess["account"],
                flags,
                pw_val,
                env=env_val,
                groups=clone_groups,
                commands=commands,
            )
            state["sessions"] = state["query_all_sessions"](
                managed_only=(not state["show_all"])
            )
            state["refilter"]()
            state["msg"] = f'Cloned as "{title}"'
            state["msg_frames"] = 40
            state["mode"] = "list"
    elif key in (curses.KEY_BACKSPACE, 127, 8):
        if state["clone_cursor"] > 0:
            state["clone_title"] = (
                state["clone_title"][: state["clone_cursor"] - 1]
                + state["clone_title"][state["clone_cursor"] :]
            )
            state["clone_cursor"] -= 1
    elif key == curses.KEY_LEFT:
        state["clone_cursor"] = max(0, state["clone_cursor"] - 1)
    elif key == curses.KEY_RIGHT:
        state["clone_cursor"] = min(
            len(state["clone_title"]), state["clone_cursor"] + 1
        )
    elif key == curses.KEY_HOME or key == 1:
        state["clone_cursor"] = 0
    elif key == curses.KEY_END or key == 5:
        state["clone_cursor"] = len(state["clone_title"])
    elif 32 <= key < 127:
        before = state["clone_title"][: state["clone_cursor"]]
        after = state["clone_title"][state["clone_cursor"] :]
        state["clone_title"] = before + chr(key) + after
        state["clone_cursor"] += 1

    return True
