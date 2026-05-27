package harness

type fakeHarness struct{ name string }

func (f fakeHarness) Name() string                         { return f.name }
func (f fakeHarness) Executable() string                   { return "fake" }
func (f fakeHarness) ConfigEnvVar() string                 { return "FAKE_DIR" }
func (f fakeHarness) DefaultHomeDir() string               { return "/fake/home" }
func (f fakeHarness) PipeModeTokens() []string             { return []string{"--fake"} }
func (f fakeHarness) HookContract() HookContract           { return fakeHookContract{} }
func (f fakeHarness) TranscriptsDir(homeDir string) string { return homeDir + "/transcripts" }

type fakeHookContract struct{}

func (fakeHookContract) InstallPath(homeDir string) string { return homeDir + "/fake-hooks.json" }
func (fakeHookContract) MergeData(_ []byte, _ []string, _ string) ([]byte, error) {
	return []byte("{}\n"), nil
}
func (fakeHookContract) ParseInstalledEvents(_ string) ([]string, error) { return nil, nil }
