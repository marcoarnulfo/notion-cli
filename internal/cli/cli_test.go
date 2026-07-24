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
