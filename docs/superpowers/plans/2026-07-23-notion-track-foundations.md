# notion-track — Piano 1: fondamenta e CLI headless

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Costruire il repo, il client HTTP Notion, il config a profili, il dominio puro e i comandi headless `init`/`upsert`/`set`/`get`/`list`/`doctor`, fino a un binario funzionante contro Notion reale.

**Architecture:** Livelli con dipendenze a senso unico. `internal/notion` fa solo HTTP; `internal/tracker` è dominio puro senza I/O; `internal/config` gestisce YAML a profili; `internal/service` orchestra; `internal/cli` è un guscio sottile su cobra. Nessun SDK di terze parti per l'API.

**Tech Stack:** Go 1.26.x, `spf13/cobra`, `gopkg.in/yaml.v3`, stdlib `net/http` e `net/http/httptest` per i test.

`golang.org/x/time` **non** entra in questo piano: il Task 4 implementa il retry reattivo sui 429, che è ciò che serve alla correttezza. Un rate limiter proattivo va valutato solo se la telemetria d'uso mostra che serve, e a quel punto è un'aggiunta contenuta al client.

**Spec di riferimento:** `docs/superpowers/specs/2026-07-23-notion-track-design.md`

**Piani successivi:** piano 2 `internal/markdown` (body Markdown → blocchi), piano 3 TUI bubbletea. Questo piano non li tocca.

## Global Constraints

- Modulo: `github.com/marcoarnulfo/notion-cli`. Binario: `notion-track`, entry point `cmd/notion-track/main.go`.
- Go `1.26.x`. Niente `pkg/`: tutto il codice sta in `internal/`.
- **Tutto ciò che vive nel repo è in inglese**: codice, identificatori, commenti, messaggi d'errore, stringhe utente, commit. Eccezioni: `README.it.md`, `CONTRIBUTING.it.md`, i documenti in `docs/superpowers/`.
- Test: **solo stdlib** (`testing`, `net/http/httptest`). Vietati testify, gomock e qualsiasi framework di asserzione.
- TDD obbligatorio: test che fallisce → implementazione minima → test che passa → commit.
- `go test ./... -race` deve passare a ogni commit.
- Conventional Commits con scope (`feat(notion):`, `fix(config):`). **MAI** `Co-Authored-By` nei messaggi di commit.
- `Notion-Version` minima `2025-09-03`, definita in **una sola costante** in `internal/notion`. Valore esatto fissato dal Task 1.
- Il token non deve **mai** comparire in output, log, errori o dump di debug.
- `internal/tracker` non importa `internal/notion` né `internal/config`. Se un test di `tracker` ha bisogno di un server HTTP, il design è sbagliato.
- Dati su stdout, errori e warning su stderr. `cli.Execute()` ritorna `int` e non chiama mai `os.Exit`.
- Exit code: 0 successo · 1 generico · 2 uso errato · 3 non trovato · 4 duplicati · 5 auth.
- Chiavi JSON in `snake_case`, timestamp RFC3339.

---

## Mappa dei file

| File | Responsabilità |
|---|---|
| `cmd/notion-track/main.go` | Solo `os.Exit(cli.Execute())` |
| `internal/notion/client.go` | Costruzione client, auth, header, `do()` |
| `internal/notion/errors.go` | Errori tipizzati (`ErrUnauthorized`, `ErrNotFound`, `APIError`) |
| `internal/notion/retry.go` | Backoff su 429/5xx, `Retry-After` |
| `internal/notion/search.go` | `POST /v1/search` — elenco data source |
| `internal/notion/datasource.go` | `GET /v1/data_sources/{id}` — schema |
| `internal/notion/query.go` | Query righe con paginazione |
| `internal/notion/page.go` | Create e update pagina |
| `internal/notion/types.go` | Tipi API: `DataSource`, `Property`, `Page`, `PropertyValue` |
| `internal/config/config.go` | Struct, load/save, precedenza, token provenance |
| `internal/config/migrate.go` | `schema_version` e migrazioni |
| `internal/tracker/decide.go` | 0/1/N righe → create/update/errore |
| `internal/tracker/payload.go` | Mapping + costruzione property payload |
| `internal/tracker/validate.go` | Validazione stato contro opzioni del server |
| `internal/tracker/mapping.go` | Euristica di mapping proprietà per `init` |
| `internal/service/service.go` | Orchestrazione: upsert, set, get, list |
| `internal/service/doctor.go` | Check di `doctor` |
| `internal/cli/cli.go` | Root cobra, `Execute() int`, exit code |
| `internal/cli/{init,upsert,set,get,list,doctor}.go` | Un comando per file |
| `internal/cli/output.go` | Formattazione human e `--json` |

---

## Task 1: Bootstrap del repo

**Files:**
- Create: `go.mod`, `.gitignore`, `LICENSE`, `.github/workflows/ci.yml`, `cmd/notion-track/main.go`, `internal/cli/cli.go`, `internal/cli/cli_test.go`

**Interfaces:**
- Consumes: niente
- Produces: `cli.Execute() int`

- [ ] **Step 1: Inizializza il repo**

```bash
cd /Users/marcoarnulfo/ProgettiPersonali/notion-cli
git init
go mod init github.com/marcoarnulfo/notion-cli
```

- [ ] **Step 2: Crea `.gitignore`**

```
# The leading slash matters: an unanchored "notion-track" would also match the
# cmd/notion-track/ source directory and silently exclude the entry point.
/notion-track
/dist/
*.test
*.out
.superpowers/
CLAUDE.md
```

- [ ] **Step 3: Crea `LICENSE`**

Licenza MIT standard, `Copyright (c) 2026 marcoarnulfo`.

- [ ] **Step 4: Scrivi il test di `Execute`**

`internal/cli/cli_test.go`:

```go
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
```

- [ ] **Step 5: Verifica che fallisca**

Run: `go test ./internal/cli/ -run TestExecute -v`
Expected: FAIL, `undefined: executeArgs`

- [ ] **Step 6: Implementa `internal/cli/cli.go`**

```go
// Package cli wires the cobra command tree.
//
// Execute never calls os.Exit: it returns an exit code so that tests can
// exercise the whole command tree in-process. main is the only place allowed
// to terminate the program.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Exit codes. Pipelines rely on these to tell failure modes apart without
// parsing error strings.
const (
	ExitOK        = 0
	ExitError     = 1
	ExitUsage     = 2
	ExitNotFound  = 3
	ExitDuplicate = 4
	ExitAuth      = 5
)

// codedError carries an exit code alongside the message.
type codedError struct {
	code int
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

// Errorf builds an error that resolves to a specific exit code.
func Errorf(code int, format string, args ...any) error {
	return &codedError{code: code, err: fmt.Errorf(format, args...)}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "notion-track",
		Short:         "Keep a Notion task database in sync from your terminal and CI",
		SilenceUsage:  true,
		SilenceErrors: true,
		// ArbitraryArgs matters: with cobra's default arg validation an unknown
		// command is rejected inside Find() with a plain error, before RunE ever
		// runs, and we lose the ability to give it exit code 2. Taking the args
		// ourselves keeps that decision here.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return Errorf(ExitUsage, "unknown command %q", args[0])
			}
			// With no arguments the TUI takes over; until it lands, show help.
			return cmd.Help()
		},
	}
	// Without this, cobra's Print* helpers write to stderr, which would put
	// human-readable results on the wrong stream.
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)

	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return Errorf(ExitUsage, "%v", err)
	})

	root.PersistentFlags().String("profile", "", "config profile to use")
	root.PersistentFlags().String("config", "", "path to config file")
	return root
}

// Execute runs the CLI with os.Args and returns the process exit code.
func Execute() int { return executeArgs(os.Args[1:]) }

func executeArgs(args []string) int {
	root := newRootCmd()
	root.SetArgs(args)

	err := root.Execute()
	if err == nil {
		return ExitOK
	}

	fmt.Fprintf(os.Stderr, "error: %v\n", err)

	var coded *codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ExitUsage
}
```

Due dettagli non ovvi, entrambi verificati contro cobra 1.10.2 e non deducibili dalla documentazione:

1. **`Args: cobra.ArbitraryArgs` è obbligatorio.** Con la validazione di default, un comando sconosciuto viene respinto dentro `Find()` con un errore piatto, prima che `RunE` venga eseguito: l'errore non è un `codedError` e finirebbe con exit code 1 invece di 2. Prendendo noi gli argomenti, la decisione resta in `RunE`.
2. **`SetOut(os.Stdout)` è obbligatorio.** I metodi `Print*` di cobra scrivono di default su **stderr** (`OutOrStderr()`). Senza questa riga tutto l'output human dei comandi finirebbe sul canale degli errori.

- [ ] **Step 7: Implementa `cmd/notion-track/main.go`**

```go
// Command notion-track keeps a Notion task database in sync.
package main

import (
	"os"

	"github.com/marcoarnulfo/notion-cli/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
```

- [ ] **Step 8: Verifica che i test passino**

Run: `go test ./... -race`
Expected: PASS

- [ ] **Step 9: Crea `.github/workflows/ci.yml`**

```yaml
name: CI
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26'
      - run: go vet ./...
      - run: go run honnef.co/go/tools/cmd/staticcheck@latest ./...
      - run: go test ./... -race
      - run: go build ./...
```

- [ ] **Step 10: Verifica la toolchain in locale**

Run: `gofmt -l . && go vet ./... && go test ./... -race && go build ./...`
Expected: nessun output da `gofmt`, tutto verde

- [ ] **Step 11: Commit**

```bash
git add .
git commit -m "chore: bootstrap repo, cobra root command and CI"
```

---

## Task 2: Fissare la versione API (spike verificato)

Questo task non produce codice applicativo ma **rimuove l'unica incognita** che può invalidare il client. Va eseguito per primo e il suo esito è vincolante per tutti i task successivi.

**Files:**
- Create: `internal/notion/version.go`, `docs/superpowers/notes/2026-07-23-notion-api-version.md`

**Interfaces:**
- Produces: `notion.APIVersion` (costante stringa)

- [ ] **Step 1: Verifica sui doc ufficiali**

Consulta e annota, con URL e data di consultazione:

1. <https://developers.notion.com/page/changelog> — qual è la versione stabile più recente
2. <https://developers.notion.com/reference/post-search> — valori validi di `filter.value`
3. <https://developers.notion.com/reference/retrieve-a-data-source> — forma della risposta di `GET /v1/data_sources/{id}`
4. Reference dell'endpoint di query di un data source — **metodo HTTP esatto** (`POST` o `PATCH`), che lo spec segnala come non confermato
5. <https://developers.notion.com/reference/request-limits> — limiti di richieste, blocchi e caratteri

- [ ] **Step 2: Scrivi le note**

In `docs/superpowers/notes/2026-07-23-notion-api-version.md` registra: versione scelta, metodo e path esatti dei cinque endpoint usati, forma dei payload di richiesta e risposta, limiti numerici. Questo file è la fonte di verità per i task 3-8: chi li implementa non deve tornare sul web.

- [ ] **Step 3: Implementa `internal/notion/version.go`**

```go
package notion

// APIVersion is the Notion-Version header sent on every request.
//
// Notion 2025-09-03 introduced data sources as a breaking change: databases
// can hold several of them, and schema, query and page-parent all moved to
// data-source endpoints. Staying on an older version breaks reads, writes and
// queries as soon as anyone adds a second data source from the UI, so this
// floor is not negotiable.
//
// Value verified against the official changelog; see
// docs/superpowers/notes/2026-07-23-notion-api-version.md
const APIVersion = "REPLACE_WITH_VERIFIED_VALUE"

// BaseURL is the Notion API root. Tests override it with an httptest server.
const BaseURL = "https://api.notion.com"
```

Sostituisci `REPLACE_WITH_VERIFIED_VALUE` con la versione verificata allo Step 1. Il piano non la fissa deliberatamente: va letta, non ricordata.

- [ ] **Step 4: Commit**

```bash
git add internal/notion/version.go docs/superpowers/notes/
git commit -m "docs(notion): pin verified API version and record endpoint shapes"
```

---

## Task 3: Client HTTP ed errori tipizzati

**Files:**
- Create: `internal/notion/client.go`, `internal/notion/errors.go`, `internal/notion/client_test.go`

**Interfaces:**
- Consumes: `notion.APIVersion`, `notion.BaseURL`
- Produces:
  - `func New(token string, opts ...Option) *Client`
  - `func WithBaseURL(string) Option`, `func WithHTTPClient(*http.Client) Option`
  - `func (c *Client) do(ctx context.Context, method, path string, body any, out any) error`
  - `type APIError struct { Status int; Code, Message string }`
  - `var ErrUnauthorized, ErrNotFound error`

- [ ] **Step 1: Scrivi i test**

`internal/notion/client_test.go`:

```go
package notion

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoSendsAuthAndVersionHeaders(t *testing.T) {
	var gotAuth, gotVersion, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("Notion-Version")
		gotContentType = r.Header.Get("Content-Type")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New("ntn_secret", WithBaseURL(srv.URL))
	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.do(context.Background(), http.MethodPost, "/v1/things", map[string]string{"a": "b"}, &out); err != nil {
		t.Fatalf("do: %v", err)
	}

	if gotAuth != "Bearer ntn_secret" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotVersion != APIVersion {
		t.Errorf("Notion-Version = %q, want %q", gotVersion, APIVersion)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q", gotContentType)
	}
	if !out.OK {
		t.Error("response was not decoded")
	}
}

func TestDoMapsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":"unauthorized","message":"API token is invalid."}`))
	}))
	defer srv.Close()

	err := New("bad", WithBaseURL(srv.URL)).do(context.Background(), http.MethodGet, "/v1/users/me", nil, nil)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("got %v, want ErrUnauthorized", err)
	}
}

func TestDoMapsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":"object_not_found","message":"Could not find data source."}`))
	}))
	defer srv.Close()

	err := New("t", WithBaseURL(srv.URL)).do(context.Background(), http.MethodGet, "/v1/data_sources/x", nil, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// The token is a shared secret: it must never surface in an error string.
func TestErrorsNeverLeakTheToken(t *testing.T) {
	const token = "ntn_supersecret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"code":"validation_error","message":"bad"}`))
	}))
	defer srv.Close()

	err := New(token, WithBaseURL(srv.URL)).do(context.Background(), http.MethodGet, "/v1/x", nil, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("error leaked the token: %v", err)
	}
}
```

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/notion/ -v`
Expected: FAIL, `undefined: New`

- [ ] **Step 3: Implementa `internal/notion/errors.go`**

```go
package notion

import (
	"errors"
	"fmt"
)

// Sentinel errors callers match with errors.Is.
var (
	ErrUnauthorized = errors.New("notion: unauthorized")
	ErrNotFound     = errors.New("notion: object not found")
	ErrRateLimited  = errors.New("notion: rate limited")
)

// APIError is a structured Notion error response. It deliberately carries no
// request context beyond status, code and message so that a token can never
// end up inside it.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("notion: %s (%d): %s", e.Code, e.Status, e.Message)
}

// Is lets errors.Is match an APIError against the sentinels above.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.Status == 401 || e.Status == 403
	case ErrNotFound:
		return e.Status == 404
	case ErrRateLimited:
		return e.Status == 429
	}
	return false
}
```

- [ ] **Step 4: Implementa `internal/notion/client.go`**

