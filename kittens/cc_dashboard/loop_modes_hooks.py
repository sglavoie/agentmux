"""Mode handlers for hooks management screens in the dashboard loop."""

import contextlib
import curses

from cc_dashboard import config as _cfg
from cc_dashboard.config import _FULL_TYPE_EVENTS, HOOK_EVENT_META
from cc_dashboard.remote import run_hooks_sync
from cc_dashboard.views import _hooks_event_rows
from cc_dashboard.views_overlays import (
    _FIELD_ASYNC,
    _FIELD_MATCHER,
    _FIELD_TIMEOUT,
    _FIELD_TYPE,
    _FIELD_VALUE,
    _HOOK_TYPES,
)


def _init_hooks_add_state(state, event_name):
    """Initialize form state for hooks_add mode."""
    state["hooks_target_event"] = event_name
    state["hooks_add_type"] = "command"
    state["hooks_add_matcher"] = ""
    state["hooks_add_value"] = ""
    state["hooks_add_async"] = True
    state["hooks_add_timeout"] = ""
    state["hooks_add_field"] = _FIELD_VALUE  # Start on value for fast common case
    state["hooks_add_cursor"] = 0
    state["hooks_add_editing"] = False
    state["hooks_add_edit_idx"] = None


def _get_hooks_add_form_state(state):
    """Build the form_state dict from dashboard state for rendering."""
    return {
        "type": state.get("hooks_add_type", "command"),
        "matcher": state.get("hooks_add_matcher", ""),
        "value": state.get("hooks_add_value", ""),
        "async_": state.get("hooks_add_async", True),
        "timeout": state.get("hooks_add_timeout", ""),
        "field": state.get("hooks_add_field", _FIELD_VALUE),
        "cursor": state.get("hooks_add_cursor", 0),
    }


def _hooks_add_visible_fields(event_name, hook_type):
    """Return ordered list of visible field indices for the current form state."""
    meta = HOOK_EVENT_META.get(event_name, {})
    has_matcher = meta.get("matcher_desc") is not None
    supports_all_types = event_name in _FULL_TYPE_EVENTS

    fields = []
    if supports_all_types:
        fields.append(_FIELD_TYPE)
    if has_matcher:
        fields.append(_FIELD_MATCHER)
    fields.append(_FIELD_VALUE)
    if hook_type == "command":
        fields.append(_FIELD_ASYNC)
    fields.append(_FIELD_TIMEOUT)
    return fields


def _hooks_add_active_text_key(state):
    """Return the state key for the currently active text field, or None."""
    field = state.get("hooks_add_field", _FIELD_VALUE)
    if field == _FIELD_MATCHER:
        return "hooks_add_matcher"
    if field == _FIELD_VALUE:
        return "hooks_add_value"
    if field == _FIELD_TIMEOUT:
        return "hooks_add_timeout"
    return None


