# Session 11 acceptance drivers

The scripts that produced the logs beside this directory, kept as they ran.
They assume an isolated world rooted at `/tmp/amxs11`:

- `bin/amx` built from the checkout, `bin/claude` = `standin-claude.sh`,
  `bin/edit-standin` = `standin-editor.sh`, `bin/asl` linked to the installed
  `asl`;
- `home/.config/agent-mux/config.toml` = `config.toml`, a hand-written Claude
  transcript under `home/.claude-a/projects/`, a plan directory under
  `home/plans/`, `proj/alpha` as a Git repository, and a three-file ASL plan in
  `aslplan/` with an empty repository in `aslrepo/`;
- `env.sh` sourced by every script: it points `HOME`, `TMPDIR`, and `PATH` at
  that world and unsets every Kitty, tmux, ASL, and AgentMux variable.

`drive_portable.sh LOG` drives the dashboard through an observer tmux
(`obs.sh`, its own socket) started inside a stock Ghostty window by
`ghostty_entry.sh`; `ghostty_entry_ov.sh` and `ghostty_direct.sh` are the
overview and no-observer entries. `drive_kitty.sh LEGACY_LOG TMUX_LOG` drives a
separate Kitty instance over `kitten @`; that Kitty was started with
`--config NONE -o allow_remote_control=yes -o env=ZDOTDIR=/tmp/amxs11/zdot
--listen-on unix:/tmp/amxs11/kitty.sock`, with `kitty-zshenv` as
`zdot/.zshenv`, because Kitty starts launched tabs through `login(1)` and that
resets `HOME`. `reset.sh` returns the world to empty between runs.
