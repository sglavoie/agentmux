"""Mode handlers for hooks management screens in the dashboard loop."""

import curses

from cc_dashboard import config as _cfg
from cc_dashboard.remote import run_hooks_sync
from cc_dashboard.views import _hooks_event_rows


def handle_hooks_modes(key, state):
    """Handle key events for hooks-related modes.

    Returns True when the mode was handled.
    """
    mode = state["mode"]

    if mode == "hooks":
        account_list = list(_cfg._accounts)
        if key in (ord("j"), curses.KEY_DOWN) and account_list:
            state["hooks_selected"] = (state["hooks_selected"] + 1) % len(account_list)
        elif key in (ord("k"), curses.KEY_UP) and account_list:
            state["hooks_selected"] = (state["hooks_selected"] - 1) % len(account_list)
        elif key in (10, 13, curses.KEY_ENTER) and account_list:
            state["hooks_account"] = account_list[state["hooks_selected"]]
            state["hooks_event_selected"] = 0
            state["mode"] = "hooks_event"
        elif key == ord("s") and account_list:
            state["hooks_sync_scope"] = account_list[state["hooks_selected"]]
            state["mode"] = "hooks_sync_confirm"
        elif key == ord("S"):
            state["hooks_sync_scope"] = "all"
            state["mode"] = "hooks_sync_confirm"
        elif key in (ord("q"), 27):
            state["mode"] = "list"
        return True

    if mode == "hooks_event":
        event_rows = _hooks_event_rows(state["hooks_account"], _cfg._hooks_overrides)
        if key in (ord("j"), curses.KEY_DOWN) and event_rows:
            state["hooks_event_selected"] = (state["hooks_event_selected"] + 1) % len(
                event_rows
            )
        elif key in (ord("k"), curses.KEY_UP) and event_rows:
            state["hooks_event_selected"] = (state["hooks_event_selected"] - 1) % len(
                event_rows
            )
        elif (
            key == ord(" ")
            and event_rows
            and state["hooks_event_selected"] < len(event_rows)
        ):
            row = event_rows[state["hooks_event_selected"]]
            if row[0] == "event":
                _, event_name, _ = row
                enabled = _cfg.toggle_hook_event(state["hooks_account"], event_name)
                state["hooks_drift"][state["hooks_account"]] = _cfg._detect_drift(
                    state["hooks_account"]
                )
                state_name = "enabled" if enabled else "disabled"
                state["msg"] = f"{event_name}: {state_name}"
                state["msg_frames"] = 15
        elif (
            key == ord("a")
            and event_rows
            and state["hooks_event_selected"] < len(event_rows)
        ):
            row = event_rows[state["hooks_event_selected"]]
            if row[0] == "event" or row[0] == "extra":
                state["hooks_target_event"] = row[1]
            state["hooks_cmd_value"] = ""
            state["hooks_cmd_cursor"] = 0
            state["mode"] = "hooks_cmd_input"
        elif (
            key == ord("d")
            and event_rows
            and state["hooks_event_selected"] < len(event_rows)
        ):
            row = event_rows[state["hooks_event_selected"]]
            if row[0] == "extra":
                _, event_name, cmd_idx, _cmd_str = row
                _cfg.remove_extra_command(state["hooks_account"], event_name, cmd_idx)
                state["hooks_drift"][state["hooks_account"]] = _cfg._detect_drift(
                    state["hooks_account"]
                )
                new_rows = _hooks_event_rows(
                    state["hooks_account"], _cfg._hooks_overrides
                )
                state["hooks_event_selected"] = min(
                    state["hooks_event_selected"], max(0, len(new_rows) - 1)
                )
                state["msg"] = "Command removed"
                state["msg_frames"] = 15
        elif key == ord("s"):
            state["hooks_sync_scope"] = state["hooks_account"]
            state["mode"] = "hooks_sync_confirm"
        elif key == 27:
            state["mode"] = "hooks"
        return True

    if mode == "hooks_sync_confirm":
        if key in (ord("y"), ord("Y")):
            if state["hooks_sync_scope"] == "all":
                ok, output = run_hooks_sync()
                scope_msg = "all accounts"
            else:
                ok, output = run_hooks_sync(state["hooks_sync_scope"])
                scope_msg = state["hooks_sync_scope"]
            state["hooks_drift"] = _cfg._detect_all_drift()
            if ok:
                state["msg"] = f"Hooks synced for {scope_msg}"
            else:
                state["msg"] = f"Sync failed: {output[:60]}"
            state["msg_frames"] = 25
            state["mode"] = "hooks"
        else:
            if (
                state["hooks_sync_scope"] == "all"
                or state["hooks_sync_scope"] not in _cfg._accounts
            ):
                state["mode"] = "hooks"
            else:
                state["mode"] = "hooks_event"
        return True

    if mode == "hooks_cmd_input":
        if key == 27:
            state["mode"] = "hooks_event"
        elif key in (10, 13, curses.KEY_ENTER):
            cmd = state["hooks_cmd_value"].strip()
            if cmd:
                _cfg.add_extra_command(
                    state["hooks_account"], state["hooks_target_event"], cmd
                )
                state["hooks_drift"][state["hooks_account"]] = _cfg._detect_drift(
                    state["hooks_account"]
                )
                state["msg"] = "Command added"
                state["msg_frames"] = 15
            state["mode"] = "hooks_event"
        elif key in (curses.KEY_BACKSPACE, 127, 8):
            if state["hooks_cmd_cursor"] > 0:
                state["hooks_cmd_value"] = (
                    state["hooks_cmd_value"][: state["hooks_cmd_cursor"] - 1]
                    + state["hooks_cmd_value"][state["hooks_cmd_cursor"] :]
                )
                state["hooks_cmd_cursor"] -= 1
        elif key == curses.KEY_LEFT:
            state["hooks_cmd_cursor"] = max(0, state["hooks_cmd_cursor"] - 1)
        elif key == curses.KEY_RIGHT:
            state["hooks_cmd_cursor"] = min(
                len(state["hooks_cmd_value"]), state["hooks_cmd_cursor"] + 1
            )
        elif key == curses.KEY_HOME or key == 1:
            state["hooks_cmd_cursor"] = 0
        elif key == curses.KEY_END or key == 5:
            state["hooks_cmd_cursor"] = len(state["hooks_cmd_value"])
        elif 32 <= key < 127:
            state["hooks_cmd_value"] = (
                state["hooks_cmd_value"][: state["hooks_cmd_cursor"]]
                + chr(key)
                + state["hooks_cmd_value"][state["hooks_cmd_cursor"] :]
            )
            state["hooks_cmd_cursor"] += 1
        return True

    return False
