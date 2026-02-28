"""Headless kitten to atomically reorder tabs.

Usage:
    kitty @ kitten cc_reorder_tabs.py [--focus=TAB_ID] TAB_ID1 TAB_ID2 ...

Each TAB_ID is a Kitty tab ID (integer).  The tabs are reordered to match the
given sequence in a single atomic call, avoiding the sequential focus+move
approach.
"""

import contextlib
import importlib
from collections.abc import Callable
from typing import Any


def main(args: list[str]) -> None:
    pass


try:
    from kittens.tui.handler import result_handler  # type: ignore[import-untyped]
except ImportError:

    def result_handler(*_args: Any, **_kwargs: Any) -> Callable[[Any], Any]:
        def _decorator(func: Any) -> Any:
            return func

        return _decorator


@result_handler(no_ui=True)
def handle_result(
    args: list[str], answer: str, target_window_id: int, boss: Any
) -> None:
    tab_args = args[1:]  # skip the kitten script name
    focus_id = None
    tab_ids = []
    for arg in tab_args:
        if arg.startswith("--focus="):
            with contextlib.suppress(ValueError):
                focus_id = int(arg.split("=", 1)[1])
        else:
            with contextlib.suppress(ValueError):
                tab_ids.append(int(arg))
    if not tab_ids:
        return
    tm = boss.active_tab_manager
    if tm is not None:
        tab_map = {tab.id: tab for tab in tm.tabs}
        ordered = []
        seen = set()
        for tid in tab_ids:
            if tid in tab_map:
                ordered.append(tab_map[tid])
                seen.add(tid)
        for tab in tm.tabs:
            if tab.id not in seen:
                ordered.append(tab)
        # Reorder by adjacent swaps, syncing the C backend each time
        # (same approach as TabManager.move_tab in Kitty 0.45.0)
        try:
            fast_data_types = importlib.import_module("kitty.fast_data_types")
            swap_tabs = fast_data_types.swap_tabs
        except ImportError:

            def swap_tabs(*_args: Any, **_kwargs: Any) -> None:
                return None

        id_to_target = {tab.id: i for i, tab in enumerate(ordered)}
        for target_pos in range(len(tm.tabs)):
            cur_pos = next(
                j
                for j in range(target_pos, len(tm.tabs))
                if id_to_target.get(tm.tabs[j].id) == target_pos
            )
            while cur_pos > target_pos:
                tm.tabs[cur_pos], tm.tabs[cur_pos - 1] = (
                    tm.tabs[cur_pos - 1],
                    tm.tabs[cur_pos],
                )
                swap_tabs(tm.os_window_id, cur_pos - 1, cur_pos)
                cur_pos -= 1
        tm.mark_tab_bar_dirty()
    if focus_id is not None:
        boss.call_remote_control(None, ("focus-tab", f"--match=id:{focus_id}"))
