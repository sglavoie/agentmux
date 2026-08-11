package daemon

import "github.com/sglavoie/agentmux/internal/conversation"

// CurrentConversation returns the still-live parent harness identity that owns
// the managed tab. Only SourceHarness facts can establish it: subagent facts
// carry a parent session for lifecycle accounting, but do not prove that parent
// still owns the managed tab. The interactive owner wins over a newer pipe-mode
// `claude -p` child (which shares the parent-harness kind but is a background
// invocation the user is not driving); among facts of the same pipe-ness the
// newest wins. Acknowledged/expired facts are stale, and a persisted source key
// must exactly agree with its fact session ID so malformed restored state cannot
// create a live mapping.
func (r *Reducer) CurrentConversation(now int64) (conversation.Identity, bool) {
	return r.currentConversation(now, false)
}

// CurrentInteractiveConversation is the interactive-only counterpart to
// CurrentConversation: it returns the still-live interactive parent identity and
// never falls back to a sole pipe-mode `claude -p` child. The Copilot transcript
// probe uses it to resolve the session backing a stuck ⚡ so a background pipe
// invocation can never select the transcript the probe settles: a pipe-only
// window resolves to no interactive conversation and the probe bails before any
// transcript resolution or read. CurrentConversation's pipe-fallback semantics
// remain correct for descriptive callers that just want the owning conversation.
func (r *Reducer) CurrentInteractiveConversation(now int64) (conversation.Identity, bool) {
	return r.currentConversation(now, true)
}

func (r *Reducer) currentConversation(now int64, interactiveOnly bool) (conversation.Identity, bool) {
	if r == nil {
		return conversation.Identity{}, false
	}

	var current *sourceFact
	var identity conversation.Identity
	for _, fact := range r.facts {
		if fact.source.Kind != SourceHarness || !conversationFactLive(fact, now) {
			continue
		}
		if interactiveOnly && fact.pipeMode {
			continue
		}
		candidate, ok := conversation.New(harnessFromSourceKey(fact.source.Key, fact.sessionID), fact.sessionID)
		if !ok || fact.source.Key != candidate.Harness+":"+candidate.SessionID {
			continue
		}
		if current == nil || moreCurrentConversation(fact, current) {
			current = fact
			identity = candidate
		}
	}
	return identity, current != nil
}

// moreCurrentConversation reports whether f is a better current-conversation
// owner than best. An interactive fact always beats a pipe-mode one regardless
// of recency, so a `claude -p` child cannot displace the interactive session
// the user is watching; among facts of the same pipe-ness, the more recent one
// wins.
func moreCurrentConversation(f, best *sourceFact) bool {
	if f.pipeMode != best.pipeMode {
		return !f.pipeMode
	}
	return moreRecent(f, best)
}

func conversationFactLive(fact *sourceFact, now int64) bool {
	if fact == nil || !fact.set || fact.acked {
		return false
	}
	// Keep a clean terminal fact only through the same retention interval used
	// by Compact. A sticky failure remains current until it is acknowledged or
	// replaced by a newer parent fact.
	if fact.terminated && fact.icon != "error" && tombstoneRetentionSeconds > 0 && now-fact.terminalAt >= tombstoneRetentionSeconds {
		return false
	}
	return true
}

func harnessFromSourceKey(key, sessionID string) string {
	suffix := ":" + sessionID
	if sessionID == "" || len(key) <= len(suffix) || key[len(key)-len(suffix):] != suffix {
		return ""
	}
	return key[:len(key)-len(suffix)]
}
