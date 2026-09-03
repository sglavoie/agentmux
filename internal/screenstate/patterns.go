package screenstate

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/contextlimit"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/patternset"
)

// This file holds the pattern-set loader: the harness UI copy an adapter keys
// on, as data, with a user override. The schema itself and its evaluation live
// in internal/patternset, because internal/contextlimit — a second independent
// home for Claude UI copy, imported BY this package — needs the same schema and
// cannot import it from here.
//
// Only UI COPY is data. Evaluation order, positional requirements and the
// veto-first contract documented on Veto stay in Go. Nothing in a pattern file
// can express "check this clause first", and that is deliberate: the ordering
// is the safety property, and a user-editable file must not be able to invert
// it.
//
// The override model is herdr's — an explicit whole-file replacement, reloaded
// by restarting the daemon rather than by watching the filesystem. It is a
// REPLACEMENT and never a merge: merging a partial override into the embedded
// default yields a pattern set that is half one release's UI copy and half
// another's, which is a classifier that vetoes nothing and settles a live pane.
// A bad override therefore degrades to the embedded default, never to "no
// patterns" — that direction matches For(), which refuses to guess an adapter
// rather than guessing wrong.

// Patterns and Group are the schema types, re-exported so this package's own
// surface (and every caller of it) is unchanged by the move to patternset.
type (
	Patterns = patternset.Patterns
	Group    = patternset.Group
)

const (
	// The validation bounds are patternset's; they are named here too because
	// this package's tests and diagnostics speak in terms of them.
	maxPatternChars      = patternset.MaxPatternChars
	maxPhraseListEntries = patternset.MaxPhraseListEntries
	// patternsDirName is the subdirectory of the config dir holding overrides.
	// It also holds contextlimit's override: the file is named for the pattern
	// set, and a user reasoning about "what the screen classifier matches" does
	// not care which Go package the copy happens to live in.
	patternsDirName = "screenstate"
	// embeddedPatternPrefix is the filename prefix embed.go binds; the harness
	// name is what follows it, before the .toml suffix.
	embeddedPatternPrefix = "patterns_"
	// SourceEmbedded is Patterns.Source for a set that came from the binary.
	SourceEmbedded = patternset.SourceEmbedded
)

// LoadWarningKind names why Load reported a warning. The values are stable
// strings because doctor turns them into findings.
type LoadWarningKind string

const (
	// WarnOverrideApplied reports that a valid override replaced the embedded
	// default. It is not a failure; it exists because a stale override is the
	// most likely explanation for a classifier that stopped matching, and that
	// fact must be visible without reading the user's config directory.
	WarnOverrideApplied LoadWarningKind = "override_applied"
	// WarnOverrideUnreadable reports an override file that exists but could not
	// be read. The embedded default was kept.
	WarnOverrideUnreadable LoadWarningKind = "override_unreadable"
	// WarnOverrideInvalid reports an override that failed to decode or validate.
	// The embedded default was kept.
	WarnOverrideInvalid LoadWarningKind = "override_invalid"
	// WarnEmbeddedInvalid reports an embedded default that failed to decode or
	// validate. That is a build-time bug rather than a user error, so it is
	// reported rather than panicking on the daemon's startup path.
	WarnEmbeddedInvalid LoadWarningKind = "embedded_invalid"
	// WarnOverrideDirUnresolved reports that no config directory could be
	// resolved, so overrides were not looked for at all.
	WarnOverrideDirUnresolved LoadWarningKind = "override_dir_unresolved"
)

// LoadWarning is one thing Load wants a human to know. Load never returns an
// error: every failure mode degrades to the embedded default and surfaces here,
// because a daemon that refuses to start over a typo in a pattern override is
// worse than one that classifies with the shipped copy.
type LoadWarning struct {
	// Kind is the stable machine-readable reason.
	Kind LoadWarningKind
	// Harness is the harness the warning concerns, empty when it concerns the
	// load as a whole.
	Harness string
	// Path is the override file the warning concerns, empty when there is none.
	Path string
	// Detail is the human-readable cause.
	Detail string
}

// String renders a warning for logs and doctor output.
func (w LoadWarning) String() string {
	parts := []string{string(w.Kind)}
	if w.Harness != "" {
		parts = append(parts, "harness="+w.Harness)
	}
	if w.Path != "" {
		parts = append(parts, "path="+w.Path)
	}
	if w.Detail != "" {
		parts = append(parts, w.Detail)
	}
	return strings.Join(parts, ": ")
}

