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

func splitShellArgs(raw string) ([]string, error) {
	var (
		args         []string
		current      strings.Builder
		quoted       bool
		inSingle     bool
		inDouble     bool
		escaping     bool
		escapeDouble bool
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
			if r == '\n' {
				escaping = false
				escapeDouble = false
				continue
			}
			if escapeDouble && !isDoubleQuoteEscapedRune(r) {
				current.WriteRune('\\')
			}
			current.WriteRune(r)
			escaping = false
			escapeDouble = false
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
				escapeDouble = true
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

func isDoubleQuoteEscapedRune(r rune) bool {
	return r == '$' || r == '`' || r == '"' || r == '\\' || r == '\n'
}
