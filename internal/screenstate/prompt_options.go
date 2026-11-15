package screenstate

import (
	"regexp"
	"strings"
)

// PromptOption is one numbered choice of a blocking prompt.
type PromptOption struct {
	Key      string // the digit to press: "1".."9"
	Label    string // the option's text, without the number
	Selected bool   // the prompt's cursor is on this option
}

// Prompt is what a blocking prompt asks, as far as it can be read.
type Prompt struct {
	Question string
	Summary  string
	Options  []PromptOption
}

var promptOptionLine = regexp.MustCompile(`^(?:([❯›>])\s*)?([1-9])\.\s+(\S.*)$`)

// ParsePrompt reads the numbered options nearest the bottom of lines.
// It reports false unless it finds at least two options numbered
// 1, 2, 3… without a gap. Text below the last option is ignored.
func ParsePrompt(lines []string) (Prompt, bool) {
	cleaned := make([]string, len(lines))
	matches := make([][]string, len(lines))
	end := -1
	for i, line := range lines {
		cleaned[i] = cleanPromptLine(line)
		matches[i] = promptOptionLine.FindStringSubmatch(cleaned[i])
		if matches[i] != nil {
			end = i
		}
	}
	if end < 0 {
		return Prompt{}, false
	}
	start := end
	for ; start >= 0; start-- {
		if cleaned[start] == "" || promptSeparator(cleaned[start]) {
			return Prompt{}, false
		}
		if match := matches[start]; match != nil && match[2] == "1" {
			break
		}
	}
	if start < 0 {
		return Prompt{}, false
	}
	var prompt Prompt
	for i := start; i <= end; i++ {
		if match := matches[i]; match != nil {
			if int(match[2][0]-'0') != len(prompt.Options)+1 {
				return Prompt{}, false
			}
			prompt.Options = append(prompt.Options, PromptOption{
				Key: match[2], Label: match[3], Selected: match[1] != "",
			})
		} else {
			prompt.Options[len(prompt.Options)-1].Label += " " + cleaned[i]
		}
	}
	if len(prompt.Options) < 2 {
		return Prompt{}, false
	}
	var context []string
	for i := start - 1; i >= 0 && len(context) < 6; i-- {
		if cleaned[i] != "" && !promptSeparator(cleaned[i]) {
			context = append(context, cleaned[i])
		}
	}
	prompt.Question, prompt.Summary = promptContext(context)
	prompt.Question = clampPromptText(prompt.Question)
	prompt.Summary = clampPromptText(prompt.Summary)
	for i := range prompt.Options {
		prompt.Options[i].Label = clampPromptText(prompt.Options[i].Label)
	}
	return prompt, true
}

func cleanPromptLine(line string) string {
	for {
		cleaned := strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│┃"))
		if cleaned == line {
			return cleaned
		}
		line = cleaned
	}
}

func promptSeparator(line string) bool {
	return line != "" && strings.Trim(line, "─━═╌▀▄-╭╮╰╯┌┐└┘ ") == ""
}

// context is ordered nearest to the options first.
func promptContext(context []string) (question, summary string) {
	questionIndex := len(context)
	for i, line := range context {
		if strings.HasSuffix(line, "?") {
			question, questionIndex = line, i
			break
		}
	}
	for _, line := range context[:questionIndex] {
		if strings.HasPrefix(line, "$ ") {
			return question, strings.TrimPrefix(line, "$ ")
		}
	}
	lower := strings.ToLower(question)
	generic := strings.Contains(lower, "this command") || strings.Contains(lower, "following command") || lower == "do you want to proceed?"
	if generic && questionIndex+1 < len(context) {
		return question, context[questionIndex+1]
	}
	return question, question
}

func clampPromptText(text string) string {
	runes := []rune(text)
	return string(runes[:min(len(runes), 200)])
}
