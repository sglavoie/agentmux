#!/usr/bin/env python3
"""Keybinding quick-reference overlay for cc-manager-kitty.

Displays all ccm-managed keybindings (Kitty-global and dashboard-internal)
in a scrollable full-screen curses overlay with less-like navigation and
search.  Dismiss with q or Escape.
"""

from cc_keybindings_bootstrap import prepare_cc_dashboard_imports

prepare_cc_dashboard_imports()


def main(args=None):
    from cc_keybindings_app import main as run_main

    run_main(args)


if __name__ == "__main__":
    main()
