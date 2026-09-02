package activity

import (
	"sort"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

// Screen-probe declines: the negative half of the idle evidence Samples
// accumulates. A decline is a screen the probe classified and Layer A refused
// to call a settleable terminal, credited per source so an operator can see why
// a window is not settling.

// ScreenDeclineObservation is one credited declined settle: the screen probe
// classified a frame and Layer A refused to call it a settleable terminal.
type ScreenDeclineObservation struct {
	// Streak is how many consecutive declines this probe source has now been
	// credited with, including the one just recorded. It restarts at one
	// whenever the observed fingerprint differs from the streak's.
	Streak int

	// Fingerprint is the normalized-screen fingerprint the streak is
	// accumulating against — the same value the stabilization gate would have
	// compared had the frame been settleable.
	Fingerprint int64

	// StableFingerprint reports whether the classifier has now declined the SAME
	// unchanging screen at least twice. This is the discriminator the decline
	// site's comment describes in prose: false means the pane is repainting,
	// which is what ordinary live work looks like, while true means the screen
	// went quiet and Layer A vetoed it anyway — the shape a classifier that has
	// stopped matching a redesigned harness UI takes. A zero fingerprint is
	// never stable, because the fail-closed zero Observation carries no hash at
	// all; that mirrors how Samples.RecordWithGeneration treats a zero
	// generation.
	StableFingerprint bool
}

// ScreenDeclineEvidence is one declined frame's diagnostic context, recorded
// alongside the streak it extends. Fingerprint is a decision input — it decides
// whether the streak continues or restarts — while Vetoes, StatusLine and At
// exist purely to be published: they are what tells an operator WHICH harness
// copy the classifier stopped matching, which a bare counter cannot.
type ScreenDeclineEvidence struct {
	// Fingerprint is the normalized-screen fingerprint of the declined frame,
	// the same value the stabilization gate would have compared.
	Fingerprint int64
	// Vetoes is screenstate.Veto.String() for the frame: the named reasons Layer
	// A refused, already rendered by the caller so this package never has to
	// know the bitset's spelling.
	Vetoes string
	// StatusLine is the harness status line the frame carried, empty when the
	// classifier found none.
	StatusLine string
	// At is when the decline was observed (unix seconds).
	At int64
}

// Declines accumulates declined settles per Ref, together with the fingerprint
// each streak is accumulating against and the evidence of the frame that last
// extended it.
//
// Like Samples it is CALLER-LOCKED: the daemon credits a decline in the same
// critical section that resets the stabilization streak and republishes the
// window's export metadata, and those three have to be one atomic step or a
// diagnostic surface could read a counter that describes a different frame than
// the one just credited.
//
// The zero value is ready to use, for the same reason Samples' is.
type Declines struct {
	streak      map[Ref]int
	fingerprint map[Ref]int64
	evidence    map[Ref]ScreenDeclineEvidence
}

// Streak reports the current consecutive-decline count for a ref, and zero when
// the ref holds none.
func (d *Declines) Streak(ref Ref) int { return d.streak[ref] }

// Fingerprint reports the normalized-screen fingerprint a ref's decline streak
// is accumulating against, and zero when the ref holds none.
func (d *Declines) Fingerprint(ref Ref) int64 { return d.fingerprint[ref] }

// Record credits one declined settle for the ref against the fingerprint the
// frame carried, and returns the resulting decline state. A fingerprint that
// differs from the one the streak is accumulating against restarts the streak
// before this decline is counted, so the streak only ever measures repeated
// refusals of the same screen. The frame's remaining evidence is retained for
// export.
func (d *Declines) Record(ref Ref, evidence ScreenDeclineEvidence) ScreenDeclineObservation {
	if d.streak == nil {
		d.streak = make(map[Ref]int)
	}
	if d.fingerprint == nil {
		d.fingerprint = make(map[Ref]int64)
	}
	if d.evidence == nil {
		d.evidence = make(map[Ref]ScreenDeclineEvidence)
	}
	if prev, ok := d.fingerprint[ref]; !ok || prev != evidence.Fingerprint {
		delete(d.streak, ref)
		d.fingerprint[ref] = evidence.Fingerprint
	}
	d.streak[ref]++
	d.evidence[ref] = evidence
	streak := d.streak[ref]
	return ScreenDeclineObservation{
		Streak:            streak,
		Fingerprint:       evidence.Fingerprint,
		StableFingerprint: evidence.Fingerprint != 0 && streak > 1,
	}
}

// Clear drops a probe source's decline streak, the fingerprint it was
// accumulating against, and the evidence of the frame that last extended it.
func (d *Declines) Clear(ref Ref) {
	delete(d.streak, ref)
	delete(d.fingerprint, ref)
	delete(d.evidence, ref)
}

// Empty reports whether no decline is recorded at all. The retraction path uses
// it to early-exit for the overwhelming majority of windows, which hold none.
func (d *Declines) Empty() bool { return len(d.streak) == 0 }

// RetractWindow drops every decline recorded for a window and reports whether
// anything was actually retracted, so the caller knows whether the window's
// export metadata needs republishing.
//
// A decline streak is a LIVE counter — "the classifier is refusing this screen
// right now" — and never a history: the durable per-window event ring is where
// history lives. That distinction is the whole reason this exists. The screen
// probe only runs while the window's reduced icon is ⚡, so the moment a native
// stop, a blocking prompt, or a clear/reset moves the window off it the probe
// stops being called and can no longer retract anything itself; without an
// explicit retraction the last streak would be frozen into the export forever
// and `amx doctor` would keep failing screenstate_persistent_decline for a
// window that already recovered.
//
// It is also what a closed window's cleanup calls: a closed pane will never be
// classified again, so a streak left behind would be a permanently frozen count
// that no later frame can ever advance or clear.
func (d *Declines) RetractWindow(windowID int) bool {
	if len(d.streak) == 0 {
		return false
	}
	retracted := false
	for ref := range d.streak {
		if ref.WindowID != windowID {
			continue
		}
		d.Clear(ref)
		retracted = true
	}
	return retracted
}

// Snapshots renders a window's live decline counters as the export shape,
// sorted by source so the published order is stable.
//
// The whole slice is rebuilt rather than patched, because clearing a streak has
// to remove its entry as surely as recording one has to add it, and a window
// carrying two probe sources must never report a merged count. StableFingerprint
// is recomputed from the stored streak and fingerprint rather than carried over
// from the observation, so the published value can never contradict the state it
// is derived from.
func (d *Declines) Snapshots(windowID int) []protocol.ScreenDeclineSnapshot {
	snapshots := make([]protocol.ScreenDeclineSnapshot, 0, 1)
	for ref, streak := range d.streak {
		if ref.WindowID != windowID || streak <= 0 {
			continue
		}
		fingerprint := d.fingerprint[ref]
		evidence := d.evidence[ref]
		snapshots = append(snapshots, protocol.ScreenDeclineSnapshot{
			Source:            ref.Key,
			Streak:            streak,
			StableFingerprint: fingerprint != 0 && streak > 1,
			Fingerprint:       fingerprint,
			Vetoes:            evidence.Vetoes,
			StatusLine:        evidence.StatusLine,
			At:                evidence.At,
		})
	}
	if len(snapshots) == 0 {
		return nil
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].Source < snapshots[j].Source })
	return snapshots
}
