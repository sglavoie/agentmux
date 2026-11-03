package recap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/transcript"
)

type generatorFunc func(context.Context, Input) (Content, error)

func (f generatorFunc) Generate(ctx context.Context, in Input) (Content, error) { return f(ctx, in) }

func contentFor(in Input) Content {
	return Content{Goal: Claim{Text: "Implement the requested feature", Messages: []int{in.Messages[0].Index}},
		Latest:      Claim{Text: "The agent reports tests passed", Messages: []int{in.Messages[len(in.Messages)-1].Index}},
		Outstanding: Claim{Text: "", Messages: []int{}}}
}

func request(messages *[]transcript.Message) Request {
	return Request{Conversation: "codex:one", Path: "/transcripts/one", Enabled: true, Profile: "personal", Home: "/claude/personal", Model: "haiku",
		Load: func() ([]transcript.Message, error) { return *messages, nil }}
}

func TestRecapsReuseProseFingerprintAcrossRestartsAndInvalidateChanges(t *testing.T) {
	messages := []transcript.Message{{Role: "user", Text: "Implement a feature"}, {Role: "assistant", Text: "Tests passed"}}
	r := request(&messages)
	var calls int
	s := Service{Directory: t.TempDir(), Generator: generatorFunc(func(_ context.Context, in Input) (Content, error) { calls++; return contentFor(in), nil })}
	first, err := s.Get(t.Context(), r)
	if err != nil || first.Status != "ready" || calls != 1 {
		t.Fatalf("first = %+v, calls %d, err %v", first, calls, err)
	}
	// A new service represents another CLI process/app restart.
	restarted := Service{Directory: s.Directory, Generator: s.Generator}
	second, err := restarted.Get(t.Context(), r)
	if err != nil || second.Recap.Fingerprint != first.Recap.Fingerprint || calls != 1 {
		t.Fatalf("cached = %+v, calls %d, err %v", second, calls, err)
	}
	messages = append(messages, transcript.Message{Role: "tool", Text: "ignored raw tool data"})
	_, err = s.Get(t.Context(), r)
	if err != nil || calls != 1 {
		t.Fatalf("tool-only changes caused generation: %d, %v", calls, err)
	}
	messages[0].Text = "User changed the goal"
	r.CachedOnly = true
	stale, err := s.Get(t.Context(), r)
	if err != nil || stale.Status != "stale" || stale.Recap == nil || calls != 1 {
		t.Fatalf("stale = %+v, %v", stale, err)
	}
	r.CachedOnly = false
	_, err = s.Get(t.Context(), r)
	if err != nil || calls != 2 {
		t.Fatalf("edited input: calls %d, %v", calls, err)
	}
	r.Model = "another-model"
	_, err = s.Get(t.Context(), r)
	if err != nil || calls != 3 {
		t.Fatalf("changed model: calls %d, %v", calls, err)
	}
	r.Home = "/different-auth-profile"
	_, err = s.Get(t.Context(), r)
	if err != nil || calls != 4 {
		t.Fatalf("changed auth profile: calls %d, %v", calls, err)
	}
	files, _ := filepath.Glob(filepath.Join(s.Directory, "*.json"))
	if len(files) != 1 {
		t.Fatalf("cache files %v", files)
	}
	stat, err := os.Stat(files[0])
	if err != nil || stat.Mode().Perm() != 0o600 {
		t.Fatalf("cache permissions: %v, %v", stat, err)
	}
}