```go
// Package notion is a minimal client for the Notion REST API.
//
// It speaks HTTP and nothing else: it knows no notion of "ticket" or "status".
// Only the endpoints notion-track actually needs are implemented, which is why
// there is no third-party SDK here — owning the client is what lets us control
// the Notion-Version header.
package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client talks to the Notion API on behalf of an internal integration.
type Client struct {
	token   string
	baseURL string
	http    *http.Client
}

// Option customises a Client.
type Option func(*Client)

// WithBaseURL points the client at another host. Tests use it with httptest.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithHTTPClient replaces the underlying HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New builds a client authenticated with an integration token.
func New(token string, opts ...Option) *Client {
	c := &Client{
		token:   token,
		baseURL: BaseURL,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// do performs one request. body and out may be nil. Non-2xx responses are
// decoded into an *APIError.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var payload io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("notion: encoding request: %w", err)
		}
		payload = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return fmt.Errorf("notion: building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Notion-Version", APIVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// url.Error would repeat the URL but never the header, so this is safe.
		return fmt.Errorf("notion: request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("notion: reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &APIError{Status: resp.StatusCode, Code: "unknown", Message: string(raw)}
		var decoded struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &decoded) == nil && decoded.Code != "" {
			apiErr.Code, apiErr.Message = decoded.Code, decoded.Message
		}
		return apiErr
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("notion: decoding response: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Verifica che i test passino**

Run: `go test ./internal/notion/ -race -v`
Expected: PASS, quattro test verdi

- [ ] **Step 6: Commit**

```bash
git add internal/notion/
git commit -m "feat(notion): add HTTP client with typed errors and token redaction"
```

---

## Task 4: Retry con backoff su 429 e 5xx

Un solo token è condiviso tra tre persone, i job CI e la TUI: è un unico bucket da ~3 richieste al secondo. Senza retry la promessa "sicuro in un job CI ritentato" è falsa.

**Files:**
- Create: `internal/notion/retry.go`, `internal/notion/retry_test.go`
- Modify: `internal/notion/client.go` (`do` chiama il retry)

**Interfaces:**
- Consumes: `Client.do`
- Produces: `func WithMaxRetries(int) Option`, `func WithSleep(func(time.Duration)) Option`

- [ ] **Step 1: Scrivi i test**

`internal/notion/retry_test.go`:

```go
package notion

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoRetriesOn429AndHonoursRetryAfter(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"code":"rate_limited","message":"slow down"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var slept []time.Duration
	c := New("t", WithBaseURL(srv.URL), WithSleep(func(d time.Duration) { slept = append(slept, d) }))

	if err := c.do(context.Background(), http.MethodGet, "/v1/x", nil, nil); err != nil {
		t.Fatalf("do: %v", err)
	}
	if calls != 2 {
		t.Fatalf("made %d calls, want 2", calls)
	}
	if len(slept) != 1 || slept[0] != 2*time.Second {
		t.Fatalf("slept %v, want one 2s wait from Retry-After", slept)
	}
}

func TestDoRetriesOn503WithExponentialBackoff(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var slept []time.Duration
	c := New("t", WithBaseURL(srv.URL), WithSleep(func(d time.Duration) { slept = append(slept, d) }))

	if err := c.do(context.Background(), http.MethodGet, "/v1/x", nil, nil); err != nil {
		t.Fatalf("do: %v", err)
	}
	if len(slept) != 2 || slept[1] <= slept[0] {
		t.Fatalf("backoff did not grow: %v", slept)
	}
}

// 529 is service_overload. Notion documents it next to 429 and asks for the
// same treatment, and it has no net/http constant, so it is easy to miss.
func TestDoRetriesOn529(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(529)
			w.Write([]byte(`{"code":"service_overload","message":"overloaded"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var slept []time.Duration
	c := New("t", WithBaseURL(srv.URL), WithSleep(func(d time.Duration) { slept = append(slept, d) }))

	if err := c.do(context.Background(), http.MethodGet, "/v1/x", nil, nil); err != nil {
		t.Fatalf("do: %v", err)
	}
	if calls != 2 {
		t.Fatalf("made %d calls, want 2", calls)
	}
	if len(slept) != 1 || slept[0] != time.Second {
		t.Fatalf("slept %v, want one 1s wait from Retry-After", slept)
	}
}

func TestDoGivesUpAfterMaxRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"code":"rate_limited","message":"nope"}`))
	}))
	defer srv.Close()

	c := New("t", WithBaseURL(srv.URL), WithMaxRetries(2), WithSleep(func(time.Duration) {}))
	err := c.do(context.Background(), http.MethodGet, "/v1/x", nil, nil)

	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("got %v, want ErrRateLimited", err)
	}
	if calls != 3 { // first attempt + 2 retries
		t.Fatalf("made %d calls, want 3", calls)
	}
}

// A 400 is the caller's fault: retrying it wastes the shared rate budget.
func TestDoDoesNotRetryClientErrors(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"code":"validation_error","message":"bad"}`))
	}))
	defer srv.Close()

	c := New("t", WithBaseURL(srv.URL), WithSleep(func(time.Duration) {}))
	if err := c.do(context.Background(), http.MethodGet, "/v1/x", nil, nil); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Fatalf("made %d calls, want 1", calls)
	}
}
```

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/notion/ -run TestDoRetries -v`
Expected: FAIL, `undefined: WithSleep`

- [ ] **Step 3: Implementa `internal/notion/retry.go`**

```go
package notion

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// Notion allows roughly three requests per second per integration. A single
// token is shared between humans, CI jobs and the TUI, so transient 429s are
// expected rather than exceptional.
const (
	defaultMaxRetries = 4
	baseBackoff       = 500 * time.Millisecond
	maxBackoff        = 30 * time.Second
)

// WithMaxRetries caps how many times a retryable response is retried.
func WithMaxRetries(n int) Option { return func(c *Client) { c.maxRetries = n } }

// WithSleep replaces the sleep function. Tests use it to run instantly.
func WithSleep(f func(time.Duration)) Option { return func(c *Client) { c.sleep = f } }

// statusServiceOverload is Notion's 529. It has no net/http constant: the code
// is outside the registered range, and Notion documents it alongside 429 —
// "handling HTTP 429 and 529 responses and respecting the Retry-After response
// header value".
const statusServiceOverload = 529

// retryable reports whether a status code is worth another attempt.
func retryable(status int) bool {
	return status == http.StatusTooManyRequests ||
		status == statusServiceOverload ||
		status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout
}

// backoffFor returns how long to wait before attempt n (zero-based).
//
// Retry-After, when present, always wins: it is the server telling us exactly
// how long the bucket needs. Only the delay-seconds form is honoured; the
// HTTP-date form the RFC also allows falls through to the exponential backoff,
// which is a safe approximation.
func backoffFor(attempt int, header string) time.Duration {
	if header != "" {
		if secs, err := strconv.Atoi(header); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	d := baseBackoff << attempt
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

// wait sleeps through the client's seam while staying cancellable. The seam
// exists so tests do not actually wait; a real sleep races the context.
func (c *Client) wait(ctx context.Context, d time.Duration) error {
	done := make(chan struct{})
	go func() {
		c.sleep(d)
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}
```

- [ ] **Step 4: Modifica `internal/notion/client.go`**

Aggiungi i campi alla struct e i default in `New`:

```go
type Client struct {
	token      string
	baseURL    string
	http       *http.Client
	maxRetries int
	sleep      func(time.Duration)
}
```

In `New`, prima del ciclo sulle opzioni:

```go
	c := &Client{
		token:      token,
		baseURL:    BaseURL,
		http:       &http.Client{Timeout: 30 * time.Second},
		maxRetries: defaultMaxRetries,
		sleep:      time.Sleep,
	}
```

Rinomina il corpo attuale di `do` in `doOnce` (stessa firma) e aggiungi il wrapper:

```go
// do performs a request, retrying rate limits and transient server errors.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		err := c.doOnce(ctx, method, path, body, out)
		if err == nil {
			return nil
		}
		lastErr = err

		var apiErr *APIError
		if !errors.As(err, &apiErr) || !retryable(apiErr.Status) || attempt == c.maxRetries {
			return err
		}

		// Sleep through the seam so tests run instantly, but let a cancelled
		// context win: a 30s backoff must not outlive a Ctrl-C.
		if err := c.wait(ctx, backoffFor(attempt, apiErr.RetryAfter)); err != nil {
			return err
		}
	}
	return lastErr
}
```

Perché `doOnce` deve esporre `Retry-After`: aggiungi il campo a `APIError` in `errors.go`

```go
type APIError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter string // raw Retry-After header, seconds, empty when absent
}
```

e popolalo in `doOnce` dove costruisci l'errore:

```go
		apiErr := &APIError{
			Status:     resp.StatusCode,
			Code:       "unknown",
			Message:    string(raw),
			RetryAfter: resp.Header.Get("Retry-After"),
		}
```

Aggiungi `"errors"` agli import di `client.go`.

- [ ] **Step 5: Verifica che tutto passi**

Run: `go test ./internal/notion/ -race -v`
Expected: PASS, otto test verdi

- [ ] **Step 6: Commit**

```bash
git add internal/notion/
git commit -m "feat(notion): retry rate limits and transient errors with backoff"
```

---

## Task 5: Elencare i data source condivisi

**Files:**
- Create: `internal/notion/types.go`, `internal/notion/search.go`, `internal/notion/search_test.go`

**Interfaces:**
- Produces:
  - `type DataSourceRef struct { ID, Title, DatabaseID, DatabaseTitle string }`
  - `func (c *Client) ListDataSources(ctx context.Context) ([]DataSourceRef, error)`

- [ ] **Step 1: Scrivi i test**

`internal/notion/search_test.go`:

```go
package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListDataSourcesFiltersOnDataSourceObjects(t *testing.T) {
	var gotFilter map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotFilter, _ = body["filter"].(map[string]any)
		w.Write([]byte(`{"results":[],"has_more":false}`))
	}))
	defer srv.Close()

	if _, err := New("t", WithBaseURL(srv.URL)).ListDataSources(context.Background()); err != nil {
		t.Fatalf("ListDataSources: %v", err)
	}
	// 2025-09-03 renamed this value from "database" to "data_source".
	if gotFilter["value"] != "data_source" || gotFilter["property"] != "object" {
		t.Fatalf("filter = %v, want object/data_source", gotFilter)
	}
}

func TestListDataSourcesFollowsPagination(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		if page == 1 {
			w.Write([]byte(`{"results":[
				{"object":"data_source","id":"ds1","title":[{"plain_text":"Tasks"}],
				 "parent":{"type":"database_id","database_id":"db1"}}
			],"has_more":true,"next_cursor":"cur"}`))
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["start_cursor"] != "cur" {
			t.Errorf("start_cursor = %v, want cur", body["start_cursor"])
		}
		w.Write([]byte(`{"results":[
			{"object":"data_source","id":"ds2","title":[{"plain_text":"Roadmap"}],
			 "parent":{"type":"database_id","database_id":"db2"}}
		],"has_more":false}`))
	}))
	defer srv.Close()

	got, err := New("t", WithBaseURL(srv.URL)).ListDataSources(context.Background())
	if err != nil {
		t.Fatalf("ListDataSources: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d data sources, want 2", len(got))
	}
	if got[0].ID != "ds1" || got[0].Title != "Tasks" || got[0].DatabaseID != "db1" {
		t.Errorf("first result = %+v", got[0])
	}
	if got[1].ID != "ds2" {
		t.Errorf("second result = %+v", got[1])
	}
}
```

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/notion/ -run TestListDataSources -v`
Expected: FAIL, `undefined: ListDataSources`

- [ ] **Step 3: Implementa `internal/notion/types.go`**

```go
package notion

// RichText is the fragment shape Notion uses for titles and text values.
type RichText struct {
	PlainText string `json:"plain_text,omitempty"`
	Text      *Text  `json:"text,omitempty"`
}

// Text is the writable half of a rich text fragment.
type Text struct {
	Content string `json:"content"`
}

// PlainText flattens rich text fragments into a single string.
func PlainText(rt []RichText) string {
	var s string
	for _, r := range rt {
		s += r.PlainText
	}
	return s
}

// DataSourceRef identifies one data source and the database holding it.
//
// A database may expose several data sources, all carrying the database's
// title, so the id is what disambiguates them in `init --list`.
type DataSourceRef struct {
	ID         string
	Title      string
	DatabaseID string
}
```

- [ ] **Step 4: Implementa `internal/notion/search.go`**

```go
package notion

import (
	"context"
	"net/http"
)

// ListDataSources returns every data source shared with this integration.
//
// Notion's search index is eventually consistent, but objects shared directly
// with a connection are guaranteed to appear — which is exactly our case.
// Callers should still offer a retry: an owner may have shared the database
// seconds ago.
func (c *Client) ListDataSources(ctx context.Context) ([]DataSourceRef, error) {
	type searchReq struct {
		Filter      map[string]string `json:"filter"`
		StartCursor string            `json:"start_cursor,omitempty"`
		PageSize    int               `json:"page_size,omitempty"`
	}
	type searchResp struct {
		Results []struct {
			ID     string     `json:"id"`
			Title  []RichText `json:"title"`
			Parent struct {
				DatabaseID string `json:"database_id"`
			} `json:"parent"`
		} `json:"results"`
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	}

	var out []DataSourceRef
	cursor := ""
	for {
		req := searchReq{
			Filter:      map[string]string{"property": "object", "value": "data_source"},
			StartCursor: cursor,
			PageSize:    100,
		}
		var resp searchResp
		if err := c.do(ctx, http.MethodPost, "/v1/search", req, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Results {
			out = append(out, DataSourceRef{
				ID:         r.ID,
				Title:      PlainText(r.Title),
				DatabaseID: r.Parent.DatabaseID,
			})
		}
		if !resp.HasMore || resp.NextCursor == "" {
			return out, nil
		}
		cursor = resp.NextCursor
	}
}
```

- [ ] **Step 5: Verifica che passino**

Run: `go test ./internal/notion/ -race -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/notion/
git commit -m "feat(notion): list data sources shared with the integration"
```

---

## Task 6: Leggere lo schema di un data source

**Files:**
- Create: `internal/notion/datasource.go`, `internal/notion/datasource_test.go`
- Modify: `internal/notion/types.go`

**Interfaces:**
- Produces:
  - `type Property struct { Name, Type string; Options []string }`
  - `type Schema struct { DataSourceID, Title string; Properties map[string]Property }`
  - `func (c *Client) GetSchema(ctx context.Context, dataSourceID string) (*Schema, error)`

- [ ] **Step 1: Scrivi i test**

`internal/notion/datasource_test.go`:

```go
package notion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const schemaFixture = `{
  "id": "ds1",
  "title": [{"plain_text": "Tasks"}],
  "properties": {
    "Name":   {"id":"title", "name":"Name",   "type":"title",     "title":{}},
    "Ticket": {"id":"abc",   "name":"Ticket", "type":"rich_text", "rich_text":{}},
    "Stato":  {"id":"def",   "name":"Stato",  "type":"status",
               "status":{"options":[{"name":"Backlog"},{"name":"In corso"},{"name":"Fatto"}]}},
    "Prio":   {"id":"ghi",   "name":"Prio",   "type":"select",
               "select":{"options":[{"name":"Low"},{"name":"High"}]}},
    "Scadenza":{"id":"jkl",  "name":"Scadenza","type":"date",     "date":{}}
  }
}`

func TestGetSchemaReadsPropertiesAndOptions(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(schemaFixture))
	}))
	defer srv.Close()

	got, err := New("t", WithBaseURL(srv.URL)).GetSchema(context.Background(), "ds1")
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}
	// The schema lives on the data source, not the database, since 2025-09-03.
	if gotPath != "/v1/data_sources/ds1" {
		t.Fatalf("path = %q", gotPath)
	}
	if got.Title != "Tasks" {
		t.Errorf("title = %q", got.Title)
	}
	if len(got.Properties) != 5 {
		t.Fatalf("got %d properties, want 5", len(got.Properties))
	}
	if p := got.Properties["Stato"]; p.Type != "status" || len(p.Options) != 3 || p.Options[2] != "Fatto" {
		t.Errorf("Stato = %+v", p)
	}
	if p := got.Properties["Prio"]; p.Type != "select" || len(p.Options) != 2 {
		t.Errorf("Prio = %+v", p)
	}
	if p := got.Properties["Scadenza"]; p.Type != "date" || len(p.Options) != 0 {
		t.Errorf("Scadenza = %+v", p)
	}
}
```

