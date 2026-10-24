module github.com/sglavoie/agentmux

go 1.26.2

require (
	charm.land/bubbletea/v2 v2.1.0
	charm.land/lipgloss/v2 v2.0.6
	github.com/BurntSushi/toml v1.6.0
	github.com/charmbracelet/x/ansi v0.11.9
	github.com/charmbracelet/x/term v0.2.2
	github.com/mattn/go-runewidth v0.0.31
	github.com/rivo/uniseg v0.4.7
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.10
	golang.org/x/sys v0.49.0
)

require (
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	// Bubble Tea v2.0.9 predates Ultraviolet's full-line repaint for rows with
	// wide or width-disputed graphemes (b2b0f8d), which prevents emoji drift.
	github.com/charmbracelet/ultraviolet v0.0.0-20261023060817-a45b7c86333f // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/xo/terminfo v1.2.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
)
