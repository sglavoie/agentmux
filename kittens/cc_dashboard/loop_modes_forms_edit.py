"""Text-edit form handlers for dashboard modes."""

import curses


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
        raw_val = state["batch_group_value"].strip()
        groups_list = [g.strip() for g in raw_val.split(",") if g.strip()]
        cc_groups_str = "\x1f".join(groups_list)
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
                    f"cc_groups={cc_groups_str}",
                )
                if result and result.returncode == 0:
                    t["groups"] = groups_list
                    count += 1
        state["marked"].clear()
        state["sessions"] = state["query_all_sessions"](
            managed_only=(not state["show_all"])
        )
        state["refilter"]()
        if groups_list:
            state["msg"] = f'Set groups to "{raw_val}" for {count} session(s)'
        else:
            state["msg"] = f"Cleared groups for {count} session(s)"
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