- [ ] **Step 2: Verifica che fallisca**

Run: `go test ./internal/notion/ -run TestGetSchema -v`
Expected: FAIL, `undefined: GetSchema`

- [ ] **Step 3: Aggiungi i tipi in `internal/notion/types.go`**

```go
// Property is one column of a data source, flattened to what notion-track
// cares about: its name, its type, and the options a select or status accepts.
type Property struct {
	Name    string
	Type    string
	Options []string
}

// Schema is the property set of a data source.
type Schema struct {
	DataSourceID string
	Title        string
	Properties   map[string]Property
}
```

- [ ] **Step 4: Implementa `internal/notion/datasource.go`**

```go
package notion

import (
	"context"
	"net/http"
)

// GetSchema reads the property schema of a data source.
//
// Before API 2025-09-03 this lived on GET /v1/databases/{id}; that endpoint now
// returns the list of data sources instead, and the schema moved here.
func (c *Client) GetSchema(ctx context.Context, dataSourceID string) (*Schema, error) {
	type option struct {
		Name string `json:"name"`
	}
	type rawProperty struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Select *struct {
			Options []option `json:"options"`
		} `json:"select"`
		Status *struct {
			Options []option `json:"options"`
		} `json:"status"`
	}
	var resp struct {
		ID         string                 `json:"id"`
		Title      []RichText             `json:"title"`
		Properties map[string]rawProperty `json:"properties"`
	}

	if err := c.do(ctx, http.MethodGet, "/v1/data_sources/"+dataSourceID, nil, &resp); err != nil {
		return nil, err
	}

	schema := &Schema{
		DataSourceID: resp.ID,
		Title:        PlainText(resp.Title),
		Properties:   make(map[string]Property, len(resp.Properties)),
	}
	for name, raw := range resp.Properties {
		p := Property{Name: name, Type: raw.Type}
		switch {
		case raw.Select != nil:
			for _, o := range raw.Select.Options {
				p.Options = append(p.Options, o.Name)
			}
		case raw.Status != nil:
			for _, o := range raw.Status.Options {
				p.Options = append(p.Options, o.Name)
			}
		}
		schema.Properties[name] = p
	}
	return schema, nil
}
```

- [ ] **Step 5: Verifica che passi**

Run: `go test ./internal/notion/ -race -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/notion/
git commit -m "feat(notion): read data source schema with select and status options"
```

---

## Task 7: Query delle righe con paginazione

**Files:**
- Create: `internal/notion/query.go`, `internal/notion/query_test.go`
- Modify: `internal/notion/types.go`

**Interfaces:**
- Produces:
  - `type Page struct { ID, URL string; Properties map[string]PropertyValue; LastEditedTime time.Time }`
  - `type PropertyValue struct { Type string; Text string; Date string; Checkbox bool }`
  - `type Filter map[string]any`
  - `func (c *Client) QueryPages(ctx context.Context, dataSourceID string, filter Filter) ([]Page, error)`
  - `func EqualsFilter(property, propType, value string) Filter`

- [ ] **Step 1: Scrivi i test**

`internal/notion/query_test.go`:

```go
package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const pageFixture = `{
  "id": "page1",
  "url": "https://notion.so/page1",
  "last_edited_time": "2026-07-20T10:00:00.000Z",
  "properties": {
    "Name":   {"type":"title","title":[{"plain_text":"Hardening"}]},
    "Ticket": {"type":"rich_text","rich_text":[{"plain_text":"BDF-231"}]},
    "Stato":  {"type":"status","status":{"name":"In corso"}},
    "Scadenza":{"type":"date","date":{"start":"2026-08-01"}}
  }
}`

func TestQueryPagesPostsFilterToDataSourceEndpoint(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"results":[` + pageFixture + `],"has_more":false}`))
	}))
	defer srv.Close()

	filter := EqualsFilter("Ticket", "rich_text", "BDF-231")
	got, err := New("t", WithBaseURL(srv.URL)).QueryPages(context.Background(), "ds1", filter)
	if err != nil {
		t.Fatalf("QueryPages: %v", err)
	}
	if gotPath != "/v1/data_sources/ds1/query" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["filter"] == nil {
		t.Fatal("filter was not sent")
	}
	if len(got) != 1 {
		t.Fatalf("got %d pages, want 1", len(got))
	}
	p := got[0]
	if p.ID != "page1" || p.URL != "https://notion.so/page1" {
		t.Errorf("page identity = %+v", p)
	}
	if p.Properties["Ticket"].Text != "BDF-231" {
		t.Errorf("Ticket = %q", p.Properties["Ticket"].Text)
	}
	if p.Properties["Stato"].Text != "In corso" {
		t.Errorf("Stato = %q", p.Properties["Stato"].Text)
	}
	if p.Properties["Name"].Text != "Hardening" {
		t.Errorf("Name = %q", p.Properties["Name"].Text)
	}
	if p.Properties["Scadenza"].Date != "2026-08-01" {
		t.Errorf("Scadenza = %q", p.Properties["Scadenza"].Date)
	}
	if p.LastEditedTime.IsZero() {
		t.Error("last_edited_time was not parsed")
	}
}

func TestQueryPagesFollowsPagination(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		if page == 1 {
			w.Write([]byte(`{"results":[` + pageFixture + `],"has_more":true,"next_cursor":"cur"}`))
			return
		}
		w.Write([]byte(`{"results":[` + pageFixture + `],"has_more":false}`))
	}))
	defer srv.Close()

	got, err := New("t", WithBaseURL(srv.URL)).QueryPages(context.Background(), "ds1", nil)
	if err != nil {
		t.Fatalf("QueryPages: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d pages, want 2", len(got))
	}
}

func TestEqualsFilterShapesPerPropertyType(t *testing.T) {
	rich := EqualsFilter("Ticket", "rich_text", "BDF-231")
	if rich["property"] != "Ticket" {
		t.Errorf("property = %v", rich["property"])
	}
	if rich["rich_text"].(map[string]string)["equals"] != "BDF-231" {
		t.Errorf("rich_text filter = %v", rich["rich_text"])
	}

	title := EqualsFilter("Name", "title", "X")
	if title["title"].(map[string]string)["equals"] != "X" {
		t.Errorf("title filter = %v", title["title"])
	}
}
```

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/notion/ -run "TestQueryPages|TestEqualsFilter" -v`
Expected: FAIL, `undefined: QueryPages`

- [ ] **Step 3: Aggiungi i tipi in `internal/notion/types.go`**

```go
// PropertyValue is a property read off a page, flattened to the shapes
// notion-track needs. Text carries title, rich_text, select and status alike.
type PropertyValue struct {
	Type     string
	Text     string
	Date     string
	Checkbox bool
}

// Page is one row of a data source.
type Page struct {
	ID             string
	URL            string
	LastEditedTime time.Time
	Properties     map[string]PropertyValue
}

// Filter is a raw Notion query filter.
type Filter map[string]any
```

Aggiungi `"time"` agli import di `types.go`.

- [ ] **Step 4: Implementa `internal/notion/query.go`**

Il metodo HTTP di questo endpoint è quello registrato nelle note del Task 2: se le note dicono `PATCH`, sostituisci `http.MethodPost` qui sotto e correggi il test di conseguenza.

```go
package notion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// EqualsFilter builds an equality filter for one property. The filter body is
// keyed by property type, so the caller must pass the type from the schema.
func EqualsFilter(property, propType, value string) Filter {
	return Filter{
		"property": property,
		propType:   map[string]string{"equals": value},
	}
}

// QueryPages returns every row matching filter, following pagination.
// A nil filter returns all rows.
func (c *Client) QueryPages(ctx context.Context, dataSourceID string, filter Filter) ([]Page, error) {
	type queryReq struct {
		Filter      Filter `json:"filter,omitempty"`
		StartCursor string `json:"start_cursor,omitempty"`
		PageSize    int    `json:"page_size,omitempty"`
	}
	var out []Page
	cursor := ""
	for {
		var resp struct {
			Results    []json.RawMessage `json:"results"`
			HasMore    bool              `json:"has_more"`
			NextCursor string            `json:"next_cursor"`
		}
		req := queryReq{Filter: filter, StartCursor: cursor, PageSize: 100}
		// url.PathEscape: the id comes from config or a flag, and an unescaped
		// "/" would silently retarget the request.
		path := "/v1/data_sources/" + url.PathEscape(dataSourceID) + "/query"
		if err := c.do(ctx, http.MethodPost, path, req, &resp); err != nil {
			return nil, err
		}
		for _, raw := range resp.Results {
			p, err := decodePage(raw)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		}
		if !resp.HasMore || resp.NextCursor == "" {
			return out, nil
		}
		// Same guard as ListDataSources: a cursor that does not advance would
		// loop forever, appending the same page every time.
		if resp.NextCursor == cursor {
			return nil, fmt.Errorf(
				"notion: query pagination stalled, cursor %q repeated", resp.NextCursor)
		}
		cursor = resp.NextCursor
	}
}

// decodePage flattens Notion's per-type property shapes into PropertyValue.
func decodePage(raw json.RawMessage) (Page, error) {
	var envelope struct {
		ID             string    `json:"id"`
		URL            string    `json:"url"`
		LastEditedTime time.Time `json:"last_edited_time"`
		Properties     map[string]struct {
			Type     string     `json:"type"`
			Title    []RichText `json:"title"`
			RichText []RichText `json:"rich_text"`
			Select   *struct {
				Name string `json:"name"`
			} `json:"select"`
			Status *struct {
				Name string `json:"name"`
			} `json:"status"`
			Date *struct {
				Start string `json:"start"`
			} `json:"date"`
			Checkbox bool `json:"checkbox"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Page{}, err
	}

	p := Page{
		ID:             envelope.ID,
		URL:            envelope.URL,
		LastEditedTime: envelope.LastEditedTime,
		Properties:     make(map[string]PropertyValue, len(envelope.Properties)),
	}
	for name, v := range envelope.Properties {
		pv := PropertyValue{Type: v.Type, Checkbox: v.Checkbox}
		switch v.Type {
		case "title":
			pv.Text = PlainText(v.Title)
		case "rich_text":
			pv.Text = PlainText(v.RichText)
		case "select":
			if v.Select != nil {
				pv.Text = v.Select.Name
			}
		case "status":
			if v.Status != nil {
				pv.Text = v.Status.Name
			}
		case "date":
			if v.Date != nil {
				pv.Date = v.Date.Start
			}
		}
		p.Properties[name] = pv
	}
	return p, nil
}
```

- [ ] **Step 5: Verifica che passino**

Run: `go test ./internal/notion/ -race -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/notion/
git commit -m "feat(notion): query data source rows with pagination"
```

---

## Task 8: Creare e aggiornare pagine

**Files:**
- Create: `internal/notion/page.go`, `internal/notion/page_test.go`

**Interfaces:**
- Produces:
  - `func (c *Client) CreatePage(ctx context.Context, dataSourceID string, props map[string]any) (Page, error)`
  - `func (c *Client) UpdatePage(ctx context.Context, pageID string, props map[string]any) (Page, error)`
  - `func (c *Client) Me(ctx context.Context) (string, error)`

- [ ] **Step 1: Scrivi i test**

`internal/notion/page_test.go`:

```go
package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreatePageUsesDataSourceParent(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/pages" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(pageFixture))
	}))
	defer srv.Close()

	props := map[string]any{"Ticket": map[string]any{"rich_text": []any{}}}
	got, err := New("t", WithBaseURL(srv.URL)).CreatePage(context.Background(), "ds1", props)
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	parent, _ := gotBody["parent"].(map[string]any)
	// 2025-09-03 moved the parent from database_id to data_source_id.
	if parent["type"] != "data_source_id" || parent["data_source_id"] != "ds1" {
		t.Fatalf("parent = %v", parent)
	}
	if got.ID != "page1" {
		t.Errorf("page id = %q", got.ID)
	}
}

func TestUpdatePagePatchesProperties(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Write([]byte(pageFixture))
	}))
	defer srv.Close()

	_, err := New("t", WithBaseURL(srv.URL)).UpdatePage(context.Background(), "page1",
		map[string]any{"Stato": map[string]any{"status": map[string]string{"name": "Fatto"}}})
	if err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/v1/pages/page1" {
		t.Fatalf("got %s %s", gotMethod, gotPath)
	}
}

func TestMeReturnsTheBotName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/users/me" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Write([]byte(`{"id":"bot1","name":"notion-track","type":"bot"}`))
	}))
	defer srv.Close()

	name, err := New("t", WithBaseURL(srv.URL)).Me(context.Background())
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if name != "notion-track" {
		t.Fatalf("name = %q", name)
	}
}
```

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/notion/ -run "TestCreatePage|TestUpdatePage|TestMe" -v`
Expected: FAIL, `undefined: CreatePage`

- [ ] **Step 3: Implementa `internal/notion/page.go`**

```go
package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// CreatePage adds a row to a data source. props holds already-built Notion
// property values, keyed by property name; building them is tracker's job.
func (c *Client) CreatePage(ctx context.Context, dataSourceID string, props map[string]any) (Page, error) {
	body := map[string]any{
		"parent": map[string]string{
			"type":           "data_source_id",
			"data_source_id": dataSourceID,
		},
		"properties": props,
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, "/v1/pages", body, &raw); err != nil {
		return Page{}, err
	}
	return decodePage(raw)
}

// UpdatePage patches properties on an existing row.
func (c *Client) UpdatePage(ctx context.Context, pageID string, props map[string]any) (Page, error) {
	var raw json.RawMessage
	body := map[string]any{"properties": props}
	if err := c.do(ctx, http.MethodPatch, "/v1/pages/"+url.PathEscape(pageID), body, &raw); err != nil {
		return Page{}, err
	}
	return decodePage(raw)
}

// Me returns the name of the bot the token belongs to. doctor uses it as the
// cheapest possible proof that the token is valid.
func (c *Client) Me(ctx context.Context) (string, error) {
	var resp struct {
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/users/me", nil, &resp); err != nil {
		return "", err
	}
	return resp.Name, nil
}
```

- [ ] **Step 4: Verifica che passino**

Run: `go test ./internal/notion/ -race -v`
Expected: PASS, tutto il pacchetto verde

- [ ] **Step 5: Commit**

```bash
git add internal/notion/
git commit -m "feat(notion): create and update pages, add token identity check"
```

---

## Task 9: Config a profili con `schema_version`

**Files:**
- Create: `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `go.mod` (aggiunge `gopkg.in/yaml.v3`)

**Interfaces:**
- Produces:
  - `type Config struct { SchemaVersion int; DefaultProfile string; Profiles map[string]Profile }`
  - `type Profile struct { DatabaseID, DataSourceID, StatusType string; Properties Properties }`
  - `type Properties struct { Ticket, Status, Title, Due string }`
  - `func Load() (*Config, error)`, `func LoadFrom(path string) (*Config, error)`
  - `func (c *Config) Save() error`
  - `func (c *Config) Resolve(name string) (Profile, error)`
  - `func Token() (string, bool)` — token e flag "proviene da env"
  - `var configPath = defaultConfigPath` (seam per i test)

- [ ] **Step 1: Scrivi i test**

`internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func withTempConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	old := configPath
	configPath = func() (string, error) { return path, nil }
	t.Cleanup(func() { configPath = old })
	return path
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	withTempConfig(t)

	cfg := &Config{
		SchemaVersion:  CurrentSchemaVersion,
		DefaultProfile: "work",
		Profiles: map[string]Profile{
			"work": {
				DatabaseID:   "db1",
				DataSourceID: "ds1",
				StatusType:   "status",
				Properties:   Properties{Ticket: "Ticket", Status: "Stato", Title: "Name", Due: "Scadenza"},
			},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, err := got.Resolve("")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.DataSourceID != "ds1" || p.Properties.Status != "Stato" {
		t.Fatalf("profile = %+v", p)
	}
}

// The config holds no secret, but it sits next to one: 0600 keeps it boring.
func TestSaveUsesRestrictivePermissions(t *testing.T) {
	path := withTempConfig(t)
	cfg := &Config{SchemaVersion: CurrentSchemaVersion, DefaultProfile: "w",
		Profiles: map[string]Profile{"w": {DataSourceID: "ds"}}}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("permissions = %o, want 600", perm)
	}
}

