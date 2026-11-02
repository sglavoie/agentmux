package conversation

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testLink(id, key string, source Identity) Link {
	return Link{ID: id, Source: source, TargetKey: key, Kind: "handoff", ArtifactPath: "/handoffs/" + id + ".md", CreatedAt: time.Unix(100, 0).UTC()}
}

func TestThreadBindingsSurviveRacesExitAndReopen(t *testing.T) {
	for _, observeFirst := range []bool{true, false} {
		t.Run(fmt.Sprint(observeFirst), func(t *testing.T) {
			store := ThreadStore{Path: filepath.Join(t.TempDir(), "private", "links.json")}
			a := Identity{Harness: "claude", SessionID: "same"}
			b := Identity{Harness: "codex", SessionID: "same"}
			c := Identity{Harness: "copilot", SessionID: "third"}
			observe := func() {
				if err := store.Observe(map[string]Identity{"receiving-b": b, "receiving-c": c}); err != nil {
					t.Fatal(err)
				}
			}
			if observeFirst {
				observe()
			}
			for _, link := range []Link{testLink("a-b", "receiving-b", a), testLink("b-c", "receiving-c", b)} {
				if err := store.Record(link); err != nil {
					t.Fatal(err)
				}
				if err := store.Record(link); err != nil {
					t.Fatal(err)
				}
			}
			if !observeFirst {
				observe()
			}
			// A new chat in the same terminal cannot steal an earlier handoff.
			if err := store.Observe(map[string]Identity{"receiving-b": {Harness: "codex", SessionID: "new-chat"}}); err != nil {
				t.Fatal(err)
			}
			reopened := ThreadStore{Path: store.Path}
			links, err := reopened.Links()
			if err != nil {
				t.Fatal(err)
			}
			if len(links) != 2 || links[0].Target == nil || *links[0].Target != b {
				t.Fatalf("lost receiving identity: %+v", links)
			}
			for _, member := range []Identity{a, b, c} {
				if len(Thread(links, member)) != 2 {
					t.Fatalf("chain not reachable from %v", member)
				}
			}
			if len(Thread(links, Identity{Harness: "copilot", SessionID: "same"})) != 0 {
				t.Fatal("identities crossed harnesses")
			}
			for path, perm := range map[string]os.FileMode{store.Path: 0o600, filepath.Dir(store.Path): 0o700} {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != perm {
					t.Fatalf("permissions %s: %v %v", path, info, err)
				}
			}
		})
	}
}

func TestThreadsKeepPendingBranchesAndTerminateCycles(t *testing.T) {
	a := Identity{Harness: "claude", SessionID: "a"}
	b := Identity{Harness: "codex", SessionID: "b"}
	links := []Link{testLink("one", "b", a), testLink("two", "a", b), testLink("pending", "pending", a)}
	links[0].Target = &b
	links[1].Target = &a
	if len(Thread(links, b)) != 3 {
		t.Fatal("lost cycle or pending branch")
	}
}

func TestConcurrentWritersDoNotLoseLinksOrBindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "links.json")
	var workers sync.WaitGroup
	for i := range 20 {
		workers.Go(func() {
			store := ThreadStore{Path: path}
			key := fmt.Sprintf("runtime-%d", i)
			if err := store.Record(testLink(key, key, Identity{Harness: "claude", SessionID: "source"})); err != nil {
				t.Error(err)
			}
			if err := store.Observe(map[string]Identity{key: {Harness: "codex", SessionID: key}}); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	links, err := (ThreadStore{Path: path}).Links()
	if err != nil || len(links) != 20 {
		t.Fatalf("lost writes: %d %v", len(links), err)
	}
	for _, link := range links {
		if link.Target == nil {
			t.Fatal("lost binding")
		}
	}
}

func TestCorruptThreadHistoryIsPreserved(t *testing.T) {
	for _, contents := range []string{"broken", `{}`, `null`, `{"schema_version":1}`, `{"schema_version":2,"bindings":{},"links":[]}`, `{"schema_version":1,"bindings":{},"links":[]} {}`} {
		store := ThreadStore{Path: filepath.Join(t.TempDir(), "links.json")}
		if err := os.WriteFile(store.Path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Links(); err == nil {
			t.Fatal("accepted corrupt file")
		}
		if err := store.Record(testLink("new", "runtime", Identity{Harness: "codex", SessionID: "s"})); err == nil {
			t.Fatal("replaced corrupt history")
		}
		got, _ := os.ReadFile(store.Path)
		if string(got) != contents {
			t.Fatal("modified unreadable history")
		}
	}
}
