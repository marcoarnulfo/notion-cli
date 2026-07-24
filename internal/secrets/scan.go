// Package secrets scans the files a git repository tracks for strings shaped
// like a Notion integration token.
//
// It exists as a safety net for the one mistake this tool makes easy: a token
// pasted into a config file, an .env, or a README snippet, and then committed.
// Nothing here ever returns or logs the matched text — a warning that quotes
// the secret it is warning about has leaked it a second time.
package secrets

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

var (
	// ErrNoRepository means the directory is not inside a git work tree, so
	// there is no set of tracked files to scan.
	ErrNoRepository = errors.New("not a git repository")
	// ErrGitUnavailable means git itself could not be run.
	ErrGitUnavailable = errors.New("git is not available")
)

// tokenPattern matches a Notion integration token: the ntn_ prefix followed by
// the long random tail.
//
// The 32-character floor is what keeps this quiet enough to be useful. Every
// stand-in that legitimately lives in a tracked file is something a human
// typed, and humans do not type 32 random characters: "ntn_test" and
// "ntn_from_file" in this repo's tests, "ntn_xxx" in a README,
// "ntn_supersecrettoken1234567890" in internal/config's leak test — all of
// them are well under the bar, while a real token's tail (around 46
// characters) clears it with room to spare.
//
// The trade is deliberate and worth stating: a genuine token shorter than 36
// characters in total would slip through. That is the safer way to be wrong.
// A check that cries wolf over every placeholder is one users learn to ignore,
// and an ignored check catches nothing at all.
var tokenPattern = regexp.MustCompile(`ntn_[A-Za-z0-9]{32,}`)

// maxFileSize caps what is read per file. A token lives in a config file, a
// script or a document, never in a 50 MB fixture, and reading every tracked
// blob in full would make doctor unusable in a repository with large assets.
const maxFileSize = 1 << 20

// binarySniffSize is how much of a file is examined for the NUL byte that
// marks it as binary — the same heuristic git itself uses.
const binarySniffSize = 8000

// Finding is one line that looks like it carries a token. It records where the
// match is and deliberately never what it was.
type Finding struct {
	Path string
	Line int
}

// Result is what a scan of a repository found, plus how much of it was read,
// so a caller can say "150 files scanned" rather than only "nothing found".
type Result struct {
	Findings []Finding
	Scanned  int
}

// ScanContent returns the 1-based numbers of the lines in b that contain a
// token-shaped string.
func ScanContent(b []byte) []int {
	var lines []int
	for i, line := range bytes.Split(b, []byte("\n")) {
		if tokenPattern.Match(line) {
			lines = append(lines, i+1)
		}
	}
	return lines
}

// ScanTracked scans every file git tracks in the repository containing dir.
//
// The whole repository, not just dir: a token committed two directories up is
// exactly as public as one committed here, and where the user happened to be
// standing when they ran doctor should not decide what gets checked.
//
// Tracked files only: an untracked .env holding a token is a file the user
// chose not to commit, and warning about it would train them to ignore this
// check. Paths are reported relative to the repository root, the way git
// prints them.
func ScanTracked(ctx context.Context, dir string) (Result, error) {
	root, err := repoRoot(ctx, dir)
	if err != nil {
		return Result{}, err
	}

	// Listed from the root, so the paths are relative to it and cover the
	// whole tree. -z, not newline separation: a path may legally contain a
	// newline, and git would otherwise quote and escape it into something that
	// no longer opens.
	out, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		return Result{}, err
	}

	var res Result
	for _, name := range bytes.Split(out, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		path := string(name)
		b, ok := readScannable(filepath.Join(root, path))
		if !ok {
			continue
		}
		res.Scanned++
		for _, line := range ScanContent(b) {
			res.Findings = append(res.Findings, Finding{Path: path, Line: line})
		}
	}
	return res, nil
}

// repoRoot returns where git considers the work tree containing dir to start.
// It is also where the two failures a caller has to tell apart surface: git
// missing from PATH, and dir not being in a repository at all.
func repoRoot(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return "", ErrGitUnavailable
		}
		// rev-parse exits non-zero outside a work tree; on an argument list
		// this simple there is nothing else for it to reject.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", ErrNoRepository
		}
		return "", err
	}
	return string(bytes.TrimSpace(out)), nil
}

// readScannable returns the file's contents, or ok=false when it is one this
// scan should pass over: too large, binary, or no longer on disk (git tracks
// a file until the deletion is committed, so an index entry does not promise
// an openable path).
func readScannable(path string) ([]byte, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileSize {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	head := b
	if len(head) > binarySniffSize {
		head = head[:binarySniffSize]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return nil, false
	}
	return b, true
}