func TestResolveNamedProfile(t *testing.T) {
	cfg := &Config{DefaultProfile: "work", Profiles: map[string]Profile{
		"work":     {DataSourceID: "ds1"},
		"personal": {DataSourceID: "ds2"},
	}}
	p, err := cfg.Resolve("personal")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.DataSourceID != "ds2" {
		t.Fatalf("data source = %q", p.DataSourceID)
	}
}

func TestResolveUnknownProfileListsAvailableOnes(t *testing.T) {
	cfg := &Config{DefaultProfile: "work", Profiles: map[string]Profile{"work": {}}}
	_, err := cfg.Resolve("nope")
	if err == nil {
		t.Fatal("expected an error")
	}
	// The message must be actionable: an agent reading it should recover.
	if got := err.Error(); !strings.Contains(got, "work") {
		t.Fatalf("error %q does not list the available profiles", got)
	}
}

func TestTokenPrefersEnvAndFlagsItsOrigin(t *testing.T) {
	t.Setenv(TokenEnv, "ntn_from_env")
	tok, fromEnv := Token()
	if tok != "ntn_from_env" || !fromEnv {
		t.Fatalf("Token() = %q, %v", tok, fromEnv)
	}
}

func TestTokenAbsent(t *testing.T) {
	t.Setenv(TokenEnv, "")
	if tok, fromEnv := Token(); tok != "" || fromEnv {
		t.Fatalf("Token() = %q, %v", tok, fromEnv)
	}
}
```

Aggiungi in testa a `withTempConfig` l'isolamento dall'ambiente, altrimenti chi ha le env var di notion-track esportate nella propria shell vede questi test fallire in modo apparentemente casuale:

```go
	t.Setenv(ProfileEnv, "")
	t.Setenv(DatabaseEnv, "")
	t.Setenv(DataSourceEnv, "")
```

`TestResolveNamedProfile` e `TestResolveUnknownProfileListsAvailableOnes` non usano `withTempConfig`: aggiungi le stesse tre righe anche lì.

Import del file di test: `os`, `path/filepath`, `strings`, `testing`.

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/config/ -v`
Expected: FAIL, `undefined: Config`

- [ ] **Step 3: Aggiungi la dipendenza**

```bash
go get gopkg.in/yaml.v3
```

- [ ] **Step 4: Implementa `internal/config/config.go`**

```go
// Package config reads and writes notion-track's YAML configuration.
//
// Two rules shape this file. First, the token never lands on disk by accident:
// a token read from the environment is remembered as such, and Save skips it.
// Second, Load may warn on stderr, Save must stay silent.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// CurrentSchemaVersion is bumped whenever the on-disk shape changes.
const CurrentSchemaVersion = 1

// Environment variables, all overriding the config file.
const (
	TokenEnv      = "NOTION_TOKEN"
	ProfileEnv    = "NOTION_TRACK_PROFILE"
	DatabaseEnv   = "NOTION_TRACK_DB"
	DataSourceEnv = "NOTION_TRACK_DATA_SOURCE"
)

// Properties maps notion-track's concepts onto real property names.
// Nothing here is hardcoded: init discovers these from the data source.
type Properties struct {
	Ticket string `yaml:"ticket"`
	Status string `yaml:"status"`
	Title  string `yaml:"title"`
	Due    string `yaml:"due,omitempty"`
}

// Profile is one configured data source.
type Profile struct {
	DatabaseID   string     `yaml:"database_id"`
	DataSourceID string     `yaml:"data_source_id"`
	Properties   Properties `yaml:"properties"`
	// StatusType is "status" or "select". It decides the payload shape and,
	// more importantly, how strict validation has to be: a select silently
	// creates unknown options, a status rejects them.
	StatusType string `yaml:"status_type"`
}

// Config is the whole file.
type Config struct {
	SchemaVersion  int                `yaml:"schema_version"`
	DefaultProfile string             `yaml:"default_profile"`
	Profiles       map[string]Profile `yaml:"profiles"`
}

// configPath is a seam: tests point it at t.TempDir().
var configPath = defaultConfigPath

func defaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locating config dir: %w", err)
	}
	return filepath.Join(dir, "notion-track", "config.yml"), nil
}

// ErrNotConfigured signals that no config file exists yet.
var ErrNotConfigured = errors.New("config: not configured; run 'notion-track init'")

// Load reads the config from its default location.
func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads the config from an explicit path.
func LoadFrom(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotConfigured
	}
	if err != nil {
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("config: parsing %s: %w", path, err)
	}
	migrate(&cfg)
	return &cfg, nil
}

// Save writes the config atomically with 0600 permissions.
func (c *Config) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: creating config dir: %w", err)
	}

	c.SchemaVersion = CurrentSchemaVersion
	raw, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("config: encoding: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("config: writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("config: replacing %s: %w", path, err)
	}
	return nil
}

// Resolve returns a profile by name, falling back to NOTION_TRACK_PROFILE and
// then to default_profile. Environment overrides are applied last so that CI
// can point an existing profile at another data source.
func (c *Config) Resolve(name string) (Profile, error) {
	if name == "" {
		name = os.Getenv(ProfileEnv)
	}
	if name == "" {
		name = c.DefaultProfile
	}

	p, ok := c.Profiles[name]
	if !ok {
		names := make([]string, 0, len(c.Profiles))
		for n := range c.Profiles {
			names = append(names, n)
		}
		sort.Strings(names)
		return Profile{}, fmt.Errorf(
			"config: no profile %q; available profiles: %s", name, strings.Join(names, ", "))
	}

	if v := os.Getenv(DatabaseEnv); v != "" {
		p.DatabaseID = v
	}
	if v := os.Getenv(DataSourceEnv); v != "" {
		p.DataSourceID = v
	}
	return p, nil
}

// Token returns the integration token and whether it came from the
// environment. Callers must never persist a token whose second return value
// is true, otherwise a CI secret ends up on a developer's disk.
func Token() (string, bool) {
	if v := os.Getenv(TokenEnv); v != "" {
		return v, true
	}
	return "", false
}
```

- [ ] **Step 5: Implementa `internal/config/migrate.go`**

```go
package config

import (
	"fmt"
	"os"
)

// migrate brings an on-disk config up to CurrentSchemaVersion.
//
// A missing version means a file written before versioning existed. A version
// from the future means the user downgraded notion-track: warn, then carry on
// reading what we understand rather than refusing to work.
func migrate(c *Config) {
	switch {
	case c.SchemaVersion == 0:
		c.SchemaVersion = CurrentSchemaVersion
	case c.SchemaVersion > CurrentSchemaVersion:
		fmt.Fprintf(os.Stderr,
			"warning: config schema version %d is newer than this build understands (%d); "+
				"some settings may be ignored\n",
			c.SchemaVersion, CurrentSchemaVersion)
	}
}
```

- [ ] **Step 6: Verifica che passino**

Run: `go test ./internal/config/ -race -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/config/ go.mod go.sum
git commit -m "feat(config): add YAML profile config with schema versioning"
```

---

## Task 10: Decisione di upsert

Il cuore del dominio. Nessun I/O, nessun mock, test in millisecondi.

**Files:**
- Create: `internal/tracker/decide.go`, `internal/tracker/decide_test.go`

**Interfaces:**
- Consumes: `notion.Page`
- Produces:
  - `type Action int` con `ActionCreate`, `ActionUpdate`
  - `type Decision struct { Action Action; PageID string }`
  - `type DuplicateError struct { Ticket string; Pages []notion.Page }`
  - `func Decide(ticket string, matches []notion.Page) (Decision, error)`

- [ ] **Step 1: Scrivi i test**

`internal/tracker/decide_test.go`:

```go
package tracker

import (
	"errors"
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

func TestDecide(t *testing.T) {
	tests := []struct {
		name       string
		matches    []notion.Page
		wantAction Action
		wantPageID string
		wantErr    bool
	}{
		{
			name:       "no match creates",
			matches:    nil,
			wantAction: ActionCreate,
		},
		{
			name:       "one match updates that page",
			matches:    []notion.Page{{ID: "page1"}},
			wantAction: ActionUpdate,
			wantPageID: "page1",
		},
		{
			name: "several matches is a data problem, not a choice",
			matches: []notion.Page{
				{ID: "page1", URL: "https://notion.so/page1"},
				{ID: "page2", URL: "https://notion.so/page2"},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decide("BDF-231", tc.matches)
			if tc.wantErr {
				var dup *DuplicateError
				if !errors.As(err, &dup) {
					t.Fatalf("got %v, want *DuplicateError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if got.Action != tc.wantAction || got.PageID != tc.wantPageID {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

// The whole point of failing on duplicates is that the user can go fix them,
// so the error has to hand over the URLs.
func TestDuplicateErrorListsPageURLs(t *testing.T) {
	err := &DuplicateError{
		Ticket: "BDF-231",
		Pages: []notion.Page{
			{ID: "page1", URL: "https://notion.so/page1"},
			{ID: "page2", URL: "https://notion.so/page2"},
		},
	}
	msg := err.Error()
	for _, want := range []string{"BDF-231", "https://notion.so/page1", "https://notion.so/page2"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message is missing %q: %s", want, msg)
		}
	}
}
```

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/tracker/ -v`
Expected: FAIL, `undefined: Decide`

- [ ] **Step 3: Implementa `internal/tracker/decide.go`**

```go
// Package tracker holds notion-track's domain logic.
//
// Nothing here performs I/O: it takes data in and returns decisions. That is
// what makes the interesting behaviour — upsert semantics, property payloads,
// status validation — testable without a network or a mock.
package tracker

import (
	"fmt"
	"strings"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

// Action is what an upsert resolved to.
type Action int

const (
	// ActionCreate means no row carries this ticket key yet.
	ActionCreate Action = iota
	// ActionUpdate means exactly one row does.
	ActionUpdate
)

// Decision is the outcome of Decide.
type Decision struct {
	Action Action
	PageID string // set only for ActionUpdate
}

// DuplicateError reports several rows sharing one ticket key.
type DuplicateError struct {
	Ticket string
	Pages  []notion.Page
}

func (e *DuplicateError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ticket %q matches %d rows; refusing to guess which one to update:",
		e.Ticket, len(e.Pages))
	for _, p := range e.Pages {
		fmt.Fprintf(&b, "\n  %s", p.URL)
	}
	b.WriteString("\n  fix: delete the duplicates in Notion, then run the command again")
	return b.String()
}

// Decide turns the rows matching a ticket key into a create-or-update choice.
//
// More than one match is refused rather than resolved: duplicates are a data
// problem, and silently updating "the most recent" one would hide it and let
// the other row drift forever.
func Decide(ticket string, matches []notion.Page) (Decision, error) {
	switch len(matches) {
	case 0:
		return Decision{Action: ActionCreate}, nil
	case 1:
		return Decision{Action: ActionUpdate, PageID: matches[0].ID}, nil
	default:
		return Decision{}, &DuplicateError{Ticket: ticket, Pages: matches}
	}
}
```

- [ ] **Step 4: Verifica che passino**

Run: `go test ./internal/tracker/ -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/
git commit -m "feat(tracker): decide create or update, refuse duplicate tickets"
```

---

## Task 11: Validazione dello stato

Va prima del payload perché il payload la usa. La ragione per validare non è risparmiare una chiamata: è che una proprietà `select` **crea silenziosamente** l'opzione sconosciuta, trasformando un typo in dati sporchi permanenti.

**Files:**
- Create: `internal/tracker/validate.go`, `internal/tracker/validate_test.go`

**Interfaces:**
- Produces: `func ValidateStatus(value string, allowed []string) error`

- [ ] **Step 1: Scrivi i test**

`internal/tracker/validate_test.go`:

```go
package tracker

import (
	"strings"
	"testing"
)

func TestValidateStatusAcceptsAKnownOption(t *testing.T) {
	if err := ValidateStatus("Fatto", []string{"Backlog", "In corso", "Fatto"}); err != nil {
		t.Fatalf("ValidateStatus: %v", err)
	}
}

func TestValidateStatusRejectsUnknownAndListsTheOptions(t *testing.T) {
	err := ValidateStatus("Fattto", []string{"Backlog", "In corso", "Fatto"})
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"Fattto", "Backlog", "In corso", "Fatto"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message is missing %q: %s", want, msg)
		}
	}
}

func TestValidateStatusIsCaseSensitive(t *testing.T) {
	// Notion option names are case sensitive; accepting "fatto" here would
	// create a second option on a select property.
	if err := ValidateStatus("fatto", []string{"Fatto"}); err == nil {
		t.Fatal("expected case mismatch to be rejected")
	}
}

func TestValidateStatusSkipsWhenNoOptionsAreKnown(t *testing.T) {
	// An empty allow-list means the schema could not be read; refusing every
	// value would be worse than letting the API have the final say.
	if err := ValidateStatus("anything", nil); err != nil {
		t.Fatalf("ValidateStatus: %v", err)
	}
}
```

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/tracker/ -run TestValidateStatus -v`
Expected: FAIL, `undefined: ValidateStatus`

- [ ] **Step 3: Implementa `internal/tracker/validate.go`**

```go
package tracker

import (
	"fmt"
	"slices"
	"strings"
)

// ValidationError marks a value the user supplied as unusable. Callers map it
// onto the "invalid usage" exit code, so it must not be used for failures the
// user could not have avoided.
type ValidationError struct {
	Field   string
	Value   string
	Allowed []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("unknown %s %q; allowed values are: %s",
		e.Field, e.Value, strings.Join(e.Allowed, ", "))
}

// ValidateStatus checks a status value against the options read from the
// server.
//
// This matters most for select properties: Notion creates an unknown select
// option on write, so an unchecked typo becomes a permanent bogus value in the
// database. Status properties reject unknown values server-side, but failing
// here still produces a far better message than the API's.
//
// An empty allowed list disables the check.
func ValidateStatus(value string, allowed []string) error {
	if len(allowed) == 0 || slices.Contains(allowed, value) {
		return nil
	}
	return &ValidationError{Field: "status", Value: value, Allowed: allowed}
}
```

- [ ] **Step 4: Verifica che passino**

Run: `go test ./internal/tracker/ -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/
git commit -m "feat(tracker): validate status values against the live schema"
```

---

## Task 12: Costruzione del payload delle proprietà

**Files:**
- Create: `internal/tracker/payload.go`, `internal/tracker/payload_test.go`

**Interfaces:**
- Consumes: `config.Properties`, `notion.Schema`, `ValidateStatus`
- Produces:
  - `type Fields struct { Ticket, Title, Status, Due string }`
  - `func BuildProperties(f Fields, props config.Properties, schema *notion.Schema) (map[string]any, error)`

- [ ] **Step 1: Scrivi i test**

`internal/tracker/payload_test.go`:

