package tui

import "fmt"

type ConfigPickerPurpose int

const (
	ConfigPickerPurposeUnspecified ConfigPickerPurpose = iota
	ConfigPickerRestartProfile
	ConfigPickerLauncherOverride
	ConfigPickerDashboardLaunch
	ConfigPickerHandoff
)

func (m Model) activeConfigPickerPurpose() ConfigPickerPurpose {
	if m.configPickerPurpose != ConfigPickerPurposeUnspecified {
		return m.configPickerPurpose
	}
	if m.configPickerReturnMode == ModePicker {
		return ConfigPickerLauncherOverride
	}
	return ConfigPickerRestartProfile
}

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
	default:
		return "Choose profile"
	}
}

func (p ConfigPickerPurpose) harnessWarning(currentHarness, selectedHarness string) string {
	switch p {
	case ConfigPickerLauncherOverride:
		return fmt.Sprintf("Switching harness: %s → %s. Future launches will use this harness.", currentHarness, selectedHarness)
	case ConfigPickerDashboardLaunch, ConfigPickerHandoff:
		return fmt.Sprintf("Switching harness: %s → %s. New session will launch with this harness.", currentHarness, selectedHarness)
	default:
		return fmt.Sprintf("Switching harness: %s → %s. Session will restart.", currentHarness, selectedHarness)
	}
}
