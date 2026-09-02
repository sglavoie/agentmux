package activity

// Stabilization streaks: the positive half of the daemon's liveness-derived
// idle evidence. A probe that reads external evidence (a screen capture, a
// transcript stat) credits one sample per agreeing observation here, and only
// settles once the run reaches the count RequiredIdleSamples charges for that
// (harness, tier). The negative half — a frame the classifier refused to call
// terminal — is credited to Declines in decline.go.

// Ref keys per-window, per-probe-source activity evidence by window and probe
// source key, mirroring the shape of the daemon's other per-window leases. Two
// probe sources watching the same window accumulate independently, which is
// what keeps a Claude screen read and a context-limit read from merging into
// one count.
type Ref struct {
	WindowID int
	Key      string
}

// Samples accumulates consecutive agreeing idle observations per Ref, together
// with the evidence generation each run is pinned to.
//
// It is CALLER-LOCKED on purpose: the daemon mutates a streak in the same
// critical section as the reducer state the streak is about to settle, so an
// internal mutex here would buy nothing and would hide the fact that the two
// have to move together. Every method below assumes the caller holds whatever
// lock guards the surrounding decision.
//
// The zero value is ready to use: the maps are created on first write, so a
// Handler assembled as a bare struct literal (which the daemon's tests do)
// behaves exactly like one built by NewHandler.
type Samples struct {
	streak     map[Ref]int
	generation map[Ref]int64
}

// Record credits one stabilized idle observation for the ref and returns the
// new consecutive-sample count.
func (s *Samples) Record(ref Ref) int {
	if s.streak == nil {
		s.streak = make(map[Ref]int)
	}
	s.streak[ref]++
	return s.streak[ref]
}

// RecordWithGeneration credits one stabilized idle observation for the ref,
// keyed to an evidence generation. When the observed generation differs from
// the last one recorded for this ref, the streak restarts before this sample is
// counted: agreeing samples must observe the SAME generation. A zero generation
// disables the check and behaves exactly like Record.
func (s *Samples) RecordWithGeneration(ref Ref, generation int64) int {
	if generation != 0 {
		if s.generation == nil {
			s.generation = make(map[Ref]int64)
		}
		if s.generation[ref] != generation {
			delete(s.streak, ref)
			s.generation[ref] = generation
		}
	}
	return s.Record(ref)
}

// Clear resets a single probe source's streak and its recorded evidence
// generation.
func (s *Samples) Clear(ref Ref) {
	delete(s.streak, ref)
	delete(s.generation, ref)
}

// ClearWindow drops every streak for a window, used when fresh activity or a
// window closure invalidates all of its in-flight idle evidence.
func (s *Samples) ClearWindow(windowID int) {
	for ref := range s.streak {
		if ref.WindowID == windowID {
			delete(s.streak, ref)
		}
	}
	for ref := range s.generation {
		if ref.WindowID == windowID {
			delete(s.generation, ref)
		}
	}
}

// Streak reports the current consecutive-sample count for a ref, and zero when
// the ref holds none — a recorded streak is always at least one, so zero and
// absent are the same answer. The settle path reads the count Record returns
// rather than looking it up again; this is the point read for assertions and
// diagnostics.
func (s *Samples) Streak(ref Ref) int { return s.streak[ref] }

// Generation reports the evidence generation a ref's streak is pinned to, and
// zero when the ref holds none — zero is also the value that disables the
// check, so absent and unpinned are again the same answer.
func (s *Samples) Generation(ref Ref) int64 { return s.generation[ref] }
