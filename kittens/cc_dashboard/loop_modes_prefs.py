"""Mode handlers for the preferences editor in the dashboard loop."""

import contextlib
import curses
import os
import tempfile
import tomllib

from cc_dashboard.config_core import (
    _defaults_config_path,
    _import_toml_writer,
)
from cc_dashboard.filters import SORT_MODES
from cc_dashboard.loop_modes_hooks import _handle_text_input
from cc_dashboard.remote import do_open_editor

PREF_FIELDS = [
    # [defaults] section
    {
        "key": "dashboard_refresh",
        "section": "defaults",
        "label": "Dashboard refresh (sec)",
        "type": "int",
        "min_val": 0,
        "max_val": 300,
    },
    {
        "key": "sort_sessions",
        "section": "defaults",
        "label": "Default sort mode",
        "type": "choice",
        "choices": list(SORT_MODES),
    },
    {
        "key": "claude_flags",
        "section": "defaults",
        "label": "Default Claude flags",
        "type": "string",
        "ext_edit": True,
    },
    {
        "key": "project_window",
        "section": "defaults",
        "label": "Project window",
        "type": "choice",
        "choices": ["", "horizontal", "vertical"],
    },
    {
        "key": "notes_editor",
        "section": "defaults",
        "label": "Notes editor",
        "type": "string",
    },
    # [notifications] section
    {
        "key": "error",
        "section": "notifications",
        "label": "Notify: error",
        "type": "bool",
    },
    {
        "key": "permission",
        "section": "notifications",
        "label": "Notify: permission",
        "type": "bool",
    },
    {
        "key": "stop",
        "section": "notifications",
        "label": "Notify: stop",
        "type": "bool",
    },
    {
        "key": "idle",
        "section": "notifications",
        "label": "Notify: idle",
        "type": "bool",
    },
    {
        "key": "idle_alert_minutes",
        "section": "notifications",
        "label": "Idle alert (min)",
        "type": "int",
        "min_val": 0,
        "max_val": 999,
    },
    {
        "key": "min_secs",
        "section": "notifications",
        "label": "Min notify interval (sec)",
        "type": "int",
        "min_val": 0,
        "max_val": 9999,
    },
    {
        "key": "ignore_patterns",
        "section": "notifications",
        "label": "Ignore patterns",
        "type": "string_array",
    },
]


def init_prefs_state(state):
    """Read defaults.toml and populate prefs state fields."""
    path = _defaults_config_path()
    data = {}
    if os.path.isfile(path):
        with open(path, "rb") as f:
            data = tomllib.load(f)

    defaults = data.get("defaults", {})
    notifications = data.get("notifications", {})

    values = {}
    for field in PREF_FIELDS:
        section = defaults if field["section"] == "defaults" else notifications
        raw = section.get(field["key"])
        if field["type"] == "bool":
            values[field["key"]] = bool(raw) if raw is not None else False
        elif field["type"] == "int":
            values[field["key"]] = int(raw) if raw is not None else 0
        elif field["type"] == "choice":
            values[field["key"]] = str(raw) if raw is not None else ""
        elif field["type"] == "string":
            values[field["key"]] = str(raw) if raw is not None else ""
        elif field["type"] == "string_array":
            if isinstance(raw, list):
                values[field["key"]] = [str(s) for s in raw]
            else:
                values[field["key"]] = []

    state["prefs_values"] = values
    state["prefs_selected"] = 0
    state["prefs_editing"] = False
    state["prefs_cursor"] = 0
    state["prefs_edit_value"] = ""


def handle_prefs_mode(key, state):
    """Handle key events for the prefs mode."""
    selected = state["prefs_selected"]
    editing = state["prefs_editing"]
    field = PREF_FIELDS[selected]
    ftype = field["type"]

    # When editing a text/int field
    if editing:
        if key == 27:  # Esc: cancel edit
            state["prefs_editing"] = False
            return
        if key in (10, 13, curses.KEY_ENTER):  # Enter: confirm edit
            _apply_edit(state, field)
            state["prefs_editing"] = False
            _save_prefs(state)
            return
        # Text input
        _handle_text_input(key, state, "prefs_edit_value", "prefs_cursor")
        return

    # Not editing
    if key in (ord("j"), curses.KEY_DOWN):
        state["prefs_selected"] = min(selected + 1, len(PREF_FIELDS) - 1)
    elif key in (ord("k"), curses.KEY_UP):
        state["prefs_selected"] = max(selected - 1, 0)
    elif key in (27, ord("q")):
        _cleanup_ext_edit(state)
        state["mode"] = "list"
    elif ftype == "bool" and key in (ord(" "), 10, 13, curses.KEY_ENTER):
        state["prefs_values"][field["key"]] = not state["prefs_values"][field["key"]]
        _save_prefs(state)
    elif ftype == "choice" and key in (ord(" "), 9):  # Space or Tab
        choices = field["choices"]
        cur = state["prefs_values"][field["key"]]
        idx = choices.index(cur) if cur in choices else 0
        state["prefs_values"][field["key"]] = choices[(idx + 1) % len(choices)]
        _save_prefs(state)
    elif ftype == "choice" and key == curses.KEY_BTAB:  # Shift-Tab: cycle backward
        choices = field["choices"]
        cur = state["prefs_values"][field["key"]]
        idx = choices.index(cur) if cur in choices else 0
        state["prefs_values"][field["key"]] = choices[(idx - 1) % len(choices)]
        _save_prefs(state)
    elif ftype in ("int", "string") and key in (10, 13, curses.KEY_ENTER) and not field.get("ext_edit"):
        val = state["prefs_values"][field["key"]]
        state["prefs_edit_value"] = str(val)
        state["prefs_cursor"] = len(state["prefs_edit_value"])
        state["prefs_editing"] = True
    elif key in (10, 13, curses.KEY_ENTER) and (ftype == "string_array" or field.get("ext_edit")):
        ext = state.get("prefs_ext_edit_path", "")
        if ext and os.path.isfile(ext) and state.get("prefs_ext_edit_field") == field["key"]:
            # Phase 2: read back from temp file, apply, and save
            with open(ext) as f:
                content = f.read()
            if ftype == "string_array":
                state["prefs_values"][field["key"]] = [
                    ln.strip() for ln in content.splitlines() if ln.strip()
                ]
            else:
                state["prefs_values"][field["key"]] = content.strip()
            _save_prefs(state)
            os.unlink(ext)
            state["prefs_ext_edit_path"] = ""
            state["prefs_ext_edit_field"] = ""
        else:
            # Phase 1: write value to temp file and open in external editor
            val = state["prefs_values"].get(field["key"], [] if ftype == "string_array" else "")
            fd, tmp = tempfile.mkstemp(suffix=".txt", prefix="ccm_prefs_")
            with os.fdopen(fd, "w") as f:
                if ftype == "string_array":
                    f.write("\n".join(val) + ("\n" if val else ""))
                else:
                    f.write(str(val) + "\n")
            do_open_editor(state["home_socket"], tmp)
            state["prefs_ext_edit_path"] = tmp
            state["prefs_ext_edit_field"] = field["key"]
            state["msg"] = "Editing in external window -- Enter to apply"
            state["msg_frames"] = 30


