package backend

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// OpInput is writing input to a pane's program without a terminal attached.
// It is deliberately absent from Operations: that list is what every backend
// is asked about, and input belongs only to a backend that holds the pane
// itself. A backend that can do it implements Typist and reports OpInput.
const OpInput Operation = "input"

// Key names one key press from the small vocabulary an agent's prompts are
// answered with. It is not a backend's key syntax: an adapter maps each name
// to its own spelling, and a name outside the vocabulary never reaches one.
type Key string

const (
	KeyEnter    Key = "enter"
	KeyEscape   Key = "escape"
	KeyTab      Key = "tab"
	KeyShiftTab Key = "shift-tab"
	KeyUp       Key = "up"
	KeyDown     Key = "down"
	KeyLeft     Key = "left"
	KeyRight    Key = "right"
	KeySpace    Key = "space"
	KeyYes      Key = "y"
	KeyNo       Key = "n"
)

// namedKeys lists the keys that are not digits, in the order help prints them.
var namedKeys = []Key{
	KeyEnter, KeyEscape, KeyTab, KeyShiftTab, KeyUp, KeyDown, KeyLeft, KeyRight, KeySpace, KeyYes, KeyNo,
}

// KeyNames is every accepted key name: the named keys, then the digits.
func KeyNames() []string {
	names := make([]string, 0, len(namedKeys)+10)
	for _, k := range namedKeys {
		names = append(names, string(k))
	}
	for d := '0'; d <= '9'; d++ {
		names = append(names, string(d))
	}
	return names
}

// ParseKey reads one key name, case-insensitively.
func ParseKey(name string) (Key, error) {
	k := Key(strings.ToLower(strings.TrimSpace(name)))
	if k.Valid() {
		return k, nil
	}
	return "", fmt.Errorf("unknown key %q (one of %s)", name, strings.Join(KeyNames(), ", "))
}

// Valid reports whether k is in the vocabulary.
func (k Key) Valid() bool {
	if len(k) == 1 && k[0] >= '0' && k[0] <= '9' {
		return true
	}
	for _, named := range namedKeys {
		if k == named {
			return true
		}
	}
	return false
}

// InputMaxBytes bounds the text of one Input.
const InputMaxBytes = 64 * 1024

// Input is one thing to write to a pane: text, pasted as a terminal pastes
// it, or a sequence of key presses. Exactly one of the two is set.
type Input struct {
	// Text is pasted as one unit. It may span lines; it never carries
	// control characters, which could end the paste and be read as keys.
	Text string
	// Submit presses Enter after Text, as its own key press.
	Submit bool
	// Keys are pressed in order.
	Keys []Key
}

// ErrInvalidInput matches every Input a backend refuses before writing.
var ErrInvalidInput = errors.New("backend: invalid input")

// Validate rejects an input that is empty, mixes text with keys, is too
// large, or carries anything a pane could read as something other than what
// was meant.
func (in Input) Validate() error {
	switch {
	case in.Text == "" && len(in.Keys) == 0:
		return fmt.Errorf("%w: nothing to send", ErrInvalidInput)
	case in.Text != "" && len(in.Keys) > 0:
		return fmt.Errorf("%w: text and keys cannot be sent together", ErrInvalidInput)
	case in.Submit && in.Text == "":
		return fmt.Errorf("%w: only text is submitted; press enter as a key", ErrInvalidInput)
	}
	for _, k := range in.Keys {
		if !k.Valid() {
			return fmt.Errorf("%w: unknown key %q", ErrInvalidInput, string(k))
		}
	}
	if in.Text == "" {
		return nil
	}
	if len(in.Text) > InputMaxBytes {
		return fmt.Errorf("%w: text is %d bytes, more than %d", ErrInvalidInput, len(in.Text), InputMaxBytes)
	}
	if !utf8.ValidString(in.Text) {
		return fmt.Errorf("%w: text is not valid UTF-8", ErrInvalidInput)
	}
	if strings.TrimSpace(in.Text) == "" {
		return fmt.Errorf("%w: text is blank", ErrInvalidInput)
	}
	for _, r := range in.Text {
		if r == '\n' || r == '\t' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return fmt.Errorf("%w: text carries a control character (U+%04X)", ErrInvalidInput, r)
		}
	}
	return nil
}

// Typist writes input to a pane's program. It is what answering or prompting
// an agent with no terminal attached needs; the caller has already decided
// that this pane, now, is the one to write to.
//
// generation, when not empty, is the backend instance the caller decided on:
// the input is written only while the backend is still that instance, so a
// pane ID a restarted backend reused never receives it. A write refused that
// way returns ErrStaleGeneration, and nothing reached the pane.
type Typist interface {
	Send(ref Ref, generation string, input Input) error
}

// ErrStaleGeneration reports input that was not written because the backend
// is no longer the instance the caller named.
var ErrStaleGeneration = errors.New("backend: instance is not the generation named")
