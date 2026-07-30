package harness

import "strings"

// InvocationIsPipeMode reports whether argv arguments following a harness
// executable request a non-interactive run such as `claude -p`,
// `copilot --prompt`, or `codex exec`. args must exclude argv[0].
func InvocationIsPipeMode(harnessName string, args []string) bool {
	tokens := Lookup(harnessName).PipeModeTokens()
	tokenSet := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		tokenSet[token] = true
	}
	positionalAvailable := true
	optionValueMode := pipeModeOptionValueNone
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if strings.HasPrefix(arg, "-") {
			// A new flag ends any preceding option's non-flag value run.
			optionValueMode = pipeModeOptionValueNone
			// Flag-shaped pipe markers may appear anywhere before the option
			// terminator. Bare markers are subcommands and are handled below.
			if matchesPipeModeToken(arg, tokenSet) {
				return true
			}
			if positionalAvailable {
				optionValueMode = pipeModeOptionValueArity(harnessName, arg)
			}
			continue
		}
		if optionValueMode != pipeModeOptionValueNone {
			if optionValueMode == pipeModeOptionValueSingle {
				optionValueMode = pipeModeOptionValueNone
			}
			continue
		}
		if positionalAvailable {
			positionalAvailable = false
			if tokenSet[arg] {
				return true
			}
		}
	}
	return false
}

type pipeModeOptionValueMode uint8

const (
	pipeModeOptionValueNone pipeModeOptionValueMode = iota
	pipeModeOptionValueSingle
	pipeModeOptionValueVariadic
)

func pipeModeOptionValueArity(harnessName, arg string) pipeModeOptionValueMode {
	if harnessName != Codex || strings.Contains(arg, "=") {
		return pipeModeOptionValueNone
	}
	// Codex's pipe markers are subcommands. Consume the values of its global
	// options before choosing the first positional argument as the subcommand;
	// otherwise values such as "--profile review" look like pipe markers.
	switch arg {
	case "-i", "--image":
		// Image is variadic: before a subcommand it greedily consumes every
		// following non-flag value, including values named exec or review.
		return pipeModeOptionValueVariadic
	case "-c", "--config", "--enable", "--disable", "--remote", "--remote-auth-token-env",
		"-m", "--model", "--local-provider", "-p", "--profile",
		"-s", "--sandbox", "-C", "--cd", "--add-dir", "-a", "--ask-for-approval":
		return pipeModeOptionValueSingle
	default:
		return pipeModeOptionValueNone
	}
}

func matchesPipeModeToken(arg string, tokenSet map[string]bool) bool {
	if tokenSet[arg] {
		return true
	}
	name, _, hasValue := strings.Cut(arg, "=")
	return hasValue && strings.HasPrefix(name, "-") && tokenSet[name]
}
