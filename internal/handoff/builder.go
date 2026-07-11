package handoff

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
)

const (
	gitFieldMaxBytes = 4 * 1024
	omittedGitField  = "(omitted to satisfy max_bytes)"
)

type preparedPlan struct {
	name      string
	directory string
	paths     []string
	excerpts  []planExcerpt
}

type planExcerpt struct {
	path string
	text string
}

type renderInput struct {
	source    Source
	goal      string
	messages  []messageView
	plans     []preparedPlan
	git       GitState
	paths     []string
	omissions []Omission
}

type messageView struct {
	role      string
	text      string
	timestamp time.Time
}

// Build creates one deterministic Markdown pack. It never mutates source
// transcripts or plans and returns ErrBudgetTooSmall rather than emitting an
// artifact that exceeds the configured maximum.
func (b *Builder) Build(ctx context.Context, source Source, options Options) (Pack, error) {
	if b == nil {
		b = NewBuilder()
	}
	opts, err := normalizeOptions(options)
	if err != nil {
		return Pack{}, err
	}
	if strings.TrimSpace(source.Goal) == "" {
		return Pack{}, fmt.Errorf("handoff: original user goal is empty")
	}
	if strings.TrimSpace(source.CWD) == "" {
		return Pack{}, fmt.Errorf("handoff: source CWD is empty")
	}

	paths, pathOmissions := collectSourcePaths(source)
	plansPrepared, planOmissions := preparePlans(source.LinkedPlans, opts.PlanExcerptBytes)
	messages, messageOmissions := selectMessages(source.Messages, opts.RecentMessages)
	inspector := b.git
	if inspector == nil {
		inspector = commandGitInspector{}
	}
	git := inspector.Inspect(ctx, source.CWD, opts.GitTimeout)

	baseOmissions := append(pathOmissions, planOmissions...)
	baseOmissions = append(baseOmissions, messageOmissions...)
	input := renderInput{
		source: source, goal: source.Goal, messages: messages, plans: plansPrepared,
		git: git, paths: paths, omissions: baseOmissions,
	}
	boundLargeGitFields(&input)

	budgetDroppedMessages := 0
	for {
		content := renderPack(input, budgetDroppedMessages)
		if len(content) <= opts.MaxBytes {
			sum := sha256.Sum256(content)
			return Pack{
				Content: append([]byte(nil), content...), ContentHash: fmt.Sprintf("%x", sum[:]),
				Git: input.git, SourcePaths: append([]string(nil), input.paths...),
				Omissions: omissionsForRender(input.omissions, budgetDroppedMessages),
			}, nil
		}
		if len(input.messages) > 0 {
			input.messages = input.messages[1:]
			budgetDroppedMessages++
			continue
		}
		if removeLastExcerpt(&input) {
			continue
		}
		if input.git.Available && input.git.DiffStat != "" && input.git.DiffStat != omittedGitField {
			input.git.DiffStat = omittedGitField
			input.omissions = append(input.omissions, Omission{Kind: "Git diff stat", Detail: "omitted to satisfy max_bytes"})
			continue
		}
		if input.git.Available && input.git.Status != "" && input.git.Status != omittedGitField {
			input.git.Status = omittedGitField
			input.omissions = append(input.omissions, Omission{Kind: "Git status", Detail: "omitted to satisfy max_bytes"})
			continue
		}
		if len(input.goal) > 128 {
			oldBytes := len(input.goal)
			input.goal = truncateUTF8(input.goal, max(128, oldBytes-(len(content)-opts.MaxBytes)-64))
			if len(input.goal) >= oldBytes {
				input.goal = truncateUTF8(input.goal, oldBytes/2)
			}
			input.omissions = append(input.omissions, Omission{
				Kind: "goal", Detail: fmt.Sprintf("original user goal truncated from %d to %d bytes to satisfy max_bytes", oldBytes, len(input.goal)),
			})
			continue
		}
		return Pack{}, fmt.Errorf("%w: required sections need %d bytes but max_bytes is %d", ErrBudgetTooSmall, len(content), opts.MaxBytes)
	}
}