def _hooks_add_submit(state):
    """Build a hook handler dict and add/update it, then return to hooks_event."""
    hook_type = state["hooks_add_type"]
    value = state["hooks_add_value"].strip()
    if not value:
        state["mode"] = "hooks_event"
        return

    handler = {"type": hook_type}
    if hook_type == "command":
        handler["command"] = value
    elif hook_type == "http":
        handler["url"] = value
    else:
        handler["prompt"] = value

    if hook_type == "command":
        handler["async"] = state["hooks_add_async"]

    timeout_str = state["hooks_add_timeout"].strip()
    if timeout_str:
        with contextlib.suppress(ValueError):
            handler["timeout"] = int(timeout_str)

    matcher = state["hooks_add_matcher"].strip() or None

    if state.get("hooks_add_editing") and state.get("hooks_add_edit_idx") is not None:
        event_name, group_idx = state["hooks_add_edit_idx"]
        _cfg.update_extra_hook(
            state["hooks_account"], event_name, group_idx, handler, matcher
        )
        state["msg"] = "Hook updated"
    else:
        _cfg.add_extra_hook(
            state["hooks_account"], state["hooks_target_event"], handler, matcher
        )
        state["msg"] = "Hook added"

    state["hooks_drift"][state["hooks_account"]] = _cfg._detect_drift(
        state["hooks_account"]
    )
    state["msg_frames"] = 15
    state["mode"] = "hooks_event"


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
        event_rows = _hooks_event_rows(
            state["hooks_account"], _cfg._hooks_overrides, _cfg._hooks_global
        )
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
            target_event = (
                row[1]
                if row[0] in ("event", "extra", "builtin")
                else state.get("hooks_target_event", "PreToolUse")
            )
            _init_hooks_add_state(state, target_event)
            state["mode"] = "hooks_add"
        elif (
            key == ord("e")
            and event_rows
            and state["hooks_event_selected"] < len(event_rows)
        ):
            row = event_rows[state["hooks_event_selected"]]
            if row[0] == "extra":
                _, event_name, group_idx, _cmd_str, handler, matcher = row
                _init_hooks_add_state(state, event_name)
                state["hooks_add_editing"] = True
                state["hooks_add_edit_idx"] = (event_name, group_idx)
                state["hooks_add_type"] = handler.get("type", "command")
                if handler.get("type") == "command":
                    state["hooks_add_value"] = handler.get("command", "")
                elif handler.get("type") == "http":
                    state["hooks_add_value"] = handler.get("url", "")
                else:
                    state["hooks_add_value"] = handler.get("prompt", "")
                state["hooks_add_matcher"] = matcher or ""
                state["hooks_add_async"] = handler.get("async", True)
                timeout = handler.get("timeout")
                state["hooks_add_timeout"] = str(timeout) if timeout is not None else ""
                state["hooks_add_cursor"] = len(state["hooks_add_value"])
                state["mode"] = "hooks_add"
        elif (
            key == ord("i")
            and event_rows
            and state["hooks_event_selected"] < len(event_rows)
        ):
            row = event_rows[state["hooks_event_selected"]]
            if row[0] == "extra":
                _, event_name, _group_idx, _cmd_str, handler, matcher = row
                state["hooks_info_handler"] = handler
                state["hooks_info_matcher"] = matcher
                state["hooks_info_event"] = event_name
                state["mode"] = "hooks_info"
            elif row[0] == "builtin":
                (
                    _,
                    _event_name,
                    _cmd_idx,
                    cmd_str,
                    _is_disabled,
                    is_async,
                    type_str,
                ) = row
                state["hooks_info_handler"] = {
                    "type": type_str or "command",
                    "command": cmd_str,
                    "async": is_async,
                }
                state["hooks_info_matcher"] = None
                state["hooks_info_event"] = _event_name
                state["mode"] = "hooks_info"
        elif (
            key == ord("d")
            and event_rows
            and state["hooks_event_selected"] < len(event_rows)
        ):
            row = event_rows[state["hooks_event_selected"]]
            if row[0] == "extra":
                _, event_name, cmd_idx, _cmd_str, _handler, _matcher = row
                state["hooks_delete_event"] = event_name
                state["hooks_delete_idx"] = cmd_idx
                state["hooks_delete_label"] = _cmd_str[:60]
                state["mode"] = "hooks_delete_confirm"
        elif (
            key == ord("E")
            and event_rows
            and state["hooks_event_selected"] < len(event_rows)
        ):
            row = event_rows[state["hooks_event_selected"]]
            state["hooks_example_event"] = row[1]
            state["mode"] = "hooks_example"
        elif key == ord("s"):
            state["hooks_sync_scope"] = state["hooks_account"]
            state["mode"] = "hooks_sync_confirm"
        elif key in (27, ord("q")):
            state["mode"] = "hooks"
        return True

    if mode == "hooks_delete_confirm":
        if key in (ord("y"), ord("Y")):
            _cfg.remove_extra_hook(
                state["hooks_account"],
                state["hooks_delete_event"],
                state["hooks_delete_idx"],
            )
            state["hooks_drift"][state["hooks_account"]] = _cfg._detect_drift(
                state["hooks_account"]
            )
            new_rows = _hooks_event_rows(
                state["hooks_account"], _cfg._hooks_overrides, _cfg._hooks_global
            )
            state["hooks_event_selected"] = min(
                state["hooks_event_selected"], max(0, len(new_rows) - 1)
            )
            state["msg"] = "Hook removed"
            state["msg_frames"] = 15
        state["mode"] = "hooks_event"
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

    if mode == "hooks_matcher_pick":
        options = state.get("hooks_matcher_options", [])
        sel = state.get("hooks_matcher_selected", 0)
        max_visible = 8
        if key in (ord("j"), curses.KEY_DOWN) and options:
            state["hooks_matcher_selected"] = min(sel + 1, len(options) - 1)
            if (
                state["hooks_matcher_selected"]
                >= state["hooks_matcher_scroll"] + max_visible
            ):
                state["hooks_matcher_scroll"] = (
                    state["hooks_matcher_selected"] - max_visible + 1
                )
        elif key in (ord("k"), curses.KEY_UP) and options:
            state["hooks_matcher_selected"] = max(sel - 1, 0)
            if state["hooks_matcher_selected"] < state["hooks_matcher_scroll"]:
                state["hooks_matcher_scroll"] = state["hooks_matcher_selected"]
        elif key in (10, 13, curses.KEY_ENTER) and options:
            state["hooks_add_matcher"] = options[state["hooks_matcher_selected"]]
            state["hooks_add_cursor"] = len(state["hooks_add_matcher"])
            state["mode"] = "hooks_add"
        elif key in (27, ord("q")):
            state["mode"] = "hooks_add"
        return True

    if mode == "hooks_add":
        return _handle_hooks_add(key, state)

    if mode == "hooks_info":
        if key in (27, ord("q"), ord("i")):
            state["mode"] = "hooks_event"
        return True

    if mode == "hooks_example":
        if key in (27, ord("q"), ord("E")):
            state["mode"] = "hooks_event"
        return True

    return False


