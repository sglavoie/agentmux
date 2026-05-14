package kitty

// MockCall records a single invocation of the mock runner.
type MockCall struct {
	Name string
	Args []string
}

// MockRunner is a test double for CommandRunner.
// Responses maps a key (joined command string) to canned stdout bytes.
// If no match is found, it returns an empty response with no error.
type MockRunner struct {
	Responses map[string][]byte
	Calls     []MockCall
}

// NewMockRunner creates a MockRunner with pre-loaded response map.
func NewMockRunner(responses map[string][]byte) *MockRunner {
	return &MockRunner{Responses: responses}
}

func (m *MockRunner) Run(name string, args ...string) ([]byte, []byte, error) {
	m.Calls = append(m.Calls, MockCall{Name: name, Args: args})
	key := commandKey(name, args)
	if data, ok := m.Responses[key]; ok {
		return data, nil, nil
	}
	return []byte{}, nil, nil
}

func commandKey(name string, args []string) string {
	s := name
	for _, a := range args {
		s += " " + a
	}
	return s
}

// CallCount returns how many times a full command was called.
func (m *MockRunner) CallCount(name string, args ...string) int {
	key := commandKey(name, args)
	count := 0
	for _, c := range m.Calls {
		if commandKey(c.Name, c.Args) == key {
			count++
		}
	}
	return count
}
