#!/usr/bin/env python3
"""Audit keybinding documentation completeness.

Compares keys handled in dashboard code against _FOOTER_HINTS to catch keys
that are handled in code but missing from footer hints. This ensures every
interactive key in the curses dashboard is discoverable via the footer.

Runs automatically as part of `just check` and `just install` (via the
check-keybindings recipe). Can also be run standalone:

    python scripts/check_keybindings.py

Exit code 0 if clean, 1 if gaps found.
"""

if __name__ == "__main__":
    from keybinding_audit import main

    raise SystemExit(main())
