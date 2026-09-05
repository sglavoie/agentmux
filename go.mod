module github.com/sglavoie/agentmux

go 1.26.2

require (
	charm.land/bubbletea/v2 v2.0.9
	charm.land/lipgloss/v2 v2.0.3
	github.com/BurntSushi/toml v1.6.0
	github.com/charmbracelet/x/ansi v0.11.8
	github.com/mattn/go-runewidth v0.0.24
	github.com/rivo/uniseg v0.4.7
	github.com/spf13/cobra v1.10.2
	golang.org/x/sys v0.47.0
)

require (
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	// Bubble Tea v2.0.9 predates Ultraviolet's full-line repaint for rows with
	// wide or width-disputed graphemes (b2b0f8d), which prevents emoji drift.
	github.com/charmbracelet/ultraviolet v0.0.0-20260901015450-b2b0f8d1567b // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/sync v0.22.0 // indirect
)