```go
package tracker

import (
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

func testSchema() *notion.Schema {
	return &notion.Schema{
		DataSourceID: "ds1",
		Properties: map[string]notion.Property{
			"Name":     {Name: "Name", Type: "title"},
			"Ticket":   {Name: "Ticket", Type: "rich_text"},
			"Stato":    {Name: "Stato", Type: "status", Options: []string{"Backlog", "In corso", "Fatto"}},
			"Scadenza": {Name: "Scadenza", Type: "date"},
		},
	}
}

func testProps() config.Properties {
	return config.Properties{Ticket: "Ticket", Status: "Stato", Title: "Name", Due: "Scadenza"}
}

func TestBuildPropertiesOnlyIncludesProvidedFields(t *testing.T) {
	got, err := BuildProperties(Fields{Ticket: "BDF-231"}, testProps(), testSchema())
	if err != nil {
		t.Fatalf("BuildProperties: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d properties, want only Ticket: %v", len(got), got)
	}
	if got["Ticket"] == nil {
		t.Fatal("Ticket is missing")
	}
}

func TestBuildPropertiesShapesEachType(t *testing.T) {
	got, err := BuildProperties(Fields{
		Ticket: "BDF-231",
		Title:  "Hardening",
		Status: "Fatto",
		Due:    "2026-08-01",
	}, testProps(), testSchema())
	if err != nil {
		t.Fatalf("BuildProperties: %v", err)
	}

	ticket := got["Ticket"].(map[string]any)["rich_text"].([]map[string]any)
	if ticket[0]["text"].(map[string]string)["content"] != "BDF-231" {
		t.Errorf("Ticket payload = %v", got["Ticket"])
	}
	title := got["Name"].(map[string]any)["title"].([]map[string]any)
	if title[0]["text"].(map[string]string)["content"] != "Hardening" {
		t.Errorf("Name payload = %v", got["Name"])
	}
	if got["Stato"].(map[string]any)["status"].(map[string]string)["name"] != "Fatto" {
		t.Errorf("Stato payload = %v", got["Stato"])
	}
	if got["Scadenza"].(map[string]any)["date"].(map[string]string)["start"] != "2026-08-01" {
		t.Errorf("Scadenza payload = %v", got["Scadenza"])
	}
}

func TestBuildPropertiesUsesSelectShapeForSelectStatus(t *testing.T) {
	schema := testSchema()
	schema.Properties["Stato"] = notion.Property{
		Name: "Stato", Type: "select", Options: []string{"Fatto"},
	}
	got, err := BuildProperties(Fields{Status: "Fatto"}, testProps(), schema)
	if err != nil {
		t.Fatalf("BuildProperties: %v", err)
	}
	if got["Stato"].(map[string]any)["select"].(map[string]string)["name"] != "Fatto" {
		t.Errorf("Stato payload = %v", got["Stato"])
	}
}

func TestBuildPropertiesRejectsUnknownStatus(t *testing.T) {
	_, err := BuildProperties(Fields{Status: "Fattto"}, testProps(), testSchema())
	if err == nil {
		t.Fatal("expected an unknown status to be rejected")
	}
}

func TestBuildPropertiesRejectsAMappingThatNoLongerMatchesTheSchema(t *testing.T) {
	props := testProps()
	props.Status = "Status" // renamed in Notion, config not updated
	_, err := BuildProperties(Fields{Status: "Fatto"}, props, testSchema())
	if err == nil {
		t.Fatal("expected a missing property to be reported")
	}
}

func TestBuildPropertiesTicketCanBeTheTitle(t *testing.T) {
	// Some databases use the title column as the ticket key.
	schema := &notion.Schema{Properties: map[string]notion.Property{
		"Key": {Name: "Key", Type: "title"},
	}}
	props := config.Properties{Ticket: "Key", Title: "Key"}
	got, err := BuildProperties(Fields{Ticket: "BDF-231"}, props, schema)
	if err != nil {
		t.Fatalf("BuildProperties: %v", err)
	}
	title := got["Key"].(map[string]any)["title"].([]map[string]any)
	if title[0]["text"].(map[string]string)["content"] != "BDF-231" {
		t.Errorf("Key payload = %v", got["Key"])
	}
}
```

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/tracker/ -run TestBuildProperties -v`
Expected: FAIL, `undefined: BuildProperties`

- [ ] **Step 3: Implementa `internal/tracker/payload.go`**

```go
package tracker

import (
	"fmt"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

// Fields are the values a user asked to write. Empty strings mean "leave this
// property alone", which is what makes `set --status` a partial update.
type Fields struct {
	Ticket string
	Title  string
	Status string
	Due    string
}

// BuildProperties turns user fields into a Notion properties payload, using
// the configured mapping and the live schema to pick each property's shape.
//
// The schema is the authority on types: a status column and a select column
// take different payloads, and which one a database uses is not our choice.
func BuildProperties(f Fields, props config.Properties, schema *notion.Schema) (map[string]any, error) {
	out := map[string]any{}

	add := func(propName, value string) error {
		if value == "" || propName == "" {
			return nil
		}
		prop, ok := schema.Properties[propName]
		if !ok {
			return fmt.Errorf(
				"property %q is configured but does not exist in the data source; "+
					"run 'notion-track doctor' to see the current schema", propName)
		}
		switch prop.Type {
		case "title":
			out[propName] = map[string]any{
				"title": []map[string]any{{"text": map[string]string{"content": value}}},
			}
		case "rich_text":
			out[propName] = map[string]any{
				"rich_text": []map[string]any{{"text": map[string]string{"content": value}}},
			}
		case "status":
			if err := ValidateStatus(value, prop.Options); err != nil {
				return err
			}
			out[propName] = map[string]any{"status": map[string]string{"name": value}}
		case "select":
			if err := ValidateStatus(value, prop.Options); err != nil {
				return err
			}
			out[propName] = map[string]any{"select": map[string]string{"name": value}}
		case "date":
			out[propName] = map[string]any{"date": map[string]string{"start": value}}
		default:
			return fmt.Errorf("property %q has unsupported type %q", propName, prop.Type)
		}
		return nil
	}

	// Title first: when the ticket key *is* the title column, the ticket value
	// must win over a separately supplied title.
	if err := add(props.Title, f.Title); err != nil {
		return nil, err
	}
	if err := add(props.Ticket, f.Ticket); err != nil {
		return nil, err
	}
	if err := add(props.Status, f.Status); err != nil {
		return nil, err
	}
	if err := add(props.Due, f.Due); err != nil {
		return nil, err
	}
	return out, nil
}
```

- [ ] **Step 4: Verifica che passino**

Run: `go test ./internal/tracker/ -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/
git commit -m "feat(tracker): build property payloads from the live schema"
```

---

## Task 13: Euristica di mapping per `init`

**Files:**
- Create: `internal/tracker/mapping.go`, `internal/tracker/mapping_test.go`

**Interfaces:**
- Produces: `func GuessMapping(schema *notion.Schema) config.Properties`

- [ ] **Step 1: Scrivi i test**

`internal/tracker/mapping_test.go`:

```go
package tracker

import (
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

func TestGuessMappingPicksTheObviousCandidates(t *testing.T) {
	got := GuessMapping(testSchema())
	if got.Title != "Name" {
		t.Errorf("title = %q, want Name", got.Title)
	}
	if got.Ticket != "Ticket" {
		t.Errorf("ticket = %q, want Ticket", got.Ticket)
	}
	if got.Status != "Stato" {
		t.Errorf("status = %q, want Stato", got.Status)
	}
	if got.Due != "Scadenza" {
		t.Errorf("due = %q, want Scadenza", got.Due)
	}
}

func TestGuessMappingRecognisesEnglishNames(t *testing.T) {
	schema := &notion.Schema{Properties: map[string]notion.Property{
		"Title":    {Name: "Title", Type: "title"},
		"Key":      {Name: "Key", Type: "rich_text"},
		"Status":   {Name: "Status", Type: "status"},
		"Due date": {Name: "Due date", Type: "date"},
	}}
	got := GuessMapping(schema)
	if got.Ticket != "Key" || got.Status != "Status" || got.Due != "Due date" {
		t.Fatalf("mapping = %+v", got)
	}
}

func TestGuessMappingFallsBackToTheOnlyCandidateOfAType(t *testing.T) {
	// No recognisable name, but a single status column: it can only be that.
	schema := &notion.Schema{Properties: map[string]notion.Property{
		"Titolo":  {Name: "Titolo", Type: "title"},
		"Fase":    {Name: "Fase", Type: "status"},
		"Codice":  {Name: "Codice", Type: "rich_text"},
	}}
	got := GuessMapping(schema)
	if got.Title != "Titolo" || got.Status != "Fase" || got.Ticket != "Codice" {
		t.Fatalf("mapping = %+v", got)
	}
}

func TestGuessMappingLeavesAmbiguityToTheUser(t *testing.T) {
	// Two unnamed rich_text columns: guessing would be a coin flip.
	schema := &notion.Schema{Properties: map[string]notion.Property{
		"Titolo": {Name: "Titolo", Type: "title"},
		"Alfa":   {Name: "Alfa", Type: "rich_text"},
		"Beta":   {Name: "Beta", Type: "rich_text"},
	}}
	if got := GuessMapping(schema); got.Ticket != "" {
		t.Fatalf("ticket = %q, want an empty guess", got.Ticket)
	}
}
```

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/tracker/ -run TestGuessMapping -v`
Expected: FAIL, `undefined: GuessMapping`

- [ ] **Step 3: Implementa `internal/tracker/mapping.go`**

```go
package tracker

import (
	"sort"
	"strings"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

// Names init recognises without asking. Matching is case-insensitive.
var (
	ticketNames = []string{"ticket", "key", "id", "chiave", "codice"}
	statusNames = []string{"status", "stato", "state", "fase"}
	dueNames    = []string{"due", "due date", "scadenza", "deadline"}
)

// GuessMapping proposes a property mapping for init to confirm.
//
// Two rules, in order: a recognisable name wins; failing that, being the only
// column of a suitable type wins. When neither applies the guess is left
// empty — a wrong guess the user waves through is worse than a question.
func GuessMapping(schema *notion.Schema) config.Properties {
	var out config.Properties

	byType := map[string][]string{}
	for name, p := range schema.Properties {
		byType[p.Type] = append(byType[p.Type], name)
	}
	for _, names := range byType {
		sort.Strings(names) // deterministic guesses
	}

	// A data source has exactly one title property.
	if titles := byType["title"]; len(titles) == 1 {
		out.Title = titles[0]
	}

	pick := func(candidates []string, known []string) string {
		for _, name := range candidates {
			for _, k := range known {
				if strings.EqualFold(name, k) {
					return name
				}
			}
		}
		if len(candidates) == 1 {
			return candidates[0]
		}
		return ""
	}

	out.Status = pick(append(byType["status"], byType["select"]...), statusNames)
	out.Due = pick(byType["date"], dueNames)

	// The ticket key is usually rich_text, but a database may use its title.
	ticketCandidates := append([]string{}, byType["rich_text"]...)
	ticketCandidates = append(ticketCandidates, byType["title"]...)
	sort.Strings(ticketCandidates)
	out.Ticket = pick(ticketCandidates, ticketNames)

	return out
}
```

- [ ] **Step 4: Verifica che passino**

Run: `go test ./internal/tracker/ -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/
git commit -m "feat(tracker): guess a property mapping for init to confirm"
```

---

## Task 14: Service — upsert, set, get, list

L'unico punto dove client, config e dominio si incontrano. CLI, TUI e (in v0.5) MCP chiamano queste funzioni identiche.

**Files:**
- Create: `internal/service/service.go`, `internal/service/service_test.go`

**Interfaces:**
- Consumes: `notion.Client`, `config.Profile`, `tracker.*`
- Produces:
  - `type Service struct { ... }`
  - `func New(client *notion.Client, profile config.Profile) *Service`
  - `type Result struct { Action string; Page notion.Page }`
  - `func (s *Service) Upsert(ctx context.Context, f tracker.Fields) (Result, error)`
  - `func (s *Service) Set(ctx context.Context, f tracker.Fields) (Result, error)`
  - `func (s *Service) Get(ctx context.Context, ticket string) (notion.Page, error)`
  - `func (s *Service) List(ctx context.Context, status string) ([]notion.Page, error)`
  - `var ErrNotFound = errors.New("ticket not found")`

- [ ] **Step 1: Scrivi i test**

`internal/service/service_test.go`:

```go
package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

const schemaJSON = `{
  "id":"ds1","title":[{"plain_text":"Tasks"}],
  "properties":{
    "Name":{"name":"Name","type":"title","title":{}},
    "Ticket":{"name":"Ticket","type":"rich_text","rich_text":{}},
    "Stato":{"name":"Stato","type":"status","status":{"options":[{"name":"In corso"},{"name":"Fatto"}]}}
  }}`

const rowJSON = `{
  "id":"page1","url":"https://notion.so/page1","last_edited_time":"2026-07-20T10:00:00.000Z",
  "properties":{
    "Name":{"type":"title","title":[{"plain_text":"Hardening"}]},
    "Ticket":{"type":"rich_text","rich_text":[{"plain_text":"BDF-231"}]},
    "Stato":{"type":"status","status":{"name":"In corso"}}
  }}`

func testProfile() config.Profile {
	return config.Profile{
		DatabaseID:   "db1",
		DataSourceID: "ds1",
		StatusType:   "status",
		Properties:   config.Properties{Ticket: "Ticket", Status: "Stato", Title: "Name"},
	}
}

// routes returns a server answering schema reads, queries, creates and updates
// with whatever the test supplies for the query result.
func routes(t *testing.T, queryResults string, seen *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + queryResults + `],"has_more":false}`))
		case r.URL.Path == "/v1/pages":
			w.Write([]byte(rowJSON))
		default: // PATCH /v1/pages/{id}
			w.Write([]byte(rowJSON))
		}
	}))
}

func TestUpsertCreatesWhenNoRowMatches(t *testing.T) {
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	got, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231", Status: "Fatto"})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got.Action != "created" {
		t.Fatalf("action = %q, want created", got.Action)
	}
	if !contains(seen, "POST /v1/pages") {
		t.Fatalf("no page was created: %v", seen)
	}
}

func TestUpsertUpdatesWhenOneRowMatches(t *testing.T) {
	var seen []string
	srv := routes(t, rowJSON, &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	got, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231", Status: "Fatto"})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got.Action != "updated" {
		t.Fatalf("action = %q, want updated", got.Action)
	}
	if !contains(seen, "PATCH /v1/pages/page1") {
		t.Fatalf("no page was updated: %v", seen)
	}
}

func TestUpsertIsIdempotent(t *testing.T) {
	var seen []string
	srv := routes(t, rowJSON, &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	f := tracker.Fields{Ticket: "BDF-231", Status: "Fatto"}
	for i := 0; i < 2; i++ {
		if _, err := s.Upsert(context.Background(), f); err != nil {
			t.Fatalf("Upsert %d: %v", i, err)
		}
	}
	if contains(seen, "POST /v1/pages") {
		t.Fatal("a second run created a row instead of updating it")
	}
}

func TestUpsertFailsOnDuplicates(t *testing.T) {
	var seen []string
	srv := routes(t, rowJSON+","+rowJSON, &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	_, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231"})
	var dup *tracker.DuplicateError
	if !errors.As(err, &dup) {
		t.Fatalf("got %v, want *tracker.DuplicateError", err)
	}
}

func TestSetFailsWhenTheRowDoesNotExist(t *testing.T) {
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	_, err := s.Set(context.Background(), tracker.Fields{Ticket: "BDF-999", Status: "Fatto"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if contains(seen, "POST /v1/pages") {
		t.Fatal("set created a row; only upsert may do that")
	}
}

func TestListFiltersByStatus(t *testing.T) {
	var gotFilter map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(schemaJSON))
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotFilter, _ = body["filter"].(map[string]any)
		w.Write([]byte(`{"results":[` + rowJSON + `],"has_more":false}`))
	}))
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	got, err := s.List(context.Background(), "In corso")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if gotFilter["property"] != "Stato" {
		t.Fatalf("filter = %v", gotFilter)
	}
}

// The schema is read once per Service, not once per call.
func TestSchemaIsCached(t *testing.T) {
	var seen []string
	srv := routes(t, rowJSON, &seen)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile())
	ctx := context.Background()
	s.Get(ctx, "BDF-231")
	s.Get(ctx, "BDF-231")

	n := 0
	for _, r := range seen {
		if r == "GET /v1/data_sources/ds1" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("schema was fetched %d times, want 1", n)
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/service/ -v`
Expected: FAIL, `undefined: New`

