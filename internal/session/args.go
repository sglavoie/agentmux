package session

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/sglavoie/agentmux/internal/config"
)

func resolveArgv(sess config.Session, acc config.Account, defaults config.Defaults) []string {
	if args := sess.EffectiveArgs(); len(args) > 0 {
		return args
	}
	if len(acc.Args) > 0 {
		return acc.Args
	}
	return defaults.EffectiveArgs()
}

func shellQuoteArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuoteWord(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuoteWord(s string) string {
	if isSafeShellWord(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func isSafeShellWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		if strings.ContainsRune("@%_+=:,./-", r) {
			continue
		}
		return false
	}
	return true
}

func splitShellArgs(raw string) ([]string, error) {
	var (
		args         []string
		current      strings.Builder
		quoted       bool
		inSingle     bool
		inDouble     bool
		escaping     bool
		tokenStarted bool
	)

	flush := func() {
		args = append(args, current.String())
		current.Reset()
		tokenStarted = false
		quoted = false
	}

	for _, r := range raw {
		switch {
		case escaping:
			current.WriteRune(r)
			escaping = false
			tokenStarted = true
		case inSingle:
			if r == '\'' {
				inSingle = false
				quoted = true
				continue
			}
			current.WriteRune(r)
			tokenStarted = true
		case inDouble:
			switch r {
			case '"':
				inDouble = false
				quoted = true
			case '\\':
				escaping = true
				tokenStarted = true
			default:
				current.WriteRune(r)
				tokenStarted = true
			}
		default:
			switch {
			case unicode.IsSpace(r):
				if tokenStarted || quoted {
					flush()
				}
			case r == '\'':
				inSingle = true
				tokenStarted = true
			case r == '"':
				inDouble = true
				tokenStarted = true
			case r == '\\':
				escaping = true
				tokenStarted = true
			default:
				current.WriteRune(r)
				tokenStarted = true
			}
		}
	}

	switch {
	case escaping:
		return nil, fmt.Errorf("unterminated escape sequence")
	case inSingle:
		return nil, fmt.Errorf("unterminated single-quoted string")
	case inDouble:
		return nil, fmt.Errorf("unterminated double-quoted string")
	case tokenStarted || quoted:
		flush()
	}

	return args, nil
}
