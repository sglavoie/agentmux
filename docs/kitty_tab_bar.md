# Kitty Tab Bar Configuration Reference

Source: https://sw.kovidgoyal.net/kitty/conf/

## Tab Bar Position and Style

`tab_bar_edge` controls where the tab bar appears, accepting `top` or `bottom`.

`tab_bar_style` sets the visual style. Options: `fade`, `slant`, `separator`, `powerline`, `custom`. The `custom` style allows a Python script to draw the tab bar.

`tab_bar_min_tabs` sets the minimum number of tabs before the tab bar is shown (default: 2). Set to 1 to always show it.

## Tab Title Templates

`tab_title_template` controls how inactive tab titles are rendered. It uses Python's `str.format()` syntax with variables like `{title}`, `{index}`, `{num_windows}`, `{layout_name}`, and `{tab.active_wd}`.

`active_tab_title_template` overrides the template for the active tab only. If not set, it uses `tab_title_template`.

## Tab Colors

These settings control the default tab bar colors in `kitty.conf`:

- `active_tab_foreground`: text color for the active tab
- `active_tab_background`: background color for the active tab
- `inactive_tab_foreground`: text color for inactive tabs
- `inactive_tab_background`: background color for inactive tabs
- `tab_bar_background`: background of the tab bar itself (the area behind tabs); use `none` to match the terminal background
- `tab_bar_margin_color`: color of the margin area around the tab bar

Colors are specified as hex values (e.g., `#ff0000`) or named colors.

## Per-Tab Color Overrides

The `kitten @ set-tab-color` remote control command overrides these defaults on a per-tab basis. It accepts fields: `active_bg`, `active_fg`, `inactive_bg`, `inactive_fg`. This is the mechanism cc-manager uses to apply account-specific colors to each session tab. Pass `none` to reset a field back to the default from `kitty.conf`.

## Relevance to cc-manager

The project uses `kitten @ set-tab-color` at launch time to color-code each tab based on the account's configured color. The 10-color palette (green, blue, red, orange, purple, teal, pink, yellow, gray, cyan) maps named colors to hex values for `active_bg` and `inactive_bg`. The `active_fg` and `inactive_fg` are typically set to black or white for contrast.
