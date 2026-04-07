"""Confirmation mode handlers for dashboard session operations."""

from cc_dashboard.loop_helpers import (
    _resolve_session_runtime_options,
)


def handle_confirm_modes(key, state):
    """Handle key events for confirmation modes (close/restart/save)."""
    mode = state["mode"]

    if mode == "confirm":
        if key in (ord("y"), ord("Y")) and state["filtered"]:
            target = state["filtered"][state["selected"]]
            target_socket = target.get("socket", state["home_socket"])
            if (
                state["host_tab_id"] is not None
                and target["tab_id"] == state["host_tab_id"]
            ):
                state["spawn_detached_close"](target_socket, target["tab_id"])
                state["should_exit"] = True
                return True

            ok = state["do_close"](target_socket, target["tab_id"])
            if ok:
                closed_id = target["tab_id"]
                state["sessions"] = [
                    s for s in state["sessions"] if s["tab_id"] != closed_id
                ]
                state["filtered"] = [
                    s for s in state["filtered"] if s["tab_id"] != closed_id
                ]
                state["marked"].discard(closed_id)
                state["selected"] = min(
                    state["selected"], max(0, len(state["filtered"]) - 1)
                )
            if ok:
                state["msg"] = "Session closed"
            else:
                inst = target.get("instance_label", target_socket)
                state["msg"] = f"Instance {inst} is no longer available"
            state["msg_frames"] = 20
        state["mode"] = "list"
        return True

    if mode == "confirm_restart":
        if key in (ord("y"), ord("Y")) and state["filtered"]:
            sess = state["filtered"][state["selected"]]
            is_host = (
                state["host_tab_id"] is not None
                and sess["tab_id"] == state["host_tab_id"]
            )
            r_flags, r_pw, r_env, r_commands = _resolve_session_runtime_options(sess)
            sess_socket = sess.get("socket", state["home_socket"])
            ok = state["do_restart"](
                sess_socket,
                sess,
                flags=r_flags,
                pw_override=r_pw,
                host_tab_id=state["host_tab_id"],
                env_vars=r_env,
                commands=r_commands,
            )
            if is_host:
                state["should_exit"] = True
                return True

            state["sessions"] = state["query_all_sessions"](
                managed_only=(not state["show_all"])
            )
            state["refilter"]()
            if ok:
                state["msg"] = "Session restarted"
            else:
                inst = sess.get("instance_label", sess_socket)
                state["msg"] = f"Instance {inst} is no longer available"
            state["msg_frames"] = 20
        state["mode"] = "list"
        return True

    if mode == "confirm_batch_close":
        if key in (ord("y"), ord("Y")) and state["batch_targets"]:
            has_host = state["host_tab_id"] is not None
            targets_sorted = sorted(
                state["batch_targets"],
                key=lambda s: (
                    1 if has_host and s["tab_id"] == state["host_tab_id"] else 0
                ),
            )
            closed = 0
            closed_ids = set()
            for t in targets_sorted:
                t_socket = t.get("socket", state["home_socket"])
                if has_host and t["tab_id"] == state["host_tab_id"]:
                    state["spawn_detached_close"](t_socket, t["tab_id"])
                    state["should_exit"] = True
                    return True
                if state["do_close"](t_socket, t["tab_id"]):
                    closed += 1
                    closed_ids.add(t["tab_id"])

            state["marked"].clear()
            if closed_ids:
                state["sessions"] = [
                    s for s in state["sessions"] if s["tab_id"] not in closed_ids
                ]
                state["filtered"] = [
                    s for s in state["filtered"] if s["tab_id"] not in closed_ids
                ]
                state["selected"] = min(
                    state["selected"], max(0, len(state["filtered"]) - 1)
                )
            state["msg"] = f"Closed {closed} session(s)"
            state["msg_frames"] = 20
        state["mode"] = "list"
        return True

    if mode == "confirm_batch_restart":
        if key in (ord("y"), ord("Y")) and state["batch_targets"]:
            has_host = state["host_tab_id"] is not None
            targets_sorted = sorted(
                state["batch_targets"],
                key=lambda s: (
                    1 if has_host and s["tab_id"] == state["host_tab_id"] else 0
                ),
            )
            restarted = 0
            for t in targets_sorted:
                r_flags, r_pw, r_env, r_commands = _resolve_session_runtime_options(t)
                t_socket = t.get("socket", state["home_socket"])
                is_host = has_host and t["tab_id"] == state["host_tab_id"]
                ok = state["do_restart"](
                    t_socket,
                    t,
                    flags=r_flags,
                    pw_override=r_pw,
                    host_tab_id=state["host_tab_id"],
                    env_vars=r_env,
                    commands=r_commands,
                )
                if is_host:
                    state["should_exit"] = True
                    return True
                if ok:
                    restarted += 1

            state["marked"].clear()
            state["sessions"] = state["query_all_sessions"](
                managed_only=(not state["show_all"])
            )
            state["refilter"]()
            state["msg"] = f"Restarted {restarted} session(s)"
            state["msg_frames"] = 20
        state["mode"] = "list"
        return True

    if mode == "confirm_batch_clone":
        if key in (ord("y"), ord("Y")) and state["batch_targets"]:
            all_title_dicts = [{"title": s["title"]} for s in state["sessions"]]
            cloned = 0
            for t in state["batch_targets"]:
                new_title = state["generate_clone_title"](all_title_dicts, t["title"])
                all_title_dicts.append({"title": new_title})
                r_flags, r_pw, r_env, r_commands = _resolve_session_runtime_options(t)
                t_socket = t.get("socket", state["home_socket"])
                loc = state["compute_insert_location"](
                    t_socket, t["account"], new_title
                )
                clone_group = t.get("group", "")
                ok = state["do_launch"](
                    t_socket,
                    new_title,
                    t["cwd"],
                    t["account"],
                    r_flags,
                    r_pw,
                    location=loc,
                    env_vars=r_env,
                    color_override=t.get("cc_color", ""),
                    group=clone_group,
                    commands=r_commands,
                )
                if ok:
                    cloned += 1

            state["marked"].clear()
            state["sessions"] = state["query_all_sessions"](
                managed_only=(not state["show_all"])
            )
            state["refilter"]()
            failed = len(state["batch_targets"]) - cloned
            if failed > 0:
                state["msg"] = f"Cloned {cloned}, failed {failed} session(s)"
            else:
                state["msg"] = f"Cloned {cloned} session(s)"
            state["msg_frames"] = 20
        state["mode"] = "list"
        return True

    if mode == "confirm_save":
        if key in (ord("y"), ord("Y")):
            try:
                count, _ = state["cfg"].save_sessions(state["sessions"])
                fname = state["cfg"]._sessions_config_filename()
                state["msg"] = f"Saved {count} session(s) to {fname}"
                state["msg_frames"] = 25
            except OSError as e:
                state["msg"] = f"Save failed: {e}"
                state["msg_frames"] = 30
        state["mode"] = "list"
        return True

    return False


__all__ = ["handle_confirm_modes"]
