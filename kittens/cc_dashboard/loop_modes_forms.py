"""Mode handlers for override, edit forms, and config picker flows."""

import curses
import os
import time

from cc_dashboard.filters import (
    _filter_config_sessions,
    _filter_pick_by_groups,
    _sort_pick_entries,
)
from cc_dashboard.views import (
    _cycle_account,
    _cycle_instance,
    _cycle_pw,
    _scan_directories,
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
            if state["pick_selected"] >= len(state["pick_filtered"]):
                state["mode"] = cancel_mode
                return True
            entry = state["pick_filtered"][state["pick_selected"]]
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
                group=entry.get("group", ""),
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
                    flags=t.get("cc_flags", ""),
                    pw_override=t.get("cc_pw", ""),
                    config_dir_override=config_dir_override,
                    host_tab_id=state["host_tab_id"],
                    env_vars=t.get("cc_env", ""),
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
                # Reverting to the account default means no override
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
                    location = state["compute_insert_location"](
                        sess_socket, account, title
                    )
                    state["do_launch"](
                        sess_socket,
                        title,
                        sess["cwd"],
                        account,
                        sess.get("cc_flags", ""),
                        sess.get("cc_pw", ""),
                        location=location,
                        config_dir_override=config_dir_override,
                        env_vars=sess.get("cc_env", ""),
                        color_override=sess.get("cc_color", ""),
                    )
                    state["should_exit"] = True
                    return True
                is_host = (
                    state["host_tab_id"] is not None
                    and sess["tab_id"] == state["host_tab_id"]
                )
                state["do_restart"](
                    sess_socket,
                    sess,
                    flags=sess.get("cc_flags", ""),
                    pw_override=sess.get("cc_pw", ""),
                    config_dir_override=config_dir_override,
                    host_tab_id=state["host_tab_id"],
                    env_vars=sess.get("cc_env", ""),
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


def handle_rename_mode(key, state):
    """Handle key events for rename mode."""
    if state["mode"] != "rename":
        return False

    if key == 27:
        state["mode"] = "list"
    elif key in (10, 13, curses.KEY_ENTER):
        new_name = state["rename_value"].strip()
        if not new_name:
            state["msg"] = "Title cannot be empty"
            state["msg_frames"] = 20
            state["mode"] = "list"
        else:
            orig_title = state["filtered"][state["selected"]]["title"]
            stripped_orig, override_emoji = state["strip_override_marker"](
                orig_title, return_emoji=True
            )
            if (
                len(stripped_orig) >= 2
                and stripped_orig[0].isupper()
                and stripped_orig[1] == ":"
            ):
                full_title = stripped_orig[:2] + new_name
            else:
                full_title = new_name
            if override_emoji:
                full_title = override_emoji + full_title
            old_tab_id = state["filtered"][state["selected"]]["tab_id"]
            rename_socket = state["filtered"][state["selected"]].get(
                "socket", state["home_socket"]
            )
            ok = state["do_rename"](rename_socket, old_tab_id, full_title)
            state["sessions"] = state["query_all_sessions"](
                managed_only=(not state["show_all"])
            )
            state["refilter"]()
            if ok:
                state["msg"] = f'Renamed to "{full_title}"'
            else:
                inst = (
                    state["filtered"][state["selected"]].get(
                        "instance_label", rename_socket
                    )
                    if state["filtered"]
                    else rename_socket
                )
                state["msg"] = f"Instance {inst} is no longer available"
            state["msg_frames"] = 20
            state["mode"] = "list"
    elif key in (curses.KEY_BACKSPACE, 127, 8):
        if state["rename_cursor"] > 0:
            state["rename_value"] = (
                state["rename_value"][: state["rename_cursor"] - 1]
                + state["rename_value"][state["rename_cursor"] :]
            )
            state["rename_cursor"] -= 1
    elif key == curses.KEY_LEFT:
        state["rename_cursor"] = max(0, state["rename_cursor"] - 1)
    elif key == curses.KEY_RIGHT:
        state["rename_cursor"] = min(
            len(state["rename_value"]), state["rename_cursor"] + 1
        )
    elif key == curses.KEY_HOME or key == 1:
        state["rename_cursor"] = 0
    elif key == curses.KEY_END or key == 5:
        state["rename_cursor"] = len(state["rename_value"])
    elif 32 <= key < 127:
        before = state["rename_value"][: state["rename_cursor"]]
        after = state["rename_value"][state["rename_cursor"] :]
        state["rename_value"] = before + chr(key) + after
        state["rename_cursor"] += 1

    return True


def handle_batch_group_mode(key, state):
    """Handle key events for batch group assignment mode."""
    if state["mode"] != "batch_group":
        return False

    if key == 27:
        state["mode"] = "list"
    elif key in (10, 13, curses.KEY_ENTER):
        group_val = state["batch_group_value"].strip()
        count = 0
        for t in state["actions_targets"]:
            win_id = t.get("claude_window_id", 0)
            if win_id:
                t_socket = t.get("socket", state["home_socket"])
                result = state["kitty_cmd"](
                    t_socket,
                    "set-user-vars",
                    "--match",
                    f"id:{win_id}",
                    f"cc_group={group_val}",
                )
                if result and result.returncode == 0:
                    t["group"] = group_val
                    count += 1
        state["marked"].clear()
        state["sessions"] = state["query_all_sessions"](
            managed_only=(not state["show_all"])
        )
        state["refilter"]()
        if group_val:
            state["msg"] = f'Set group to "{group_val}" for {count} session(s)'
        else:
            state["msg"] = f"Cleared group for {count} session(s)"
        state["msg_frames"] = 20
        state["mode"] = "list"
    elif key in (curses.KEY_BACKSPACE, 127, 8):
        if state["batch_group_cursor"] > 0:
            state["batch_group_value"] = (
                state["batch_group_value"][: state["batch_group_cursor"] - 1]
                + state["batch_group_value"][state["batch_group_cursor"] :]
            )
            state["batch_group_cursor"] -= 1
    elif key == curses.KEY_LEFT:
        state["batch_group_cursor"] = max(0, state["batch_group_cursor"] - 1)
    elif key == curses.KEY_RIGHT:
        state["batch_group_cursor"] = min(
            len(state["batch_group_value"]), state["batch_group_cursor"] + 1
        )
    elif key == curses.KEY_HOME or key == 1:
        state["batch_group_cursor"] = 0
    elif key == curses.KEY_END or key == 5:
        state["batch_group_cursor"] = len(state["batch_group_value"])
    elif 32 <= key < 127:
        before = state["batch_group_value"][: state["batch_group_cursor"]]
        after = state["batch_group_value"][state["batch_group_cursor"] :]
        state["batch_group_value"] = before + chr(key) + after
        state["batch_group_cursor"] += 1

    return True


def _resolve_live_or_config_options(sess, cfg, strip_override_marker):
    flags = sess.get("cc_flags", "")
    pw_val = sess.get("cc_pw", "")
    env_val = sess.get("cc_env", "")
    if not flags and not pw_val and not env_val:
        orig_title = sess["title"]
        stripped = strip_override_marker(orig_title)
        if len(stripped) >= 2 and stripped[0].isupper() and stripped[1] == ":":
            stripped = stripped[2:]
        src_cfg = next((s for s in cfg._sessions if s["title"] == stripped), None)
        flags = src_cfg["flags"] if src_cfg else ""
        pw_val = src_cfg["pw"] if src_cfg else ""
        env_val = src_cfg["env"] if src_cfg else ""
    return flags, pw_val, env_val


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
                flags, pw_val, env_val = _resolve_live_or_config_options(
                    sess, state["cfg"], state["strip_override_marker"]
                )
                target_socket = sess.get("socket", state["home_socket"])
                loc = state["compute_insert_location"](
                    target_socket, sess["account"], new_title
                )
                sess_group = sess.get("group", "")
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
                    group=sess_group,
                )
                state["cfg"].append_session(
                    new_title,
                    wt_path,
                    sess["account"],
                    flags,
                    pw_val,
                    env=env_val,
                    group=sess_group,
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
        else:
            sess = state["filtered"][state["selected"]]
            flags, pw_val, env_val = _resolve_live_or_config_options(
                sess, state["cfg"], state["strip_override_marker"]
            )
            target_socket = sess.get("socket", state["home_socket"])
            loc = state["compute_insert_location"](
                target_socket, sess["account"], title
            )
            clone_group = sess.get("group", "")
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
                group=clone_group,
            )
            state["cfg"].append_session(
                title,
                sess["cwd"],
                sess["account"],
                flags,
                pw_val,
                env=env_val,
                group=clone_group,
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


def handle_new_mode(stdscr, key, state):
    """Handle key events for new/edit session form mode."""
    if state["mode"] != "new":
        return False

    if state["form_mode"] == "edit":
        field_keys = ["title", "dir", "account", "flags", "pw", "env", "group"]
    else:
        field_keys = ["title", "dir", "account", "flags", "pw", "env", "group", "save"]
        if state["multi_instance"]:
            field_keys.insert(-1, "instance")

    last_field = len(field_keys) - 1
    fk = field_keys[state["new_active"]]

    if state["dir_browse_active"] and fk == "dir":
        if key == 27:
            state["dir_browse_active"] = False
        elif key == curses.KEY_UP:
            state["dir_browse_selected"] = max(0, state["dir_browse_selected"] - 1)
            if state["dir_browse_selected"] < state["dir_browse_scroll"]:
                state["dir_browse_scroll"] = state["dir_browse_selected"]
        elif key == curses.KEY_DOWN:
            if state["dir_browse_entries"]:
                state["dir_browse_selected"] = min(
                    len(state["dir_browse_entries"]) - 1,
                    state["dir_browse_selected"] + 1,
                )
                max_vis = min(10, stdscr.getmaxyx()[0] - 10)
                if state["dir_browse_selected"] >= state["dir_browse_scroll"] + max_vis:
                    state["dir_browse_scroll"] = (
                        state["dir_browse_selected"] - max_vis + 1
                    )
        elif key in (10, 13, curses.KEY_ENTER, 9):
            if state["dir_browse_entries"]:
                selected_name = state["dir_browse_entries"][
                    state["dir_browse_selected"]
                ]
                val = state["new_fields"]["dir"]
                if val.endswith("/"):
                    base = val
                else:
                    idx = val.rfind("/")
                    base = val[: idx + 1] if idx >= 0 else ""
                state["new_fields"]["dir"] = base + selected_name + "/"
                state["new_cursor"] = len(state["new_fields"]["dir"])
                state["dir_browse_entries"] = _scan_directories(
                    state["new_fields"]["dir"],
                    show_hidden=state["dir_browse_show_hidden"],
                )
                state["dir_browse_selected"] = 0
                state["dir_browse_scroll"] = 0
                if not state["dir_browse_entries"]:
                    state["dir_browse_active"] = False
            else:
                state["dir_browse_active"] = False
        elif key == ord("."):
            state["dir_browse_show_hidden"] = not state["dir_browse_show_hidden"]
            state["dir_browse_entries"] = _scan_directories(
                state["new_fields"]["dir"],
                show_hidden=state["dir_browse_show_hidden"],
            )
            state["dir_browse_selected"] = 0
            state["dir_browse_scroll"] = 0
        elif key in (curses.KEY_BACKSPACE, 127, 8):
            if state["new_cursor"] > 0:
                val = state["new_fields"]["dir"]
                state["new_fields"]["dir"] = (
                    val[: state["new_cursor"] - 1] + val[state["new_cursor"] :]
                )
                state["new_cursor"] -= 1
                state["dir_browse_entries"] = _scan_directories(
                    state["new_fields"]["dir"],
                    show_hidden=state["dir_browse_show_hidden"],
                )
                state["dir_browse_selected"] = 0
                state["dir_browse_scroll"] = 0
                if not state["new_fields"]["dir"]:
                    state["dir_browse_active"] = False
        elif 32 <= key < 127:
            val = state["new_fields"]["dir"]
            state["new_fields"]["dir"] = (
                val[: state["new_cursor"]] + chr(key) + val[state["new_cursor"] :]
            )
            state["new_cursor"] += 1
            state["dir_browse_entries"] = _scan_directories(
                state["new_fields"]["dir"],
                show_hidden=state["dir_browse_show_hidden"],
            )
            state["dir_browse_selected"] = 0
            state["dir_browse_scroll"] = 0
        return True

    if key == 27:
        state["dir_browse_active"] = False
        state["form_mode"] = "new"
        state["mode"] = "list"
    elif key == curses.KEY_UP:
        state["new_active"] = max(state["new_active"] - 1, 0)
        state["new_cursor"] = len(state["new_fields"][field_keys[state["new_active"]]])
    elif key == curses.KEY_DOWN:
        state["new_active"] = min(state["new_active"] + 1, last_field)
        state["new_cursor"] = len(state["new_fields"][field_keys[state["new_active"]]])
    elif key == 9:
        if fk == "account":
            state["new_fields"]["account"] = _cycle_account(
                state["new_fields"]["account"]
            )
        elif fk == "pw":
            state["new_fields"]["pw"] = _cycle_pw(state["new_fields"]["pw"])
        elif fk == "save":
            state["new_fields"]["save"] = (
                "no" if state["new_fields"]["save"] == "yes" else "yes"
            )
        elif fk == "instance":
            state["new_fields"]["instance"] = _cycle_instance(
                state["new_fields"]["instance"],
                state["instance_labels"],
            )
        elif fk == "dir":
            state["dir_browse_entries"] = _scan_directories(
                state["new_fields"]["dir"],
                show_hidden=state["dir_browse_show_hidden"],
            )
            state["dir_browse_active"] = True
            state["dir_browse_selected"] = 0
            state["dir_browse_scroll"] = 0
        else:
            state["new_active"] = min(state["new_active"] + 1, last_field)
            state["new_cursor"] = len(
                state["new_fields"][field_keys[state["new_active"]]]
            )
    elif key in (10, 13, curses.KEY_ENTER):
        if state["new_active"] < last_field:
            state["new_active"] += 1
            state["new_cursor"] = len(
                state["new_fields"][field_keys[state["new_active"]]]
            )
        else:
            t = state["new_fields"]["title"].strip()
            d = state["new_fields"]["dir"].strip()
            a = state["new_fields"]["account"]
            fl = state["new_fields"]["flags"].strip()
            pw_val = state["new_fields"]["pw"]
            pw_val = "" if pw_val == "none" else pw_val
            if state["form_mode"] == "edit":
                if not t or not d:
                    state["msg"] = "Title and directory are required"
                    state["msg_frames"] = 20
                elif t != state["edit_original_title"] and state[
                    "cfg"
                ]._find_config_session(t):
                    state["msg"] = "A session with that title already exists"
                    state["msg_frames"] = 20
                else:
                    edit_env = state["new_fields"].get("env", "")
                    edit_group = state["new_fields"].get("group", "")
                    state["cfg"].update_session(
                        state["edit_original_title"],
                        t,
                        d,
                        a,
                        fl,
                        pw_val,
                        env=edit_env,
                        group=edit_group,
                    )
                    running_titles = {s["title"] for s in state["sessions"]}
                    stripped_running = {
                        state["strip_override_marker"](rt) for rt in running_titles
                    }
                    if state["edit_original_title"] in stripped_running:
                        state["msg"] = "Config updated (restart session to apply)"
                    else:
                        state["msg"] = "Config updated"
                    state["msg_frames"] = 20
                    state["mode"] = "list"
            else:
                target_socket = state["home_socket"]
                if state["multi_instance"] and "instance" in state["new_fields"]:
                    target_socket = state["instance_socket_map"].get(
                        state["new_fields"]["instance"], state["home_socket"]
                    )
                if t and d:
                    expanded_d = os.path.expanduser(d)
                    if not os.path.isdir(expanded_d):
                        state["mode"] = "confirm_mkdir"
                        state["mkdir_path"] = expanded_d
                        state["mkdir_pending"] = {
                            "socket": target_socket,
                            "title": t,
                            "dir": d,
                            "account": a,
                            "flags": fl,
                            "pw": pw_val,
                            "env": state["new_fields"].get("env", "").strip(),
                            "group": state["new_fields"].get("group", "").strip(),
                            "save": state["new_fields"].get("save"),
                        }
                        return True
                    env_val = state["new_fields"].get("env", "").strip()
                    group_val = state["new_fields"].get("group", "").strip()
                    loc = state["compute_insert_location"](target_socket, a, t)
                    state["do_launch"](
                        target_socket,
                        t,
                        d,
                        a,
                        fl,
                        pw_val,
                        location=loc,
                        env_vars=env_val,
                        group=group_val,
                    )
                    if state["new_fields"].get("save") == "yes":
                        state["cfg"].append_session(
                            t, d, a, fl, pw_val, env=env_val, group=group_val
                        )
                    state["should_exit"] = True
                    return True
                state["msg"] = "Title and directory are required"
                state["msg_frames"] = 20
                state["mode"] = "list"
    elif fk not in ("account", "pw", "instance", "save"):
        val = state["new_fields"][fk]
        if key in (curses.KEY_BACKSPACE, 127, 8):
            if state["new_cursor"] > 0:
                state["new_fields"][fk] = (
                    val[: state["new_cursor"] - 1] + val[state["new_cursor"] :]
                )
                state["new_cursor"] -= 1
        elif key == curses.KEY_LEFT:
            state["new_cursor"] = max(0, state["new_cursor"] - 1)
        elif key == curses.KEY_RIGHT:
            state["new_cursor"] = min(
                len(state["new_fields"][fk]), state["new_cursor"] + 1
            )
        elif key == curses.KEY_HOME or key == 1:
            state["new_cursor"] = 0
        elif key == curses.KEY_END or key == 5:
            state["new_cursor"] = len(state["new_fields"][fk])
        elif 32 <= key < 127:
            state["new_fields"][fk] = (
                val[: state["new_cursor"]] + chr(key) + val[state["new_cursor"] :]
            )
            state["new_cursor"] += 1

    return True


def handle_confirm_mkdir_mode(key, state):
    """Handle key events for directory creation confirmation."""
    if state["mode"] != "confirm_mkdir":
        return False

    if key == ord("y") or key == ord("Y"):
        try:
            os.makedirs(state["mkdir_path"], exist_ok=True)
        except OSError as exc:
            state["msg"] = f"Failed to create directory: {exc}"
            state["msg_frames"] = 30
            state["mode"] = "new"
            return True
        p = state["mkdir_pending"]
        loc = state["compute_insert_location"](p["socket"], p["account"], p["title"])
        state["do_launch"](
            p["socket"],
            p["title"],
            p["dir"],
            p["account"],
            p["flags"],
            p["pw"],
            location=loc,
            env_vars=p["env"],
            group=p["group"],
        )
        if p.get("save") == "yes":
            state["cfg"].append_session(
                p["title"],
                p["dir"],
                p["account"],
                p["flags"],
                p["pw"],
                env=p["env"],
                group=p["group"],
            )
        state["should_exit"] = True
    else:
        state["mode"] = "new"
    return True


def _refilter_pick(state):
    """Re-apply group filter and text search to rebuild pick_filtered."""
    entries = list(state["cfg"]._sessions)
    entries = _filter_pick_by_groups(entries, state.get("pick_group_filter"))
    if state["pick_query"]:
        entries = _filter_config_sessions(entries, state["pick_query"])
    state["pick_filtered"], state["pick_running_count"] = _sort_pick_entries(
        entries, state["pick_running"]
    )
    state["pick_selected"] = min(
        state["pick_selected"], max(0, len(state["pick_filtered"]) - 1)
    )


def handle_pick_mode(key, state):
    """Handle key events for launch-from-config picker mode."""
    if state["mode"] != "pick":
        return False

    if state["pick_search_active"]:
        if key == 27:
            state["pick_search_active"] = False
            state["pick_query"] = ""
            state["pick_cursor"] = 0
            _refilter_pick(state)
            return True
        if key in (10, 13, curses.KEY_ENTER):
            state["pick_search_active"] = False
            return True
        if key in (curses.KEY_UP, ord("k"), ord("K")) and state["pick_filtered"]:
            state["pick_selected"] = (state["pick_selected"] - 1) % len(
                state["pick_filtered"]
            )
            return True
        if key in (curses.KEY_DOWN, ord("j"), ord("J")) and state["pick_filtered"]:
            state["pick_selected"] = (state["pick_selected"] + 1) % len(
                state["pick_filtered"]
            )
            return True
        if key in (curses.KEY_BACKSPACE, 127, 8):
            if state["pick_cursor"] > 0:
                state["pick_query"] = (
                    state["pick_query"][: state["pick_cursor"] - 1]
                    + state["pick_query"][state["pick_cursor"] :]
                )
                state["pick_cursor"] -= 1
                _refilter_pick(state)
            return True
        if key == curses.KEY_LEFT:
            state["pick_cursor"] = max(0, state["pick_cursor"] - 1)
            return True
        if key == curses.KEY_RIGHT:
            state["pick_cursor"] = min(
                len(state["pick_query"]), state["pick_cursor"] + 1
            )
            return True
        if 32 <= key < 127:
            state["pick_query"] = (
                state["pick_query"][: state["pick_cursor"]]
                + chr(key)
                + state["pick_query"][state["pick_cursor"] :]
            )
            state["pick_cursor"] += 1
            _refilter_pick(state)
            return True
        return True

    if state.get("pick_group_filter_mode") and ord("1") <= key <= ord("9"):
        groups = sorted(
            {s.get("group", "") or "(ungrouped)" for s in state["cfg"]._sessions}
        )
        idx = key - ord("1")
        if idx < len(groups):
            name = groups[idx]
            pgf = state.get("pick_group_filter")
            if pgf is None:
                pgf = set()
                state["pick_group_filter"] = pgf
            if name in pgf:
                pgf.discard(name)
                if not pgf:
                    state["pick_group_filter"] = None
            else:
                pgf.add(name)
            _refilter_pick(state)
        return True

    if key in (ord("q"), ord("Q"), 27):
        if state.get("pick_group_filter_mode") and key == 27:
            state["pick_group_filter_mode"] = False
            state["pick_group_filter"] = None
            _refilter_pick(state)
        elif state["pick_query"] and key == 27:
            state["pick_query"] = ""
            state["pick_cursor"] = 0
            _refilter_pick(state)
        elif state.get("pick_group_filter") is not None and key == 27:
            state["pick_group_filter"] = None
            _refilter_pick(state)
        else:
            state["mode"] = "list"
    elif key == ord("/"):
        state["pick_search_active"] = True
        state["pick_query"] = ""
        state["pick_cursor"] = 0
        state["pick_filtered"], state["pick_running_count"] = _sort_pick_entries(
            list(state["cfg"]._sessions), state["pick_running"]
        )
    elif key == ord(" ") and state["pick_filtered"]:
        idx = state["pick_selected"]
        if idx >= state["pick_running_count"]:
            title = state["pick_filtered"][idx]["title"]
            state.setdefault("pick_marked", set()).symmetric_difference_update({title})
        state["pick_selected"] = min(idx + 1, len(state["pick_filtered"]) - 1)
    elif key == ord("U") and state.get("pick_marked"):
        state["pick_marked"].clear()
    elif key in (curses.KEY_UP, ord("k"), ord("K")) and state["pick_filtered"]:
        state["pick_selected"] = (state["pick_selected"] - 1) % len(
            state["pick_filtered"]
        )
    elif key in (curses.KEY_DOWN, ord("j"), ord("J")) and state["pick_filtered"]:
        state["pick_selected"] = (state["pick_selected"] + 1) % len(
            state["pick_filtered"]
        )
    elif key == ord("i") and state["multi_instance"]:
        state["pick_target_instance"] = _cycle_instance(
            state["pick_target_instance"], state["instance_labels"]
        )
    elif key == ord("g"):
        groups = sorted(
            {s.get("group", "") or "(ungrouped)" for s in state["cfg"]._sessions}
        )
        if len(groups) > 1:
            state["pick_group_filter_mode"] = not state.get(
                "pick_group_filter_mode", False
            )
            if not state["pick_group_filter_mode"]:
                # Exiting filter mode — keep current filter active
                pass
        else:
            state["msg"] = "Only one group in config"
            state["msg_frames"] = 20
    elif key == ord("O") and state["pick_filtered"]:
        state["mode"] = "override"
        state["override_source"] = "pick"
        current_dir = state["pick_filtered"][state["pick_selected"]].get(
            "config_dir", ""
        )
        if not current_dir:
            current_dir = (
                state["cfg"]
                ._accounts.get(
                    state["pick_filtered"][state["pick_selected"]]["account"], {}
                )
                .get("config_dir", "")
            )
        dirs = state["cfg"]._unique_config_dirs()
        state["override_selected"] = 0
        for i, d in enumerate(dirs):
            if d == current_dir:
                state["override_selected"] = i
                break
    elif key in (10, 13, curses.KEY_ENTER) and state["pick_filtered"]:
        target_socket = (
            state["instance_socket_map"].get(
                state["pick_target_instance"], state["home_socket"]
            )
            if state["multi_instance"]
            else state["home_socket"]
        )
        if state.get("pick_marked"):
            for entry in state["pick_filtered"]:
                if entry["title"] not in state["pick_marked"]:
                    continue
                loc = state["compute_insert_location"](
                    target_socket, entry["account"], entry["title"]
                )
                state["do_launch"](
                    target_socket,
                    entry["title"],
                    entry["dir"],
                    entry["account"],
                    entry["flags"],
                    entry["pw"],
                    location=loc,
                    env_vars=entry.get("env", ""),
                    group=entry.get("group", ""),
                )
        else:
            entry = state["pick_filtered"][state["pick_selected"]]
            loc = state["compute_insert_location"](
                target_socket, entry["account"], entry["title"]
            )
            state["do_launch"](
                target_socket,
                entry["title"],
                entry["dir"],
                entry["account"],
                entry["flags"],
                entry["pw"],
                location=loc,
                env_vars=entry.get("env", ""),
                group=entry.get("group", ""),
            )
        state["should_exit"] = True

    return True
