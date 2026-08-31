// Package patternset holds the on-disk schema for harness UI copy: named
// regexes, named phrase lists and named boolean groups over both, plus the
// decoder that validates one such file.
//
// It exists because two packages need the same schema and one of them cannot
// import the other. internal/screenstate imports internal/contextlimit
// (claude.go, for the context-limit veto), so contextlimit cannot reach back
// into screenstate for the loader — yet contextlimit is a second independent
// home for Claude UI copy and must be externalizable on the same terms. The
// alternative was a second decoder with a second copy of the validation bounds,
// which is the divergence this package exists to prevent.
//
// This package owns the SCHEMA and its EVALUATION only. Where the files live,
// which harnesses exist, how an override is found and what a load failure
// should tell a human stay with the packages that own those questions —
// screenstate for the adapters, contextlimit for its own marker.
//
// Only UI COPY is data. Evaluation order, positional requirements and
// veto-first contracts stay in Go. Nothing in a pattern file can express "check
// this clause first", and that is deliberate: the ordering is the safety
// property, and a user-editable file must not be able to invert it.
package patternset

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	// MaxPatternChars bounds a single regex source or phrase. Borrowed from
	// herdr's manifest bounds: the point is not that 512 is special but that an
	// override cannot smuggle in a pattern nobody can read or reason about.
	MaxPatternChars = 512
	// MaxPhraseListEntries bounds one phrase list, and one group's ref list.
	MaxPhraseListEntries = 64
	// MaxGroupDepth bounds group-ref recursion at evaluation time. Validation
	// already rejects cycles, so this is a backstop rather than the guard.
	MaxGroupDepth = 16
	// SourceEmbedded is the Source of a set that came from the binary, as
	// opposed to a user override. It is one string here so the two packages
	// that load pattern files report the same word.
	SourceEmbedded = "embedded"
)

// Group is a boolean combination of other clauses, named by clause name.
//
// A group matches when every ref in All matches AND at least one ref in Any
// matches; an empty list is skipped rather than treated as false, so a group
// may use either half alone. Refs may name a regex, a phrase list or another
// group, which is what lets a small flat shape express the nested conditionals
// the adapters actually contain — `allow AND permission`, `(nav || submit) &&
// paneAction` — without giving the file format an expression language.
type Group struct {
	All []string `toml:"all"`
	Any []string `toml:"any"`
}

// Patterns is one harness's UI copy: named regexes, named phrase lists and
// named boolean groups over both.
//
// It is a value type whose maps are built once by the decoder and never written
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

// Source is whatever the loading package recorded about where this set came
// from — the string "embedded", or the absolute path of an override file.
// Diagnostics only.
func (p Patterns) Source() string { return p.source }

// WithSource returns a copy tagged with source. The maps are shared rather than
// cloned; they are never written after Decode returns.
func (p Patterns) WithSource(source string) Patterns {
	p.source = source
	return p
}

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

// RegexNames, PhraseNames and GroupNames list the clause names of each kind in
// sorted order. They exist so diagnostics and tests can describe a set without
// reaching into it.
func (p Patterns) RegexNames() []string  { return SortedKeys(p.regexes) }
func (p Patterns) PhraseNames() []string { return SortedKeys(p.phrases) }
func (p Patterns) GroupNames() []string  { return SortedKeys(p.groups) }

