"""Config picker handlers for dashboard form modes."""

import curses

from cc_dashboard.filters import _session_groups, _sort_pick_entries
from cc_dashboard.loop_modes_forms_shared import _get_pick_entry, _refilter_pick
from cc_dashboard.views import _cycle_instance


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
        all_groups: set[str] = set()
        for s in state["cfg"]._sessions:
            all_groups.update(_session_groups(s))
        groups = sorted(all_groups)
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
            list(state["cfg"]._sessions), state["pick_running"], state["sort_mode"]
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
        pick_all_groups: set[str] = set()
        for s in state["cfg"]._sessions:
            pick_all_groups.update(_session_groups(s))
        groups = sorted(pick_all_groups)
        if len(groups) > 1:
            state["pick_group_filter_mode"] = not state.get(
                "pick_group_filter_mode", False
            )
        else:
            state["msg"] = "Only one group in config"
            state["msg_frames"] = 20
    elif key == ord("O") and state["pick_filtered"]:
        entry = _get_pick_entry(state)
        if entry is None:
            return True
        state["mode"] = "override"
        state["override_source"] = "pick"
        current_dir = entry.get("config_dir", "")
        if not current_dir:
            current_dir = (
                state["cfg"]._accounts.get(entry["account"], {}).get("config_dir", "")
            )
        dirs = state["cfg"]._unique_config_dirs()
        state["override_selected"] = 0
        for i, d in enumerate(dirs):
            if d == current_dir:
                state["override_selected"] = i
                break
    elif key == ord("o") and state["pick_filtered"]:
        entry = _get_pick_entry(state)
        if entry is None:
            return True
        target_socket = (
            state["instance_socket_map"].get(
                state["pick_target_instance"], state["home_socket"]
            )
            if state["multi_instance"]
            else state["home_socket"]
        )
        loc = state["compute_insert_location"](
            target_socket, entry["account"], entry["title"]
        )
        state["open_terminal"](
            target_socket, entry["dir"], entry["account"], location=loc
        )
        state["should_exit"] = True
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
                    groups=entry.get("groups", []),
                    mute=entry.get("mute", ""),
                    commands=entry.get("commands", []),
                )
        else:
            entry = _get_pick_entry(state)
            if entry is None:
                return True
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
                groups=entry.get("groups", []),
                mute=entry.get("mute", ""),
                commands=entry.get("commands", []),
            )
        state["should_exit"] = True

    return True
