"""Create and edit session form handlers for dashboard modes."""

import curses
import os

from cc_dashboard.views import (
    _cycle_account,
    _cycle_instance,
    _cycle_pw,
    _scan_directories,
)


def handle_new_mode(stdscr, key, state):
    """Handle key events for new/edit session form mode."""
    if state["mode"] != "new":
        return False

    _base_keys = ["title", "dir", "account", "flags", "pw", "env", "groups", "commands"]
    if state["form_mode"] == "edit":
        field_keys = _base_keys
    else:
        field_keys = [*_base_keys, "save"]
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
                    edit_groups = [
                        g.strip()
                        for g in state["new_fields"].get("groups", "").split(",")
                        if g.strip()
                    ]
                    raw_cmds_str = state["new_fields"].get("commands", "")
                    edit_commands = [
                        c.strip() for c in raw_cmds_str.split(";;") if c.strip()
                    ]
                    updated = state["cfg"].update_session(
                        state["edit_original_title"],
                        t,
                        d,
                        a,
                        fl,
                        pw_val,
                        env=edit_env,
                        groups=edit_groups,
                        commands=edit_commands,
                    )
                    if updated:
                        running_titles = {s["title"] for s in state["sessions"]}
                        stripped_running = {
                            state["strip_override_marker"](rt) for rt in running_titles
                        }
                        if state["edit_original_title"] in stripped_running:
                            state["msg"] = "Config updated (restart session to apply)"
                        else:
                            state["msg"] = "Config updated"
                    else:
                        state["msg"] = "Session not found in config"
                    state["msg_frames"] = 20
                    state["mode"] = "list"
            else:
                target_socket = state["home_socket"]
                if state["multi_instance"] and "instance" in state["new_fields"]:
                    target_socket = state["instance_socket_map"].get(
                        state["new_fields"]["instance"], state["home_socket"]
                    )
                if t and d:
                    if any(s["title"] == t for s in state["sessions"]):
                        state["msg"] = "A session with that title is already running"
                        state["msg_frames"] = 20
                        return True
                    if state["new_fields"].get("save") == "yes" and state[
                        "cfg"
                    ]._find_config_session(t):
                        state["msg"] = "Title already exists in config"
                        state["msg_frames"] = 20
                        return True
                    expanded_d = os.path.expanduser(d)
                    raw_cmds_str = state["new_fields"].get("commands", "")
                    cmds_list = [
                        c.strip() for c in raw_cmds_str.split(";;") if c.strip()
                    ]
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
                            "groups": [
                                g.strip()
                                for g in state["new_fields"]
                                .get("groups", "")
                                .split(",")
                                if g.strip()
                            ],
                            "commands": cmds_list,
                            "save": state["new_fields"].get("save"),
                        }
                        return True
                    env_val = state["new_fields"].get("env", "").strip()
                    groups_val = [
                        g.strip()
                        for g in state["new_fields"].get("groups", "").split(",")
                        if g.strip()
                    ]
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
                        groups=groups_val,
                        commands=cmds_list,
                    )
                    if state["new_fields"].get("save") == "yes":
                        state["cfg"].append_session(
                            t,
                            d,
                            a,
                            fl,
                            pw_val,
                            env=env_val,
                            groups=groups_val,
                            commands=cmds_list,
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
            groups=p.get("groups", []),
            commands=p.get("commands", []),
        )
        if p.get("save") == "yes":
            state["cfg"].append_session(
                p["title"],
                p["dir"],
                p["account"],
                p["flags"],
                p["pw"],
                env=p["env"],
                groups=p.get("groups", []),
                commands=p.get("commands", []),
            )
        state["should_exit"] = True
    else:
        state["mode"] = "new"
    return True
