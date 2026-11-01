// Package recap generates disposable, source-linked conversation recaps. It
// never modifies transcripts, resumes agents, or writes Flight Log events.
package recap

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/cachefile"
	"github.com/sglavoie/agentmux/internal/transcript"
	"golang.org/x/sys/unix"
)

const version = 1
const MaxInputBytes = 32 * 1024

type Message struct {
	Index int    `json:"index"`
	Role  string `json:"role"`
	Text  string `json:"text"`
}

type Input struct {
	Messages []Message `json:"messages"`
	Omitted  bool      `json:"omitted"`
}

// Claim points into session show's visible user/assistant prose, not raw JSONL
// records. Empty claims are allowed when the conversation offers no evidence.
type Claim struct {
	Text     string `json:"text"`
	Messages []int  `json:"messages"`
}

type Content struct {
	Goal        Claim `json:"goal"`
	Latest      Claim `json:"latest"`
	Outstanding Claim `json:"outstanding"`
}

type Recap struct {
	Content
	GeneratedAt time.Time `json:"generated_at"`
	Fingerprint string    `json:"fingerprint"`
	Profile     string    `json:"profile"`
	Model       string    `json:"model"`
	Partial     bool      `json:"partial"`
}

type Report struct {
	Conversation string     `json:"conversation"`
	Status       string     `json:"status"`
	Recap        *Recap     `json:"recap,omitempty"`
	Warning      string     `json:"warning,omitempty"`
	RetryAfter   *time.Time `json:"retry_after,omitempty"`
}

type Request struct {
	Conversation, Path, Profile, Home, Model string
	Enabled, CachedOnly, Force               bool
	// Load reads authoritative transcript content, including after waiting for
	// another process's worker. It must not return an index's cached snippets.
	Load func() ([]transcript.Message, error)
}

type Generator interface {
	Generate(context.Context, Input) (Content, error)
}

type Service struct {
	Directory string
	Generator Generator
	Now       func() time.Time
}

type entry struct {
	Version           int       `json:"version"`
	Recap             *Recap    `json:"recap,omitempty"`
	FailedFingerprint string    `json:"failed_fingerprint,omitempty"`
	FailedAt          time.Time `json:"failed_at,omitempty"`
	Failure           string    `json:"failure,omitempty"`
}

func Directory() string {
	root := os.Getenv("XDG_CACHE_HOME")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".cache")
	}
	return filepath.Join(root, "agent-mux", "recaps")
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Get first reads the atomic cache without taking a worker lock. CachedOnly
// consequently stays responsive while a model request is in progress. Workers
// serialize per conversation, then globally, across CLI and app processes.
func (s Service) Get(ctx context.Context, r Request) (Report, error) {
	report := Report{Conversation: r.Conversation, Status: "missing"}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	if s.Directory == "" {
		s.Directory = Directory()
	}
	key := sha256.Sum256([]byte(r.Conversation + "\x00" + filepath.Clean(r.Path)))
	path := filepath.Join(s.Directory, fmt.Sprintf("%x.json", key))
	e, cacheErr := read(path)
	report.Recap = e.Recap
	if cacheErr != nil {
		report.Warning = "Saved recap could not be read; it can be regenerated."
	}
	if !r.Enabled {
		report.Status = "disabled"
		return report, nil
	}
	if r.Profile == "" {
		report.Status = "unconfigured"
		return report, nil
	}
	if r.Load == nil {
		return report, errors.New("recap: transcript reader is required")
	}
	input, fingerprint, err := prepare(r)
	if err != nil {
		return report, err
	}
	if len(input.Messages) == 0 {
		report.Status = "empty"
		return report, nil
	}
	report = s.describe(report, e, fingerprint)
	if r.CachedOnly || (!r.Force && (report.Status == "ready" || report.RetryAfter != nil)) {
		return report, nil
	}
	if s.Generator == nil {
		return report, errors.New("recap: generator is unavailable")
	}
	if err := os.MkdirAll(s.Directory, 0o700); err != nil {
		return report, err
	}
	// Do not queue obsolete selections behind an existing job. The app polls
	// only its current selection and can pick up the completed cached result.
	unlock, err := lock(ctx, path+".worker", false)
	if errors.Is(err, errBusy) {
		report.Status = "pending"
		return report, nil
	}
	if err != nil {
		return report, err
	}
	defer unlock()
	globalUnlock, err := lock(ctx, filepath.Join(s.Directory, "generation.lock"), true)
	if err != nil {
		return report, err
	}
	defer globalUnlock()
	// A queued request may have been generated, changed, or failed meanwhile.
	e, _ = read(path)
	input, fingerprint, err = prepare(r)
	if err != nil {
		return report, err
	}
	if len(input.Messages) == 0 {
		report.Status = "empty"
		return report, nil
	}
	report.Recap = e.Recap
	report = s.describe(report, e, fingerprint)
	if !r.Force && (report.Status == "ready" || report.RetryAfter != nil) {
		return report, nil
	}
	content, generateErr := s.Generator.Generate(ctx, input)
	if generateErr == nil {
		for _, claim := range []*Claim{&content.Goal, &content.Latest, &content.Outstanding} {
			claim.Text = strings.TrimSpace(claim.Text)
			if claim.Messages == nil {
				claim.Messages = []int{}
			}
		}
		generateErr = validate(content, input)
	}
	if generateErr != nil {
		e.FailedFingerprint, e.FailedAt = fingerprint, s.now()
		e.Failure = generateErr.Error()
		if err := save(path, e); err != nil {
			return report, fmt.Errorf("saving recap retry state: %w", err)
		}
		report = s.describe(report, e, fingerprint)
		return report, nil
	}
	generated := &Recap{Content: content, GeneratedAt: s.now(), Fingerprint: fingerprint,
		Profile: r.Profile, Model: r.Model, Partial: input.Omitted}
	// Never overwrite a newer transcript's result with a late model answer.
	_, current, err := prepare(r)
	if err != nil {
		return report, err
	}
	if current != fingerprint {
		report.Status = "stale"
		report.Warning = "Conversation changed during generation; refresh to recap its latest messages."
		return report, nil
	}
	if err := save(path, entry{Recap: generated}); err != nil {
		return report, fmt.Errorf("saving recap: %w", err)
	}
	report.Recap, report.Status, report.Warning, report.RetryAfter = generated, "ready", "", nil
	return report, nil
}

