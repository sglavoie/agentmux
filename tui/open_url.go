package tui

import (
	"errors"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

// openBrowserSupported reports whether this platform has a URL opener the
// dashboard is willing to use. Callers check it before offering an action so a
// deep link is disabled up front instead of failing after the link is minted.
func openBrowserSupported() bool {
	switch runtime.GOOS {
	case "darwin":
		return true
	case "linux", "freebsd", "netbsd", "openbsd":
		return true
	default:
		return false
	}
}

// openBrowserURL hands one http or https URL to the platform opener as a single
// argv element. No shell is involved, so a URL containing shell metacharacters
// is data rather than syntax. The error deliberately excludes the URL because
// callers pass tokenized links.
func openBrowserURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("refusing to open a non-http(s) URL")
	}
	var name string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "linux", "freebsd", "netbsd", "openbsd":
		name = "xdg-open"
	default:
		return errors.New("no supported URL opener on this platform")
	}
	binary, err := exec.LookPath(name)
	if err != nil {
		return errors.New("URL opener is not installed")
	}
	if err := exec.Command(binary, parsed.String()).Start(); err != nil {
		return errors.New("URL opener failed to start")
	}
	return nil
}