def handle_prefs_array_mode(key, state):
    """Handle key events for the prefs_array sub-mode."""
    items = state["prefs_array_items"]
    selected = state["prefs_array_selected"]
    editing = state["prefs_array_editing"]

    if editing:
        if key == 27:  # Esc: cancel
            state["prefs_array_editing"] = False
            return
        if key in (10, 13, curses.KEY_ENTER):  # Enter: confirm
            val = state["prefs_array_edit_value"]
            if selected < len(items):
                items[selected] = val
            else:
                items.append(val)
                state["prefs_array_selected"] = len(items) - 1
            state["prefs_array_editing"] = False
            _save_array(state)
            return
        _handle_text_input(key, state, "prefs_array_edit_value", "prefs_array_cursor")
        return

    # Not editing
    if key in (ord("j"), curses.KEY_DOWN) and items:
        state["prefs_array_selected"] = min(selected + 1, len(items) - 1)
    elif key in (ord("k"), curses.KEY_UP) and items:
        state["prefs_array_selected"] = max(selected - 1, 0)
    elif key == ord("a"):
        state["prefs_array_edit_value"] = ""
        state["prefs_array_cursor"] = 0
        state["prefs_array_selected"] = len(items)
        state["prefs_array_editing"] = True
    elif key == ord("d") and items and selected < len(items):
        items.pop(selected)
        if selected >= len(items) and items:
            state["prefs_array_selected"] = len(items) - 1
        _save_array(state)
    elif key in (ord("e"), 10, 13, curses.KEY_ENTER) and items and selected < len(items):
        state["prefs_array_edit_value"] = items[selected]
        state["prefs_array_cursor"] = len(items[selected])
        state["prefs_array_editing"] = True
    elif key in (27, ord("q")):
        state["mode"] = "prefs"


def _cleanup_ext_edit(state):
    """Remove any pending external-edit temp file."""
    ext = state.get("prefs_ext_edit_path", "")
    if ext and os.path.isfile(ext):
        os.unlink(ext)
    state["prefs_ext_edit_path"] = ""
    state["prefs_ext_edit_field"] = ""


def _apply_edit(state, field):
    """Apply an edited value from prefs_edit_value to prefs_values."""
    val = state["prefs_edit_value"].strip()
    if field["type"] == "int":
        with contextlib.suppress(ValueError):
            num = int(val)
            min_v = field.get("min_val", 0)
            max_v = field.get("max_val", 99999)
            state["prefs_values"][field["key"]] = max(min_v, min(max_v, num))
    elif field["type"] == "string":
        state["prefs_values"][field["key"]] = val


def _save_array(state):
    """Sync array items back to prefs_values and save."""
    field_key = state["prefs_array_field_key"]
    state["prefs_values"][field_key] = list(state["prefs_array_items"])
    _save_prefs(state)


def _save_prefs(state):
    """Write changed preferences to defaults.toml, preserving structure."""
    path = _defaults_config_path()
    toml_writer = _import_toml_writer()

    header = ""
    defaults = {}
    notifications = {}
    if os.path.isfile(path):
        from toml_reader import read_header_comments

        header = read_header_comments(path)
        with open(path, "rb") as f:
            data = tomllib.load(f)
        defaults = data.get("defaults", {})
        notifications = data.get("notifications", {})

    values = state["prefs_values"]
    for field in PREF_FIELDS:
        section = defaults if field["section"] == "defaults" else notifications
        val = values[field["key"]]
        # Only write non-default values to keep the file clean
        section[field["key"]] = val

    out = {}
    if defaults:
        out["defaults"] = defaults
    if notifications:
        out["notifications"] = notifications
    with open(path, "w") as f:
        f.write(toml_writer.dumps(out, header=header))
        f.write("\n")

    # Reload config so changes take effect immediately
    if "reload_all_config" in state:
        state["reload_all_config"]()
        state["msg"] = "Preferences saved"
        state["msg_frames"] = 15
