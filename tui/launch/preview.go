package launch

import (
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// runningPeekLines caps the scrollback tail rendered below the
// "── running ──" divider in the launcher preview pane.
const runningPeekLines = 20

// RenderPreview renders the right-hand pane for the launcher: a labeled
// config block, then (if the highlighted session is currently running) a
// divider and the tail of its scrollback. Output is exactly height lines.
func (in PickerInput) RenderPreview(origIdx, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	sess := in.SessionWithResolvedProfile(origIdx)
	defaultProfile := in.Sessions[origIdx].EffectiveProfile()
	acctColor := dashboard.ProfileColorMap(in.Cfg)

	lines := make([]string, 0, height)
	header := accountStyle(acctColor[defaultProfile]).Render(
		layout.Truncate(layout.RenderTitleIcon(configSessionDisplayTitle(sess)), width, "…"),
	)
	lines = append(lines, header)

	addField := func(label, value string) {
		if value == "" {
			return
		}
		body := styleDim.Render(label+": ") + value
		lines = append(lines, layout.StyledTruncate(body, width, "…"))
	}
	addField("title", sess.Title)
	addField("dir", sess.Directory)
	if sess.EffectiveProfile() != defaultProfile {
		addField("default profile", defaultProfile)
		addField("launch profile", sess.EffectiveProfile())
	} else {
		addField("profile", sess.EffectiveProfile())
	}
	if acc, ok := findAccountByName(in.Cfg.Accounts, sess.EffectiveProfile()); ok {
		addField("harness", acc.EffectiveHarness())
	}
	if len(sess.Args) > 0 {
		addField("args", strings.Join(sess.Args, " "))
	}
	if len(sess.Env) > 0 {
		keys := make([]string, 0, len(sess.Env))
		for k := range sess.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		kvs := make([]string, 0, len(keys))
		for _, k := range keys {
			kvs = append(kvs, k+"="+sess.Env[k])
		}
		addField("env", strings.Join(kvs, " "))
	}
	if len(sess.Groups) > 0 {
		addField("groups", strings.Join(sess.Groups, ", "))
	}

	if in.Running[session.SessionKey(sess.EffectiveProfile(), sess.Title)] > 0 {
		lines = append(lines, styleDim.Render("── running ──"))
		remaining := height - len(lines)
		if remaining > runningPeekLines {
			remaining = runningPeekLines
		}
		if remaining > 0 {
			lines = append(lines, scrollbackTail(in.PreviewText, in.PreviewError, remaining, width)...)
		}
	}

	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// scrollbackTail returns up to height lines from the end of text, truncated to
// width. Mirrors the dashboard preview body's tail logic without the padding
// (RenderPreview owns the final padding to exactly height).
func scrollbackTail(text, errText string, height, width int) []string {
	if height <= 0 {
		return nil
	}
	if errText != "" {
		return []string{styleError.Render(layout.Truncate("error: "+errText, width, "…"))}
	}
	if text == "" {
		return []string{styleDim.Render("(loading…)")}
	}
	raw := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(raw) > height {
		raw = raw[len(raw)-height:]
	}
	out := make([]string, len(raw))
	for i, ln := range raw {
		out[i] = layout.Truncate(ln, width, "…")
	}
	return out
}
