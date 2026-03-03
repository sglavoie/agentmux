"""Color constants, initialization, and account color-pair lookup."""

import curses

from cc_dashboard import config as _cfg
from cc_dashboard.config import PALETTE
from cc_dashboard.config_data import PALETTE_NAMES

# Fixed color pair IDs for UI chrome (not account-specific)
CP_NORMAL = 1
CP_HEADER = 2
CP_DIM = 3
CP_WARN = 4
CP_INPUT = 5
CP_INPUT_ACTIVE = 6
CP_ATTENTION = 7
CP_MATCH = 8
CP_BORDER = 9

# Dynamic account color pairs start at this offset.
# Each account gets two pair IDs: one normal, one selected.
_CP_ACCOUNT_BASE = 10

# RGB values for shared UI colors
SEL_BG = (40, 40, 60)
DIM_FG = (110, 110, 110)
WARN_FG = (220, 180, 50)
ATTENTION_FG = (255, 80, 60)
MATCH_BG = (60, 50, 20)

# Maps account name -> (cp_normal, cp_selected) curses color pair IDs
_account_cp: dict[str, tuple[int, int]] = {}

# Maps palette color name -> (cp_normal, cp_selected) curses color pair IDs
_palette_cp: dict[str, tuple[int, int]] = {}


def _c(r, g, b):
    """Convert 0-255 RGB to curses 0-1000 range."""
    return int(r * 1000 / 255), int(g * 1000 / 255), int(b * 1000 / 255)


def init_colors():
    """Set up curses color pairs dynamically based on loaded accounts."""
    global _account_cp, _palette_cp
    curses.start_color()
    curses.use_default_colors()

    _account_cp = {}
    _palette_cp = {}

    if curses.can_change_color() and curses.COLORS >= 256:
        # Reserve custom color slots starting at 16
        base = 16
        # Shared UI colors
        curses.init_color(base, *_c(*SEL_BG))
        curses.init_color(base + 1, *_c(*DIM_FG))
        curses.init_color(base + 2, *_c(*WARN_FG))
        curses.init_color(base + 3, *_c(*ATTENTION_FG))
        sel_bg_slot = base
        dim_slot = base + 1
        warn_slot = base + 2
        attention_slot = base + 3

        curses.init_pair(CP_NORMAL, -1, -1)
        curses.init_pair(CP_HEADER, curses.COLOR_WHITE, -1)
        curses.init_pair(CP_DIM, dim_slot, -1)
        curses.init_pair(CP_WARN, warn_slot, -1)
        curses.init_pair(CP_INPUT, -1, -1)
        curses.init_pair(CP_INPUT_ACTIVE, curses.COLOR_WHITE, sel_bg_slot)
        curses.init_pair(CP_ATTENTION, attention_slot, -1)
        curses.init_pair(CP_BORDER, curses.COLOR_GREEN, -1)

        curses.init_color(base + 4, *_c(*MATCH_BG))
        match_bg_slot = base + 4
        curses.init_pair(CP_MATCH, curses.COLOR_WHITE, match_bg_slot)

        # Dynamically allocate a color slot + two pair IDs per account
        color_slot = base + 5
        pair_id = _CP_ACCOUNT_BASE
        for name in _cfg._account_names:
            color_name = _cfg._accounts[name]["color"]
            _, _, text_fg = PALETTE.get(color_name, PALETTE["gray"])
            curses.init_color(color_slot, *_c(*text_fg))
            curses.init_pair(pair_id, color_slot, -1)  # normal
            curses.init_pair(pair_id + 1, color_slot, sel_bg_slot)  # selected
            _account_cp[name] = (pair_id, pair_id + 1)
            color_slot += 1
            pair_id += 2

        # Allocate color pairs for all palette colors not already covered by accounts
        acct_color_to_cp: dict[str, tuple[int, int]] = {}
        for name in _cfg._account_names:
            c = _cfg._accounts[name]["color"]
            if c not in acct_color_to_cp:
                acct_color_to_cp[c] = _account_cp[name]

        for pname in PALETTE_NAMES:
            if pname in acct_color_to_cp:
                _palette_cp[pname] = acct_color_to_cp[pname]
            else:
                _, _, text_fg = PALETTE.get(pname, PALETTE["gray"])
                curses.init_color(color_slot, *_c(*text_fg))
                curses.init_pair(pair_id, color_slot, -1)
                curses.init_pair(pair_id + 1, color_slot, sel_bg_slot)
                _palette_cp[pname] = (pair_id, pair_id + 1)
                color_slot += 1
                pair_id += 2
    else:
        # Fallback: cycle through a few basic curses colors
        _FALLBACK_COLORS = [
            curses.COLOR_GREEN,
            curses.COLOR_CYAN,
            curses.COLOR_RED,
            curses.COLOR_YELLOW,
            curses.COLOR_MAGENTA,
            curses.COLOR_WHITE,
        ]

        curses.init_pair(CP_NORMAL, -1, -1)
        curses.init_pair(CP_HEADER, curses.COLOR_WHITE, -1)
        curses.init_pair(CP_DIM, curses.COLOR_WHITE, -1)
        curses.init_pair(CP_WARN, curses.COLOR_YELLOW, -1)
        curses.init_pair(CP_INPUT, -1, -1)
        curses.init_pair(CP_INPUT_ACTIVE, curses.COLOR_WHITE, curses.COLOR_BLACK)
        curses.init_pair(CP_ATTENTION, curses.COLOR_RED, -1)
        curses.init_pair(CP_BORDER, curses.COLOR_GREEN, -1)
        curses.init_pair(CP_MATCH, curses.COLOR_WHITE, curses.COLOR_BLACK)

        pair_id = _CP_ACCOUNT_BASE
        for i, name in enumerate(_cfg._account_names):
            fg = _FALLBACK_COLORS[i % len(_FALLBACK_COLORS)]
            curses.init_pair(pair_id, fg, -1)
            curses.init_pair(pair_id + 1, fg, curses.COLOR_BLACK)
            _account_cp[name] = (pair_id, pair_id + 1)
            pair_id += 2

        acct_color_to_cp_fb: dict[str, tuple[int, int]] = {}
        for name in _cfg._account_names:
            c = _cfg._accounts[name]["color"]
            if c not in acct_color_to_cp_fb:
                acct_color_to_cp_fb[c] = _account_cp[name]

        for j, pname in enumerate(PALETTE_NAMES):
            if pname in acct_color_to_cp_fb:
                _palette_cp[pname] = acct_color_to_cp_fb[pname]
            else:
                fg = _FALLBACK_COLORS[j % len(_FALLBACK_COLORS)]
                curses.init_pair(pair_id, fg, -1)
                curses.init_pair(pair_id + 1, fg, curses.COLOR_BLACK)
                _palette_cp[pname] = (pair_id, pair_id + 1)
                pair_id += 2


def _cp_for_account(account, selected=False):
    """Return the curses color pair ID for a given account name."""
    cp_normal, cp_sel = _account_cp.get(account, (CP_NORMAL, CP_NORMAL))
    return cp_sel if selected else cp_normal


def _cp_for_palette_color(color_name, selected=False):
    """Return the curses color pair ID for a palette color name."""
    cp_normal, cp_sel = _palette_cp.get(color_name, (CP_NORMAL, CP_NORMAL))
    return cp_sel if selected else cp_normal