// Without returns a copy with the named clauses removed, whatever kind they
// are. It models the one thing a real user override does that a hand-written
// test set cannot: ship a set that is missing a clause the Go side asks for.
// Group refs are NOT rewritten, so a group whose ref was removed evaluates that
// ref as no-match — which is exactly what happens with a real override, since
// ref validation only runs at decode time.
func (p Patterns) Without(names ...string) Patterns {
	drop := make(map[string]struct{}, len(names))
	for _, name := range names {
		drop[name] = struct{}{}
	}
	copied := Patterns{
		harness: p.harness,
		source:  p.source,
		regexes: make(map[string]*regexp.Regexp, len(p.regexes)),
		phrases: make(map[string][]string, len(p.phrases)),
		groups:  make(map[string]Group, len(p.groups)),
	}
	for name, value := range p.regexes {
		if _, skip := drop[name]; !skip {
			copied.regexes[name] = value
		}
	}
	for name, value := range p.phrases {
		if _, skip := drop[name]; !skip {
			copied.phrases[name] = value
		}
	}
	for name, value := range p.groups {
		if _, skip := drop[name]; !skip {
			copied.groups[name] = value
		}
	}
	return copied
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
	if depth > MaxGroupDepth {
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

// patternFile is the on-disk schema. Every field is a named collection: the
// file format has no positional list of rules, so nothing about the order of
// keys can reach the classifier.
type patternFile struct {
	Harness string              `toml:"harness"`
	Regex   map[string]string   `toml:"regex"`
	Phrases map[string][]string `toml:"phrases"`
	Group   map[string]Group    `toml:"group"`
}

// Decode decodes and validates one pattern file. expectedHarness is the name
// the caller loaded the file as — its basename, or the fixed name of the
// package that owns it; the file must agree with it, so a copy-pasted override
// can never end up classifying the wrong harness.
//
// An unknown key is an error rather than a shrug: silently ignoring a
// misspelled clause name produces a pattern set that looks loaded and vetoes
// nothing, which is the exact failure this schema exists to prevent.
func Decode(data []byte, expectedHarness string) (Patterns, error) {
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

	for _, name := range SortedKeys(file.Regex) {
		if err := claim("regex", name); err != nil {
			return Patterns{}, err
		}
		source := file.Regex[name]
		if source == "" {
			return Patterns{}, fmt.Errorf("regex.%s: required field is empty", name)
		}
		if len(source) > MaxPatternChars {
			return Patterns{}, fmt.Errorf(
				"regex.%s: pattern is %d bytes, over the %d limit", name, len(source), MaxPatternChars)
		}
		compiled, err := regexp.Compile(source)
		if err != nil {
			return Patterns{}, fmt.Errorf("regex.%s: %w", name, err)
		}
		patterns.regexes[name] = compiled
	}

	for _, name := range SortedKeys(file.Phrases) {
		if err := claim("phrases", name); err != nil {
			return Patterns{}, err
		}
		list := file.Phrases[name]
		if len(list) == 0 {
			return Patterns{}, fmt.Errorf("phrases.%s: required field is empty", name)
		}
		if len(list) > MaxPhraseListEntries {
			return Patterns{}, fmt.Errorf(
				"phrases.%s: %d entries, over the %d limit", name, len(list), MaxPhraseListEntries)
		}
		for i, phrase := range list {
			switch {
			case strings.TrimSpace(phrase) == "":
				return Patterns{}, fmt.Errorf("phrases.%s[%d]: phrase is empty", name, i)
			case len(phrase) > MaxPatternChars:
				return Patterns{}, fmt.Errorf(
					"phrases.%s[%d]: phrase is %d bytes, over the %d limit",
					name, i, len(phrase), MaxPatternChars)
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

	for _, name := range SortedKeys(file.Group) {
		if err := claim("group", name); err != nil {
			return Patterns{}, err
		}
		group := file.Group[name]
		if len(group.All) == 0 && len(group.Any) == 0 {
			return Patterns{}, fmt.Errorf("group.%s: required field is empty", name)
		}
		if len(group.All) > MaxPhraseListEntries || len(group.Any) > MaxPhraseListEntries {
			return Patterns{}, fmt.Errorf(
				"group.%s: over the %d ref limit", name, MaxPhraseListEntries)
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
	for _, name := range SortedKeys(patterns.groups) {
		if err := walk(name); err != nil {
			return err
		}
	}
	return nil
}

// SortedKeys returns a map's keys in sorted order, so every list this package
// produces is deterministic.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
