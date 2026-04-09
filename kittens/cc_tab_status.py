#!/usr/bin/env python3
"""Tab status overlay for setting a status suffix on the current tab's title."""

from cc_tab_status_bootstrap import prepare_cc_dashboard_imports, result_handler

prepare_cc_dashboard_imports()

from cc_tab_status_app import main
from cc_tab_status_restore import handle_restore_result


@result_handler()
def handle_result(args, answer, target_window_id, boss):
    handle_restore_result(answer, boss)


if __name__ == "__main__":
    main()