func (s Service) describe(r Report, e entry, fingerprint string) Report {
	r.Status, r.RetryAfter = "missing", nil
	if e.Recap != nil {
		r.Status = "stale"
		if e.Recap.Fingerprint == fingerprint {
			r.Status = "ready"
		}
	}
	if e.FailedFingerprint == fingerprint && s.now().Before(e.FailedAt.Add(time.Minute)) {
		retry := e.FailedAt.Add(time.Minute)
		r.RetryAfter, r.Warning = &retry, e.Failure
	}
	return r
}

func prepare(r Request) (Input, string, error) {
	messages, err := r.Load()
	if err != nil {
		return Input{}, "", fmt.Errorf("reading conversation for recap: %w", err)
	}
	visible := make([]Message, 0, len(messages))
	for _, m := range messages {
		if text, ok := m.TextForHandoff(); ok {
			visible = append(visible, Message{Index: len(visible), Role: strings.ToLower(strings.TrimSpace(m.Role)), Text: text})
		}
	}
	// Hash all visible prose, so an edit or truncation invalidates the cache
	// even if the changed message falls outside the bounded model input.
	encoded, err := json.Marshal(struct {
		Version              int
		Profile, Home, Model string
		Messages             []Message
	}{version, r.Profile, r.Home, r.Model, visible})
	if err != nil {
		return Input{}, "", err
	}
	digest := sha256.Sum256(encoded)
	in := Input{Messages: []Message{}}
	for i, m := range visible {
		if i >= 2 && i < len(visible)-12 {
			in.Omitted = true
			continue
		}
		if len(m.Text) > 1800 {
			m.Text = truncate(m.Text, 900) + "\n[message excerpt omitted]\n" + tail(m.Text, 900)
			in.Omitted = true
		}
		in.Messages = append(in.Messages, m)
	}
	// JSON escaping can expand text by up to 6x. Keep the actual wire input
	// bounded rather than assuming the source strings equal its encoded size.
	for {
		data, _ := json.Marshal(in)
		if len(data) <= MaxInputBytes {
			break
		}
		in.Omitted = true
		if len(in.Messages) > 3 {
			in.Messages = append(in.Messages[:1], in.Messages[2:]...)
			continue
		}
		for i := range in.Messages {
			in.Messages[i].Text = truncate(in.Messages[i].Text, len(in.Messages[i].Text)/2)
		}
	}
	return in, fmt.Sprintf("%x", digest), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.ValidString(s[:n]) {
		n--
	}
	return s[:n]
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := len(s) - n
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return s[i:]
}

func validate(c Content, input Input) error {
	indices := map[int]bool{}
	for _, m := range input.Messages {
		indices[m.Index] = true
	}
	for _, claim := range []Claim{c.Goal, c.Latest, c.Outstanding} {
		if utf8.RuneCountInString(claim.Text) > 600 || len(claim.Messages) > 14 {
			return errors.New("summarizer returned an oversized recap")
		}
		if strings.TrimSpace(claim.Text) != "" && len(claim.Messages) == 0 {
			return errors.New("summarizer returned a claim without supporting messages")
		}
		for _, index := range claim.Messages {
			if !indices[index] {
				return errors.New("summarizer cited a message outside its input")
			}
		}
	}
	if strings.TrimSpace(c.Goal.Text+c.Latest.Text+c.Outstanding.Text) == "" {
		return errors.New("summarizer returned an empty recap")
	}
	return nil
}

func read(path string) (entry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return entry{}, nil
	}
	if err != nil {
		return entry{}, err
	}
	var e entry
	if err := json.Unmarshal(data, &e); err != nil {
		return entry{}, err
	}
	if e.Version != version {
		return entry{}, errors.New("unsupported recap cache version")
	}
	return e, nil
}

func save(path string, e entry) error {
	e.Version = version
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return cachefile.WriteFileAtomic(path, data, 0o600)
}

var errBusy = errors.New("recap worker busy")

func lock(ctx context.Context, path string, wait bool) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = f.Close()
			return nil, err
		}
		if !wait {
			_ = f.Close()
			return nil, errBusy
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