func TestBoundedInputKeepsBeginningAndTailWithStableEvidenceIndices(t *testing.T) {
	var messages []transcript.Message
	for range 80 {
		messages = append(messages, transcript.Message{Role: "user", Text: strings.Repeat("😀\x01", 5000)})
	}
	input, _, err := prepare(request(&messages))
	data, _ := json.Marshal(input)
	if err != nil || len(data) > MaxInputBytes || !input.Omitted {
		t.Fatalf("input bytes %d, omitted %v, err %v", len(data), input.Omitted, err)
	}
	if input.Messages[0].Index != 0 || input.Messages[len(input.Messages)-1].Index != 79 {
		t.Fatalf("lost opening/tail: %+v", input.Messages)
	}
	if err := validate(contentFor(input), input); err != nil {
		t.Fatal(err)
	}
	invalid := contentFor(input)
	invalid.Goal.Messages = []int{40}
	if err := validate(invalid, input); err == nil {
		t.Fatal("accepted evidence outside selected input")
	}
	invalid.Goal.Messages = nil
	if err := validate(invalid, input); err == nil {
		t.Fatal("accepted unsourced claim")
	}
}

func TestLateGenerationDoesNotOverwriteChangedConversation(t *testing.T) {
	messages := []transcript.Message{{Role: "user", Text: "Old goal"}}
	s := Service{Directory: t.TempDir(), Generator: generatorFunc(func(_ context.Context, in Input) (Content, error) {
		messages[0].Text = "A newer goal"
		return contentFor(in), nil
	})}
	result, err := s.Get(t.Context(), request(&messages))
	if err != nil || result.Status != "stale" || result.Recap != nil {
		t.Fatalf("late result %+v, %v", result, err)
	}
	files, _ := filepath.Glob(filepath.Join(s.Directory, "*.json"))
	if len(files) != 0 {
		t.Fatalf("obsolete answer was persisted: %v", files)
	}
}

