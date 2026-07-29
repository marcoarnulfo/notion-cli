package cli

import "runtime/debug"

// Version is the build's version string, stamped at link time by the release
// pipeline with `-ldflags "-X github.com/marcoarnulfo/notion-cli/internal/cli.Version=v1.2.3"`.
//
// It lives here, in the package that already needs it twice, rather than in
// main: cobra reads it to answer `--version`, and the MCP handshake reports it
// so an agent can tell which build it is talking to. Putting it in main would
// mean threading it through Execute's signature to reach both.
//
// "dev" is this variable's own default, and it stays the honest answer for a
// local `go build` or `go run` nobody released. It is no longer the whole
// story, though: resolveVersion, not this variable directly, is what
// --version and the MCP handshake actually call, because a plain `go install`
// never runs the ldflags above yet still deserves a real answer — see
// resolveVersion.
var Version = "dev"

// readBuildInfo is a seam over debug.ReadBuildInfo, swapped out in tests: a
// test binary's own build info never carries a module version worth
// asserting on, so there is no way to exercise resolveVersion's fallback
// branches against the real function.
var readBuildInfo = debug.ReadBuildInfo

// resolveVersion is what --version and the MCP handshake report. It resolves
// in this order:
//
//  1. Version, if the release pipeline's -ldflags stamped it — that is an
//     explicit, trusted answer and nothing should second-guess it.
//  2. Otherwise, the main module's version from the running binary's own
//     build info. This is the fix `go install` needed: installing
//     `.../notion-track@v0.6.0` never runs the release ldflags, but Go still
//     records "v0.6.0" as the resolved module version inside the binary
//     itself, and runtime/debug.ReadBuildInfo can read it back.
//  3. Otherwise, "dev".
//
// Step 2 has two dead ends that must not be shown verbatim: "(devel)", which
// is what ReadBuildInfo reports for the main module in a local `go build` or
// `go run` (nobody released that), and "", which is what it reports inside a
// test binary. Both fall back to "dev" like a build with no version
// information at all.
func resolveVersion() string {
	if Version != "dev" {
		return Version
	}
	info, ok := readBuildInfo()
	if !ok {
		return "dev"
	}
	switch v := info.Main.Version; v {
	case "", "(devel)":
		return "dev"
	default:
		return v
	}
}
