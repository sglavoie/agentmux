"""Mode handlers for delete workflow screens in the dashboard loop."""

import curses
import os
import shutil

from cc_dashboard import config as _cfg
from cc_dashboard.filters import _filter_config_sessions, _sort_pick_entries
from cc_dashboard.loop_helpers import _build_delete_entries
from cc_dashboard.remote.git_ops import check_git_safety, do_remove_worktree


def handle_delete_mode(key, state):
    """Handle key events for delete mode.

    Returns True when the mode was handled.
    """
    if state["mode"] != "delete":
        return False

    if state["delete_step"] == "confirm_config":
        if key == ord("y"):
            title = state["delete_target_entry"]["title"]
            _cfg.remove_session(title)
            state["delete_step"] = "confirm_data"
            expanded = os.path.expanduser(state["delete_target_entry"]["dir"])
            if (
                os.path.isdir(expanded)
                and state["delete_target_entry"].get("type_tag") == "[repo]"
            ):
                state["delete_warnings"] = check_git_safety(expanded)
            else:
                state["delete_warnings"] = []
            if (
                not os.path.isdir(expanded)
                or state["delete_target_entry"].get("type_tag") == "[n/a]"
            ):
                state["msg"] = f'Removed "{title}" from config (directory not found)'
                state["msg_frames"] = 30
                state["delete_entries"] = _build_delete_entries(_cfg._sessions)
                (
                    state["delete_filtered"],
                    state["delete_running_count"],
                ) = _sort_pick_entries(state["delete_entries"], state["delete_running"])
                state["delete_selected"] = min(
                    state["delete_selected"], max(0, len(state["delete_filtered"]) - 1)
                )
                state["delete_step"] = "select"
        else:
            state["delete_step"] = "select"
        return True

    if state["delete_step"] == "confirm_data":
        if key == ord("y"):
            title = state["delete_target_entry"]["title"]
            expanded = os.path.expanduser(state["delete_target_entry"]["dir"])
            type_tag = state["delete_target_entry"].get("type_tag", "[dir]")
            err = None
            if type_tag == "[worktree]":
                main_repo = state["delete_target_entry"].get("wt_main_repo", "")
                err = do_remove_worktree(main_repo, expanded)
            else:
                try:
                    shutil.rmtree(expanded)
                except OSError as exc:
                    err = str(exc)
            if err:
                state["msg"] = f"Error: {err}"
            else:
                state["msg"] = f'Deleted "{title}" data at {expanded}'
            state["msg_frames"] = 30
        else:
            state["msg"] = "Config entry removed, directory kept"
            state["msg_frames"] = 30

        state["delete_entries"] = _build_delete_entries(_cfg._sessions)
        (
            state["delete_filtered"],
            state["delete_running_count"],
        ) = _sort_pick_entries(state["delete_entries"], state["delete_running"])
        state["delete_selected"] = min(
            state["delete_selected"], max(0, len(state["delete_filtered"]) - 1)
        )
        state["delete_step"] = "select"
        return True

    if state["delete_search_active"]:
        if key == 27:
            state["delete_search_active"] = False
            state["delete_query"] = ""
            state["delete_cursor"] = 0
            (
                state["delete_filtered"],
                state["delete_running_count"],
            ) = _sort_pick_entries(state["delete_entries"], state["delete_running"])
            state["delete_selected"] = min(
                state["delete_selected"], max(0, len(state["delete_filtered"]) - 1)
            )
            return True
        if key in (10, 13, curses.KEY_ENTER):
            state["delete_search_active"] = False
            return True
        if key in (curses.KEY_UP, ord("k"), ord("K")) and state["delete_filtered"]:
            state["delete_selected"] = (state["delete_selected"] - 1) % len(
                state["delete_filtered"]
            )
            return True
        if key in (curses.KEY_DOWN, ord("j"), ord("J")) and state["delete_filtered"]:
            state["delete_selected"] = (state["delete_selected"] + 1) % len(
                state["delete_filtered"]
            )
            return True
        if key in (curses.KEY_BACKSPACE, 127, 8):
            if state["delete_cursor"] > 0:
                state["delete_query"] = (
                    state["delete_query"][: state["delete_cursor"] - 1]
                    + state["delete_query"][state["delete_cursor"] :]
                )
                state["delete_cursor"] -= 1
                state["delete_filtered"] = _filter_config_sessions(
                    state["delete_entries"], state["delete_query"]
                )
                (
                    state["delete_filtered"],
                    state["delete_running_count"],
                ) = _sort_pick_entries(
                    state["delete_filtered"], state["delete_running"]
                )
                state["delete_selected"] = min(
                    state["delete_selected"], max(0, len(state["delete_filtered"]) - 1)
                )
            return True
        if key == curses.KEY_LEFT:
            state["delete_cursor"] = max(0, state["delete_cursor"] - 1)
            return True
        if key == curses.KEY_RIGHT:
            state["delete_cursor"] = min(
                len(state["delete_query"]), state["delete_cursor"] + 1
            )
            return True
        if 32 <= key < 127:
            state["delete_query"] = (
                state["delete_query"][: state["delete_cursor"]]
                + chr(key)
                + state["delete_query"][state["delete_cursor"] :]
            )
            state["delete_cursor"] += 1
            state["delete_filtered"] = _filter_config_sessions(
                state["delete_entries"], state["delete_query"]
            )
            (
                state["delete_filtered"],
                state["delete_running_count"],
            ) = _sort_pick_entries(state["delete_filtered"], state["delete_running"])
            state["delete_selected"] = min(
                state["delete_selected"], max(0, len(state["delete_filtered"]) - 1)
            )
            return True
        return True

    if key in (ord("q"), ord("Q"), 27):
        if state["delete_query"] and key == 27:
            state["delete_query"] = ""
            state["delete_cursor"] = 0
            (
                state["delete_filtered"],
                state["delete_running_count"],
            ) = _sort_pick_entries(state["delete_entries"], state["delete_running"])
            state["delete_selected"] = min(
                state["delete_selected"], max(0, len(state["delete_filtered"]) - 1)
            )
        else:
            state["mode"] = "list"
        return True

    if key == ord("/"):
        state["delete_search_active"] = True
        state["delete_query"] = ""
        state["delete_cursor"] = 0
        (
            state["delete_filtered"],
            state["delete_running_count"],
        ) = _sort_pick_entries(state["delete_entries"], state["delete_running"])
        return True

    if key in (curses.KEY_UP, ord("k"), ord("K")) and state["delete_filtered"]:
        state["delete_selected"] = (state["delete_selected"] - 1) % len(
            state["delete_filtered"]
        )
        return True

    if key in (curses.KEY_DOWN, ord("j"), ord("J")) and state["delete_filtered"]:
        state["delete_selected"] = (state["delete_selected"] + 1) % len(
            state["delete_filtered"]
        )
        return True

    if key in (10, 13, curses.KEY_ENTER) and state["delete_filtered"]:
        state["delete_target_entry"] = dict(
            state["delete_filtered"][state["delete_selected"]]
        )
        state["delete_target_entry"]["is_running"] = (
            state["delete_target_entry"]["title"] in state["delete_running"]
        )
        state["delete_step"] = "confirm_config"
        return True

    return True
