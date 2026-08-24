package screenstate

// Veto.String is a diagnostics contract, not cosmetics: the daemon logs a
// declined observation's veto set verbatim, and that log is the only answer to
// "why is this tab still on ⚡". A bitset number there names nothing.

import "testing"

func TestVetoStringNamesEveryBit(t *testing.T) {
	cases := []struct {
		name  string
		vetos Veto
		want  string
	}{
		{"empty set", 0, "none"},
		{"single", VetoLiveStatusLine, "live_status_line"},
		{"context limit", VetoContextLimit, "context_limit"},
		// A real permission capture carries both: the prompt itself, and the
		// absence of an empty composer behind it.
		{"prompt pair", VetoBlockingPrompt | VetoComposerBusy, "blocking_prompt|composer_busy"},
		// Bit order, not set order — the rendering has to be deterministic so two
		// journal lines for the same screen read identically.
		{"reversed input", VetoComposerBusy | VetoBlockingPrompt, "blocking_prompt|composer_busy"},
		{"every named bit", VetoInterruptHint | VetoLiveStatusLine | VetoBackgroundTask |
			VetoBlockingPrompt | VetoContextLimit | VetoComposerBusy,
			"interrupt_hint|live_status_line|background_task|blocking_prompt|context_limit|composer_busy"},
		// A veto added without a name here must still be visible. Dropping it
		// silently would make the log claim fewer reasons than actually fired.
		{"unnamed bit", 1 << 15, "0x8000"},
		{"named plus unnamed", VetoInterruptHint | 1<<15, "interrupt_hint|0x8000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.vetos.String(); got != tc.want {
				t.Fatalf("Veto(%06b).String() = %q, want %q", tc.vetos, got, tc.want)
			}
		})
	}
}

// TestVetoStringCoversEveryDeclaredBit fails when a new Veto constant is
// declared without a vetoNames entry, which would otherwise show up only as a
// hex value in a journal at the worst possible moment.
func TestVetoStringCoversEveryDeclaredBit(t *testing.T) {
	// The highest declared bit today. Widening the Veto set means bumping this
	// deliberately and adding the name.
	const declared = VetoInterruptHint | VetoLiveStatusLine | VetoBackgroundTask |
		VetoBlockingPrompt | VetoContextLimit | VetoComposerBusy
	var named Veto
	for _, entry := range vetoNames {
		named |= entry.bit
	}
	if named != declared {
		t.Fatalf("vetoNames covers %06b, want %06b — every declared Veto bit needs a name", named, declared)
	}
}
