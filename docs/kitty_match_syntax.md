# Kitty Match Expression Syntax

Source: https://sw.kovidgoyal.net/kitty/remote-control/

## Overview

Many `kitten @` commands accept a `--match` flag to target specific windows or tabs. The match expression uses a `field:query` syntax, and expressions can be combined with Boolean operators.

## Tab Match Fields

When used with tab-level commands (`close-tab`, `focus-tab`, `set-tab-title`, `set-tab-color`):

- `id`: tab identifier (numeric)
- `index`: tab position in the current OS window (numeric, 0-based)
- `title`: tab title (regex)
- `window_id`: ID of a window within the tab (numeric)
- `window_title`: title of a window within the tab (regex)
- `pid`: process ID of a window in the tab (numeric)
- `cwd`: current working directory of a window (regex)
- `cmdline`: command line of a window's process (regex)
- `env`: environment variable of a window's process, format `env:NAME=VALUE` (regex on value)
- `var`: user variable set via `--var` at launch, format `var:NAME=VALUE` (regex on value)
- `state`: tab state, one of `active`, `focused`, `needs_attention`, `parent_active`, `parent_focused`, `focused_os_window`
- `session`: tabs created in a specific session
- `recent`: recently active tabs (numeric, 0 = most recent)

## Window Match Fields

Window-level commands support the same fields plus `num` (window number within tab) and `neighbor` (spatial relationship).

## Query Types

Numeric fields (`id`, `index`, `pid`, `window_id`, `recent`) interpret the query as a number. Negative values count from the highest value downward.

Text fields (`title`, `window_title`, `cwd`, `cmdline`) interpret the query as a Python-style regular expression.

The special value `all` matches everything.

## Boolean Operators

Combine expressions with `and`, `or`, `not`, and parentheses:

```
not state:focused and state:parent_focused
title:^Work or title:^Personal
(var:cc_account=work or var:cc_account=personal) and not state:focused
```

## Usage in cc-manager

The project primarily uses `var:cc_account` matching to identify managed tabs by account name. This relies on the `--var cc_account=NAME` flag passed to `kitten @ launch` when creating session tabs.
