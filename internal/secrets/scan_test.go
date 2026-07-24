package secrets

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeToken is assembled at run time on purpose. Written out as one literal it
// would be a token-shaped string inside a tracked file, and this package's own
// test data would then trip the warning it exists to raise.
func fakeToken() string { return "ntn_" + strings.Repeat("a", 46) }

func TestScanContentFindsATokenAndReportsItsLine(t *testing.T) {
	content := "first\nsecond\ntoken = " + fakeToken() + "\nfourth\n"

	got := ScanContent([]byte(content))

	if len(got) != 1 || got[0] != 3 {
		t.Fatalf("lines = %v, want [3]", got)
	}
}

// The scan has to stay quiet on the placeholders that legitimately live in
// tracked files, or it becomes noise the user learns to skip past.
func TestScanContentIgnoresPlaceholders(t *testing.T) {
	content := strings.Join([]string{
		"ntn_test",
		"ntn_from_file",
		"ntn_xxx",
		"export NOTION_TOKEN=ntn_<paste your token>",
		"the token starts with ntn_",
		// Straight out of internal/config's own leak test, and the case that
		// set the floor: long enough to look the part, short enough that only
		// a human could have typed it.
		"ntn_supersecrettoken1234567890",
	}, "\n")

	if got := ScanContent([]byte(content)); len(got) != 0 {
		t.Fatalf("lines = %v, want none", got)
	}
}

func TestScanContentReportsEveryMatchingLine(t *testing.T) {
	content := "a = " + fakeToken() + "\nplain\nb = " + fakeToken() + "\n"

	got := ScanContent([]byte(content))

	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("lines = %v, want [1 3]", got)
	}
}

// gitRepo makes a repository with the given files staged, and skips the test
// when git is not installed rather than failing on an environment problem.
func gitRepo(t *testing.T, files map[string]string, track ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Staging is enough: ls-files reads the index, so nothing has to be
	// committed, which also keeps the test independent of a git identity being
	// configured on the machine running it.
	for _, name := range track {
		run("add", "--", name)
	}
	return dir
}

func TestScanTrackedFindsATokenInATrackedFile(t *testing.T) {
	dir := gitRepo(t, map[string]string{
		"config/app.yml": "token: " + fakeToken() + "\n",
		"README.md":      "nothing to see\n",
	}, "config/app.yml", "README.md")

	res, err := ScanTracked(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	if res.Scanned != 2 {
		t.Errorf("scanned = %d, want 2", res.Scanned)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %v, want one", res.Findings)
	}
	if res.Findings[0] != (Finding{Path: "config/app.yml", Line: 1}) {
		t.Errorf("finding = %+v", res.Findings[0])
	}
}

// A token in a file the user chose not to commit is not the mistake this check
// is for, and warning about it would train them to ignore the warning.
func TestScanTrackedIgnoresUntrackedFiles(t *testing.T) {
	dir := gitRepo(t, map[string]string{
		"tracked.txt": "clean\n",
		".env":        "NOTION_TOKEN=" + fakeToken() + "\n",
	}, "tracked.txt")

	res, err := ScanTracked(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Findings) != 0 {
		t.Fatalf("findings = %v, want none", res.Findings)
	}
}

func TestScanTrackedSkipsBinaryFiles(t *testing.T) {
	dir := gitRepo(t, map[string]string{
		"blob.bin": "\x00\x00" + fakeToken(),
	}, "blob.bin")

	res, err := ScanTracked(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Findings) != 0 {
		t.Fatalf("findings = %v, want none", res.Findings)
	}
	if res.Scanned != 0 {
		t.Errorf("scanned = %d, want 0: a binary file should not count as read", res.Scanned)
	}
}

func TestScanTrackedOutsideARepositoryIsRecognisable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// A temp dir is not a work tree, unless the machine's temp path happens to
	// sit inside one — which would make this assertion meaningless, so check.
	dir := t.TempDir()
	if err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Run(); err == nil {
		t.Skip("temp dir is inside a git work tree")
	}

	if _, err := ScanTracked(context.Background(), dir); !errors.Is(err, ErrNoRepository) {
		t.Fatalf("err = %v, want ErrNoRepository", err)
	}
}

// This repository is the scan's most convenient real-world subject, and one it
// has an obvious interest in keeping clean: a token committed here would be a
// public leak. It also proves the placeholder floor holds against a real tree
// — this one tracks "ntn_test" and "ntn_from_file" in its own test files.
func TestThisRepositoryCarriesNoToken(t *testing.T) {
	res, err := ScanTracked(context.Background(), ".")
	if errors.Is(err, ErrNoRepository) || errors.Is(err, ErrGitUnavailable) {
		t.Skipf("cannot scan: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Findings) != 0 {
		t.Fatalf("token-looking strings in tracked files: %v", res.Findings)
	}
	if res.Scanned == 0 {
		t.Fatal("scanned no files at all, so this assertion proves nothing")
	}
	t.Logf("%d tracked files scanned", res.Scanned)
}