func normalizeOptions(options Options) (Options, error) {
	defaults := DefaultOptions()
	if options.MaxBytes == 0 {
		options.MaxBytes = defaults.MaxBytes
	}
	if options.RecentMessages == 0 {
		options.RecentMessages = defaults.RecentMessages
	}
	if options.PlanExcerptBytes == 0 {
		options.PlanExcerptBytes = defaults.PlanExcerptBytes
	}
	if options.GitTimeout == 0 {
		options.GitTimeout = defaults.GitTimeout
	}
	if options.MaxBytes < MinimumMaxBytes {
		return Options{}, fmt.Errorf("%w: max_bytes must be at least %d", ErrBudgetTooSmall, MinimumMaxBytes)
	}
	if options.RecentMessages < 0 {
		return Options{}, fmt.Errorf("handoff: recent_messages must be non-negative")
	}
	if options.PlanExcerptBytes < 0 {
		return Options{}, fmt.Errorf("handoff: plan excerpt limit must be non-negative")
	}
	if options.GitTimeout < 0 {
		return Options{}, fmt.Errorf("handoff: Git timeout must be non-negative")
	}
	return options, nil
}

func collectSourcePaths(source Source) ([]string, []Omission) {
	paths := make([]string, 0, 2+len(source.LinkedPlans)*2)
	omissions := make([]Omission, 0)
	add := func(kind, path string) {
		if path == "" {
			omissions = append(omissions, Omission{Kind: "source path", Detail: kind + " path was not supplied"})
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			omissions = append(omissions, Omission{Kind: "source path", Detail: kind + " path could not be resolved"})
			return
		}
		paths = append(paths, filepath.Clean(abs))
	}
	add("working directory", source.CWD)
	add("transcript", source.TranscriptPath)
	for _, plan := range source.LinkedPlans {
		add("linked plan directory", plan.DirPath)
		for _, file := range plan.Files {
			add("linked plan file", planFilePath(plan.DirPath, file))
		}
	}
	sort.Strings(paths)
	paths = compactStrings(paths)
	return paths, omissions
}

func selectMessages(messages []transcript.Message, recent int) ([]messageView, []Omission) {
	eligible := make([]messageView, 0, len(messages))
	filtered := 0
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		text, ok := message.TextForHandoff()
		if !ok {
			filtered++
			continue
		}
		eligible = append(eligible, messageView{role: role, text: text, timestamp: message.Timestamp})
	}
	omissions := make([]Omission, 0, 2)
	if filtered > 0 {
		omissions = append(omissions, Omission{Kind: "transcript", Detail: fmt.Sprintf("%d non-user/assistant or empty message(s) excluded", filtered)})
	}
	if recent < len(eligible) {
		omissions = append(omissions, Omission{Kind: "transcript", Detail: fmt.Sprintf("%d older user/assistant message(s) outside recent_messages=%d", len(eligible)-recent, recent)})
		eligible = eligible[len(eligible)-recent:]
	}
	return eligible, omissions
}

func preparePlans(metas []plans.PlanMeta, totalLimit int) ([]preparedPlan, []Omission) {
	ordered := append([]plans.PlanMeta(nil), metas...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].DirPath != ordered[j].DirPath {
			return ordered[i].DirPath < ordered[j].DirPath
		}
		return ordered[i].Name < ordered[j].Name
	})
	remaining := totalLimit
	prepared := make([]preparedPlan, 0, len(ordered))
	omissions := make([]Omission, 0)
	for _, meta := range ordered {
		planName := strings.TrimSpace(meta.Name)
		if planName == "" && meta.DirPath != "" {
			planName = filepath.Base(filepath.Clean(meta.DirPath))
		}
		if planName == "" {
			planName = "(unnamed plan)"
		}
		plan := preparedPlan{name: planName}
		if meta.DirPath == "" {
			plan.directory = "(not supplied)"
			omissions = append(omissions, Omission{Kind: "plan source", Detail: "linked plan directory was not supplied"})
		} else {
			plan.directory = absoluteClean(meta.DirPath)
		}
		files := append([]string(nil), meta.Files...)
		sort.Strings(files)
		if len(files) == 0 {
			omissions = append(omissions, Omission{Kind: "plan source", Detail: "linked plan has no source files: " + plan.directory})
		}
		for _, file := range files {
			if meta.DirPath == "" && !filepath.IsAbs(file) {
				omissions = append(omissions, Omission{Kind: "plan excerpt", Detail: "omitted because the plan directory is missing: " + file})
				continue
			}
			path := absoluteClean(planFilePath(meta.DirPath, file))
			plan.paths = append(plan.paths, path)
			if remaining <= 0 {
				omissions = append(omissions, Omission{Kind: "plan excerpt", Detail: "omitted by plan excerpt cap: " + path})
				continue
			}
			text, used, truncated, reason := readBoundedRegularFile(path, remaining)
			if reason != "" {
				omissions = append(omissions, Omission{Kind: "plan excerpt", Detail: reason + ": " + path})
				continue
			}
			plan.excerpts = append(plan.excerpts, planExcerpt{path: path, text: text})
			remaining -= used
			if truncated {
				omissions = append(omissions, Omission{Kind: "plan excerpt", Detail: fmt.Sprintf("truncated at %d bytes by plan excerpt cap: %s", used, path)})
			}
		}
		prepared = append(prepared, plan)
	}
	return prepared, omissions
}