def _handle_hooks_add(key, state):
    """Handle key events for the hooks_add multi-field form."""
    event_name = state.get("hooks_target_event", "PreToolUse")
    hook_type = state.get("hooks_add_type", "command")
    visible = _hooks_add_visible_fields(event_name, hook_type)
    active_field = state.get("hooks_add_field", _FIELD_VALUE)

    # Ensure active field is in visible set
    if active_field not in visible:
        state["hooks_add_field"] = visible[0] if visible else _FIELD_VALUE
        active_field = state["hooks_add_field"]

    if key == 27:
        state["mode"] = "hooks_event"
        return True

    # Tab / Shift-Tab: cycle fields (or open matcher picker)
    if key == 9:  # Tab
        if active_field == _FIELD_MATCHER:
            examples_str = HOOK_EVENT_META.get(event_name, {}).get("matcher_examples")
            if examples_str:
                options = examples_str.split("|")
                state["hooks_matcher_options"] = options
                state["hooks_matcher_selected"] = 0
                state["hooks_matcher_scroll"] = 0
                state["mode"] = "hooks_matcher_pick"
                return True
        idx = visible.index(active_field) if active_field in visible else 0
        state["hooks_add_field"] = visible[(idx + 1) % len(visible)]
        state["hooks_add_cursor"] = _cursor_for_field(state, state["hooks_add_field"])
        return True
    if key == curses.KEY_BTAB or key == 353:  # Shift-Tab
        idx = visible.index(active_field) if active_field in visible else 0
        state["hooks_add_field"] = visible[(idx - 1) % len(visible)]
        state["hooks_add_cursor"] = _cursor_for_field(state, state["hooks_add_field"])
        return True

    # Up/Down arrows: navigate fields (same as Shift-Tab/Tab)
    if key == curses.KEY_UP:
        idx = visible.index(active_field) if active_field in visible else 0
        state["hooks_add_field"] = visible[(idx - 1) % len(visible)]
        state["hooks_add_cursor"] = _cursor_for_field(state, state["hooks_add_field"])
        return True
    if key == curses.KEY_DOWN:
        idx = visible.index(active_field) if active_field in visible else 0
        state["hooks_add_field"] = visible[(idx + 1) % len(visible)]
        state["hooks_add_cursor"] = _cursor_for_field(state, state["hooks_add_field"])
        return True

    # Enter: submit
    if key in (10, 13, curses.KEY_ENTER):
        _hooks_add_submit(state)
        return True

    # Type field: left/right or space to cycle
    if active_field == _FIELD_TYPE:
        if key in (curses.KEY_LEFT, curses.KEY_RIGHT, ord(" ")):
            from cc_dashboard.config import _FULL_TYPE_EVENTS

            if event_name in _FULL_TYPE_EVENTS:
                idx = _HOOK_TYPES.index(hook_type) if hook_type in _HOOK_TYPES else 0
                if key == curses.KEY_LEFT:
                    idx = (idx - 1) % len(_HOOK_TYPES)
                else:
                    idx = (idx + 1) % len(_HOOK_TYPES)
                state["hooks_add_type"] = _HOOK_TYPES[idx]
        return True

    # Async field: space to toggle
    if active_field == _FIELD_ASYNC:
        if key == ord(" "):
            state["hooks_add_async"] = not state["hooks_add_async"]
        return True

    # Text fields: matcher, value, timeout
    text_key = _hooks_add_active_text_key(state)
    if text_key:
        _handle_text_input(key, state, text_key, "hooks_add_cursor")
    return True


def _cursor_for_field(state, field):
    """Return the appropriate cursor position when switching to a field."""
    if field == _FIELD_MATCHER:
        return len(state.get("hooks_add_matcher", ""))
    if field == _FIELD_VALUE:
        return len(state.get("hooks_add_value", ""))
    if field == _FIELD_TIMEOUT:
        return len(state.get("hooks_add_timeout", ""))
    return 0


def _handle_text_input(key, state, text_key, cursor_key):
    """Handle text input keys for a state[text_key] / state[cursor_key] pair."""
    value = state[text_key]
    cursor = state[cursor_key]

    if key in (curses.KEY_BACKSPACE, 127, 8):
        if cursor > 0:
            state[text_key] = value[: cursor - 1] + value[cursor:]
            state[cursor_key] = cursor - 1
    elif key == curses.KEY_LEFT:
        state[cursor_key] = max(0, cursor - 1)
    elif key == curses.KEY_RIGHT:
        state[cursor_key] = min(len(value), cursor + 1)
    elif key == curses.KEY_HOME or key == 1:
        state[cursor_key] = 0
    elif key == curses.KEY_END or key == 5:
        state[cursor_key] = len(value)
    elif 32 <= key < 127:
        state[text_key] = value[:cursor] + chr(key) + value[cursor:]
        state[cursor_key] = cursor + 1
