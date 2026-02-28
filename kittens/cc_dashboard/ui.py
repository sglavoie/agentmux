"""Re-export shim: preserves the public API of ``cc_dashboard.ui``.

All symbols that were previously defined here have been moved to focused
sub-modules (colors, drawing, filters, views, loop).  This file re-exports
them so that existing ``from cc_dashboard.ui import ...`` statements
continue to work without modification.
"""

# ── colors ───────────────────────────────────────────────────────────────────
from cc_dashboard.colors import (  # noqa: F401
    CP_ATTENTION,
    CP_DIM,
    CP_HEADER,
    CP_INPUT,
    CP_INPUT_ACTIVE,
    CP_NORMAL,
    CP_WARN,
    _cp_for_account,
    init_colors,
)

# ── drawing ──────────────────────────────────────────────────────────────────
from cc_dashboard.drawing import (  # noqa: F401
    _display_width,
    _pad_to_width,
    safe_addstr,
    shorten_path,
)

# ── filters ──────────────────────────────────────────────────────────────────
from cc_dashboard.filters import (  # noqa: F401
    SORT_LABELS,
    SORT_MODES,
    _apply_filters,
    _apply_sort,
    _filter_by_accounts,
    _filter_config_sessions,
    _filter_sessions,
    _sort_pick_entries,
)

# ── loop ─────────────────────────────────────────────────────────────────────
from cc_dashboard.loop import (  # noqa: F401
    _session_fingerprint,
    dashboard,
)

# ── views ────────────────────────────────────────────────────────────────────
from cc_dashboard.views import (  # noqa: F401
    _FOOTER_HINTS,
    _PW_OPTIONS,
    DETAIL_PANE_HEIGHT,
    _check_idle_notifications,
    _cycle_account,
    _cycle_pw,
    draw_confirm_prompt,
    draw_detail_pane,
    draw_filter_bar,
    draw_sort_picker,
    draw_footer,
    draw_list_view,
    draw_new_form,
    draw_pick_view,
    draw_search_bar,
    draw_status_message,
)
