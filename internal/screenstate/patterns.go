package screenstate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"

	"github.com/sglavoie/agentmux/internal/config"
)

// This file holds the pattern-set loader: the harness UI copy an adapter keys
// on, as data, with a user override.
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

const (
	// maxPatternChars bounds a single regex source or phrase. Borrowed from
	// herdr's manifest bounds: the point is not that 512 is special but that an
	// override cannot smuggle in a pattern nobody can read or reason about.
	maxPatternChars = 512
	// maxPhraseListEntries bounds one phrase list, and one group's ref list.
	maxPhraseListEntries = 64
	// maxGroupDepth bounds group-ref recursion at evaluation time. Validation
	// already rejects cycles, so this is a backstop rather than the guard.
	maxGroupDepth = 16
	// patternsDirName is the subdirectory of the config dir holding overrides.
	patternsDirName = "screenstate"
	// embeddedPatternsDir is the package-relative directory embed.go binds.
	embeddedPatternsDir = "patterns"
	// SourceEmbedded is Patterns.Source for a set that came from the binary.
	SourceEmbedded = "embedded"
)

// Group is a boolean combination of other clauses, named by clause name.
//
// A group matches when every ref in All matches AND at least one ref in Any
// matches; an empty list is skipped rather than treated as false, so a group
// may use either half alone. Refs may name a regex, a phrase list or another
// group, which is what lets a small flat shape express the nested conditionals
// the adapters actually contain — `allow AND permission`, `(nav || submit) &&
// paneAction`, and the context-limit marker's three-group conditional — without
// giving the file format an expression language.
type Group struct {
	All []string `toml:"all"`
	Any []string `toml:"any"`
}

// Patterns is one harness's UI copy: named regexes, named phrase lists and
// named boolean groups over both.
//
// It is a value type whose maps are built once by the loader and never written
// afterwards, so copying one is safe and callers may hold it across goroutines.
// The zero value has no clauses and matches nothing; every accessor tolerates
// it, because "the harness has no pattern set" must never be a panic on the
// probe path.
type Patterns struct {
	harness string
	source  string
	regexes map[string]*regexp.Regexp
	phrases map[string][]string
	groups  map[string]Group
}

// Harness is the canonical lowercase harness name this set classifies.
func (p Patterns) Harness() string { return p.harness }

// Source is SourceEmbedded, or the absolute path of the override file that
// replaced the embedded default. Diagnostics only.
func (p Patterns) Source() string { return p.source }

// Regexp returns the compiled regex named name, or nil when the set has no
// such clause. A nil result is the caller's cue to skip the check, not to
// panic: an override written against an older release can legitimately be
// missing a clause a newer adapter looks for.
func (p Patterns) Regexp(name string) *regexp.Regexp { return p.regexes[name] }

// Phrases returns the phrase list named name, or nil. Every phrase is already
// lowercase — validation enforces it — so callers match against a folded
// screen and case handling stays in exactly one place.
func (p Patterns) Phrases(name string) []string { return p.phrases[name] }

// Group returns the group named name, reporting whether one exists.
func (p Patterns) Group(name string) (Group, bool) {
	group, ok := p.groups[name]
	return group, ok
}

// Has reports whether any clause — regex, phrase list or group — is named name.
func (p Patterns) Has(name string) bool {
	if _, ok := p.regexes[name]; ok {
		return true
	}
	if _, ok := p.phrases[name]; ok {
		return true
	}
	_, ok := p.groups[name]
	return ok
}

// Match evaluates the clause named name against one screen.
//
// raw is the capture as read; lower is the same text folded with
// strings.ToLower. Both are parameters rather than one because the adapters
// already fold the screen once per Classify and regex clauses must still see
// the unfolded text. An unknown clause name reports false — the fail-closed
// answer, matching Regexp returning nil.
func (p Patterns) Match(name, raw, lower string) bool {
	return p.match(name, raw, lower, 0)
}

