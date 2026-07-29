package cli

// Version is the build's version string, stamped at link time by the release
// pipeline with `-ldflags "-X github.com/marcoarnulfo/notion-cli/internal/cli.Version=v1.2.3"`.
//
// It lives here, in the package that already needs it twice, rather than in
// main: cobra reads it to answer `--version`, and the MCP handshake reports it
// so an agent can tell which build it is talking to. Putting it in main would
// mean threading it through Execute's signature to reach both.
//
// "dev" is what an unstamped build says — `go install`, `go run`, a local
// `go build`. That is the honest answer for a binary nobody released, and it
// is why the default is not an empty string or a plausible-looking number.
var Version = "dev"
