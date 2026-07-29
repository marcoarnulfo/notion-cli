package cli

import (
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/tui"
)

func TestExecuteUnknownCommandReturnsUsageError(t *testing.T) {
	code := executeArgs([]string{"definitely-not-a-command"})
	if code != ExitUsage {
		t.Fatalf("got exit code %d, want %d", code, ExitUsage)
	}
}

func TestExecuteHelpSucceeds(t *testing.T) {
	if code := executeArgs([]string{"--help"}); code != ExitOK {
		t.Fatalf("got exit code %d, want %d", code, ExitOK)
	}
}

// withFakeBrowser swaps the bubbletea launch for a recorder, so the wiring
// around the browsing TUI can be tested without a terminal.
func withFakeBrowser(t *testing.T, err error) *int {
	t.Helper()
	var calls int
	old := runBrowser
	runBrowser = func(tui.BrowseModel) error {
		calls++
		return err
	}
	t.Cleanup(func() { runBrowser = old })
	return &calls
}

// Piped or redirected, a bare `notion-track` must still print help: a
// full-screen UI would be garbage on the other end of the pipe.
func TestBareCommandWithoutATerminalPrintsHelp(t *testing.T) {
	withInteractivePrompt(t, false, nil, nil)
	calls := withFakeBrowser(t, nil)

	out := captureStdout(t, func() {
		if code := executeArgs(nil); code != ExitOK {
			t.Errorf("exit code = %d", code)
		}
	})

	if *calls != 0 {
		t.Fatal("the browsing TUI opened without a terminal")
	}
	if !strings.Contains(out, "notion-track") || !strings.Contains(out, "Available Commands") {
		t.Errorf("no help on stdout:\n%s", out)
	}
}

func TestBareCommandAtATerminalOpensTheBrowser(t *testing.T) {
	cfg := withStubbedAPI(t, stubbedRow)
	withInteractivePrompt(t, true, nil, nil)
	calls := withFakeBrowser(t, nil)

	if code := executeArgs([]string{"--config", cfg}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if *calls != 1 {
		t.Fatalf("the browser ran %d times, want once", *calls)
	}
}

// An unknown command is still a usage error, terminal or not — it must never
// be mistaken for "no arguments, open the UI".
func TestAnUnknownCommandAtATerminalIsStillAUsageError(t *testing.T) {
	withInteractivePrompt(t, true, nil, nil)
	calls := withFakeBrowser(t, nil)

	if code := executeArgs([]string{"definitely-not-a-command"}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if *calls != 0 {
		t.Fatal("the browser opened for an unknown command")
	}
}

// The version is a scripting surface: a release workflow or an install script
// reads it to decide whether to upgrade, so it must be the bare string and
// nothing else — not cobra's default "notion-track version X" sentence.
func TestVersionFlagPrintsTheBareVersion(t *testing.T) {
	old := Version
	Version = "v9.9.9"
	t.Cleanup(func() { Version = old })

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"--version"}); code != ExitOK {
			t.Fatalf("exit code = %d, want %d", code, ExitOK)
		}
	})

	if out != "v9.9.9\n" {
		t.Errorf("output = %q, want just the version and a newline", out)
	}
}

// An unstamped build says "dev" rather than an empty string or a plausible
// number: whoever reads it must be able to tell a release from a `go install`.
func TestVersionDefaultsToDev(t *testing.T) {
	if Version != "dev" && Version != "v9.9.9" {
		t.Errorf("Version = %q, want the unstamped default to be %q", Version, "dev")
	}
}