func readBoundedRegularFile(path string, limit int) (string, int, bool, string) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, false, "source file missing"
		}
		return "", 0, false, "source file stat failed"
	}
	if !info.Mode().IsRegular() {
		return "", 0, false, "source path is not a regular file"
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, false, "source file read failed"
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return "", 0, false, "source file read failed"
	}
	truncated := len(data) > limit
	if truncated {
		data = data[:limit]
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
	}
	return string(data), len(data), truncated, ""
}

func boundLargeGitFields(input *renderInput) {
	if len(input.git.Status) > gitFieldMaxBytes {
		old := len(input.git.Status)
		input.git.Status = truncateUTF8(input.git.Status, gitFieldMaxBytes)
		input.omissions = append(input.omissions, Omission{Kind: "Git status", Detail: fmt.Sprintf("truncated from %d to %d bytes", old, len(input.git.Status))})
	}
	if len(input.git.DiffStat) > gitFieldMaxBytes {
		old := len(input.git.DiffStat)
		input.git.DiffStat = truncateUTF8(input.git.DiffStat, gitFieldMaxBytes)
		input.omissions = append(input.omissions, Omission{Kind: "Git diff stat", Detail: fmt.Sprintf("truncated from %d to %d bytes", old, len(input.git.DiffStat))})
	}
}

func removeLastExcerpt(input *renderInput) bool {
	for i := len(input.plans) - 1; i >= 0; i-- {
		last := len(input.plans[i].excerpts) - 1
		if last < 0 {
			continue
		}
		path := input.plans[i].excerpts[last].path
		input.plans[i].excerpts = input.plans[i].excerpts[:last]
		input.omissions = append(input.omissions, Omission{Kind: "plan excerpt", Detail: "omitted to satisfy max_bytes: " + path})
		return true
	}
	return false
}

