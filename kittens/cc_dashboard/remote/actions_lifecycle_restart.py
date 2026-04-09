"""Restart helpers for dashboard sessions."""

import time

from cc_dashboard.remote.actions_launch import compute_insert_location, do_launch
from cc_dashboard.remote.actions_tabs import do_close
from cc_dashboard.remote.actions_lifecycle_detached import _run_detached_steps
from cc_dashboard.remote.actions_lifecycle_shared import (
    _build_launch_steps,
    _prepare_relaunch_session,
)
from cc_dashboard.remote.actions_sorting import do_sort_tabs


def _spawn_detached_restart(
    socket,
    tab_id,
    title,
    cwd,
    account,
    flags="",
    pw_override="",
    config_dir_override=None,
    env_vars="",
    color_override="",
    group="",
    mute="",
    commands=None,
):
    """Spawn a fully detached subprocess to close and relaunch a session."""
    steps = _build_launch_steps(
        close_socket=socket,
        target_socket=socket,
        tab_id=tab_id,
        title=title,
        cwd=cwd,
        account=account,
        flags=flags,
        pw_override=pw_override,
        config_dir_override=config_dir_override,
        env_vars=env_vars,
        color_override=color_override,
        group=group,
        exclude_tab_id_for_location=tab_id,
        mute=mute,
        commands=commands,
    )
    _run_detached_steps(steps)


def do_restart(
    socket,
    session,
    flags="",
    pw_override="",
    config_dir_override=None,
    host_tab_id=None,
    env_vars="",
    commands=None,
):
    """Close and relaunch a session with the same title, directory, and account."""
    relaunch = _prepare_relaunch_session(
        session,
        commands=commands,
        config_dir_override=config_dir_override,
    )

    if host_tab_id is not None and relaunch["tab_id"] == host_tab_id:
        _spawn_detached_restart(
            socket,
            relaunch["tab_id"],
            relaunch["title"],
            relaunch["cwd"],
            relaunch["account"],
            flags,
            pw_override,
            relaunch["config_dir_override"],
            env_vars,
            color_override=relaunch["cc_color"],
            group=relaunch["group"],
            mute=relaunch["mute"],
            commands=relaunch["commands"],
        )
        return True

    ok = do_close(socket, relaunch["tab_id"])
    if not ok:
        return False
    time.sleep(0.3)
    location = compute_insert_location(socket, relaunch["account"], relaunch["title"])
    ok = do_launch(
        socket,
        relaunch["title"],
        relaunch["cwd"],
        relaunch["account"],
        flags,
        pw_override,
        location=location,
        config_dir_override=relaunch["config_dir_override"],
        env_vars=env_vars,
        color_override=relaunch["cc_color"],
        group=relaunch["group"],
        mute=relaunch["mute"],
        commands=relaunch["commands"],
    )
    if ok:
        do_sort_tabs(socket)
    return ok
