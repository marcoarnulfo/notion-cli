package cli

import "testing"

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