func renderPack(input renderInput, budgetDroppedMessages int) []byte {
	var out bytes.Buffer
	out.WriteString("# Agent Mux Handoff Pack\n\n")
	out.WriteString("## Provenance\n\n")
	writeField(&out, "Workspace ID", input.source.WorkspaceID)
	writeField(&out, "Workspace", input.source.WorkspaceName)
	writeField(&out, "Source harness", input.source.Harness)
	writeField(&out, "Source session", input.source.SessionID)
	if input.source.ConversationID != nil {
		writeField(&out, "Conversation row", strconv.FormatInt(*input.source.ConversationID, 10))
	}
	if !input.source.CapturedAt.IsZero() {
		writeField(&out, "Snapshot time", input.source.CapturedAt.UTC().Format(time.RFC3339Nano))
	}
	writeField(&out, "Working directory", absoluteClean(input.source.CWD))

	out.WriteString("\n## Original User Goal\n\n")
	writeFenced(&out, input.goal)

	out.WriteString("\n## Recent Conversation\n\n")
	if len(input.messages) == 0 {
		out.WriteString("No user/assistant messages were retained.\n")
	}
	for _, message := range input.messages {
		out.WriteString("### ")
		if message.role == "user" {
			out.WriteString("User")
		} else {
			out.WriteString("Assistant")
		}
		if !message.timestamp.IsZero() {
			out.WriteString(" — ")
			out.WriteString(message.timestamp.UTC().Format(time.RFC3339Nano))
		}
		out.WriteString("\n\n")
		writeFenced(&out, message.text)
		out.WriteByte('\n')
	}

	out.WriteString("## Linked Plans\n\n")
	if len(input.plans) == 0 {
		out.WriteString("No plans are explicitly linked.\n\n")
	}
	for _, plan := range input.plans {
		out.WriteString("### ")
		out.WriteString(singleLine(plan.name))
		out.WriteString("\n\n- Directory: `")
		out.WriteString(escapeBackticks(plan.directory))
		out.WriteString("`\n")
		for _, path := range plan.paths {
			out.WriteString("- Source file: `")
			out.WriteString(escapeBackticks(path))
			out.WriteString("`\n")
		}
		out.WriteByte('\n')
		for _, excerpt := range plan.excerpts {
			out.WriteString("#### Excerpt: `")
			out.WriteString(escapeBackticks(excerpt.path))
			out.WriteString("`\n\n")
			writeFenced(&out, excerpt.text)
			out.WriteByte('\n')
		}
	}

	out.WriteString("## Git Snapshot\n\n")
	if !input.git.Available {
		out.WriteString("- State: unavailable\n")
		writeField(&out, "Kind", input.git.Kind)
		writeField(&out, "Detail", input.git.Detail)
	} else {
		writeField(&out, "Branch", input.git.Branch)
		writeField(&out, "HEAD", input.git.Head)
		out.WriteString("- Status:\n\n")
		writeFenced(&out, emptyLabel(input.git.Status, "clean"))
		out.WriteString("\n- Diff stat:\n\n")
		writeFenced(&out, emptyLabel(input.git.DiffStat, "no changes"))
	}

	out.WriteString("\n## Full Source Paths\n\n")
	if len(input.paths) == 0 {
		out.WriteString("- (none)\n")
	}
	for _, path := range input.paths {
		out.WriteString("- `")
		out.WriteString(escapeBackticks(path))
		out.WriteString("`\n")
	}

	out.WriteString("\n## Omissions and Truncation\n\n")
	omissions := omissionsForRender(input.omissions, budgetDroppedMessages)
	if len(omissions) == 0 {
		out.WriteString("- None.\n")
	}
	for _, omission := range omissions {
		out.WriteString("- ")
		out.WriteString(singleLine(omission.Kind))
		out.WriteString(": ")
		out.WriteString(singleLine(omission.Detail))
		out.WriteByte('\n')
	}
	return out.Bytes()
}

func omissionsForRender(base []Omission, budgetDroppedMessages int) []Omission {
	out := append([]Omission(nil), base...)
	if budgetDroppedMessages > 0 {
		out = append(out, Omission{Kind: "transcript", Detail: fmt.Sprintf("%d oldest recent message(s) omitted to satisfy max_bytes", budgetDroppedMessages)})
	}
	return out
}

func writeField(out *bytes.Buffer, label, value string) {
	if value == "" {
		value = "(not supplied)"
	}
	out.WriteString("- ")
	out.WriteString(label)
	out.WriteString(": `")
	out.WriteString(escapeBackticks(singleLine(value)))
	out.WriteString("`\n")
}

func writeFenced(out *bytes.Buffer, text string) {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	out.WriteString(fence)
	out.WriteByte('\n')
	out.WriteString(text)
	if !strings.HasSuffix(text, "\n") {
		out.WriteByte('\n')
	}
	out.WriteString(fence)
	out.WriteByte('\n')
}

func planFilePath(directory, file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(directory, file)
}

func absoluteClean(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for value != "" && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func singleLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func escapeBackticks(value string) string { return strings.ReplaceAll(value, "`", "\\`") }

func emptyLabel(value, label string) string {
	if strings.TrimSpace(value) == "" {
		return "(" + label + ")"
	}
	return value
}
