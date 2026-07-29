package cli

import (
	"runtime/debug"
	"testing"
)

// withBuildInfo swaps the readBuildInfo seam so resolveVersion's fallback
// path can be driven without depending on how *this* test binary happens to
// have been built.
func withBuildInfo(t *testing.T, info *debug.BuildInfo, ok bool) {
	t.Helper()
	old := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) { return info, ok }
	t.Cleanup(func() { readBuildInfo = old })
}

// A linker-stamped Version always wins, regardless of what build info would
// otherwise say — a release binary must never be second-guessed by its own
// module version.
func TestResolveVersionPrefersTheLinkerStamp(t *testing.T) {
	old := Version
	Version = "v1.2.3"
	t.Cleanup(func() { Version = old })
	withBuildInfo(t, &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}}, true)

	if got := resolveVersion(); got != "v1.2.3" {
		t.Errorf("resolveVersion() = %q, want the stamped %q", got, "v1.2.3")
	}
}

// This is the case the fix exists for: `go install .../notion-track@v0.6.0`
// never runs the release ldflags, but Go still records the module version it
// resolved in the binary's own build info, and that is what an unstamped
// binary should report instead of a flat "dev".
func TestResolveVersionFallsBackToTheModuleVersionFromBuildInfo(t *testing.T) {
	old := Version
	Version = "dev"
	t.Cleanup(func() { Version = old })
	withBuildInfo(t, &debug.BuildInfo{Main: debug.Module{Version: "v0.6.0"}}, true)

	if got := resolveVersion(); got != "v0.6.0" {
		t.Errorf("resolveVersion() = %q, want the module version %q", got, "v0.6.0")
	}
}

// "(devel)" is what ReadBuildInfo reports for a local `go build` or `go run`
// of the main module — not a version anyone released, so it must fall back
// to "dev" rather than being shown verbatim.
func TestResolveVersionTreatsDevelAsUnstamped(t *testing.T) {
	old := Version
	Version = "dev"
	t.Cleanup(func() { Version = old })
	withBuildInfo(t, &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true)

	if got := resolveVersion(); got != "dev" {
		t.Errorf("resolveVersion() = %q, want %q for an unreleased build", got, "dev")
	}
}

// An empty module version — what a test binary's own build info reports —
// is just as unusable as "(devel)" and must fall back the same way.
func TestResolveVersionTreatsAnEmptyModuleVersionAsUnstamped(t *testing.T) {
	old := Version
	Version = "dev"
	t.Cleanup(func() { Version = old })
	withBuildInfo(t, &debug.BuildInfo{Main: debug.Module{Version: ""}}, true)

	if got := resolveVersion(); got != "dev" {
		t.Errorf("resolveVersion() = %q, want %q when build info has no module version", got, "dev")
	}
}

// ReadBuildInfo itself can report ok=false (binary not built with module
// support). That is just another shade of "no information available".
func TestResolveVersionFallsBackToDevWhenBuildInfoIsUnavailable(t *testing.T) {
	old := Version
	Version = "dev"
	t.Cleanup(func() { Version = old })
	withBuildInfo(t, nil, false)

	if got := resolveVersion(); got != "dev" {
		t.Errorf("resolveVersion() = %q, want %q when build info is unavailable", got, "dev")
	}
}
