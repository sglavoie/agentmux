package launch

import "fmt"

// ConfigPickerPurpose is why the profile-override overlay is open. Every
// purpose ends the same way — a harness process starts under the chosen
// profile — but the wording of the overlay header and of the harness-switch
// warning differs by how that start is reached.
type ConfigPickerPurpose int

const (
	ConfigPickerPurposeUnspecified ConfigPickerPurpose = iota
	ConfigPickerRestartProfile
	ConfigPickerLauncherOverride
	ConfigPickerDashboardLaunch
	ConfigPickerHandoff
	ConfigPickerWorktreeReopen
)

func (p ConfigPickerPurpose) header() string {
	switch p {
	case ConfigPickerRestartProfile:
		return "Restart with profile"
	case ConfigPickerLauncherOverride:
		return "Launch profile"
	case ConfigPickerDashboardLaunch:
		return "Launch new session with profile"
	case ConfigPickerHandoff:
		return "Handoff to profile"
	case ConfigPickerWorktreeReopen:
		return "Reopen worktree with profile"
	default:
		return "Choose profile"
	}
}

func (p ConfigPickerPurpose) harnessWarning(currentHarness, selectedHarness string) string {
	switch p {
	case ConfigPickerLauncherOverride:
		return fmt.Sprintf("Switching harness: %s → %s. Future launches will use this harness.", currentHarness, selectedHarness)
	case ConfigPickerDashboardLaunch, ConfigPickerHandoff, ConfigPickerWorktreeReopen:
		return fmt.Sprintf("Switching harness: %s → %s. New session will launch with this harness.", currentHarness, selectedHarness)
	default:
		return fmt.Sprintf("Switching harness: %s → %s. Session will restart.", currentHarness, selectedHarness)
	}
}