// patternsMu guards the installed pattern sets. The adapters map next door is
// read lock-free because it is a compile-time constant; this one is not, since
// Load replaces it at startup, and a mutex costs nothing at probe cadence.
var (
	patternsMu  sync.RWMutex
	patternSets = embeddedPatterns()
)

// PatternsFor returns the installed pattern set for a harness, reporting
// whether one exists. Like For it does not fall back to another harness.
//
// The embedded defaults are installed at package init, so this returns usable
// patterns even when Load was never called.
func PatternsFor(harnessName string) (Patterns, bool) {
	patternsMu.RLock()
	defer patternsMu.RUnlock()
	set, ok := patternSets[strings.ToLower(strings.TrimSpace(harnessName))]
	return set, ok
}

// LoadedPatternSources maps harness name to the source of its installed set,
// for diagnostics.
func LoadedPatternSources() map[string]string {
	patternsMu.RLock()
	defer patternsMu.RUnlock()
	sources := make(map[string]string, len(patternSets))
	for name, set := range patternSets {
		sources[name] = set.Source()
	}
	return sources
}

// Load installs the pattern sets, taking the embedded default for each and
// replacing it wholesale with <dir>/screenstate/<name>.toml when that file
// exists and validates. An empty dir means config.DefaultConfigDir().
//
// It is a one-shot startup call: there is no config reload path in this daemon
// and no file watcher here, so "reload" is `amx daemon restart`. It mutates a
// package global and must therefore run on the startup goroutine before any
// probe goroutine reads a pattern set.
//
// The set named by contextlimit.PatternsName is not an adapter's — it belongs
// to internal/contextlimit, which cannot import this package. Load carries it
// so a user gets one override directory and one reload story for every pattern
// file the classifier consults, and install() hands the result back to the
// package that owns it.
//
// A missing override is not a warning. Anything else that goes wrong keeps the
// embedded default and returns a LoadWarning; the returned slice is ordered by
// harness name so callers and tests see a deterministic sequence.
func Load(dir string) []LoadWarning {
	sets, warnings := loadEmbedded()

	if strings.TrimSpace(dir) == "" {
		dir = config.DefaultConfigDir()
	}
	// DefaultConfigDir swallows the os.UserHomeDir error, so an unresolvable
	// home yields a bare ".config/agent-mux". Reading overrides from a relative
	// path — i.e. from whatever directory the daemon happens to be started in —
	// is worse than reading none, so refuse.
	if !filepath.IsAbs(dir) {
		warnings = append(warnings, LoadWarning{
			Kind:   WarnOverrideDirUnresolved,
			Path:   dir,
			Detail: "override directory is not absolute; using embedded patterns only",
		})
		install(sets)
		return warnings
	}

	for _, name := range sortedNames(sets) {
		overridePath := filepath.Join(dir, patternsDirName, name+".toml")
		data, err := readFileIfPresent(overridePath)
		if err != nil {
			warnings = append(warnings, LoadWarning{
				Kind:    WarnOverrideUnreadable,
				Harness: name,
				Path:    overridePath,
				Detail:  err.Error(),
			})
			continue
		}
		if data == nil {
			continue
		}
		override, err := decodePatterns(data, name)
		if err == nil {
			// Syntax is not enough. A file that decodes can still be missing a
			// clause that is the sole source of a veto, and installing it would
			// delete that refusal with nothing red anywhere: the daemon starts,
			// doctor reports a valid override, and the classifier settles panes
			// that are still working. requirementsFor is where each pattern set
			// says which of its clauses are refusals rather than claims.
			err = requirementsFor(name).Validate(override)
		}
		if err != nil {
			warnings = append(warnings, LoadWarning{
				Kind:    WarnOverrideInvalid,
				Harness: name,
				Path:    overridePath,
				Detail:  err.Error(),
			})
			continue
		}
		sets[name] = override.WithSource(overridePath)
		warnings = append(warnings, LoadWarning{
			Kind:    WarnOverrideApplied,
			Harness: name,
			Path:    overridePath,
			Detail:  "embedded patterns replaced by override",
		})
	}

	install(sets)
	return warnings
}