func TestCachedReadsDoNotWaitAndConcurrentWorkersDeduplicate(t *testing.T) {
	messages := []transcript.Message{{Role: "user", Text: "A goal"}}
	r := request(&messages)
	started, finish := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s := Service{Directory: t.TempDir(), Generator: generatorFunc(func(_ context.Context, in Input) (Content, error) {
		calls.Add(1)
		close(started)
		<-finish
		return contentFor(in), nil
	})}
	done := make(chan error, 1)
	go func() { _, err := s.Get(t.Context(), r); done <- err }()
	<-started
	r.CachedOnly = true
	result, err := s.Get(t.Context(), r)
	if err != nil || result.Status != "missing" {
		t.Fatalf("cached while running: %+v, %v", result, err)
	}
	r.CachedOnly = false
	result, err = s.Get(t.Context(), r)
	if err != nil || result.Status != "pending" || calls.Load() != 1 {
		t.Fatalf("duplicate worker %+v, %v", result, err)
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	result, err = s.Get(t.Context(), r)
	if err != nil || result.Status != "ready" || calls.Load() != 1 {
		t.Fatalf("ready %+v, %v", result, err)
	}
}

func TestFailureKeepsSavedRecapAndBacksOffUntilExplicitRetry(t *testing.T) {
	messages := []transcript.Message{{Role: "user", Text: "Goal"}}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	fail, calls := false, 0
	s := Service{Directory: t.TempDir(), Now: func() time.Time { return now }, Generator: generatorFunc(func(_ context.Context, in Input) (Content, error) {
		calls++
		if fail {
			return Content{}, errors.New("provider temporarily unavailable")
		}
		return contentFor(in), nil
	})}
	r := request(&messages)
	first, err := s.Get(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	messages = append(messages, transcript.Message{Role: "assistant", Text: "More work"})
	fail = true
	failed, err := s.Get(t.Context(), r)
	if err != nil || failed.Recap.Fingerprint != first.Recap.Fingerprint || failed.RetryAfter == nil || failed.Warning == "" {
		t.Fatalf("failed %+v, %v", failed, err)
	}
	_, _ = s.Get(t.Context(), r)
	if calls != 2 {
		t.Fatalf("automatic retry ignored backoff: %d calls", calls)
	}
	r.Force = true
	fail = false
	retried, err := s.Get(t.Context(), r)
	if err != nil || retried.Status != "ready" || retried.Warning != "" || retried.RetryAfter != nil || calls != 3 {
		t.Fatalf("retry %+v, calls %d, %v", retried, calls, err)
	}
}

func TestDisabledUnconfiguredAndEmptyDoNotGenerate(t *testing.T) {
	for _, status := range []string{"disabled", "unconfigured", "empty"} {
		t.Run(status, func(t *testing.T) {
			messages := []transcript.Message{{Role: "user", Text: "Goal"}}
			r := request(&messages)
			s := Service{Directory: t.TempDir(), Generator: generatorFunc(func(context.Context, Input) (Content, error) { t.Fatal("unexpected generation"); return Content{}, nil })}
			switch status {
			case "disabled":
				r.Enabled = false
			case "unconfigured":
				r.Profile = ""
			case "empty":
				messages = nil
			}
			got, err := s.Get(t.Context(), r)
			if err != nil || got.Status != status {
				t.Fatalf("%+v, %v", got, err)
			}
		})
	}
}

func TestGenerationGlobalLockHonorsCancellation(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lock(t.Context(), filepath.Join(dir, "generation.lock"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	messages := []transcript.Message{{Role: "user", Text: "Goal"}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s := Service{Directory: dir, Generator: generatorFunc(func(context.Context, Input) (Content, error) {
		t.Fatal("generation after cancellation")
		return Content{}, nil
	})}
	_, err = s.Get(ctx, request(&messages))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestCancelledGenerationDoesNotBackOffTheNextRequest(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(fmt.Sprintf("saved=%t", saved), func(t *testing.T) {
			messages := []transcript.Message{{Role: "user", Text: "Goal"}}
			r := request(&messages)
			s := Service{Directory: t.TempDir(), Generator: generatorFunc(func(_ context.Context, in Input) (Content, error) {
				return contentFor(in), nil
			})}
			if saved {
				if _, err := s.Get(t.Context(), r); err != nil {
					t.Fatal(err)
				}
				messages = append(messages, transcript.Message{Role: "assistant", Text: "More work"})
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s.Generator = generatorFunc(func(context.Context, Input) (Content, error) {
				cancel()
				// Providers can wrap cancellation in their own diagnostic.
				return Content{}, errors.New("request interrupted")
			})
			if _, err := s.Get(ctx, r); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled generation = %v; want context.Canceled", err)
			}
			r.CachedOnly = true
			got, err := s.Get(t.Context(), r)
			if err != nil || got.RetryAfter != nil || got.Warning != "" || (got.Recap != nil) != saved {
				t.Fatalf("cancellation changed the saved retry state: %+v, %v", got, err)
			}
			r.CachedOnly = false
			calls := 0
			s.Generator = generatorFunc(func(_ context.Context, in Input) (Content, error) {
				calls++
				return contentFor(in), nil
			})
			got, err = s.Get(t.Context(), r)
			if err != nil || got.Status != "ready" || calls != 1 {
				t.Fatalf("retry after cancellation: %+v, %v, calls=%d", got, err, calls)
			}
		})
	}
}

func TestCorruptCacheIsRegeneratedWithoutTrustingItsContent(t *testing.T) {
	messages := []transcript.Message{{Role: "user", Text: "Goal"}}
	s := Service{Directory: t.TempDir(), Generator: generatorFunc(func(_ context.Context, in Input) (Content, error) { return contentFor(in), nil })}
	r := request(&messages)
	if _, err := s.Get(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(s.Directory, "*.json"))
	if err := os.WriteFile(files[0], []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.CachedOnly = true
	got, err := s.Get(t.Context(), r)
	if err != nil || got.Recap != nil || got.Warning == "" {
		t.Fatalf("corrupt %+v, %v", got, err)
	}
	r.CachedOnly = false
	got, err = s.Get(t.Context(), r)
	if err != nil || got.Status != "ready" || got.Warning != "" {
		t.Fatalf("rebuilt %+v, %v", got, err)
	}
}
