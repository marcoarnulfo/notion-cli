package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/atotto/clipboard"
)

// copyToClipboard and openInBrowser are the two things the browser does that
// leave the terminal. Both are injected into the model rather than called
// directly, so a test asserts what would have happened and no test ever
// touches the real clipboard or spawns a browser.

func copyToClipboard(s string) error { return clipboard.WriteAll(s) }

// openInBrowser hands a URL to the platform's opener.
//
// The scheme is checked first, and the URL is passed as an argument rather
// than through a shell: `open` on macOS will happily launch a local file or an
// application, and the value arrives here from an API response, so "it is
// definitely an https URL" is worth verifying rather than assuming.
func openInBrowser(url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("refusing to open %q: not an http(s) URL", url)
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	// Start, not Run: xdg-open can block for as long as the browser it
	// launched lives, and the TUI must not freeze behind it.
	return cmd.Start()
}
