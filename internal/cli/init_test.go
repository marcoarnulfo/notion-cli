package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/config"
)

// withInteractivePrompt swaps the three prompt seams and restores them on
// cleanup, so each test only states what the fake terminal returns.
func withInteractivePrompt(t *testing.T, interactive bool, token func() (string, error), line func() (string, error)) {
	t.Helper()
	oldInteractive, oldReadToken, oldReadLine := isInteractive, readToken, readLine
	isInteractive = func() bool { return interactive }
	if token != nil {
		readToken = token
	}
	if line != nil {
		readLine = line
	}
	t.Cleanup(func() {
		isInteractive, readToken, readLine = oldInteractive, oldReadToken, oldReadLine
	})
}

// The worst regression this feature could introduce: a CI job blocking
// forever on a prompt nobody can answer. isInteractive is forced false and
// both read seams panic the test if ever called, so any accidental prompt
// fails loudly instead of hanging.
func TestInitNonInteractiveNeverPrompts(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	t.Setenv(config.TokenEnv, "")
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false,
		func() (string, error) { t.Fatal("readToken called in a non-interactive run"); return "", nil },
		func() (string, error) { t.Fatal("readLine called in a non-interactive run"); return "", nil },
	)

	code := executeArgs([]string{
		"init", "--data-source-id", "ds1",
		"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
		"--config", cfg,
	})
	if code != ExitAuth {
		t.Fatalf("exit code = %d, want %d (ExitAuth)", code, ExitAuth)
	}
}

// An already-available token (env or file) must skip the prompt entirely,
// even at an interactive terminal: asking again would be pure friction.
func TestInitSkipsPromptWhenATokenIsAlreadyAvailable(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	// withStubbedAPI already exported NOTION_TOKEN; leave it set.
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, true,
		func() (string, error) {
			t.Fatal("readToken called though a token was already available")
			return "", nil
		},
		func() (string, error) {
			t.Fatal("readLine called though a token was already available")
			return "", nil
		},
	)

	code := executeArgs([]string{
		"init", "--data-source-id", "ds1",
		"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
		"--config", cfg,
	})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d (ExitOK)", code, ExitOK)
	}
}

// The default answer ("just press Enter") must save the token, and the
// token itself must never show up in anything printed along the way.
func TestInitInteractivePromptsAndSavesTokenByDefault(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	t.Setenv(config.TokenEnv, "")
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, true,
		func() (string, error) { return "ntn_typed", nil },
		func() (string, error) { return "", nil }, // bare Enter accepts the recommended default
	)

	var code int
	out := captureStdout(t, func() {
		code = executeArgs([]string{
			"init", "--data-source-id", "ds1",
			"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
			"--config", cfg,
		})
	})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d: %s", code, ExitOK, out)
	}
	if strings.Contains(out, "ntn_typed") {
		t.Fatalf("the token leaked into output: %q", out)
	}

	tok, source, err := config.LoadToken()
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if tok != "ntn_typed" || source != "file" {
		t.Fatalf("LoadToken() = %q, %q, want ntn_typed, file", tok, source)
	}

	// The printed permission claim must describe the file that actually
	// landed on disk, not a hardcoded string that could go stale.
	credPath, err := config.CredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(credPath)
	if err != nil {
		t.Fatal(err)
	}
	wantPerm := fmt.Sprintf("%04o", info.Mode().Perm())
	if !strings.Contains(out, "permissions "+wantPerm) {
		t.Fatalf("output %q does not report the actual on-disk permissions (%s)", out, wantPerm)
	}
}

// Declining must not write anything, and the fallback command it prints
// must not itself leak the secret it's trying to keep off disk.
func TestInitInteractiveDeclinesSave(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	t.Setenv(config.TokenEnv, "")
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, true,
		func() (string, error) { return "ntn_typed", nil },
		func() (string, error) { return "n", nil },
	)

	var code int
	out := captureStdout(t, func() {
		code = executeArgs([]string{
			"init", "--data-source-id", "ds1",
			"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
			"--config", cfg,
		})
	})
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d: %s", code, ExitOK, out)
	}
	if strings.Contains(out, "ntn_typed") {
		t.Fatalf("the token leaked into output: %q", out)
	}
	if !strings.Contains(out, "export "+config.TokenEnv+"=") {
		t.Fatalf("output does not show the export line to run for this session: %q", out)
	}

	credPath, err := config.CredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(credPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credentials file was written despite declining to save (stat err = %v)", err)
	}
}

// A prompt that persists a secret must be permissive about what counts as
// "no": only "n" and "no" used to decline, so a typo or a slightly longer
// answer like "nope" fell through to the save branch and silently wrote the
// token to disk — the opposite of what the user typed to avoid.
func TestInitInteractiveDeclineAcceptsAnyAnswerStartingWithN(t *testing.T) {
	for _, answer := range []string{"nope", "N", "N.", "No thanks", "never"} {
		t.Run(answer, func(t *testing.T) {
			cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(cliSchemaJSON))
			})
			t.Setenv(config.TokenEnv, "")
			withIsolatedUserConfigDir(t)
			withInteractivePrompt(t, true,
				func() (string, error) { return "ntn_typed", nil },
				func() (string, error) { return answer, nil },
			)

			code := executeArgs([]string{
				"init", "--data-source-id", "ds1",
				"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
				"--config", cfg,
			})
			if code != ExitOK {
				t.Fatalf("exit code = %d, want %d", code, ExitOK)
			}

			credPath, err := config.CredentialsPath()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(credPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("answer %q was not treated as a decline: credentials file was written (stat err = %v)", answer, err)
			}
		})
	}
}

// An empty answer to the token prompt means the user still has no usable
// token: this must fail exactly like every other "no token" path.
func TestInitInteractiveEmptyTokenExitsAuth(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	t.Setenv(config.TokenEnv, "")
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, true,
		func() (string, error) { return "", nil },
		nil,
	)

	code := executeArgs([]string{
		"init", "--data-source-id", "ds1",
		"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
		"--config", cfg,
	})
	if code != ExitAuth {
		t.Fatalf("exit code = %d, want %d (ExitAuth)", code, ExitAuth)
	}
}