- [ ] **Step 3: Implementa `internal/service/service.go`**

```go
// Package service orchestrates the client, the config and the domain.
//
// It is the only layer where those three meet, which is what lets the CLI, the
// TUI and (later) the MCP adapter share one implementation of every operation.
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

// ErrNotFound means no row carries the requested ticket key.
var ErrNotFound = errors.New("ticket not found")

// Service performs notion-track's operations against one profile.
type Service struct {
	client  *notion.Client
	profile config.Profile
	schema  *notion.Schema // read lazily, once
}

// New builds a Service for a profile.
func New(client *notion.Client, profile config.Profile) *Service {
	return &Service{client: client, profile: profile}
}

// Schema returns the data source schema, fetching it at most once.
func (s *Service) Schema(ctx context.Context) (*notion.Schema, error) {
	if s.schema != nil {
		return s.schema, nil
	}
	schema, err := s.client.GetSchema(ctx, s.profile.DataSourceID)
	if err != nil {
		return nil, err
	}
	s.schema = schema
	return schema, nil
}

// Result reports what an upsert or set did.
type Result struct {
	Action string // "created" or "updated"
	Page   notion.Page
}

// findByTicket returns every row whose ticket property equals key.
func (s *Service) findByTicket(ctx context.Context, key string) ([]notion.Page, error) {
	schema, err := s.Schema(ctx)
	if err != nil {
		return nil, err
	}
	name := s.profile.Properties.Ticket
	prop, ok := schema.Properties[name]
	if !ok {
		return nil, fmt.Errorf(
			"ticket property %q does not exist in the data source; run 'notion-track doctor'", name)
	}
	filter := notion.EqualsFilter(name, prop.Type, key)
	return s.client.QueryPages(ctx, s.profile.DataSourceID, filter)
}

// Upsert creates the row for a ticket or updates it if it already exists.
func (s *Service) Upsert(ctx context.Context, f tracker.Fields) (Result, error) {
	matches, err := s.findByTicket(ctx, f.Ticket)
	if err != nil {
		return Result{}, err
	}
	decision, err := tracker.Decide(f.Ticket, matches)
	if err != nil {
		return Result{}, err
	}

	schema, err := s.Schema(ctx)
	if err != nil {
		return Result{}, err
	}
	props, err := tracker.BuildProperties(f, s.profile.Properties, schema)
	if err != nil {
		return Result{}, err
	}

	if decision.Action == tracker.ActionCreate {
		page, err := s.client.CreatePage(ctx, s.profile.DataSourceID, props)
		return Result{Action: "created", Page: page}, err
	}
	page, err := s.client.UpdatePage(ctx, decision.PageID, props)
	return Result{Action: "updated", Page: page}, err
}

// Set updates an existing row and fails if it does not exist. In CI a missing
// ticket is usually a symptom worth surfacing, not a row to conjure up.
func (s *Service) Set(ctx context.Context, f tracker.Fields) (Result, error) {
	matches, err := s.findByTicket(ctx, f.Ticket)
	if err != nil {
		return Result{}, err
	}
	if len(matches) == 0 {
		return Result{}, fmt.Errorf("%w: %s", ErrNotFound, f.Ticket)
	}
	decision, err := tracker.Decide(f.Ticket, matches)
	if err != nil {
		return Result{}, err
	}

	schema, err := s.Schema(ctx)
	if err != nil {
		return Result{}, err
	}
	props, err := tracker.BuildProperties(f, s.profile.Properties, schema)
	if err != nil {
		return Result{}, err
	}
	page, err := s.client.UpdatePage(ctx, decision.PageID, props)
	return Result{Action: "updated", Page: page}, err
}

// Get returns the row for a ticket.
func (s *Service) Get(ctx context.Context, ticket string) (notion.Page, error) {
	matches, err := s.findByTicket(ctx, ticket)
	if err != nil {
		return notion.Page{}, err
	}
	if len(matches) == 0 {
		return notion.Page{}, fmt.Errorf("%w: %s", ErrNotFound, ticket)
	}
	if len(matches) > 1 {
		return notion.Page{}, &tracker.DuplicateError{Ticket: ticket, Pages: matches}
	}
	return matches[0], nil
}

// List returns rows, optionally filtered by status.
func (s *Service) List(ctx context.Context, status string) ([]notion.Page, error) {
	schema, err := s.Schema(ctx)
	if err != nil {
		return nil, err
	}
	var filter notion.Filter
	if status != "" {
		name := s.profile.Properties.Status
		prop, ok := schema.Properties[name]
		if !ok {
			return nil, fmt.Errorf(
				"status property %q does not exist in the data source; run 'notion-track doctor'", name)
		}
		if err := tracker.ValidateStatus(status, prop.Options); err != nil {
			return nil, err
		}
		filter = notion.EqualsFilter(name, prop.Type, status)
	}
	return s.client.QueryPages(ctx, s.profile.DataSourceID, filter)
}
```

- [ ] **Step 4: Verifica che passino**

Run: `go test ./internal/service/ -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/
git commit -m "feat(service): orchestrate upsert, set, get and list"
```

---

## Task 15: Service — `doctor`

**Files:**
- Create: `internal/service/doctor.go`, `internal/service/doctor_test.go`

**Interfaces:**
- Produces:
  - `type Check struct { Name, Status, Detail string }` — `Status` è `"ok"`, `"warn"` o `"fail"`
  - `func (s *Service) Doctor(ctx context.Context) []Check`

- [ ] **Step 1: Scrivi i test**

`internal/service/doctor_test.go`:

```go
package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

func findCheck(checks []Check, name string) (Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

func TestDoctorReportsAHealthySetup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track","type":"bot"}`))
		case "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	}))
	defer srv.Close()

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()).Doctor(context.Background())
	for _, c := range checks {
		if c.Status == "fail" {
			t.Errorf("check %q failed: %s", c.Name, c.Detail)
		}
	}
}

func TestDoctorReportsAnInvalidToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":"unauthorized","message":"API token is invalid."}`))
	}))
	defer srv.Close()

	checks := New(notion.New("bad", notion.WithBaseURL(srv.URL)), testProfile()).Doctor(context.Background())
	c, ok := findCheck(checks, "token")
	if !ok || c.Status != "fail" {
		t.Fatalf("token check = %+v", c)
	}
}

func TestDoctorSpotsARenamedProperty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			// "Stato" is gone; "Status" took its place.
			w.Write([]byte(`{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{
				"Name":{"name":"Name","type":"title","title":{}},
				"Ticket":{"name":"Ticket","type":"rich_text","rich_text":{}},
				"Status":{"name":"Status","type":"status","status":{"options":[{"name":"Fatto"}]}}
			}}`))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	}))
	defer srv.Close()

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()).Doctor(context.Background())
	c, ok := findCheck(checks, "properties")
	if !ok || c.Status != "fail" {
		t.Fatalf("properties check = %+v", c)
	}
	// The whole value of this check is naming the likely replacement.
	if !strings.Contains(c.Detail, "Stato") || !strings.Contains(c.Detail, "Status") {
		t.Errorf("detail does not point at the rename: %s", c.Detail)
	}
}

func TestDoctorListsDuplicateTickets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		default:
			w.Write([]byte(`{"results":[` + rowJSON + `,` + rowJSON + `],"has_more":false}`))
		}
	}))
	defer srv.Close()

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()).Doctor(context.Background())
	c, ok := findCheck(checks, "duplicates")
	if !ok || c.Status != "fail" {
		t.Fatalf("duplicates check = %+v", c)
	}
	if !strings.Contains(c.Detail, "BDF-231") || !strings.Contains(c.Detail, "https://notion.so/page1") {
		t.Errorf("detail does not identify the duplicates: %s", c.Detail)
	}
}
```

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/service/ -run TestDoctor -v`
Expected: FAIL, `undefined: Check`

- [ ] **Step 3: Implementa `internal/service/doctor.go`**

```go
package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

// Check is one diagnostic result. Status is "ok", "warn" or "fail".
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Doctor verifies token, access, schema drift and duplicate tickets.
//
// It never stops at the first failure: when something is broken the user wants
// the whole picture, not one symptom at a time.
func (s *Service) Doctor(ctx context.Context) []Check {
	var checks []Check

	name, err := s.client.Me(ctx)
	if err != nil {
		return append(checks, Check{"token", "fail", fmt.Sprintf(
			"%v\n  fix: check NOTION_TOKEN, or create a new internal integration token in Notion", err)})
	}
	checks = append(checks, Check{"token", "ok", "authenticated as " + name})

	schema, err := s.Schema(ctx)
	if err != nil {
		return append(checks, Check{"data_source", "fail", fmt.Sprintf(
			"cannot read data source %s: %v\n"+
				"  fix: open the database in Notion → ••• → Connections → add your integration",
			s.profile.DataSourceID, err)})
	}
	checks = append(checks, Check{"data_source", "ok", "reachable: " + schema.Title})

	checks = append(checks, s.checkProperties(schema))
	checks = append(checks, s.checkDuplicates(ctx))
	return checks
}

// checkProperties compares the configured mapping against the live schema and,
// when a property is gone, names the most likely replacement.
func (s *Service) checkProperties(schema *notion.Schema) Check {
	mapped := map[string]string{
		"ticket": s.profile.Properties.Ticket,
		"status": s.profile.Properties.Status,
		"title":  s.profile.Properties.Title,
		"due":    s.profile.Properties.Due,
	}
	wantType := map[string][]string{
		"ticket": {"rich_text", "title"},
		"status": {"status", "select"},
		"title":  {"title"},
		"due":    {"date"},
	}

	var problems []string
	roles := []string{"ticket", "status", "title", "due"}
	for _, role := range roles {
		name := mapped[role]
		if name == "" {
			continue
		}
		prop, ok := schema.Properties[name]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"%s property %q no longer exists%s", role, name, suggest(schema, wantType[role])))
			continue
		}
		if !containsString(wantType[role], prop.Type) {
			problems = append(problems, fmt.Sprintf(
				"%s property %q has type %q, expected one of %s",
				role, name, prop.Type, strings.Join(wantType[role], ", ")))
		}
	}

	// status_type records what the status property was when init ran. A change
	// here is not fatal but it flips the safety model: a select silently
	// creates unknown options, a status rejects them.
	if want := s.profile.StatusType; want != "" {
		if got, ok := schema.Properties[s.profile.Properties.Status]; ok && got.Type != want {
			problems = append(problems, fmt.Sprintf(
				"status property changed type from %q to %q since init; "+
					"re-run 'notion-track init' to record it", want, got.Type))
		}
	}

	if len(problems) == 0 {
		return Check{"properties", "ok", "all mapped properties exist with the expected types"}
	}
	return Check{"properties", "fail", strings.Join(problems, "\n  ") +
		"\n  fix: run 'notion-track init' to remap, or rename the property back in Notion"}
}

// suggest names the columns that could stand in for a missing property.
func suggest(schema *notion.Schema, types []string) string {
	var candidates []string
	for name, p := range schema.Properties {
		if containsString(types, p.Type) {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Strings(candidates)
	return "; candidates of the right type: " + strings.Join(candidates, ", ")
}

// checkDuplicates scans every row for repeated ticket keys. Duplicates make
// upsert fail permanently, so this is the command that tells you which rows to
// go delete.
func (s *Service) checkDuplicates(ctx context.Context) Check {
	pages, err := s.client.QueryPages(ctx, s.profile.DataSourceID, nil)
	if err != nil {
		return Check{"duplicates", "warn", fmt.Sprintf("could not scan rows: %v", err)}
	}

	byTicket := map[string][]notion.Page{}
	prop := s.profile.Properties.Ticket
	for _, p := range pages {
		if key := p.Properties[prop].Text; key != "" {
			byTicket[key] = append(byTicket[key], p)
		}
	}

	var keys []string
	for key, rows := range byTicket {
		if len(rows) > 1 {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return Check{"duplicates", "ok", fmt.Sprintf("%d rows, no repeated ticket keys", len(pages))}
	}

	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "%d ticket keys appear more than once:", len(keys))
	for _, key := range keys {
		fmt.Fprintf(&b, "\n  %s", key)
		for _, p := range byTicket[key] {
			fmt.Fprintf(&b, "\n    %s", p.URL)
		}
	}
	b.WriteString("\n  fix: delete the extra rows in Notion; upsert refuses to guess between them")
	return Check{"duplicates", "fail", b.String()}
}

func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Verifica che passino**

Run: `go test ./internal/service/ -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/
git commit -m "feat(service): add doctor checks for token, schema drift and duplicates"
```

---

## Task 16: Wiring dei comandi e output

**Files:**
- Create: `internal/cli/output.go`, `internal/cli/context.go`, `internal/cli/output_test.go`
- Modify: `internal/cli/cli.go`

**Interfaces:**
- Produces:
  - `func printJSON(w io.Writer, v any) error`
  - `func exitCodeFor(err error) int`
  - `func buildService(cmd *cobra.Command) (*service.Service, error)`
  - `var loadConfig = config.Load` e `var loadConfigFrom = config.LoadFrom` (seam per i test)

- [ ] **Step 1: Scrivi i test**

`internal/cli/output_test.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

func TestPrintJSONUsesSnakeCaseKeys(t *testing.T) {
	var buf bytes.Buffer
	if err := printJSON(&buf, map[string]string{"page_id": "page1"}); err != nil {
		t.Fatalf("printJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %s", buf.String())
	}
	if got["page_id"] != "page1" {
		t.Fatalf("got %v", got)
	}
}

func TestExitCodeForMapsDomainErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitOK},
		{"not found", fmt.Errorf("wrapped: %w", service.ErrNotFound), ExitNotFound},
		{"duplicates", &tracker.DuplicateError{Ticket: "X"}, ExitDuplicate},
		{"rejected value", &tracker.ValidationError{Field: "status", Value: "X"}, ExitUsage},
		{"unauthorized", fmt.Errorf("wrapped: %w", notion.ErrUnauthorized), ExitAuth},
		{"anything else", errors.New("boom"), ExitError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCodeFor(tc.err); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/cli/ -run "TestPrintJSON|TestExitCodeFor" -v`
Expected: FAIL, `undefined: printJSON`

- [ ] **Step 3: Implementa `internal/cli/output.go`**

```go
package cli

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
)

// printJSON writes v as indented JSON. The shape of what callers pass in is a
// documented, stable scripting contract: changing a key is a breaking change.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// exitCodeFor maps an error onto the process exit code, so that pipelines can
// tell "not found" from "token expired" without parsing messages.
func exitCodeFor(err error) int {
	if err == nil {
		return ExitOK
	}
	var (
		dup     *tracker.DuplicateError
		invalid *tracker.ValidationError
	)
	switch {
	case errors.As(err, &dup):
		return ExitDuplicate
	case errors.As(err, &invalid):
		return ExitUsage
	case errors.Is(err, service.ErrNotFound), errors.Is(err, notion.ErrNotFound):
		return ExitNotFound
	case errors.Is(err, notion.ErrUnauthorized):
		return ExitAuth
	}
	var coded *codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ExitError
}
```

- [ ] **Step 4: Implementa `internal/cli/context.go`**

```go
package cli

import (
	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/marcoarnulfo/notion-cli/internal/service"
	"github.com/spf13/cobra"
)

// Package-level seams. Tests replace them instead of touching the filesystem.
var (
	loadConfig     = config.Load
	loadConfigFrom = config.LoadFrom
	newClient      = func(token string) *notion.Client { return notion.New(token) }
)

