package tui

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/tui/commands"
)

func TestAcceptanceMalformedPlanCacheRebuildsWithoutDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	planRoot := t.TempDir()
	seedPlanDir(t, planRoot, "recovered-aabb112233cc", map[string]string{
		"manifest.md": "# Recovered plan\n\nfilesystem authority",
	})
	cachePath := plans.IndexPath()
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte(`{"entries":`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Defaults.PlansDirectory = planRoot
	m := Model{mode: ModeList, cfg: cfg, width: 100, height: 30}
	updated, loadCmd := m.enterPlanSearch()
	m = updated.(Model)
	loaded := loadCmd().(planCacheLoadDoneMsg)
	if loaded.idx == nil || loaded.loadErr == nil {
		t.Fatalf("recovering load = index %p warning %v", loaded.idx, loaded.loadErr)
	}
	if matches, _ := filepath.Glob(cachePath + ".corrupt-*"); len(matches) != 1 {
		t.Fatalf("quarantined caches = %v, want one", matches)
	}

	updated, refreshBatch := m.Update(loaded)
	m = updated.(Model)
	refreshDone := runPlansRefreshTestCmd(t, refreshBatch)
	updated, searchCmd := m.Update(refreshDone)
	m = updated.(Model)
	searchDone := searchCmd().(planSearchDoneMsg)
	updated, _ = m.Update(searchDone)
	m = updated.(Model)
	if m.planRefreshErr != nil || m.planSearchErr != nil || len(m.planResults) != 1 {
		t.Fatalf("rebuilt plans = results %d refresh %v search %v", len(m.planResults), m.planRefreshErr, m.planSearchErr)
	}
	assertNoRetiredDatabaseArtifacts(t, home)
}

