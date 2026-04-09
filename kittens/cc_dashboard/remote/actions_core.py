"""Compatibility facade for split tab action helpers."""

from cc_dashboard.remote import actions_launch as _actions_launch
from cc_dashboard.remote import actions_tab_ui as _actions_tab_ui
from cc_dashboard.remote import actions_tabs as _actions_tabs
from cc_dashboard.remote.command import kitty_cmd

_normalize_commands = _actions_launch._normalize_commands
do_edit_notes = _actions_tab_ui.do_edit_notes
generate_clone_title = _actions_tabs.generate_clone_title


def _sync_kitty_cmd():
    """Preserve legacy monkeypatch behavior on this compatibility module."""
    _actions_launch.kitty_cmd = kitty_cmd
    _actions_tab_ui.kitty_cmd = kitty_cmd
    _actions_tabs.kitty_cmd = kitty_cmd


def compute_insert_location(*args, **kwargs):
    _sync_kitty_cmd()
    return _actions_launch.compute_insert_location(*args, **kwargs)


def do_close(*args, **kwargs):
    _sync_kitty_cmd()
    return _actions_tabs.do_close(*args, **kwargs)


def do_cross_instance_focus(*args, **kwargs):
    _sync_kitty_cmd()
    return _actions_tabs.do_cross_instance_focus(*args, **kwargs)


def do_focus(*args, **kwargs):
    _sync_kitty_cmd()
    return _actions_tabs.do_focus(*args, **kwargs)


def do_launch(*args, **kwargs):
    _sync_kitty_cmd()
    return _actions_launch.do_launch(*args, **kwargs)


def do_rename(*args, **kwargs):
    _sync_kitty_cmd()
    return _actions_tabs.do_rename(*args, **kwargs)


def do_set_color(*args, **kwargs):
    _sync_kitty_cmd()
    return _actions_tab_ui.do_set_color(*args, **kwargs)


def do_toggle_mute(*args, **kwargs):
    _sync_kitty_cmd()
    return _actions_tab_ui.do_toggle_mute(*args, **kwargs)


__all__ = [
    "_normalize_commands",
    "compute_insert_location",
    "do_close",
    "do_cross_instance_focus",
    "do_edit_notes",
    "do_focus",
    "do_launch",
    "do_rename",
    "do_set_color",
    "do_toggle_mute",
    "generate_clone_title",
    "kitty_cmd",
]
