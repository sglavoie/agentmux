package debug

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The debug leaves inherit their record selectors from `amx debug`, and cobra
// prints every inherited flag under "Global Flags" beside --debug, as though
// --stage or --trace applied to every amx command. The leaves' usage instead
// prints the selectors as their own "Selectors" section and keeps "Global
// Flags" for the flags the root really does give every command.
const (
	debugGlobalFlagsSection = `{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}`
	debugSelectorFlagsSection = `{{with debugSelectorFlagUsages .}}

Selectors:
{{. | trimTrailingWhitespaces}}{{end}}{{with debugGlobalFlagUsages .}}

Global Flags:
{{. | trimTrailingWhitespaces}}{{end}}`
)

func init() {
	cobra.AddTemplateFunc("debugSelectorFlagUsages", func(cmd *cobra.Command) string {
		return inheritedFlagUsages(cmd, true)
	})
	cobra.AddTemplateFunc("debugGlobalFlagUsages", func(cmd *cobra.Command) string {
		return inheritedFlagUsages(cmd, false)
	})
	// The replacement depends on cobra's default template; if a cobra upgrade
	// changes that section the default usage stays in force rather than a
	// half-edited one, and TestDebugLeafUsageNamesSelectors reports it.
	if template := debugCmd.UsageTemplate(); strings.Contains(template, debugGlobalFlagsSection) {
		debugCmd.SetUsageTemplate(strings.Replace(template, debugGlobalFlagsSection, debugSelectorFlagsSection, 1))
	}
}

// inheritedFlagUsages renders the visible flags cmd inherits, keeping either
// the selectors `amx debug` declares or everything else.
func inheritedFlagUsages(cmd *cobra.Command, selectors bool) string {
	kept := pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
	cmd.InheritedFlags().VisitAll(func(flag *pflag.Flag) {
		if (debugCmd.PersistentFlags().Lookup(flag.Name) != nil) == selectors {
			kept.AddFlag(flag)
		}
	})
	return kept.FlagUsages()
}