func TestAcceptanceRepeatedPlanRefreshRetainsLastGoodResults(t *testing.T) {
	planRoot := t.TempDir()
	seedPlanDir(t, planRoot, "alpha-aabb112233cc", map[string]string{"manifest.md": "# Alpha"})
	idx, err := plans.LoadIndexAt(filepath.Join(t.TempDir(), "plans.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Refresh(planRoot); err != nil {
		t.Fatal(err)
	}
	results, err := plans.Search(idx, plans.Query{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{}
	cfg.Defaults.PlansDirectory = planRoot
	m := Model{
		mode: ModePlanSearch, cfg: cfg, plansIndex: idx,
		planResults: results, width: 100, height: 30,
	}

	for iteration := 0; iteration < 2; iteration++ {
		updated, batchCmd := m.startPlanSearchRefresh()
		m = updated.(Model)
		refreshDone := runPlansRefreshTestCmd(t, batchCmd)
		updated, searchCmd := m.Update(refreshDone)
		m = updated.(Model)
		updated, _ = m.Update(searchCmd())
		m = updated.(Model)
		if m.planRefreshErr != nil || m.planSearchErr != nil || len(m.planResults) != 1 {
			t.Fatalf("refresh %d = results %d refresh %v search %v", iteration, len(m.planResults), m.planRefreshErr, m.planSearchErr)
		}
	}

	lastGood := append([]plans.Result(nil), m.planResults...)
	m.planSearchSeq++
	updated, _ := m.Update(planSearchDoneMsg{seq: m.planSearchSeq, err: errors.New("injected search failure")})
	m = updated.(Model)
	if !reflect.DeepEqual(m.planResults, lastGood) {
		t.Fatalf("search failure replaced last-good rows: before %#v after %#v", lastGood, m.planResults)
	}

	cfg.Defaults.PlansDirectory = filepath.Join(t.TempDir(), "missing")
	updated, batchCmd := m.startPlanSearchRefresh()
	m = updated.(Model)
	refreshDone := runPlansRefreshTestCmd(t, batchCmd)
	if refreshDone.err == nil {
		t.Fatal("missing plans root did not fail refresh")
	}
	updated, searchCmd := m.Update(refreshDone)
	m = updated.(Model)
	updated, _ = m.Update(searchCmd())
	m = updated.(Model)
	if !reflect.DeepEqual(m.planResults, lastGood) {
		t.Fatalf("refresh failure replaced last-good rows: before %#v after %#v", lastGood, m.planResults)
	}
}

func TestAcceptanceWarmPastSessionsStayVisibleAndPartialRefreshPublishesSuccessfulRows(t *testing.T) {
	homeRoot := t.TempDir()
	t.Setenv("HOME", homeRoot)
	snapshot, root, err := newHermeticUITestSnapshot(t.TempDir())
	if err != nil {
		t.Fatalf("create hermetic snapshot: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove fixture root: %v", err)
		}
	})

	home := filepath.Join(root, "slow-profile")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(home, "slow.jsonl")
	if err := os.WriteFile(sourcePath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseReader := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseReader)
	reader := &blockingAcceptanceReader{
		started:     started,
		release:     release,
		discoverErr: errors.New("one transcript source was unreadable"),
		metas: []transcript.SessionMeta{{
			ID: "slow-refresh", Harness: harness.Claude, Path: sourcePath,
			FileSize: info.Size(), FileModTime: info.ModTime(), MessageCount: 1,
			StartedAt: time.Now().Add(-time.Minute), LastAt: time.Now(),
			Snippet: "fresh row after slow refresh", SearchText: "fresh row after slow refresh",
		}},
	}
	cfg := &config.Config{Profiles: []config.Account{{Name: "work", Harness: harness.Claude, HomeDir: home}}}
	m := Model{
		cfg:               cfg,
		transcriptIndex:   snapshot.transcriptIndex,
		transcriptReaders: map[string]transcript.Reader{harness.Claude: reader},
		sessionResults:    append([]transcript.Result(nil), snapshot.transcriptResults...),
	}
	oldResults := append([]transcript.Result(nil), m.sessionResults...)
	updated, cmd := m.enterSessionSearch()
	m = updated.(Model)
	if cmd == nil || len(oldResults) == 0 {
		t.Fatalf("warm refresh setup = cmd %v old results %d", cmd != nil, len(oldResults))
	}
	done := make(chan any, 1)
	go func() { done <- cmd() }()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		releaseReader()
		t.Fatal("transcript refresh did not reach the blocking reader")
	}
	if !reflect.DeepEqual(m.sessionResults, oldResults) {
		t.Fatalf("in-flight refresh replaced warm rows: before %#v after %#v", oldResults, m.sessionResults)
	}
	releaseReader()

	var msg any
	select {
	case msg = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("transcript refresh did not complete")
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.sessionRefreshErr == nil || m.sessionSearchErr != nil {
		t.Fatalf("partial refresh errors = refresh %v search %v; want surfaced refresh warning and usable search", m.sessionRefreshErr, m.sessionSearchErr)
	}
	found := false
	for _, result := range m.sessionResults {
		if result.Meta.ID == "slow-refresh" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("completed refresh did not publish the new transcript: %#v", m.sessionResults)
	}
	assertNoRetiredDatabaseArtifacts(t, homeRoot)
}

func TestAcceptanceTUIContinuityForkUsesFilesystemSnapshotWithoutDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fixture, err := uitestFixtureByName("basic")
	if err != nil {
		t.Fatal(err)
	}
	driver, client := newUITestFixtureDriver(fixture, fixture.Size)
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Errorf("close TUI driver: %v", err)
		}
	})
	if err := driver.Key("S"); err != nil {
		t.Fatalf("open Past Sessions: %v", err)
	}
	if err := driver.Key("f"); err != nil {
		t.Fatalf("fork past session: %v", err)
	}
	if got := len(client.CallsFor("LaunchTab")); got != 1 {
		t.Fatalf("recorded LaunchTab calls = %d, want 1", got)
	}
	if mode := driver.Model().mode; mode != ModeSessionSearch {
		t.Fatalf("mode after continuity fork = %v, want Past Sessions", mode)
	}
	assertNoRetiredDatabaseArtifacts(t, home)
}