// buildService resolves token, config and profile into a ready Service.
func buildService(cmd *cobra.Command) (*service.Service, error) {
	token, _ := config.Token()
	if token == "" {
		return nil, Errorf(ExitAuth,
			"no integration token found\n"+
				"  set %s, or run 'notion-track init'\n"+
				"  a workspace owner creates the token at https://www.notion.so/my-integrations",
			config.TokenEnv)
	}

	path, _ := cmd.Flags().GetString("config")
	var (
		cfg *config.Config
		err error
	)
	if path != "" {
		cfg, err = loadConfigFrom(path)
	} else {
		cfg, err = loadConfig()
	}
	if err != nil {
		return nil, err
	}

	profileName, _ := cmd.Flags().GetString("profile")
	profile, err := cfg.Resolve(profileName)
	if err != nil {
		return nil, Errorf(ExitUsage, "%v", err)
	}
	if profile.DataSourceID == "" {
		return nil, Errorf(ExitUsage,
			"profile has no data_source_id; run 'notion-track init' to configure it")
	}
	return service.New(newClient(token), profile), nil
}
```

- [ ] **Step 5: Aggiorna `executeArgs` in `internal/cli/cli.go`**

Sostituisci il blocco finale di `executeArgs` con:

```go
	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	return exitCodeFor(err)
```

e rimuovi l'import ora inutilizzato `"errors"` da `cli.go`, dato che `errors.As` si è spostato in `exitCodeFor`.

`newRootCmd` non va toccato: `Args: cobra.ArbitraryArgs`, `RunE` e `SetFlagErrorFunc` sono già a posto dal Task 1, ed è proprio quello che tiene `ExitUsage` per i comandi sconosciuti anche ora che esistono dei sottocomandi.

Un caso che `SetFlagErrorFunc` **non** copre: un flag obbligatorio mancante (`upsert` senza `--ticket`). Cobra valida i required flag dentro `execute()`, non nel parsing, quindi l'errore non passa da lì. Per rispettare la tabella degli exit code — "flag mancante" è uso errato, quindi 2 — aggiungi a `exitCodeFor`, prima del ramo finale:

```go
	// Cobra reports missing required flags with this exact prefix and no typed
	// error to match on.
	if strings.HasPrefix(err.Error(), `required flag(s) `) {
		return ExitUsage
	}
```

con `"strings"` fra gli import di `output.go`. È fragile per costruzione: il test qui sotto è ciò che ci accorge se cobra cambia il messaggio.

Aggiungi il test a `output_test.go`:

```go
func TestMissingRequiredFlagExitsUsage(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {})
	if code := executeArgs([]string{"upsert", "--config", cfg}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
}
```

Questo test va aggiunto **dopo** il Task 18, quando `upsert` esiste: annotalo e riprendilo lì.

- [ ] **Step 6: Verifica**

Run: `go test ./internal/cli/ -race -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/cli/
git commit -m "feat(cli): add JSON output, exit code mapping and service wiring"
```

---

## Task 17: Comandi `get` e `list`

**Files:**
- Create: `internal/cli/get.go`, `internal/cli/list.go`, `internal/cli/get_test.go`
- Modify: `internal/cli/cli.go` (registra i comandi)

**Interfaces:**
- Consumes: `buildService`, `printJSON`, `service.Service`
- Produces: `type pageJSON struct { ... }`, `func newGetCmd() *cobra.Command`, `func newListCmd() *cobra.Command`

- [ ] **Step 1: Scrivi il test end-to-end**

`internal/cli/get_test.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

// withStubbedAPI points the CLI at a fake Notion and a temp config file.
func withStubbedAPI(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	oldClient := newClient
	newClient = func(token string) *notion.Client {
		return notion.New(token, notion.WithBaseURL(srv.URL))
	}
	t.Cleanup(func() { newClient = oldClient })

	t.Setenv(config.TokenEnv, "ntn_test")

	path := filepath.Join(t.TempDir(), "config.yml")
	os.WriteFile(path, []byte(`schema_version: 1
default_profile: work
profiles:
  work:
    database_id: db1
    data_source_id: ds1
    status_type: status
    properties:
      ticket: Ticket
      status: Stato
      title: Name
`), 0o600)
	return path
}

const cliSchemaJSON = `{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{
	"Name":{"name":"Name","type":"title","title":{}},
	"Ticket":{"name":"Ticket","type":"rich_text","rich_text":{}},
	"Stato":{"name":"Stato","type":"status","status":{"options":[{"name":"Fatto"}]}}}}`

const cliRowJSON = `{"id":"page1","url":"https://notion.so/page1",
	"last_edited_time":"2026-07-20T10:00:00.000Z","properties":{
	"Name":{"type":"title","title":[{"plain_text":"Hardening"}]},
	"Ticket":{"type":"rich_text","rich_text":[{"plain_text":"BDF-231"}]},
	"Stato":{"type":"status","status":{"name":"Fatto"}}}}`

func TestGetJSONPrintsAStableSchema(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--json", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	for _, key := range []string{"ticket", "page_id", "url", "status", "title", "last_edited_time"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing key %q in %v", key, got)
		}
	}
}

func TestGetMissingTicketExitsNotFound(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})

	if code := executeArgs([]string{"get", "--ticket", "NOPE", "--config", cfg}); code != ExitNotFound {
		t.Fatalf("exit code = %d, want %d", code, ExitNotFound)
	}
}

func TestGetWithoutTokenExitsAuth(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {})
	t.Setenv(config.TokenEnv, "")

	if code := executeArgs([]string{"get", "--ticket", "X", "--config", cfg}); code != ExitAuth {
		t.Fatalf("exit code = %d, want %d", code, ExitAuth)
	}
}

// captureStdout redirects os.Stdout for the duration of f.
//
// The reader runs in its own goroutine: reading only after f returns would
// deadlock as soon as the output fills the pipe buffer, which a real
// `list --json` easily does. The restore is deferred so that a t.Fatalf inside
// f cannot leave os.Stdout pointing at a closed pipe for every later test.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	old := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()

	defer func() {
		os.Stdout = old
		w.Close()
	}()
	f()

	os.Stdout = old
	w.Close()
	return <-done
}
```

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/cli/ -run TestGet -v`
Expected: FAIL, comando `get` sconosciuto

- [ ] **Step 3: Implementa `internal/cli/get.go`**

```go
package cli

import (
	"time"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/spf13/cobra"
)

// pageJSON is the stable scripting shape of a row. Renaming a key here breaks
// every script and agent that consumes it: treat it as public API.
type pageJSON struct {
	Ticket         string `json:"ticket"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	PageID         string `json:"page_id"`
	URL            string `json:"url"`
	LastEditedTime string `json:"last_edited_time"`
}

func toPageJSON(p notion.Page, props config.Properties) pageJSON {
	return pageJSON{
		Ticket:         p.Properties[props.Ticket].Text,
		Title:          p.Properties[props.Title].Text,
		Status:         p.Properties[props.Status].Text,
		PageID:         p.ID,
		URL:            p.URL,
		LastEditedTime: p.LastEditedTime.Format(time.RFC3339),
	}
}

func newGetCmd() *cobra.Command {
	var ticket string
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "get",
		Short: "Read the row for a ticket",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			page, err := svc.Get(cmd.Context(), ticket)
			if err != nil {
				return err
			}
			profile := svc.Profile()
			if asJSON {
				// cmd.OutOrStdout(), never os.Stdout: it is what the root sets
				// and what tests can capture.
				return printJSON(cmd.OutOrStdout(), toPageJSON(page, profile.Properties))
			}
			cmd.Printf("%s  %s  [%s]\n  %s\n",
				page.Properties[profile.Properties.Ticket].Text,
				page.Properties[profile.Properties.Title].Text,
				page.Properties[profile.Properties.Status].Text,
				page.URL)
			return nil
		},
	}
	cmd.Flags().StringVar(&ticket, "ticket", "", "ticket key (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	cmd.MarkFlagRequired("ticket")
	return cmd
}
```

Aggiungi a `internal/service/service.go` l'accessore usato qui:

```go
// Profile exposes the profile this service was built for, so that callers can
// map property names back onto output fields.
func (s *Service) Profile() config.Profile { return s.profile }
```

- [ ] **Step 4: Implementa `internal/cli/list.go`**

```go
package cli

import (
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var status string
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List rows, optionally filtered by status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			pages, err := svc.List(cmd.Context(), status)
			if err != nil {
				return err
			}
			profile := svc.Profile()

			if asJSON {
				rows := make([]pageJSON, 0, len(pages))
				for _, p := range pages {
					rows = append(rows, toPageJSON(p, profile.Properties))
				}
				return printJSON(cmd.OutOrStdout(), rows)
			}
			for _, p := range pages {
				cmd.Printf("%-20s %-40s [%s]\n",
					p.Properties[profile.Properties.Ticket].Text,
					p.Properties[profile.Properties.Title].Text,
					p.Properties[profile.Properties.Status].Text)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status value")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	return cmd
}
```

- [ ] **Step 5: Registra i comandi in `newRootCmd`**

```go
	root.AddCommand(newGetCmd(), newListCmd())
```

- [ ] **Step 6: Verifica**

Run: `go test ./... -race`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/cli/ internal/service/
git commit -m "feat(cli): add get and list commands with stable JSON output"
```

---

## Task 18: Comandi `upsert` e `set`

**Files:**
- Create: `internal/cli/upsert.go`, `internal/cli/set.go`, `internal/cli/upsert_test.go`
- Modify: `internal/cli/cli.go`

**Interfaces:**
- Consumes: `buildService`, `tracker.Fields`, `toPageJSON`
- Produces: `func newUpsertCmd() *cobra.Command`, `func newSetCmd() *cobra.Command`

- [ ] **Step 1: Scrivi i test**

`internal/cli/upsert_test.go`:

```go
package cli

import (
	"net/http"
	"testing"
)

func TestUpsertCreatesAndIsQuietOnSuccess(t *testing.T) {
	var created bool
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[],"has_more":false}`))
		case "/v1/pages":
			created = true
			w.Write([]byte(cliRowJSON))
		default:
			w.Write([]byte(cliRowJSON))
		}
	})

	out := captureStdout(t, func() {
		code := executeArgs([]string{"upsert", "--ticket", "BDF-231", "--status", "Fatto", "--config", cfg})
		if code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	if !created {
		t.Fatal("no page was created")
	}
	// Quiet on success: CI logs stay readable, --json is the opt-in.
	if out != "" {
		t.Fatalf("expected no output, got %q", out)
	}
}

func TestUpsertRejectsAnUnknownStatusBeforeCallingTheAPI(t *testing.T) {
	var wrote bool
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1/pages" {
			wrote = true
		}
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})

	// A rejected value is invalid usage, not a generic failure.
	if code := executeArgs([]string{"upsert", "--ticket", "X", "--status", "Fattto", "--config", cfg}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if wrote {
		t.Fatal("a bogus status reached the API; a select property would have created it")
	}
}

func TestUpsertExitsDuplicateOnSeveralMatches(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `,` + cliRowJSON + `],"has_more":false}`))
	})

	if code := executeArgs([]string{"upsert", "--ticket", "BDF-231", "--config", cfg}); code != ExitDuplicate {
		t.Fatalf("exit code = %d, want %d", code, ExitDuplicate)
	}
}

func TestSetExitsNotFoundInsteadOfCreating(t *testing.T) {
	var created bool
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case "/v1/pages":
			created = true
			w.Write([]byte(cliRowJSON))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	})

	if code := executeArgs([]string{"set", "--ticket", "NOPE", "--status", "Fatto", "--config", cfg}); code != ExitNotFound {
		t.Fatalf("exit code = %d, want %d", code, ExitNotFound)
	}
	if created {
		t.Fatal("set created a row; only upsert may do that")
	}
}
```

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/cli/ -run "TestUpsert|TestSet" -v`
Expected: FAIL, comando `upsert` sconosciuto

- [ ] **Step 3: Implementa `internal/cli/upsert.go`**

```go
package cli

import (
	"github.com/marcoarnulfo/notion-cli/internal/tracker"
	"github.com/spf13/cobra"
)

// writeFlags are the fields upsert and set share.
type writeFlags struct {
	ticket string
	title  string
	status string
	due    string
	asJSON bool
}

func (wf *writeFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&wf.ticket, "ticket", "", "ticket key (required)")
	cmd.Flags().StringVar(&wf.title, "title", "", "title to set")
	cmd.Flags().StringVar(&wf.status, "status", "", "status to set")
	cmd.Flags().StringVar(&wf.due, "due", "", "due date, YYYY-MM-DD")
	cmd.Flags().BoolVar(&wf.asJSON, "json", false, "print machine-readable JSON")
	cmd.MarkFlagRequired("ticket")
}

func (wf *writeFlags) fields() tracker.Fields {
	return tracker.Fields{Ticket: wf.ticket, Title: wf.title, Status: wf.status, Due: wf.due}
}

func newUpsertCmd() *cobra.Command {
	var wf writeFlags
	cmd := &cobra.Command{
		Use:   "upsert",
		Short: "Create the row for a ticket, or update it if it already exists",
		Long: "Create the row for a ticket, or update it if it already exists.\n\n" +
			"Running it twice yields one row, which is what makes it safe in a retried CI job.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			res, err := svc.Upsert(cmd.Context(), wf.fields())
			if err != nil {
				return err
			}
			if wf.asJSON {
				out := toPageJSON(res.Page, svc.Profile().Properties)
				return printJSON(cmd.OutOrStdout(), map[string]any{"action": res.Action, "page": out})
			}
			return nil // quiet on success
		},
	}
	wf.bind(cmd)
	return cmd
}
```

- [ ] **Step 4: Implementa `internal/cli/set.go`**

```go
package cli

import (
	"github.com/spf13/cobra"
)

func newSetCmd() *cobra.Command {
	var wf writeFlags
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Update an existing row; fail if the ticket does not exist",
		Long: "Update an existing row; fail if the ticket does not exist.\n\n" +
			"Use this when a missing ticket is a symptom worth surfacing rather than\n" +
			"a row to create.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			res, err := svc.Set(cmd.Context(), wf.fields())
			if err != nil {
				return err
			}
			if wf.asJSON {
				out := toPageJSON(res.Page, svc.Profile().Properties)
				return printJSON(cmd.OutOrStdout(), map[string]any{"action": res.Action, "page": out})
			}
			return nil
		},
	}
	wf.bind(cmd)
	return cmd
}
```

- [ ] **Step 5: Registra i comandi**

In `newRootCmd`: `root.AddCommand(newGetCmd(), newListCmd(), newUpsertCmd(), newSetCmd())`

- [ ] **Step 6: Verifica**

Run: `go test ./... -race`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/cli/
git commit -m "feat(cli): add upsert and set commands"
```

---

## Task 19: Comandi `doctor` e `init --headless`

Il wizard TUI di `init` arriva col piano 3. Qui si consegna la forma a flag, che è quella che serve a CI e agenti, più `doctor`.

**Files:**
- Create: `internal/cli/doctor.go`, `internal/cli/init.go`, `internal/cli/doctor_test.go`
- Modify: `internal/cli/cli.go`

**Interfaces:**
- Produces: `func newDoctorCmd() *cobra.Command`, `func newInitCmd() *cobra.Command`

- [ ] **Step 1: Scrivi i test**

`internal/cli/doctor_test.go`:

```go
package cli

import (
	"net/http"
	"strings"
	"testing"
)

func TestDoctorReportsEveryCheck(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track"}`))
		case "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	})

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"doctor", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	for _, want := range []string{"token", "data_source", "properties", "duplicates"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing the %q check: %s", want, out)
		}
	}
}

func TestDoctorExitsNonZeroWhenACheckFails(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"code":"unauthorized","message":"API token is invalid."}`))
	})

	captureStdout(t, func() {
		if code := executeArgs([]string{"doctor", "--config", cfg}); code == ExitOK {
			t.Fatal("doctor exited 0 despite a failing check")
		}
	})
}

func TestInitHeadlessWritesTheProfile(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})

	code := executeArgs([]string{
		"init", "--data-source-id", "ds1", "--database-id", "db1",
		"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
		"--profile", "work", "--config", cfg,
	})
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	loaded, err := loadConfigFrom(cfg)
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	p, err := loaded.Resolve("work")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.DataSourceID != "ds1" || p.Properties.Status != "Stato" || p.StatusType != "status" {
		t.Fatalf("profile = %+v", p)
	}
}

func TestInitListPrintsSharedDataSources(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[
			{"id":"ds1","title":[{"plain_text":"Tasks"}],"parent":{"database_id":"db1"}}
		],"has_more":false}`))
	})

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"init", "--list", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	if !strings.Contains(out, "ds1") || !strings.Contains(out, "Tasks") {
		t.Fatalf("output does not list the data source: %q", out)
	}
}

