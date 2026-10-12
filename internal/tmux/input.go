package tmux

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/sglavoie/agentmux/internal/backend"
)

// tmuxKeys spells each named key as tmux reads it; a digit is its own name.
var tmuxKeys = map[backend.Key]string{
	backend.KeyEnter:    "Enter",
	backend.KeyEscape:   "Escape",
	backend.KeyTab:      "Tab",
	backend.KeyShiftTab: "BTab",
	backend.KeyUp:       "Up",
	backend.KeyDown:     "Down",
	backend.KeyLeft:     "Left",
	backend.KeyRight:    "Right",
	backend.KeySpace:    "Space",
	backend.KeyYes:      "y",
	backend.KeyNo:       "n",
}

// Send writes input to a pane's program, with no client attached and without
// changing who owns the session.
//
// Text is loaded into a buffer of its own from the client's standard input
// and pasted from there, so it is never an argument: not in a process
// listing, and not in a CommandError. The paste is bracketed when the program
// asked for bracketed paste, which is what keeps a multi-line prompt one
// prompt, and the buffer is deleted by the paste. Enter follows as a key
// press in the same client invocation, so nothing else's keys come between.
func (b *Backend) Send(ref backend.Ref, input backend.Input) error {
	if err := paneOnly(ref); err != nil {
		return err
	}
	if err := input.Validate(); err != nil {
		return err
	}
	if input.Text == "" {
		keys := []string{"send-keys", "-t", ref.ID}
		for _, k := range input.Keys {
			name, ok := tmuxKeys[k]
			if !ok {
				name = string(k)
			}
			keys = append(keys, name)
		}
		_, err := b.server.run(keys)
		return err
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("tmux: naming input buffer: %w", err)
	}
	buffer := "amx-input-" + hex.EncodeToString(suffix[:])
	chain := [][]string{
		{"load-buffer", "-b", buffer, "-"},
		{"paste-buffer", "-d", "-p", "-b", buffer, "-t", ref.ID},
	}
	if input.Submit {
		chain = append(chain, []string{"send-keys", "-t", ref.ID, "Enter"})
	}
	_, err := b.server.runChainInput(chain, []byte(input.Text))
	if err != nil {
		// A paste that failed left the text in the buffer; it must not stay
		// on the server. A buffer that was never loaded is not an error.
		_, _ = b.server.run([]string{"delete-buffer", "-b", buffer})
	}
	return err
}
