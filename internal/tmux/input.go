package tmux

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

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
//
// With a generation, the write is made inside an if-shell on the server's
// identity, in that same invocation, and the invocation reports which branch
// ran: there is no moment between the check and the write for a restarted
// server to slip into.
func (b *Backend) Send(ref backend.Ref, generation string, input backend.Input) error {
	if err := paneOnly(ref); err != nil {
		return err
	}
	if err := input.Validate(); err != nil {
		return err
	}
	if strings.ContainsAny(generation, "#{},'") {
		return fmt.Errorf("tmux: %q is not a server generation", generation)
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
		if generation == "" {
			_, err := b.server.run(keys)
			return err
		}
		return b.sendIn(generation, nil, [][]string{keys}, nil)
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("tmux: naming input buffer: %w", err)
	}
	buffer := "amx-input-" + hex.EncodeToString(suffix[:])
	load := []string{"load-buffer", "-b", buffer, "-"}
	write := [][]string{{"paste-buffer", "-d", "-p", "-b", buffer, "-t", ref.ID}}
	if input.Submit {
		write = append(write, []string{"send-keys", "-t", ref.ID, "Enter"})
	}
	var err error
	if generation == "" {
		_, err = b.server.runChainInput(append([][]string{load}, write...), []byte(input.Text))
	} else {
		err = b.sendIn(generation, load, write, []byte(input.Text))
	}
	if err != nil {
		// A paste that failed or was refused left the text in the buffer; it
		// must not stay on the server. A buffer that was never loaded is not
		// an error.
		_, _ = b.server.run([]string{"delete-buffer", "-b", buffer})
	}
	return err
}

// sendIn runs write only while the server is generation, after load (when
// there is one) in the same client invocation.
func (b *Backend) sendIn(generation string, load []string, write [][]string, stdin []byte) error {
	same := "#{==:#{pid}-#{start_time}," + generation + "}"
	var commands []string
	for _, cmd := range write {
		quoted := make([]string, len(cmd))
		for i, arg := range cmd {
			quoted[i] = quoteArg(arg)
		}
		commands = append(commands, strings.Join(quoted, " "))
	}
	var chain [][]string
	if load != nil {
		chain = append(chain, load)
	}
	chain = append(chain,
		[]string{"if-shell", "-F", same, strings.Join(commands, " ; ")},
		[]string{"display-message", "-p", same},
	)
	out, err := b.server.runChainInput(chain, stdin)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "1" {
		return fmt.Errorf("%w: the tmux server is not %s", backend.ErrStaleGeneration, generation)
	}
	return nil
}
