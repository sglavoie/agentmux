"""Rendering helpers for hooks dashboard modes."""

from cc_dashboard.views import (
    draw_footer,
    draw_hooks_add_form,
    draw_hooks_delete_confirm,
    draw_hooks_event_view,
    draw_hooks_example,
    draw_hooks_info,
    draw_hooks_matcher_picker,
    draw_hooks_sync_confirm,
    draw_hooks_view,
)


def _draw_hooks_event(stdscr, state):
    draw_hooks_event_view(
        stdscr,
        state["hooks_account"],
        state["cfg"]._hooks_global,
        state["cfg"]._hooks_overrides,
        state["hooks_drift"].get(state["hooks_account"], "missing"),
        state["hooks_event_selected"],
    )


def render_hooks_modes(stdscr, state):
    """Render hooks management modes."""
    mode = state["mode"]

    if mode == "hooks":
        draw_hooks_view(
            stdscr,
            state["cfg"]._accounts,
            state["hooks_drift"],
            state["cfg"]._hooks_global,
            state["cfg"]._hooks_overrides,
            state["hooks_selected"],
        )
        return True

    if mode == "hooks_event":
        _draw_hooks_event(stdscr, state)
        return True

    if mode == "hooks_delete_confirm":
        _draw_hooks_event(stdscr, state)
        draw_hooks_delete_confirm(stdscr, state.get("hooks_delete_label", ""))
        draw_footer(stdscr, "hooks_delete_confirm")
        return True

    if mode == "hooks_sync_confirm":
        draw_hooks_sync_confirm(stdscr, state["hooks_sync_scope"])
        return True

    if mode == "hooks_add":
        from cc_dashboard.loop_modes_hooks import _get_hooks_add_form_state

        _draw_hooks_event(stdscr, state)
        draw_hooks_add_form(
            stdscr,
            state["hooks_account"],
            state["hooks_target_event"],
            _get_hooks_add_form_state(state),
            editing=state.get("hooks_add_editing", False),
        )
        draw_footer(stdscr, "hooks_add")
        return True

    if mode == "hooks_matcher_pick":
        from cc_dashboard.loop_modes_hooks import _get_hooks_add_form_state

        _draw_hooks_event(stdscr, state)
        draw_hooks_add_form(
            stdscr,
            state["hooks_account"],
            state["hooks_target_event"],
            _get_hooks_add_form_state(state),
            editing=state.get("hooks_add_editing", False),
        )
        draw_hooks_matcher_picker(
            stdscr,
            state.get("hooks_matcher_options", []),
            state.get("hooks_matcher_selected", 0),
            state.get("hooks_matcher_scroll", 0),
        )
        draw_footer(stdscr, "hooks_matcher_pick")
        return True

    if mode == "hooks_info":
        _draw_hooks_event(stdscr, state)
        draw_hooks_info(
            stdscr,
            state.get("hooks_info_handler", {}),
            state.get("hooks_info_matcher"),
            state.get("hooks_info_event", ""),
        )
        draw_footer(stdscr, "hooks_info")
        return True

    if mode == "hooks_example":
        _draw_hooks_event(stdscr, state)
        draw_hooks_example(stdscr, state.get("hooks_example_event", ""))
        draw_footer(stdscr, "hooks_example")
        return True

    return False
