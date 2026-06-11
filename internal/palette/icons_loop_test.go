package palette

import "testing"

func TestLoopIcon(t *testing.T) {
	if got := LoopIcon(); got != "🔁" {
		t.Errorf("LoopIcon() = %q, want %q", got, "🔁")
	}
}

func TestBuildParseTitlePartsLoopRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		parts TitleParts
		want  string
	}{
		{
			name:  "loop only",
			parts: TitleParts{Base: "api", Looping: true},
			want:  "🔁api",
		},
		{
			name:  "state and loop",
			parts: TitleParts{Base: "api", State: "bolt", Looping: true},
			want:  "⚡🔁api",
		},
		{
			name: "star mute state and loop",
			parts: TitleParts{
				Base:    "api",
				State:   "bolt",
				Starred: true,
				Muted:   true,
				Looping: true,
			},
			want: "★🔇⚡🔁api",
		},
		{
			name:  "harness emoji base title",
			parts: TitleParts{Base: "🦊api", State: "key", Looping: true},
			want:  "🔑🔁🦊api",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			title := BuildTitleParts(tc.parts)
			if title != tc.want {
				t.Fatalf("BuildTitleParts(%+v) = %q, want %q", tc.parts, title, tc.want)
			}

			got := ParseTitleParts(title)
			if got != tc.parts {
				t.Errorf("ParseTitleParts(%q) = %+v, want %+v", title, got, tc.parts)
			}
			if got := StripIcons(title); got != tc.parts.Base {
				t.Errorf("StripIcons(%q) = %q, want %q", title, got, tc.parts.Base)
			}
			if got := DetectState(title); got != tc.parts.State {
				t.Errorf("DetectState(%q) = %q, want %q", title, got, tc.parts.State)
			}
		})
	}
}

func TestParseTitlePartsLegacyLoopOrder(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  TitleParts
		canon string
	}{
		{
			name:  "loop before state",
			input: "🔁⚡api",
			want:  TitleParts{Base: "api", State: "bolt", Looping: true},
			canon: "⚡🔁api",
		},
		{
			name:  "loop between star mute and state",
			input: "★🔇🔁⚡api",
			want:  TitleParts{Base: "api", State: "bolt", Starred: true, Muted: true, Looping: true},
			canon: "★🔇⚡🔁api",
		},
		{
			name:  "loop before all icons",
			input: "🔁★🔇⚡api",
			want:  TitleParts{Base: "api", State: "bolt", Starred: true, Muted: true, Looping: true},
			canon: "★🔇⚡🔁api",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseTitleParts(tc.input)
			if got != tc.want {
				t.Fatalf("ParseTitleParts(%q) = %+v, want %+v", tc.input, got, tc.want)
			}
			if canon := BuildTitleParts(got); canon != tc.canon {
				t.Errorf("BuildTitleParts(ParseTitleParts(%q)) = %q, want %q", tc.input, canon, tc.canon)
			}
		})
	}
}

func TestParseTitleFullWrapperDiscardsLoop(t *testing.T) {
	title := BuildTitleParts(TitleParts{
		Base:    "api",
		State:   "bolt",
		Starred: true,
		Muted:   true,
		Looping: true,
	})
	base, state, starred, muted := ParseTitleFull(title)
	if base != "api" || state != "bolt" || !starred || !muted {
		t.Errorf("ParseTitleFull(%q) = (%q, %q, %v, %v), want (api, bolt, true, true)",
			title, base, state, starred, muted)
	}

	if rebuilt := BuildTitleFull(base, state, starred, muted); rebuilt != "★🔇⚡api" {
		t.Errorf("BuildTitleFull wrapper after loop parse = %q, want %q", rebuilt, "★🔇⚡api")
	}
}