func TestAcceptancePastSessionsSharedProjectAliasContinuesAndForksInAliasContext(t *testing.T) {
	root := t.TempDir()
	sharedDir := filepath.Join(root, "shared")
	if err := os.MkdirAll(sharedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(root, "shared-session.jsonl")
	if err := os.WriteFile(sourcePath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	meta := transcript.SessionMeta{
		ID:       "shared-session",
		Harness:  harness.Claude,
		Profile:  "work",
		Project:  "SS",
		Projects: []string{"SS", "streamutil"},
		CWD:      sharedDir,
		Path:     sourcePath,
	}
	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: harness.Claude}},
		Projects: []config.Session{
			{Title: "SS", Directory: sharedDir, Profile: "work"},
			{Title: "streamutil", Directory: sharedDir, Profile: "work"},
		},
	}
	reader := sessionSearchTestReader{harness: harness.Claude}

	for _, key := range []string{"c", "f"} {
		t.Run(key, func(t *testing.T) {
			client := newUITestKittyClient(nil)
			m := Model{
				mode:              ModeSessionSearch,
				cfg:               cfg,
				client:            client,
				filterProject:     []string{"streamutil"},
				transcriptReaders: map[string]transcript.Reader{harness.Claude: reader},
				sessionResults:    []transcript.Result{{Meta: meta}},
				daemonCommand: func(protocol.Request) (protocol.Response, error) {
					return protocol.Response{OK: true}, nil
				},
			}

			updated, cmd := m.Update(keyPress(key))
			got := updated.(Model)
			if cmd == nil || got.mode != ModeSessionSearch {
				t.Fatalf("%s = mode %v cmd %T, want continuity command in Past Sessions", key, got.mode, cmd)
			}
			done, ok := cmd().(commands.ActionDoneMsg)
			if !ok {
				t.Fatalf("%s command produced unexpected message", key)
			}
			if done.Err != nil {
				t.Fatalf("%s action failed: %v", key, done.Err)
			}
			launches := client.CallsFor("LaunchTab")
			if len(launches) != 1 || launches[0].Opts == nil {
				t.Fatalf("%s LaunchTab calls = %+v, want one launch", key, launches)
			}
			opts := launches[0].Opts
			if opts.UserVars["amx_project"] != "streamutil" || opts.Directory != sharedDir {
				t.Fatalf("%s launch = project %q directory %q, want streamutil and %q", key, opts.UserVars["amx_project"], opts.Directory, sharedDir)
			}
			if !strings.Contains(strings.Join(opts.Cmdline, " "), meta.ID) {
				t.Fatalf("%s launch command %q did not retain transcript ID %q", key, strings.Join(opts.Cmdline, " "), meta.ID)
			}
			if m.sessionResults[0].Meta.Project != "SS" {
				t.Fatalf("%s mutated stored project to %q", key, m.sessionResults[0].Meta.Project)
			}
		})
	}
}

type blockingAcceptanceReader struct {
	started     chan struct{}
	release     chan struct{}
	metas       []transcript.SessionMeta
	discoverErr error
	once        sync.Once
}

func (r *blockingAcceptanceReader) Harness() string { return harness.Claude }

func (r *blockingAcceptanceReader) Discover(string, transcript.DiscoverOptions) ([]transcript.SessionMeta, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	metas := append([]transcript.SessionMeta(nil), r.metas...)
	for i := range metas {
		if metas[i].SessionClass == "" {
			metas[i].SessionClass = transcript.SessionClassMain
		}
	}
	return metas, r.discoverErr
}

func (*blockingAcceptanceReader) ReadMessages(string) ([]transcript.Message, error) {
	return nil, nil
}

func assertNoRetiredDatabaseArtifacts(t *testing.T, home string) {
	t.Helper()
	dataDir := filepath.Join(home, ".local", "share", "agent-mux")
	for _, name := range []string{
		"agent-mux.db",
		"agent-mux.db-wal",
		"agent-mux.db-shm",
		"agent-mux.db.recovery.lock",
		"agent-mux.db.rebuild",
		"agent-mux.db.rebuild-startup.lock",
		"recovery",
	} {
		if _, err := os.Lstat(filepath.Join(dataDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("retired database artifact %s exists after TUI flow: %v", name, err)
		}
	}
}

func runPlansRefreshTestCmd(t *testing.T, cmd tea.Cmd) plansRefreshDoneMsg {
	t.Helper()
	msg := cmd()
	if done, ok := msg.(plansRefreshDoneMsg); ok {
		return done
	}
	batch, ok := msg.(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("plans refresh command produced %T", msg)
	}
	done, ok := batch[0]().(plansRefreshDoneMsg)
	if !ok {
		t.Fatalf("first plans refresh command produced %T", batch[0]())
	}
	return done
}