func (p Patterns) match(name, raw, lower string, depth int) bool {
	if depth > maxGroupDepth {
		return false
	}
	if re, ok := p.regexes[name]; ok {
		return re.MatchString(raw)
	}
	if list, ok := p.phrases[name]; ok {
		for _, phrase := range list {
			if strings.Contains(lower, phrase) {
				return true
			}
		}
		return false
	}
	group, ok := p.groups[name]
	if !ok {
		return false
	}
	for _, ref := range group.All {
		if !p.match(ref, raw, lower, depth+1) {
			return false
		}
	}
	if len(group.Any) == 0 {
		return len(group.All) > 0
	}
	for _, ref := range group.Any {
		if p.match(ref, raw, lower, depth+1) {
			return true
		}
	}
	return false
}

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
		sources[name] = set.source
	}
	return sources
}

// Load installs the pattern sets, taking the embedded default for each harness
// and replacing it wholesale with <dir>/screenstate/<harness>.toml when that
// file exists and validates. An empty dir means config.DefaultConfigDir().
//
// It is a one-shot startup call: there is no config reload path in this daemon
// and no file watcher here, so "reload" is `amx daemon restart`. It mutates a
// package global and must therefore run on the startup goroutine before any
// probe goroutine reads a pattern set.
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
		if err != nil {
			warnings = append(warnings, LoadWarning{
				Kind:    WarnOverrideInvalid,
				Harness: name,
				Path:    overridePath,
				Detail:  err.Error(),
			})
			continue
		}
		override.source = overridePath
		sets[name] = override
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

func install(sets map[string]Patterns) {
	patternsMu.Lock()
	defer patternsMu.Unlock()
	patternSets = sets
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

	entries, err := fs.ReadDir(defaultPatternFiles, embeddedPatternsDir)
	if err != nil {
		return sets, append(warnings, LoadWarning{
			Kind:   WarnEmbeddedInvalid,
			Detail: err.Error(),
		})
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".toml")
		data, err := fs.ReadFile(defaultPatternFiles, path.Join(embeddedPatternsDir, entry.Name()))
		if err != nil {
			warnings = append(warnings, LoadWarning{
				Kind:    WarnEmbeddedInvalid,
				Harness: name,
				Detail:  err.Error(),
			})
			continue
		}
		set, err := decodePatterns(data, name)
		if err != nil {
			warnings = append(warnings, LoadWarning{
				Kind:    WarnEmbeddedInvalid,
				Harness: name,
				Detail:  err.Error(),
			})
			continue
		}
		set.source = SourceEmbedded
		sets[name] = set
	}
	return sets, warnings
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

// patternFile is the on-disk schema. Every field is a named collection: the
// file format has no positional list of rules, so nothing about the order of
// keys can reach the classifier.
type patternFile struct {
	Harness string              `toml:"harness"`
	Regex   map[string]string   `toml:"regex"`
	Phrases map[string][]string `toml:"phrases"`
	Group   map[string]Group    `toml:"group"`
}