// install publishes the sets, and hands contextlimit's back to it. A set that
// failed to build is absent from the map rather than zero, and must not be
// pushed: contextlimit installed its own embedded default at ITS package init,
// and overwriting that with an empty set would turn a build-time bug in one
// file into a classifier that never sees a context-limit banner.
func install(sets map[string]Patterns) {
	patternsMu.Lock()
	patternSets = sets
	patternsMu.Unlock()

	if set, ok := sets[contextlimit.PatternsName]; ok {
		contextlimit.InstallPatterns(set)
	}
}

// embeddedPatterns is the package-init form of loadEmbedded: warnings are
// dropped because there is nobody to hand them to yet, and Load reproduces them
// for the caller that can report them.
func embeddedPatterns() map[string]Patterns {
	sets, _ := loadEmbedded()
	return sets
}

// loadEmbedded decodes every embedded pattern file. A file that fails
// validation is skipped rather than installed half-built.
func loadEmbedded() (map[string]Patterns, []LoadWarning) {
	sets := make(map[string]Patterns)
	var warnings []LoadWarning

	entries, err := fs.ReadDir(defaultPatternFiles, ".")
	if err != nil {
		return sets, append(warnings, LoadWarning{
			Kind:   WarnEmbeddedInvalid,
			Detail: err.Error(),
		})
	}
	for _, entry := range entries {
		if entry.IsDir() ||
			!strings.HasPrefix(entry.Name(), embeddedPatternPrefix) ||
			!strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), embeddedPatternPrefix), ".toml")
		data, err := fs.ReadFile(defaultPatternFiles, entry.Name())
		if err != nil {
			warnings = append(warnings, LoadWarning{
				Kind:    WarnEmbeddedInvalid,
				Harness: name,
				Detail:  err.Error(),
			})
			continue
		}
		set, err := decodePatterns(data, name)
		if err == nil {
			// Held to the same semantic contract as an override. A shipped file
			// that lost a required clause to a rename is a build-time bug, and
			// the alternative to failing here is shipping a release whose
			// classifier quietly cannot refuse.
			err = requirementsFor(name).Validate(set)
		}
		if err != nil {
			warnings = append(warnings, LoadWarning{
				Kind:    WarnEmbeddedInvalid,
				Harness: name,
				Detail:  err.Error(),
			})
			continue
		}
		sets[name] = set.WithSource(SourceEmbedded)
	}

	// contextlimit ships its pattern file with the code that reads it, so its
	// embedded default is decoded there and joined here for override handling.
	set, err := contextlimit.EmbeddedPatterns()
	if err == nil {
		err = requirementsFor(contextlimit.PatternsName).Validate(set)
	}
	if err != nil {
		warnings = append(warnings, LoadWarning{
			Kind:    WarnEmbeddedInvalid,
			Harness: contextlimit.PatternsName,
			Detail:  err.Error(),
		})
	} else {
		sets[contextlimit.PatternsName] = set
	}

	return sets, warnings
}

// requirementsFor returns the semantic contract a pattern set must satisfy
// before it is installed: which of its clauses are REFUSALS, whose absence
// deletes the only reason a screen would not settle, rather than claims whose
// absence merely withholds a conclusion.
//
// Decode cannot answer this and must not try. The schema is generic — it knows
// about regexes, phrase lists and groups — while which clause carries which
// veto is knowledge that belongs to the package that reads it. So each owner
// declares its own list and this function is the only place they are collected.
//
// A set with no declared requirements is accepted as-is. That is the honest
// answer for a pattern set nobody has audited rather than a silent pass: adding
// a harness means adding its adapter to the adapters map, and the requirements
// list belongs in the same review as the fail-closed analysis that produces it.
func requirementsFor(name string) patternset.Requirements {
	switch name {
	case harness.Claude:
		return claudeRequirements
	case harness.Codex:
		return codexRequirements
	case harness.Copilot:
		return copilotRequirements
	case contextlimit.PatternsName:
		return contextlimit.Requirements()
	default:
		return nil
	}
}

// decodePatterns is patternset.Decode under this package's old name, kept so
// the loader's tests and error strings stay put.
func decodePatterns(data []byte, expectedHarness string) (Patterns, error) {
	return patternset.Decode(data, expectedHarness)
}

// readFileIfPresent returns the file's bytes, or (nil, nil) when it does not
// exist. A missing override is the normal case and must not read as a failure;
// anything else — a directory in its place, a permission denial — is a real
// warning, because the user meant to install something there.
func readFileIfPresent(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

func sortedKeys[V any](m map[string]V) []string { return patternset.SortedKeys(m) }

func sortedNames(sets map[string]Patterns) []string { return sortedKeys(sets) }