// An empty list is the single most likely first-run failure, so the message
// has to name the fix rather than just report emptiness.
func TestInitListExplainsAnEmptyResult(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[],"has_more":false}`))
	})
	captureStdout(t, func() {
		if code := executeArgs([]string{"init", "--list", "--config", cfg}); code == ExitOK {
			t.Fatal("an empty list should not exit 0")
		}
	})
}

// init must refuse a mapping the data source does not support, instead of
// writing a config that fails on first use.
func TestInitHeadlessRejectsAnInvalidMapping(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})

	code := executeArgs([]string{
		"init", "--data-source-id", "ds1",
		"--ticket-prop", "Nonexistent", "--status-prop", "Stato", "--title-prop", "Name",
		"--config", cfg,
	})
	if code == ExitOK {
		t.Fatal("init accepted a property that does not exist")
	}
}
```

- [ ] **Step 2: Verifica che falliscano**

Run: `go test ./internal/cli/ -run "TestDoctor|TestInit" -v`
Expected: FAIL, comando `doctor` sconosciuto

- [ ] **Step 3: Implementa `internal/cli/doctor.go`**

```go
package cli

import (
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check token, data source access, property mapping and duplicates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			checks := svc.Doctor(cmd.Context())

			if asJSON {
				if err := printJSON(cmd.OutOrStdout(), checks); err != nil {
					return err
				}
			} else {
				for _, c := range checks {
					symbol := map[string]string{"ok": "✓", "warn": "!", "fail": "✗"}[c.Status]
					cmd.Printf("%s %-14s %s\n", symbol, c.Name, c.Detail)
				}
			}

			for _, c := range checks {
				if c.Status == "fail" {
					return Errorf(ExitError, "one or more checks failed")
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable JSON")
	return cmd
}
```

Nota: `doctor` stampa i suoi risultati **e poi** ritorna l'errore, così l'utente vede tutti i check anche quando uno fallisce. Il messaggio `error: one or more checks failed` finisce su stderr, i check su stdout.

- [ ] **Step 4: Implementa `internal/cli/init.go`**

```go
package cli

import (
	"fmt"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
	"github.com/spf13/cobra"
)

// newInitCmd writes a profile from flags. The interactive TUI wizard is added
// separately; this form is what CI and agents use.
func newInitCmd() *cobra.Command {
	var (
		databaseID   string
		dataSourceID string
		ticketProp   string
		statusProp   string
		titleProp    string
		dueProp      string
		list         bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Configure a profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token, _ := config.Token()
			if token == "" {
				return Errorf(ExitAuth, "no integration token found; set %s", config.TokenEnv)
			}
			client := newClient(token)

			// --list is how a user discovers the id that --data-source-id wants.
			if list {
				refs, err := client.ListDataSources(cmd.Context())
				if err != nil {
					return err
				}
				if len(refs) == 0 {
					return Errorf(ExitError,
						"no data sources are shared with this integration\n"+
							"  fix: a workspace owner must open the database in Notion →\n"+
							"       ••• → Connections → add the integration, then retry")
				}
				for _, r := range refs {
					cmd.Printf("%s\t%s\n", r.ID, r.Title)
				}
				return nil
			}

			if dataSourceID == "" {
				return Errorf(ExitUsage,
					"--data-source-id is required\n"+
						"  run 'notion-track init --list' to see the data sources shared with your integration")
			}

			// Validate the mapping against the live schema before writing a
			// config that would fail on first use.
			schema, err := client.GetSchema(cmd.Context(), dataSourceID)
			if err != nil {
				return err
			}
			statusType, err := validateMapping(schema, ticketProp, statusProp, titleProp, dueProp)
			if err != nil {
				return Errorf(ExitUsage, "%v", err)
			}

			path, _ := cmd.Flags().GetString("config")
			cfg, err := loadExistingOrNew(path)
			if err != nil {
				return err
			}

			name, _ := cmd.Flags().GetString("profile")
			if name == "" {
				name = "default"
			}
			cfg.Profiles[name] = config.Profile{
				DatabaseID:   databaseID,
				DataSourceID: dataSourceID,
				StatusType:   statusType,
				Properties: config.Properties{
					Ticket: ticketProp, Status: statusProp, Title: titleProp, Due: dueProp,
				},
			}
			if cfg.DefaultProfile == "" {
				cfg.DefaultProfile = name
			}

			if err := saveConfigTo(cfg, path); err != nil {
				return err
			}
			cmd.Printf("profile %q configured for data source %q\n", name, schema.Title)
			return nil
		},
	}

	cmd.Flags().StringVar(&databaseID, "database-id", "", "database id")
	cmd.Flags().StringVar(&dataSourceID, "data-source-id", "", "data source id (required)")
	cmd.Flags().StringVar(&ticketProp, "ticket-prop", "", "property holding the ticket key")
	cmd.Flags().StringVar(&statusProp, "status-prop", "", "property holding the status")
	cmd.Flags().StringVar(&titleProp, "title-prop", "", "title property")
	cmd.Flags().StringVar(&dueProp, "due-prop", "", "date property (optional)")
	cmd.Flags().BoolVar(&list, "list", false, "list the data sources shared with the integration and exit")
	return cmd
}

// validateMapping checks each mapped property against the schema and returns
// the status property's actual type.
func validateMapping(schema *notion.Schema, ticket, status, title, due string) (string, error) {
	check := func(role, name string, want ...string) (string, error) {
		if name == "" {
			return "", nil
		}
		p, ok := schema.Properties[name]
		if !ok {
			return "", fmt.Errorf("%s property %q does not exist in this data source", role, name)
		}
		for _, t := range want {
			if p.Type == t {
				return p.Type, nil
			}
		}
		return "", fmt.Errorf("%s property %q has type %q, which is not usable as %s",
			role, name, p.Type, role)
	}

	if _, err := check("ticket", ticket, "rich_text", "title"); err != nil {
		return "", err
	}
	if _, err := check("title", title, "title"); err != nil {
		return "", err
	}
	if _, err := check("due", due, "date"); err != nil {
		return "", err
	}
	statusType, err := check("status", status, "status", "select")
	if err != nil {
		return "", err
	}
	return statusType, nil
}
```

Aggiungi in `internal/cli/context.go` i due helper usati sopra:

```go
// loadExistingOrNew returns the config at path, or an empty one if absent.
func loadExistingOrNew(path string) (*config.Config, error) {
	var (
		cfg *config.Config
		err error
	)
	if path != "" {
		cfg, err = loadConfigFrom(path)
	} else {
		cfg, err = loadConfig()
	}
	if errors.Is(err, config.ErrNotConfigured) {
		return &config.Config{
			SchemaVersion: config.CurrentSchemaVersion,
			Profiles:      map[string]config.Profile{},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]config.Profile{}
	}
	return cfg, nil
}

// saveConfigTo writes to an explicit path when given, otherwise the default.
func saveConfigTo(cfg *config.Config, path string) error {
	if path == "" {
		return cfg.Save()
	}
	return cfg.SaveTo(path)
}
```

E in `internal/config/config.go` estrai `SaveTo`, rendendo `Save` un wrapper:

```go
// SaveTo writes the config to an explicit path.
func (c *Config) SaveTo(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: creating config dir: %w", err)
	}
	c.SchemaVersion = CurrentSchemaVersion
	raw, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("config: encoding: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("config: writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("config: replacing %s: %w", path, err)
	}
	return nil
}

// Save writes the config to its default location.
func (c *Config) Save() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	return c.SaveTo(path)
}
```

Aggiungi `"errors"` agli import di `context.go`.

- [ ] **Step 5: Registra i comandi**

```go
	root.AddCommand(newGetCmd(), newListCmd(), newUpsertCmd(), newSetCmd(),
		newDoctorCmd(), newInitCmd())
```

- [ ] **Step 6: Verifica**

Run: `go test ./... -race && go vet ./... && gofmt -l .`
Expected: PASS, nessun file da riformattare

- [ ] **Step 7: Commit**

```bash
git add internal/cli/ internal/config/
git commit -m "feat(cli): add doctor and headless init commands"
```

---

## Task 20: Documentazione e impianto open source

**Files:**
- Create: `README.md`, `README.it.md`, `CONTRIBUTING.md`, `CONTRIBUTING.it.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `.github/ISSUE_TEMPLATE/{bug_report.md,feature_request.md,config.yml}`, `.github/pull_request_template.md`

- [ ] **Step 1: Scrivi `README.md` (inglese, primario)**

Prima riga: `**English** · [Italiano](README.it.md)`. Badge: CI, latest release, Go version, MIT, PRs welcome.

Sezioni, nell'ordine: titolo e tagline · Features · Requirements · Installation · Quick start (crea l'integrazione, condividi il database, `init`, primo `upsert`) · Usage (un blocco per comando, con i flag) · Configuration (percorso per OS, YAML commentato campo per campo, tabella delle env var, precedenza flag → env → config) · JSON output (schema dichiarato stabile) · Exit codes (la tabella del Task 16) · CI usage (esempio GitHub Actions) · Limitations · Contributing · Roadmap · License.

La sezione **Limitations** deve dichiarare, senza attenuanti:

1. Ogni modifica risulta fatta dal bot dell'integrazione: `last_edited_by` non identifica la persona.
2. `upsert` fallisce sui ticket duplicati anziché sceglierne uno; usa `doctor` per trovarli.
3. Due job concorrenti sullo stesso ticket nuovo possono creare un duplicato: l'API Notion non offre vincoli di unicità.
4. Solo un Workspace Owner può creare l'integrazione e condividere un database con essa.
5. Il body Markdown arriva nel piano 2; la TUI nel piano 3.

- [ ] **Step 2: Scrivi `README.it.md`**

Traduzione integrale, prima riga `[English](README.md) · **Italiano**`. Stessa struttura sezione per sezione.

- [ ] **Step 3: Scrivi `SECURITY.md`**

Come segnalare una vulnerabilità; il fatto che il token è un segreto condiviso; **rotazione**: revocare l'integrazione da <https://www.notion.so/my-integrations>, crearne una nuova, aggiornare i secret CI e le shell locali; blast radius limitato ai soli database condivisi con l'integrazione.

- [ ] **Step 4: Scrivi `CONTRIBUTING.md` e `CONTRIBUTING.it.md`**

Sezioni: Ways to contribute · Prerequisites (Go 1.26) · Development setup · Before opening a PR (gli stessi comandi della CI: `gofmt -l .`, `go vet ./...`, `staticcheck`, `go test ./... -race`) · Project layout & conventions, con le due regole architetturali non negoziabili:

> `internal/tracker` and `internal/markdown` must stay pure: no I/O, no imports of `internal/notion` or `internal/config`. Domain logic goes there so it can be tested without mocks.

> Only stdlib in tests. No testify, no gomock. Fake the API with `httptest.Server`.

Più: Conventional Commits con scope, **no `Co-Authored-By`**, e la regola che ogni cambiamento di comportamento aggiorna **entrambi** i README.

- [ ] **Step 5: Scrivi `CODE_OF_CONDUCT.md`**

Contributor Covenant standard.

- [ ] **Step 6: Scrivi i template GitHub**

Issue template bilingui (`## 🇬🇧 English`, `---`, `## 🇮🇹 Italiano`) per bug e feature; `config.yml` con `blank_issues_enabled: true`. PR template con checklist: test `-race` verdi, `gofmt`/`vet`/`staticcheck` puliti, entrambi i README aggiornati, Conventional Commits, nessun `Co-Authored-By`.

- [ ] **Step 7: Verifica finale**

Run: `gofmt -l . && go vet ./... && go test ./... -race && go build ./...`
Expected: tutto verde

Prova manuale contro Notion reale, con un'integrazione di test e un database di prova:

```bash
export NOTION_TOKEN=ntn_...
go run ./cmd/notion-track init --data-source-id <id> --ticket-prop Ticket --status-prop Stato --title-prop Name
go run ./cmd/notion-track doctor
go run ./cmd/notion-track upsert --ticket TEST-1 --title "Hello" --status "In corso"
go run ./cmd/notion-track upsert --ticket TEST-1 --status "Fatto"   # deve aggiornare, non creare
go run ./cmd/notion-track get --ticket TEST-1 --json
go run ./cmd/notion-track list --status "Fatto"
```

Verifica che il secondo `upsert` **non** abbia creato una seconda riga: è la prova dell'idempotenza, il cuore del tool.

- [ ] **Step 8: Commit**

```bash
git add README.md README.it.md CONTRIBUTING.md CONTRIBUTING.it.md CODE_OF_CONDUCT.md SECURITY.md .github/
git commit -m "docs: add bilingual README, contributing guide and issue templates"
```

---

## Self-review del piano

**Copertura dello spec.** §1 obiettivo → Task 14. §2 versione API e data source → Task 2, e i path aggiornati nei Task 5-8. §3 architettura → mappa dei file, un task per pacchetto. §4 config e profili → Task 9. §5 discovery → Task 5, 6, 13, 19. §6 select contro status → Task 11, 12. §7 comandi → Task 17-19. §8 output ed exit code → Task 16. §9 rate limit → Task 4. §11 testing → ogni task. §14 impianto OSS → Task 1 e 20.

**Fuori da questo piano, per scelta dichiarata:** §10 body Markdown (piano 2), TUI di browsing e wizard TUI di `init` (piano 3), §12 v0.2+ (GoReleaser, GitHub Action, MCP), §13 distribuzione.

**Requisiti dello spec deliberatamente rinviati, da non scambiare per dimenticanze:**

- **Token letto da file di config** (spec §4). Qui il token viene **solo** da `NOTION_TOKEN`. Il fallback su file ha senso insieme al wizard TUI, che è l'unico posto in cui l'utente potrebbe volerlo salvare; arriva col piano 3, e lo spec va emendato di conseguenza. Il flag di provenienza di `Token()` esiste già ed è ciò che impedirà, allora, di riscrivere su disco un token letto dall'ambiente.
- **Check di `doctor` sui token nei file tracciati** (spec §7, punto 5). Richiede di interrogare `git ls-files` e di riconoscere il pattern `ntn_...`: è un check utile ma indipendente dal resto, e va aggiunto quando il repo ha una storia da scandire.

**Codice prodotto qui ma consumato altrove, dichiarato apertamente:** `tracker.GuessMapping` (Task 13) non è usato da nessun comando di questo piano — serve al wizard TUI del piano 3, che proporrà il mapping da confermare. È stato messo qui perché è dominio puro e appartiene al pacchetto `tracker`, non perché serva ora. `notion.ListDataSources` (Task 5) invece è consumato da `init --list` nel Task 19.

**Coerenza dei tipi.** `notion.Page`, `notion.Schema`, `notion.Property`, `notion.PropertyValue` sono definiti nei Task 5-7 e usati con gli stessi nomi nei Task 10-19. `tracker.Fields` è definito nel Task 12 e consumato nei Task 14, 18. `config.Properties` è definito nel Task 9 e usato nei Task 12, 13, 17. `service.Result` nel Task 14, `service.Check` nel Task 15. `Service.Profile()` è aggiunto nel Task 17, dove serve per la prima volta.

**Ordine delle dipendenze.** Ogni task usa solo ciò che i precedenti hanno prodotto. L'unica eccezione dichiarata è il Task 11 (validazione) che precede il Task 12 (payload) pur essendo logicamente parte dello stesso pacchetto: il payload la chiama, quindi deve esistere prima.