// decodePatterns decodes and validates one pattern file. expectedHarness is the
// file's basename; the file must agree with it, so a copy-pasted override can
// never end up classifying the wrong harness.
//
// An unknown key is an error rather than a shrug: silently ignoring a
// misspelled clause name produces a pattern set that looks loaded and vetoes
// nothing, which is the exact failure this package exists to prevent.
func decodePatterns(data []byte, expectedHarness string) (Patterns, error) {
	var file patternFile
	md, err := toml.Decode(string(data), &file)
	if err != nil {
		return Patterns{}, fmt.Errorf("decode: %w", err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		sort.Strings(keys)
		return Patterns{}, fmt.Errorf("unknown key(s): %s", strings.Join(keys, ", "))
	}

	harnessName := strings.ToLower(strings.TrimSpace(file.Harness))
	if harnessName == "" {
		return Patterns{}, fmt.Errorf("harness: required field is empty")
	}
	if harnessName != expectedHarness {
		return Patterns{}, fmt.Errorf(
			"harness: file declares %q but is loaded as %q", harnessName, expectedHarness)
	}

	patterns := Patterns{
		harness: harnessName,
		regexes: make(map[string]*regexp.Regexp, len(file.Regex)),
		phrases: make(map[string][]string, len(file.Phrases)),
		groups:  make(map[string]Group, len(file.Group)),
	}

	// One namespace across all three sections: a group ref names a clause
	// without saying which kind it is, so a name used twice would be ambiguous.
	seen := make(map[string]string, len(file.Regex)+len(file.Phrases)+len(file.Group))
	claim := func(section, name string) error {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%s: clause name is empty", section)
		}
		if other, ok := seen[name]; ok {
			return fmt.Errorf("%s.%s: name already defined in [%s]", section, name, other)
		}
		seen[name] = section
		return nil
	}

	for _, name := range sortedKeys(file.Regex) {
		if err := claim("regex", name); err != nil {
			return Patterns{}, err
		}
		source := file.Regex[name]
		if source == "" {
			return Patterns{}, fmt.Errorf("regex.%s: required field is empty", name)
		}
		if len(source) > maxPatternChars {
			return Patterns{}, fmt.Errorf(
				"regex.%s: pattern is %d bytes, over the %d limit", name, len(source), maxPatternChars)
		}
		compiled, err := regexp.Compile(source)
		if err != nil {
			return Patterns{}, fmt.Errorf("regex.%s: %w", name, err)
		}
		patterns.regexes[name] = compiled
	}

	for _, name := range sortedKeys(file.Phrases) {
		if err := claim("phrases", name); err != nil {
			return Patterns{}, err
		}
		list := file.Phrases[name]
		if len(list) == 0 {
			return Patterns{}, fmt.Errorf("phrases.%s: required field is empty", name)
		}
		if len(list) > maxPhraseListEntries {
			return Patterns{}, fmt.Errorf(
				"phrases.%s: %d entries, over the %d limit", name, len(list), maxPhraseListEntries)
		}
		for i, phrase := range list {
			switch {
			case strings.TrimSpace(phrase) == "":
				return Patterns{}, fmt.Errorf("phrases.%s[%d]: phrase is empty", name, i)
			case len(phrase) > maxPatternChars:
				return Patterns{}, fmt.Errorf(
					"phrases.%s[%d]: phrase is %d bytes, over the %d limit",
					name, i, len(phrase), maxPatternChars)
			case phrase != strings.ToLower(phrase):
				// Phrases match a folded screen, so an uppercase phrase would
				// silently never fire. herdr mixes case-sensitive regex with
				// case-insensitive contains; refusing the mix is cheaper than
				// documenting it.
				return Patterns{}, fmt.Errorf(
					"phrases.%s[%d]: phrase must be lowercase, got %q", name, i, phrase)
			}
		}
		patterns.phrases[name] = append([]string(nil), list...)
	}

	for _, name := range sortedKeys(file.Group) {
		if err := claim("group", name); err != nil {
			return Patterns{}, err
		}
		group := file.Group[name]
		if len(group.All) == 0 && len(group.Any) == 0 {
			return Patterns{}, fmt.Errorf("group.%s: required field is empty", name)
		}
		if len(group.All) > maxPhraseListEntries || len(group.Any) > maxPhraseListEntries {
			return Patterns{}, fmt.Errorf(
				"group.%s: over the %d ref limit", name, maxPhraseListEntries)
		}
		patterns.groups[name] = Group{
			All: append([]string(nil), group.All...),
			Any: append([]string(nil), group.Any...),
		}
	}

	if err := validateGroupRefs(patterns); err != nil {
		return Patterns{}, err
	}
	return patterns, nil
}

// validateGroupRefs rejects refs to clauses that do not exist and reference
// cycles. Both are load-time errors so evaluation stays a plain recursive walk.
func validateGroupRefs(patterns Patterns) error {
	state := make(map[string]int, len(patterns.groups)) // 1 = on stack, 2 = done
	var walk func(name string) error
	walk = func(name string) error {
		group, ok := patterns.groups[name]
		if !ok {
			return nil
		}
		switch state[name] {
		case 1:
			return fmt.Errorf("group.%s: reference cycle", name)
		case 2:
			return nil
		}
		state[name] = 1
		for _, ref := range append(append([]string(nil), group.All...), group.Any...) {
			if !patterns.Has(ref) {
				return fmt.Errorf("group.%s: unknown ref %q", name, ref)
			}
			if err := walk(ref); err != nil {
				return err
			}
		}
		state[name] = 2
		return nil
	}
	for _, name := range sortedKeys(patterns.groups) {
		if err := walk(name); err != nil {
			return err
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedNames(sets map[string]Patterns) []string { return sortedKeys(sets) }
