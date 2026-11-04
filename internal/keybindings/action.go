package keybindings

// Action describes a key shown in the workspace or tmux reference.
type Action struct {
	Key         string
	Description string
	Section     string
	AmxCommand  string
	LaunchType  string
}
