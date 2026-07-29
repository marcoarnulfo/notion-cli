# notion-track — Ruolo `id` / indirizzamento `unique_id` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Aggiungere un settimo ruolo opzionale, `id`, mappato su una colonna Notion di tipo `unique_id`, così che una riga si possa indirizzare con `--id BDF-271` e il suo id compaia nell'output JSON.

**Architecture:** `unique_id` è un identificatore di sola lettura, quindi entra come *terzo modo di indirizzare* accanto a `--ticket` e `--page-id`, non come estensione della ticket-prop. Il codice di scrittura (`tracker.BuildProperties`) non conosce il ruolo, quindi non serve nessuna esclusione esplicita dal payload. La risoluzione id → page-id avviene nel service e delega poi ai percorsi di lettura/scrittura già esistenti.

**Tech Stack:** Go 1.26.5, cobra, `net/http/httptest`, bubbletea (TUI). Nessuna dipendenza nuova.

**Spec:** `docs/superpowers/specs/2026-07-29-notion-track-unique-id-design.md`

## Global Constraints

- **Nessuna dipendenza nuova.** Solo la standard library e ciò che è già in `go.mod`.
- **Lingua:** codice, commenti e messaggi d'errore in inglese. I design doc restano in italiano.
- **Ogni commit lascia l'albero verde.** Prima di ogni commit: `gofmt -l .` deve stampare nulla, poi `go vet ./...`, `go build ./...`, `go test ./... -race`. Un task non è finito se uno dei quattro fallisce.
- **`pageJSON` (`internal/cli/get.go`) e `mcp.Row` (`internal/mcp/server.go`) si convertono direttamente** con `mcp.Row(toPageJSON(...))`: compila solo finché le due struct sono identiche per **nome, tipo e ordine** dei campi. Vanno cambiate nello stesso commit (Task 11). Lo stesso vale per `mcp.Fields`↔`tracker.Fields` e `mcp.ListFilter`↔`service.ListFilter`, che questo piano non tocca.
- **`internal/tracker/payload.go` non deve mai menzionare `unique_id` né `Properties.ID`.** È il requisito §3 dello spec, ed è verificabile con `grep`.
- **Cifre solo ASCII.** Dove il piano dice "interamente cifre", si intende `c >= '0' && c <= '9'`, mai `unicode.IsDigit` — quest'ultimo accetta le cifre arabo-indiane, che `strconv.ParseInt` poi rifiuta.
- **Il ruolo `id` è opzionale ovunque**, come `due`, `assignee` e `priority`: non mapparlo non è un guasto.
- **Messaggi di commit:** conventional commits, in inglese. **Mai** aggiungere `Co-Authored-By`.

---

## Struttura dei file

| File | Responsabilità in questo lavoro |
|---|---|
| `internal/notion/types.go` | `Property.Prefix` |
| `internal/notion/datasource.go` | `GetSchema` decodifica `unique_id.prefix` |
| `internal/notion/query.go` | `decodePage` legge `unique_id`; `UniqueIDEqualsFilter` |
| `internal/tracker/uniqueid.go` | **nuovo** — `ParseUniqueID`, `InvalidIDError` |
| `internal/tracker/mapping.go` | `GuessMapping` propone il ruolo `id` |
| `internal/config/config.go` | `Properties.ID` |
| `internal/cli/init.go` | `--id-prop`, validazione del tipo |
| `internal/tui/wizard.go` | il ruolo `id` nel wizard |
| `internal/service/doctor.go` | `doctor` valida il ruolo `id` |
| `internal/service/service.go` | `findByUniqueID`, `GetByUniqueID`, `SetByUniqueID`, gli errori |
| `internal/cli/output.go` | `exitCodeFor` per i nuovi errori; `idPrefix` |
| `internal/cli/list.go` | i due formati di riga guadagnano il prefisso |
| `internal/cli/get.go` | `pageJSON.ID`, `toPageJSON`, flag `--id` |
| `internal/cli/upsert.go` | `writeFlags.id`, `bindWithPageID` a tre vie |
| `internal/cli/set.go` | il ramo `--id` |
| `internal/mcp/server.go` | `Row.ID` |
| `README.md`, `README.it.md`, `skills/notion-track/` | documentazione — **non** `.claude/skills/`, che contiene solo `settings.local.json` |

Ordine dei task: prima i tre strati che non dipendono da nulla (`internal/notion`, `internal/tracker`), poi la configurazione del ruolo, poi il service, infine la superficie. Ogni task compila e passa i test da solo.

---

### Task 1: Il prefisso `unique_id` nello schema

**Files:**
- Modify: `internal/notion/types.go:35-41`
- Modify: `internal/notion/datasource.go:13-60`
- Test: `internal/notion/datasource_test.go`

**Interfaces:**
- Consumes: niente
- Produces: `notion.Property.Prefix string` — vuoto per ogni tipo diverso da `unique_id`, e anche per una colonna `unique_id` configurata senza prefisso.

- [ ] **Step 1: Scrivi il test che fallisce**

In fondo a `internal/notion/datasource_test.go`:

```go
const uniqueIDSchemaFixture = `{
  "id": "ds1",
  "title": [{"plain_text": "Tasks"}],
  "properties": {
    "Name": {"id":"title","name":"Name","type":"title","title":{}},
    "ID":   {"id":"uid","name":"ID","type":"unique_id","unique_id":{"prefix":"BDF"}},
    "Seq":  {"id":"seq","name":"Seq","type":"unique_id","unique_id":{"prefix":null}}
  }
}`

func TestGetSchemaReadsTheUniqueIDPrefix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(uniqueIDSchemaFixture))
	}))
	defer srv.Close()

	got, err := New("t", WithBaseURL(srv.URL)).GetSchema(context.Background(), "ds1")
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}
	if p := got.Properties["ID"]; p.Type != "unique_id" || p.Prefix != "BDF" {
		t.Errorf("ID = %+v, want type unique_id with prefix BDF", p)
	}
	// A unique_id column configured without a prefix must read as "", not as
	// the string "null" and not as a panic on the nil pointer.
	if p := got.Properties["Seq"]; p.Type != "unique_id" || p.Prefix != "" {
		t.Errorf("Seq = %+v, want type unique_id with an empty prefix", p)
	}
	// Every other type keeps an empty prefix: nothing else may start filling
	// this field in.
	if p := got.Properties["Name"]; p.Prefix != "" {
		t.Errorf("Name.Prefix = %q, want empty", p.Prefix)
	}
}
```

- [ ] **Step 2: Esegui il test e verifica che fallisca**

Run: `go test ./internal/notion/ -run TestGetSchemaReadsTheUniqueIDPrefix -v`
Expected: FAIL alla compilazione, `p.Prefix undefined (type Property has no field or method Prefix)`.

- [ ] **Step 3: Aggiungi il campo**

In `internal/notion/types.go`, dentro `type Property struct`, dopo `Options []string`:

```go
	// Prefix is the string Notion prepends to a unique_id column's numbers
	// ("BDF" in "BDF-271"). Empty for every other property type, and also for
	// a unique_id column configured without one.
	Prefix string
```

- [ ] **Step 4: Decodifica il prefisso**

In `internal/notion/datasource.go`, dentro `type rawProperty struct`, dopo il campo `Status`:

```go
		UniqueID *struct {
			Prefix *string `json:"prefix"`
		} `json:"unique_id"`
```

Il puntatore su `Prefix` è deliberato: Notion manda `"prefix": null` per una colonna senza prefisso, e un `string` semplice lo leggerebbe come `""` — che qui è il risultato giusto, ma solo per caso. Il puntatore rende esplicito che i due casi sono distinti nella risposta.

Poi, nello `switch` che riempie `p`, aggiungi un terzo caso dopo `case raw.Status != nil:`:

```go
		case raw.UniqueID != nil:
			if raw.UniqueID.Prefix != nil {
				p.Prefix = *raw.UniqueID.Prefix
			}
```

- [ ] **Step 5: Esegui i test e verifica che passino**

Run: `go test ./internal/notion/ -run TestGetSchema -v`
Expected: PASS, compresi i test preesistenti su schema e opzioni.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -race
git add internal/notion/types.go internal/notion/datasource.go internal/notion/datasource_test.go
git commit -m "feat(notion): read a unique_id column's prefix from the schema"
```

---

### Task 2: Leggere il valore `unique_id` da una riga

**Files:**
- Modify: `internal/notion/query.go:104-163` (`decodePage`)
- Test: `internal/notion/query_test.go`

**Interfaces:**
- Consumes: niente
- Produces: `notion.PropertyValue.Text` popolato per le proprietà `unique_id`, nella forma `"BDF-271"` (o `"271"` senza prefisso). È ciò che fa comparire l'id in `toPageJSON` senza toccarlo.

- [ ] **Step 1: Scrivi il test che fallisce**

In fondo a `internal/notion/query_test.go`:

```go
const uniqueIDPageFixture = `{
  "id": "page1",
  "url": "https://notion.so/page1",
  "last_edited_time": "2026-07-20T10:00:00.000Z",
  "properties": {
    "ID":  {"type":"unique_id","unique_id":{"prefix":"BDF","number":271}},
    "Seq": {"type":"unique_id","unique_id":{"prefix":null,"number":8}}
  }
}`

func TestQueryPagesReadsUniqueIDInTheFormThePersonSees(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[` + uniqueIDPageFixture + `],"has_more":false}`))
	}))
	defer srv.Close()

	pages, err := New("t", WithBaseURL(srv.URL)).QueryPages(context.Background(), "ds1", nil)
	if err != nil {
		t.Fatalf("QueryPages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(pages))
	}
	// The exact string matters: this is what --json prints and what a person
	// reads off the board. A test that only checked "not empty" would pass on
	// "271", on "BDF271", and on the raw JSON.
	if got := pages[0].Properties["ID"].Text; got != "BDF-271" {
		t.Errorf("ID = %q, want %q", got, "BDF-271")
	}
	// Without a prefix there is no separator to invent: the number alone.
	if got := pages[0].Properties["Seq"].Text; got != "8" {
		t.Errorf("Seq = %q, want %q", got, "8")
	}
	if got := pages[0].Properties["ID"].Type; got != "unique_id" {
		t.Errorf("ID.Type = %q, want %q", got, "unique_id")
	}
}
```

- [ ] **Step 2: Esegui il test e verifica che fallisca**

Run: `go test ./internal/notion/ -run TestQueryPagesReadsUniqueID -v`
Expected: FAIL con `ID = "", want "BDF-271"` — `decodePage` non ha il ramo, quindi `Text` resta vuoto.

- [ ] **Step 3: Decodifica il valore**

In `internal/notion/query.go`, dentro la struct anonima `Properties` di `decodePage`, dopo `Checkbox bool`:

```go
			UniqueID *struct {
				Prefix *string `json:"prefix"`
				Number int64   `json:"number"`
			} `json:"unique_id"`
```

E nello `switch v.Type`, dopo `case "date":`:

```go
		case "unique_id":
			// Rendered the way the board renders it, so what the CLI prints and
			// what the person sees in Notion are the same string. A prefixless
			// column has no separator to invent.
			if v.UniqueID != nil {
				pv.Text = strconv.FormatInt(v.UniqueID.Number, 10)
				if v.UniqueID.Prefix != nil && *v.UniqueID.Prefix != "" {
					pv.Text = *v.UniqueID.Prefix + "-" + pv.Text
				}
			}
```

Aggiungi `"strconv"` agli import di `internal/notion/query.go`.

- [ ] **Step 4: Esegui i test e verifica che passino**

Run: `go test ./internal/notion/ -v`
Expected: PASS.

- [ ] **Step 5: Mutation test — verifica che il test morda**

Sostituisci temporaneamente il corpo del `case "unique_id"` con la sola riga del numero, senza il ramo del prefisso:

```go
		case "unique_id":
			if v.UniqueID != nil {
				pv.Text = strconv.FormatInt(v.UniqueID.Number, 10)
			}
```

Run: `go test ./internal/notion/ -run TestQueryPagesReadsUniqueID`
Expected: FAIL con `ID = "271", want "BDF-271"`. Se passa, il test non sta asserendo la stringa esatta ed è da riscrivere. Ripristina il corpo completo.

Non svuotare il `case`: `strconv` resterebbe importato e inutilizzato, il package non compilerebbe, e il FAIL osservato sarebbe un errore di build invece dell'assert che morde — cioè una conferma illusoria.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -race
git add internal/notion/query.go internal/notion/query_test.go
git commit -m "feat(notion): decode unique_id values as the board renders them"
```

---

### Task 3: Il filtro numerico `unique_id`

**Files:**
- Modify: `internal/notion/query.go:12-36` (accanto a `EqualsFilter` e `IsEmptyFilter`)
- Test: `internal/notion/query_test.go`

**Interfaces:**
- Consumes: niente
- Produces: `notion.UniqueIDEqualsFilter(property string, number int64) Filter`

- [ ] **Step 1: Scrivi il test che fallisce**

In fondo a `internal/notion/query_test.go`:

```go
func TestUniqueIDEqualsFilterCarriesANumber(t *testing.T) {
	got := UniqueIDEqualsFilter("ID", 271)
	want := Filter{"property": "ID", "unique_id": map[string]int64{"equals": 271}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UniqueIDEqualsFilter = %#v, want %#v", got, want)
	}
	// The whole reason this is a separate constructor is the wire format:
	// Notion rejects a quoted value here. DeepEqual above would still pass if
	// someone switched int64 to string in both the code and the want, so the
	// marshalled form is what actually pins the contract.
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if s := string(b); !strings.Contains(s, `"equals":271`) {
		t.Errorf("marshalled to %s, want an unquoted 271", s)
	}
}
```

Gli import `encoding/json`, `reflect` e `strings` sono già presenti in quel file.

- [ ] **Step 2: Esegui il test e verifica che fallisca**

Run: `go test ./internal/notion/ -run TestUniqueIDEqualsFilter -v`
Expected: FAIL alla compilazione, `undefined: UniqueIDEqualsFilter`.

- [ ] **Step 3: Scrivi la funzione**

In `internal/notion/query.go`, subito dopo `IsEmptyFilter`:

```go
// UniqueIDEqualsFilter matches the row carrying one unique_id number.
//
// Separate from EqualsFilter, rather than another case inside it, because
// unique_id is the one property notion-track matches on whose filter value is
// a number instead of a string. EqualsFilter's signature says "string" on
// purpose — its doc comment already warns that other types need a different
// operator or a non-string value — and widening it to carry both would break
// the promise that comment makes to every other caller.
//
// The number is the bare id: "BDF-271" is filtered as 271, with the prefix
// stripped by tracker.ParseUniqueID before it reaches here.
func UniqueIDEqualsFilter(property string, number int64) Filter {
	return Filter{
		"property":  property,
		"unique_id": map[string]int64{"equals": number},
	}
}
```

- [ ] **Step 4: Esegui i test e verifica che passino**

Run: `go test ./internal/notion/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -race
git add internal/notion/query.go internal/notion/query_test.go
git commit -m "feat(notion): add a numeric unique_id equality filter"
```

---

### Task 4: `ParseUniqueID` — il parsing, dominio puro

**Files:**
- Create: `internal/tracker/uniqueid.go`
- Test: `internal/tracker/uniqueid_test.go` (nuovo)

**Interfaces:**
- Consumes: niente
- Produces:
  - `tracker.ParseUniqueID(input, prefix string) (int64, error)`
  - `tracker.InvalidIDError` con campi `Value string` e `Reason string`, e `Error() string` che produce `invalid id %q: %s`

- [ ] **Step 1: Scrivi il test che fallisce**

Crea `internal/tracker/uniqueid_test.go`:

```go
package tracker

import (
	"errors"
	"strings"
	"testing"
)

func TestParseUniqueID(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		prefix string
		want   int64
		reason string // substring the error must contain; "" means success
	}{
		{"canonical form", "BDF-271", "BDF", 271, ""},
		{"lowercase prefix", "bdf-271", "BDF", 271, ""},
		{"bare number", "271", "BDF", 271, ""},
		{"surrounding whitespace", "  BDF-271  ", "BDF", 271, ""},
		{"bare number on a prefixless column", "271", "", 271, ""},
		{"another board's prefix", "ABC-271", "BDF", 0, `ids start with "BDF"`},
		{"prefix on a prefixless column", "BDF-271", "", 0, "have no prefix"},
		{"empty number half", "BDF-", "BDF", 0, "expected a number"},
		{"non-numeric number half", "BDF-abc", "BDF", 0, "expected a number"},
		{"empty prefix half", "-271", "BDF", 0, "expected a number"},
		{"zero", "0", "BDF", 0, "ids start at 1"},
		{"zero with prefix", "BDF-0", "BDF", 0, "ids start at 1"},
		{"empty input", "", "BDF", 0, "expected a number"},
		// Arabic-Indic digits: unicode.IsDigit accepts these, strconv does not.
		// The test exists to keep the digit check ASCII-only.
		{"non-ASCII digits", "٢٧١", "BDF", 0, "expected a number"},
		{"beyond int64", "99999999999999999999", "BDF", 0, "expected a number"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUniqueID(tt.input, tt.prefix)
			if tt.reason == "" {
				if err != nil {
					t.Fatalf("ParseUniqueID(%q, %q) = error %v, want %d", tt.input, tt.prefix, err, tt.want)
				}
				if got != tt.want {
					t.Errorf("ParseUniqueID(%q, %q) = %d, want %d", tt.input, tt.prefix, got, tt.want)
				}
				return
			}
			var invalid *InvalidIDError
			if !errors.As(err, &invalid) {
				t.Fatalf("ParseUniqueID(%q, %q) error = %v, want *InvalidIDError", tt.input, tt.prefix, err)
			}
			if !strings.Contains(invalid.Error(), tt.reason) {
				t.Errorf("error = %q, want it to mention %q", invalid.Error(), tt.reason)
			}
			// The message quotes what the user typed, not the trimmed or
			// half-parsed form: that is what they can compare against.
			if invalid.Value != tt.input {
				t.Errorf("InvalidIDError.Value = %q, want the raw input %q", invalid.Value, tt.input)
			}
		})
	}
}

func TestParseUniqueIDNamesTheBoardsPrefixInTheExample(t *testing.T) {
	_, err := ParseUniqueID("nope", "BDF")
	if err == nil {
		t.Fatal("ParseUniqueID(\"nope\", \"BDF\") = nil error, want a failure")
	}
	// The example is built from this board's prefix, so the fix is readable
	// without a trip to the docs.
	if !strings.Contains(err.Error(), `"BDF-1"`) {
		t.Errorf("error = %q, want it to show a BDF-shaped example", err.Error())
	}
}
```

- [ ] **Step 2: Esegui il test e verifica che fallisca**

Run: `go test ./internal/tracker/ -run TestParseUniqueID -v`
Expected: FAIL alla compilazione, `undefined: ParseUniqueID`.

- [ ] **Step 3: Scrivi l'implementazione**

Crea `internal/tracker/uniqueid.go`:

```go
package tracker

import (
	"fmt"
	"strconv"
	"strings"
)

// InvalidIDError marks an --id value that cannot be turned into the number
// Notion filters on.
//
// Callers map it onto the "invalid usage" exit code. It lives here because the
// parsing does, not because a caller outside the CLI produces it today: with
// manifests and MCP tool arguments out of scope, the CLI is the only path that
// can.
type InvalidIDError struct {
	// Value is what the user typed, verbatim: the message quotes it so they can
	// compare it against what they meant to type.
	Value string
	// Reason is the clause after the colon in Error(). It already carries the
	// column's prefix wherever the prefix is what went wrong, which is why
	// there is no separate Prefix field for a caller nobody has yet.
	Reason string
}

func (e *InvalidIDError) Error() string {
	return fmt.Sprintf("invalid id %q: %s", e.Value, e.Reason)
}

// isASCIIDigits reports whether s is one or more ASCII digits.
//
// Explicitly ASCII, never unicode.IsDigit: the latter accepts Arabic-Indic
// digits and dozens of other numeral systems that strconv.ParseInt then
// rejects, which would turn a precise "this is not a number" into a generic
// parse failure — for input a copy-paste can produce without anyone meaning to.
func isASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ParseUniqueID turns what the user typed into the number Notion filters on.
//
// Both "BDF-271" and "271" are accepted: the first is what the board shows, the
// second is what the API wants, and refusing either would be a rule to
// remember for no gain.
//
// prefix is the column's own prefix, read from the schema. It is what makes
// "ABC-271 belongs to another board" a message we can produce before any
// request is sent, rather than a query that quietly comes back empty.
func ParseUniqueID(input, prefix string) (int64, error) {
	trimmed := strings.TrimSpace(input)

	bad := func(reason string) (int64, error) {
		return 0, &InvalidIDError{Value: input, Reason: reason}
	}
	malformed := func() (int64, error) {
		if prefix != "" {
			return bad(fmt.Sprintf("expected a number, optionally prefixed (e.g. %q or %q)",
				prefix+"-1", "1"))
		}
		return bad(`expected a number (e.g. "1")`)
	}

	digits := trimmed
	if !isASCIIDigits(digits) {
		// Split at the last "-", not the first: a prefix containing one would
		// otherwise take the number half with it.
		i := strings.LastIndex(trimmed, "-")
		// i <= 0 covers both "no dash at all" and a leading dash, which is an
		// empty prefix half rather than a prefixless id — "-271" is a typo, not
		// a valid way to spell 271.
		if i <= 0 || i == len(trimmed)-1 {
			return malformed()
		}
		given, rest := trimmed[:i], trimmed[i+1:]
		if !isASCIIDigits(rest) {
			return malformed()
		}
		if prefix == "" {
			return bad("this board's ids have no prefix, so a bare number is expected")
		}
		if !strings.EqualFold(given, prefix) {
			return bad(fmt.Sprintf("this board's ids start with %q", prefix))
		}
		digits = rest
	}

	// isASCIIDigits has already ruled out everything but length: ParseInt can
	// still fail here on a number too large for int64.
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return malformed()
	}
	if n < 1 {
		return bad("ids start at 1")
	}
	return n, nil
}
```

- [ ] **Step 4: Esegui i test e verifica che passino**

Run: `go test ./internal/tracker/ -run TestParseUniqueID -v`
Expected: PASS, tutti i 15 sottocasi più il test sull'esempio.

- [ ] **Step 5: Mutation test — verifica che il confronto del prefisso morda**

Sostituisci temporaneamente `if !strings.EqualFold(given, prefix)` con `if len(given) < 0` ed esegui `go test ./internal/tracker/ -run TestParseUniqueID`.
Expected: FAIL sul caso `another board's prefix`. Se passa, il test non copre il confronto. Ripristina.

`if len(given) < 0` e non `if false`: quest'ultimo lascerebbe `given` dichiarata e non usata, il package non compilerebbe, e il FAIL osservato sarebbe un errore di build invece dell'assert che morde — la stessa trappola del mutation test del Task 2.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -race
git add internal/tracker/uniqueid.go internal/tracker/uniqueid_test.go
git commit -m "feat(tracker): parse board ids like BDF-271 into the number Notion filters on"
```

---

### Task 5: Il ruolo `id` in configurazione e in `GuessMapping`

**Files:**
- Modify: `internal/config/config.go:41-52`
- Modify: `internal/tracker/mapping.go:107-122`
- Test: `internal/tracker/mapping_test.go`

**Interfaces:**
- Consumes: `notion.Property.Prefix` (Task 1) — solo per costruire le fixture di test
- Produces: `config.Properties.ID string` con tag `yaml:"id,omitempty"`; `GuessMapping` lo riempie quando la data source ha esattamente una colonna `unique_id`

- [ ] **Step 1: Scrivi i test che falliscono**

In fondo a `internal/tracker/mapping_test.go`:

```go
func TestGuessMappingTakesTheOnlyUniqueIDColumn(t *testing.T) {
	schema := &notion.Schema{Properties: map[string]notion.Property{
		"Name": {Name: "Name", Type: "title"},
		"ID":   {Name: "ID", Type: "unique_id", Prefix: "BDF"},
	}}
	if got := GuessMapping(schema).ID; got != "ID" {
		t.Errorf("ID = %q, want %q", got, "ID")
	}
}

func TestGuessMappingLeavesTheIDEmptyWhenTwoColumnsCompete(t *testing.T) {
	schema := &notion.Schema{Properties: map[string]notion.Property{
		"Name": {Name: "Name", Type: "title"},
		"ID":   {Name: "ID", Type: "unique_id", Prefix: "BDF"},
		"Seq":  {Name: "Seq", Type: "unique_id"},
	}}
	if got := GuessMapping(schema).ID; got != "" {
		t.Errorf("ID = %q, want empty: a wrong guess waved through is worse than a question", got)
	}
}

func TestGuessMappingDoesNotOfferAUniqueIDColumnAsTheTicket(t *testing.T) {
	schema := &notion.Schema{Properties: map[string]notion.Property{
		"Name": {Name: "Name", Type: "title"},
		"ID":   {Name: "ID", Type: "unique_id", Prefix: "BDF"},
	}}
	// "id" is in ticketNames, but the ticket is a value notion-track writes and
	// a unique_id column is read-only: the two roles must not collide.
	if got := GuessMapping(schema).Ticket; got == "ID" {
		t.Errorf("Ticket = %q, want anything but the unique_id column", got)
	}
}
```

Il terzo test è **documentale, non guida codice**: i candidati del ticket si pescano già solo da `rich_text` e `title` (`mapping.go:69-72`), quindi passerebbe anche senza questo task. Vale la pena scriverlo — è il guard-rail che si romperà il giorno in cui qualcuno allarga quei candidati — ma non aspettarti che sia lui a fallire allo Step 2.

- [ ] **Step 2: Esegui i test e verifica che falliscano**

Run: `go test ./internal/tracker/ -run TestGuessMapping -v`
Expected: FAIL alla compilazione, `GuessMapping(schema).ID undefined`.

- [ ] **Step 3: Aggiungi il campo di configurazione**

In `internal/config/config.go`, dentro `type Properties struct`, dopo `Priority`:

```go
	// ID is the column carrying Notion's own row identifier ("BDF-271").
	// Optional, and read-only by nature: it is a way to address a row, never a
	// value to write, which is why nothing in tracker.BuildProperties knows it
	// exists.
	ID string `yaml:"id,omitempty"`
```

Nessun bump di `CurrentSchemaVersion` e nessuna migrazione: un campo `omitempty` assente da un profilo esistente si legge come `""`, che è già il valore "non mappato" con cui convivono gli altri ruoli opzionali.

- [ ] **Step 4: Aggiungi la guess**

In `internal/tracker/mapping.go`, subito prima di `return out`:

```go
	// The only unique_id column wins, by type rather than by name. Here the
	// "only candidate of a suitable type wins" rule is safe in a way it is not
	// for assignee and priority: no other role accepts unique_id, so there is
	// no role to steal a column from. Two or more and the guess stays empty,
	// the same rule every other role follows.
	//
	// That "id" also appears in ticketNames is not a conflict: ticket
	// candidates are drawn from rich_text and title only.
	if ids := byType["unique_id"]; len(ids) == 1 {
		out.ID = ids[0]
	}
```

- [ ] **Step 5: Esegui i test e verifica che passino**

Run: `go test ./internal/tracker/ ./internal/config/ -v`
Expected: PASS.

- [ ] **Step 6: Mutation test — verifica che la guardia morda**

Sostituisci temporaneamente `if ids := byType["unique_id"]; len(ids) == 1` con `if ids := byType["unique_id"]; len(ids) >= 1` ed esegui `go test ./internal/tracker/ -run TestGuessMapping`.
Expected: FAIL su `TestGuessMappingLeavesTheIDEmptyWhenTwoColumnsCompete`. Ripristina.

- [ ] **Step 7: Commit**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -race
git add internal/config/config.go internal/tracker/mapping.go internal/tracker/mapping_test.go
git commit -m "feat(config): add the optional id role and guess it from the schema"
```

---

### Task 6: `init --id-prop` e la voce nel wizard

**Files:**
- Modify: `internal/cli/init.go:139-142` (`configFlags`), `:215-216` e `:327` (chiamate a `validateMapping`), `:355-359` (costruzione di `Properties`), `:365-374` (registrazione flag), `:378-425` (`validateMapping`)
- Modify: `internal/tui/wizard.go:56-63` (`roles`), `:433-465` (`roleValue`, `setRole`)
- Test: `internal/cli/init_test.go`, `internal/tui/wizard_test.go`

**Interfaces:**
- Consumes: `config.Properties.ID` (Task 5)
- Produces: `validateMapping(schema *notion.Schema, p config.Properties) (string, error)` — **firma cambiata**, vedi Step 3

- [ ] **Step 1: Scrivi il test che fallisce**

In fondo a `internal/cli/init_test.go`. Gli helper `withStubbedAPI`, `executeArgs` e `writtenProfile` esistono già nel package `cli` — non riscriverli:

```go
// cliSchemaWithIDJSON is cliSchemaJSON plus the unique_id column the id role
// maps onto. A separate const, so the fixtures the existing tests were written
// against keep the property set they assert on.
const cliSchemaWithIDJSON = `{"id":"ds1","title":[{"plain_text":"Tasks"}],"properties":{
	"Name":{"name":"Name","type":"title","title":{}},
	"Ticket":{"name":"Ticket","type":"rich_text","rich_text":{}},
	"Stato":{"name":"Stato","type":"status","status":{"options":[{"name":"Fatto"}]}},
	"ID":{"name":"ID","type":"unique_id","unique_id":{"prefix":"BDF"}}}}`

func TestInitMapsTheIDColumn(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaWithIDJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	if code := executeArgs(initArgs(cfg, "--id-prop", "ID")); code != ExitOK {
		t.Fatalf("exit code = %d, want %d (ExitOK)", code, ExitOK)
	}
	if got := writtenProfile(t, cfg).Properties.ID; got != "ID" {
		t.Errorf("Properties.ID = %q, want %q", got, "ID")
	}
}

func TestInitRejectsAnIDColumnOfTheWrongType(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaWithIDJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	// A rich_text column cannot carry Notion's own row id.
	if code := executeArgs(initArgs(cfg, "--id-prop", "Ticket")); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
}
```

**`initArgs` non è una comodità, è obbligatorio.** Costruisce l'invocazione con `--profile work`, e il commento sopra di esso (`init_test.go:458-462`) spiega perché: `withStubbedAPI` scrive una config il cui `default_profile` è già `work`, mentre `saveInitProfile` senza `--profile` scrive sul profilo `default` e lascia `default_profile` intatto perché non è vuoto. `writtenProfile` fa `Resolve("")`, quindi rileggerebbe il **vecchio** profilo e il test fallirebbe contro un'implementazione perfettamente corretta. È esattamente la trappola in cui sono cascati i test gemelli prima di essere corretti — guarda `TestInitMapsTheAssigneeColumn` e `TestInitMapsThePriorityColumn`, che usano tutti `initArgs`.

`cliSchemaWithIDJSON` sta in `init_test.go` ma è visibile a tutto il package `cli`: i Task 12 e 13 lo riusano invece di ridefinirlo.

- [ ] **Step 2: Esegui i test e verifica che falliscano**

Run: `go test ./internal/cli/ -run 'TestInitMapsTheIDColumn|TestInitRejectsAnIDColumn' -v`
Expected: FAIL con `unknown flag: --id-prop` ed exit code 2.

La regex va quotata e deve matchare i nomi veri: `go test -run` su un nome inesistente esegue zero test e stampa `ok`, cioè mostra un PASS dove il piano promette un FAIL.

- [ ] **Step 3: Cambia `validateMapping` per prendere `config.Properties`**

`validateMapping` ha già sei parametri stringa posizionali; il ruolo `id` ne farebbe sette, e sette stringhe in fila sono sette occasioni di invertirne due senza che il compilatore dica niente. Entrambi i chiamanti hanno già a disposizione la struct — il ramo del wizard passa letteralmente i campi di `res.Props` uno per uno.

Nuova firma e corpo in `internal/cli/init.go`:

```go
// validateMapping checks each mapped property against the schema and returns
// the status property's actual type.
//
// It takes the whole Properties struct rather than one string per role: the
// roles are named at the call site by field name, so no caller can silently
// swap two of them, and adding a role is a line here instead of a new
// positional parameter at every call site.
//
// ticket, status and title are required: internal/service/doctor.go reports
// each of them as a "fail" when unmapped, and get/list/upsert key every
// lookup off them, so writing a profile with one left blank produces a
// config that is broken on first use. due, assignee, priority and id are the
// roles doctor treats as optional, so they are the only ones that may be
// left unmapped here too.
func validateMapping(schema *notion.Schema, p config.Properties) (string, error) {
	check := func(role, flag, name string, required bool, want ...string) (string, error) {
		// ... corpo invariato ...
	}

	if _, err := check("ticket", "ticket-prop", p.Ticket, true, "rich_text", "title"); err != nil {
		return "", err
	}
	if _, err := check("title", "title-prop", p.Title, true, "title"); err != nil {
		return "", err
	}
	if _, err := check("due", "due-prop", p.Due, false, "date"); err != nil {
		return "", err
	}
	if _, err := check("assignee", "assignee-prop", p.Assignee, false, "select"); err != nil {
		return "", err
	}
	if _, err := check("priority", "priority-prop", p.Priority, false, "select"); err != nil {
		return "", err
	}
	if _, err := check("id", "id-prop", p.ID, false, "unique_id"); err != nil {
		return "", err
	}
	statusType, err := check("status", "status-prop", p.Status, true, "status", "select")
	if err != nil {
		return "", err
	}
	return statusType, nil
}
```

Il corpo di `check` non cambia.

- [ ] **Step 4: Aggiorna i due chiamanti**

Ramo wizard (`internal/cli/init.go:215-216`):

```go
	statusType, err := validateMapping(res.Schema, res.Props)
```

Ramo flag (`internal/cli/init.go:327`): costruisci la struct una volta sola e usala sia per validare sia per salvare. Sostituisci la riga della chiamata con:

```go
			props := config.Properties{
				Ticket: ticketProp, Status: statusProp, Title: titleProp, Due: dueProp,
				Assignee: assigneeProp, Priority: priorityProp, ID: idProp,
			}
			statusType, err := validateMapping(schema, props)
```

e più sotto, nella costruzione del profilo (`internal/cli/init.go:355-359`), sostituisci il literal `config.Properties{...}` con `Properties: props,`.

- [ ] **Step 5: Registra il flag**

In `internal/cli/init.go`, dichiara `var idProp string` accanto agli altri `*Prop`, aggiungi `"id-prop"` alla slice `configFlags`, e registra il flag dopo `priority-prop`:

```go
	cmd.Flags().StringVar(&idProp, "id-prop", "", "unique_id property holding the board id, e.g. BDF-271 (optional)")
```

- [ ] **Step 6: Aggiungi il ruolo al wizard**

In `internal/tui/wizard.go`, in fondo alla slice `roles`:

```go
	// id's key is "u", for unique_id: "i" went to title, which itself only has
	// "i" because ticket claimed "t".
	{name: "id", key: "u", types: []string{"unique_id"}, optional: true},
```

E il caso corrispondente in `roleValue`:

```go
	case "id":
		return p.ID
```

e in `setRole`:

```go
	case "id":
		p.ID = value
```

- [ ] **Step 7: Scrivi il test del wizard**

In fondo a `internal/tui/wizard_test.go`:

```go
func TestRoleAccessorsRoundTripEveryRole(t *testing.T) {
	// Every role must survive setRole -> roleValue. A role added to the slice
	// but forgotten in one of the two switches would otherwise be silently
	// unsettable, and the wizard would show it as unmapped no matter what the
	// user picked.
	var p config.Properties
	for _, r := range roles {
		setRole(&p, r.name, "col-"+r.name)
	}
	for _, r := range roles {
		if got := roleValue(p, r.name); got != "col-"+r.name {
			t.Errorf("roleValue(%q) = %q, want %q", r.name, got, "col-"+r.name)
		}
	}
}
```

- [ ] **Step 8: Esegui i test e verifica che passino**

Run: `go test ./internal/cli/ ./internal/tui/ -v`
Expected: PASS, compresi i test preesistenti di `init` e del wizard.

- [ ] **Step 9: Mutation test — verifica che il round-trip morda**

Commenta temporaneamente il `case "id"` dentro `setRole` ed esegui `go test ./internal/tui/ -run TestRoleAccessorsRoundTripEveryRole`.
Expected: FAIL. Ripristina.

- [ ] **Step 10: Commit**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -race
git add internal/cli/init.go internal/cli/init_test.go internal/tui/wizard.go internal/tui/wizard_test.go
git commit -m "feat(init): map the id property from flags and from the wizard"
```

---

### Task 7: `doctor` valida il ruolo `id`

**Files:**
- Modify: `internal/service/doctor.go:69-93`
- Test: `internal/service/doctor_test.go`

**Interfaces:**
- Consumes: `config.Properties.ID` (Task 5)
- Produces: `doctor` riporta il ruolo `id` come opzionale, e fallisce quando la colonna mappata non esiste o non è di tipo `unique_id`

- [ ] **Step 1: Scrivi il test che fallisce**

In fondo a `internal/service/doctor_test.go`. Gli helper `doctorRoutes`, `findCheck` e `testProfile` esistono già nel package `service`, e `schemaJSON` è la fixture che `doctorRoutes` serve:

```go
func TestDoctorRejectsAnIDPropertyOfTheWrongType(t *testing.T) {
	srv := doctorRoutes(t)
	defer srv.Close()

	// schemaJSON's "Ticket" is rich_text: exactly the shape of mistake this
	// check exists for — it looks plausible in the config and can never work,
	// because a rich_text column cannot carry Notion's own row id.
	p := testProfile()
	p.Properties.ID = "Ticket"
	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), p).Doctor(context.Background())

	props, ok := findCheck(checks, "properties")
	if !ok {
		t.Fatal("no properties check")
	}
	if props.Status != "fail" {
		t.Errorf("properties = %s (%s), want fail for an id property of type rich_text",
			props.Status, props.Detail)
	}
	if !strings.Contains(props.Detail, "unique_id") {
		t.Errorf("detail = %q, want it to name the type the role needs", props.Detail)
	}
}

func TestDoctorTreatsTheIDAsOptional(t *testing.T) {
	srv := doctorRoutes(t)
	defer srv.Close()

	// testProfile leaves the role unmapped, the way every profile written
	// before this feature does. A board with no unique_id column is not
	// broken — it is addressed the other two ways.
	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()).
		Doctor(context.Background())

	props, ok := findCheck(checks, "properties")
	if !ok {
		t.Fatal("no properties check")
	}
	if props.Status == "fail" {
		t.Errorf("properties = fail (%s), want the optional role to be skipped", props.Detail)
	}
}
```

- [ ] **Step 2: Esegui i test e verifica che falliscano**

Run: `go test ./internal/service/ -run 'TestDoctorRejectsAnIDProperty|TestDoctorTreatsTheIDAsOptional' -v`
Expected: FAIL — `TestDoctorRejectsAnIDPropertyOfTheWrongType` non vede nessun errore, perché `doctor` non conosce ancora il ruolo.

Regex sempre quotata: la shell di questo ambiente è zsh, dove un `-run TestDoctor.*ID` non quotato viene espanso come glob sui file e il comando non parte affatto ("no matches found").

- [ ] **Step 3: Aggiungi il ruolo ai tre punti**

In `internal/service/doctor.go`:

Nella mappa `mapped`, accanto agli altri ruoli:

```go
		"id":       s.profile.Properties.ID,
```

In `wantType`:

```go
		"id":       {"unique_id"},
```

Nell'elenco `roles`:

```go
	roles := []string{"ticket", "status", "title", "due", "assignee", "priority", "id"}
```

In `optionalRoles`, estendendo il commento già presente:

```go
	// A board may legitimately track nobody, so an unmapped assignee is a
	// skip, not a failure — the same judgement already made for due. A
	// priority is the same story again: not every board ranks urgency, and
	// there is no identity to resolve for it, so checkProperties existing
	// (column present, right type) is the whole check it needs. An id is the
	// fourth: it is a way to address a row, and a board without one is simply
	// addressed the other two ways.
	optionalRoles := map[string]bool{"due": true, "assignee": true, "priority": true, "id": true}
```

- [ ] **Step 4: Esegui i test e verifica che passino**

Run: `go test ./internal/service/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -race
git add internal/service/doctor.go internal/service/doctor_test.go
git commit -m "feat(doctor): validate the optional id property"
```

---

### Task 8: Risolvere un id in una riga

**Files:**
- Modify: `internal/service/service.go` (errori in testa al file, `findByUniqueID` e `GetByUniqueID` accanto a `findByTicket` e `Get`)
- Test: `internal/service/service_test.go`

**Interfaces:**
- Consumes: `notion.Property.Prefix` (Task 1), `notion.UniqueIDEqualsFilter` (Task 3), `tracker.ParseUniqueID` (Task 4), `config.Properties.ID` (Task 5)
- Produces:
  - `service.ErrEmptyID` — `errors.New("id must not be empty")`
  - `service.ErrNoIDProperty` — `errors.New("no id property is mapped")`
  - `(s *Service) GetByUniqueID(ctx context.Context, input string) (notion.Page, error)`
  - `(s *Service) findByUniqueID(ctx context.Context, input string) (notion.Page, error)` (non esportata, usata anche dal Task 9)

- [ ] **Step 1: Scrivi i test che falliscono**

In fondo a `internal/service/service_test.go`. Il file ha già `testProfile()`, `schemaJSON` e `rowJSON`; un `Service` si costruisce con `New(notion.New("t", notion.WithBaseURL(srv.URL)), profile)`. Servono tre fixture nuove, sul modello di `assigneeProfile`/`priorityProfile` e di `routes`:

```go
// idSchemaJSON is schemaJSON's shape plus the unique_id column the id role maps
// onto. A separate const, so the existing fixtures keep the property set the
// tests written against them assert on.
const idSchemaJSON = `{
  "id":"ds1","title":[{"plain_text":"Tasks"}],
  "properties":{
    "Name":{"name":"Name","type":"title","title":{}},
    "Ticket":{"name":"Ticket","type":"rich_text","rich_text":{}},
    "Stato":{"name":"Stato","type":"status","status":{"options":[{"name":"In corso"},{"name":"Fatto"}]}},
    "ID":{"name":"ID","type":"unique_id","unique_id":{"prefix":"BDF"}}
  }}`

// idRowJSON is a row carrying a board id.
//
// The page id is a real UUID, not the "page1" the older fixtures use: Task 9
// sends this row's id through SetByID, which normalises it, and
// notion.NormalizePageID accepts only a URL, a bare 32-hex id or a dashed UUID.
// A fixture with "page1" would fail there with "malformed page id" — a failure
// about the fixture, dressed up as a failure of the feature.
const idRowJSON = `{
  "id":"23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5",
  "url":"https://notion.so/23fb4e5c8a5f4d21b7c9d0e1f2a3b4c5",
  "last_edited_time":"2026-07-20T10:00:00.000Z",
  "parent":{"type":"data_source_id","data_source_id":"ds1"},
  "properties":{
    "Name":{"type":"title","title":[{"plain_text":"Hardening"}]},
    "Stato":{"type":"status","status":{"name":"In corso"}},
    "ID":{"type":"unique_id","unique_id":{"prefix":"BDF","number":271}}
  }}`

// idProfile is testProfile with the id role mapped.
func idProfile() config.Profile {
	p := testProfile()
	p.Properties.ID = "ID"
	return p
}

// idRoutes is routes() against the schema that has the unique_id column, with
// a copy of the query body: the filter this feature sends is the thing worth
// asserting on, and it never appears in the response.
func idRoutes(t *testing.T, queryResults string, sent *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(idSchemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			if sent != nil {
				if err := json.NewDecoder(r.Body).Decode(sent); err != nil {
					t.Errorf("decoding the query body: %v", err)
				}
			}
			w.Write([]byte(`{"results":[` + queryResults + `],"has_more":false}`))
		default: // POST /v1/pages, PATCH /v1/pages/{id}
			w.Write([]byte(idRowJSON))
		}
	}))
}

func TestGetByUniqueIDFiltersByTheBareNumber(t *testing.T) {
	var sent map[string]any
	srv := idRoutes(t, idRowJSON, &sent)
	defer srv.Close()

	page, err := New(notion.New("t", notion.WithBaseURL(srv.URL)), idProfile()).
		GetByUniqueID(context.Background(), "BDF-271")
	if err != nil {
		t.Fatalf("GetByUniqueID: %v", err)
	}
	if page.ID != "23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5" {
		t.Errorf("page.ID = %q, want the fixture's page id", page.ID)
	}
	// The request body is the contract: Notion rejects a quoted value here, so
	// a test that only checked the response would pass against a filter that
	// matches nothing on a real board.
	filter, _ := sent["filter"].(map[string]any)
	unique, _ := filter["unique_id"].(map[string]any)
	if filter["property"] != "ID" {
		t.Errorf("filter.property = %v, want ID", filter["property"])
	}
	// encoding/json decodes every JSON number into float64.
	if unique["equals"] != float64(271) {
		t.Errorf("filter.unique_id.equals = %#v, want the number 271", unique["equals"])
	}
}

func TestGetByUniqueIDReportsAMissingRowAsNotFound(t *testing.T) {
	srv := idRoutes(t, "", nil)
	defer srv.Close()

	_, err := New(notion.New("t", notion.WithBaseURL(srv.URL)), idProfile()).
		GetByUniqueID(context.Background(), "BDF-999")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByUniqueID error = %v, want ErrNotFound", err)
	}
}

func TestGetByUniqueIDRefusesTwoRowsRatherThanPickingOne(t *testing.T) {
	srv := idRoutes(t, idRowJSON+","+idRowJSON, nil)
	defer srv.Close()

	// A unique_id matching twice is impossible on a healthy board. Returning
	// the first would bury a fault nobody else would ever notice.
	_, err := New(notion.New("t", notion.WithBaseURL(srv.URL)), idProfile()).
		GetByUniqueID(context.Background(), "BDF-271")
	if err == nil {
		t.Error("GetByUniqueID = nil error for two matches, want a failure")
	}
}

func TestGetByUniqueIDWithoutAMappedColumn(t *testing.T) {
	// An unreachable address on purpose: this must fail before any request
	// goes out, and a working stub would hide a regression that moved the
	// check after the query.
	svc := New(notion.New("t", notion.WithBaseURL("http://127.0.0.1:1")), testProfile())
	_, err := svc.GetByUniqueID(context.Background(), "BDF-271")
	if !errors.Is(err, ErrNoIDProperty) {
		t.Fatalf("GetByUniqueID error = %v, want ErrNoIDProperty", err)
	}
	// The message has to carry the fix: nobody can guess the flag name from
	// "no id property is mapped".
	if !strings.Contains(err.Error(), "--id-prop") {
		t.Errorf("error = %q, want it to name the init flag", err.Error())
	}
}

func TestGetByUniqueIDRejectsAnEmptyValue(t *testing.T) {
	svc := New(notion.New("t", notion.WithBaseURL("http://127.0.0.1:1")), idProfile())
	if _, err := svc.GetByUniqueID(context.Background(), ""); !errors.Is(err, ErrEmptyID) {
		t.Errorf("GetByUniqueID error = %v, want ErrEmptyID", err)
	}
}

func TestGetByUniqueIDRejectsAWronglyTypedColumn(t *testing.T) {
	srv := idRoutes(t, idRowJSON, nil)
	defer srv.Close()

	// The config points the role at a rich_text column: doctor reports this,
	// but the lookup has to refuse it too rather than send a filter Notion
	// will reject with a 400.
	p := idProfile()
	p.Properties.ID = "Ticket"
	_, err := New(notion.New("t", notion.WithBaseURL(srv.URL)), p).
		GetByUniqueID(context.Background(), "BDF-271")
	if err == nil || !strings.Contains(err.Error(), "unique_id") {
		t.Errorf("GetByUniqueID error = %v, want it to name the type the role needs", err)
	}
}
```

- [ ] **Step 2: Esegui i test e verifica che falliscano**

Run: `go test ./internal/service/ -run TestGetByUniqueID -v`
Expected: FAIL alla compilazione, `svc.GetByUniqueID undefined`.

- [ ] **Step 3: Aggiungi gli errori**

In `internal/service/service.go`, accanto agli altri errori dichiarati in testa al file:

```go
// ErrEmptyID mirrors ErrEmptyTicket and ErrEmptyPageID for the third way of
// addressing a row: cobra reports that a flag was passed, never that it carries
// a value, so `--id ""` would otherwise reach ParseUniqueID and surface as a
// malformed id rather than a missing one.
var ErrEmptyID = errors.New("id must not be empty")

// ErrNoIDProperty means a row was addressed by board id on a profile that has
// no id column mapped. It is "not configured yet", not "broken": the role is
// optional, and a board without a unique_id column is addressed the other two
// ways.
var ErrNoIDProperty = errors.New("no id property is mapped")
```

- [ ] **Step 4: Scrivi la risoluzione**

In `internal/service/service.go`, subito dopo `findByTicket`:

```go
// findByUniqueID resolves a board id ("BDF-271", or a bare "271") to the single
// row carrying it.
//
// Every failure it can produce is decided before the query goes out, except the
// last two: a column that is missing, wrongly typed, or an id that does not
// parse is a mistake the user can fix by rewriting the command, and saying so
// without a round trip is both faster and clearer than an empty result set.
func (s *Service) findByUniqueID(ctx context.Context, input string) (notion.Page, error) {
	if strings.TrimSpace(input) == "" {
		return notion.Page{}, ErrEmptyID
	}
	name := s.profile.Properties.ID
	if name == "" {
		return notion.Page{}, fmt.Errorf(
			"id addressing was requested but %w; "+
				"run 'notion-track init --id-prop <name>' to map it", ErrNoIDProperty)
	}
	schema, err := s.Schema(ctx)
	if err != nil {
		return notion.Page{}, err
	}
	prop, ok := schema.Properties[name]
	if !ok {
		return notion.Page{}, fmt.Errorf(
			"property %q is configured but does not exist in the data source; "+
				"run 'notion-track doctor' to see the current schema", name)
	}
	if prop.Type != "unique_id" {
		return notion.Page{}, fmt.Errorf(
			"id property %q has type %q, not unique_id; run 'notion-track doctor'",
			name, prop.Type)
	}
	number, err := tracker.ParseUniqueID(input, prop.Prefix)
	if err != nil {
		return notion.Page{}, err
	}
	pages, err := s.client.QueryPages(ctx, s.profile.DataSourceID,
		notion.UniqueIDEqualsFilter(name, number))
	if err != nil {
		return notion.Page{}, err
	}
	switch len(pages) {
	case 0:
		// Spelled out rather than wrapped the way Get does it: ErrNotFound
		// reads "ticket not found", and "ticket not found: BDF-271" would name
		// a flag this command never used.
		return notion.Page{}, fmt.Errorf("%w: no row has id %s", ErrNotFound, input)
	case 1:
		return pages[0], nil
	default:
		// Impossible on a healthy board — the column's whole job is to be
		// unique — but "impossible" and "silently wrong" are different things,
		// and picking the first would bury the fault.
		return notion.Page{}, fmt.Errorf(
			"id %s matches %d rows, which a unique_id column should make impossible; "+
				"run 'notion-track doctor'", input, len(pages))
	}
}

// GetByUniqueID returns the row carrying a board id, bypassing the ticket
// lookup that Get performs.
func (s *Service) GetByUniqueID(ctx context.Context, input string) (notion.Page, error) {
	return s.findByUniqueID(ctx, input)
}
```

Verifica che `strings` sia fra gli import di `service.go`; se non c'è, aggiungilo.

- [ ] **Step 5: Esegui i test e verifica che passino**

Run: `go test ./internal/service/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -race
git add internal/service/service.go internal/service/service_test.go
git commit -m "feat(service): resolve a board id to the row carrying it"
```

---

### Task 9: Scrivere su una riga indirizzata per id

**Files:**
- Modify: `internal/service/service.go` (accanto a `SetByID`)
- Test: `internal/service/service_test.go`

**Interfaces:**
- Consumes: `findByUniqueID` (Task 8), `SetByID` (già esistente)
- Produces: `(s *Service) SetByUniqueID(ctx context.Context, input string, f tracker.Fields, body *BodyRequest) (Result, error)`

- [ ] **Step 1: Scrivi il test che fallisce**

In fondo a `internal/service/service_test.go`, riusando `idSchemaJSON`, `idRowJSON` e `idProfile()` del Task 8. Qui serve un server che registri le richieste, sul modello di `routes`:

```go
// idWriteRoutes is idRoutes plus a record of every "METHOD path": the point of
// these two tests is which requests went out, and in which order.
func idWriteRoutes(t *testing.T, queryResults string, seen *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(idSchemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + queryResults + `],"has_more":false}`))
		default: // POST /v1/pages, PATCH /v1/pages/{id}
			w.Write([]byte(idRowJSON))
		}
	}))
}

func TestSetByUniqueIDUpdatesTheResolvedPage(t *testing.T) {
	var seen []string
	srv := idWriteRoutes(t, idRowJSON, &seen)
	defer srv.Close()

	res, err := New(notion.New("t", notion.WithBaseURL(srv.URL)), idProfile()).
		SetByUniqueID(context.Background(), "BDF-271", tracker.Fields{Status: "Fatto"}, nil)
	if err != nil {
		t.Fatalf("SetByUniqueID: %v", err)
	}
	// The write must land on the page the id resolved to, through the same
	// SetByID path --page-id uses: one write path, three ways to address it.
	if !contains(seen, "PATCH /v1/pages/23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5") {
		t.Errorf("requests = %v, want a PATCH on the resolved page", seen)
	}
	if res.Action != "updated" {
		t.Errorf("action = %q, want %q", res.Action, "updated")
	}
}

func TestSetByUniqueIDFailsBeforeWritingWhenTheIDIsUnknown(t *testing.T) {
	var seen []string
	srv := idWriteRoutes(t, "", &seen)
	defer srv.Close()

	_, err := New(notion.New("t", notion.WithBaseURL(srv.URL)), idProfile()).
		SetByUniqueID(context.Background(), "BDF-999", tracker.Fields{Status: "Fatto"}, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetByUniqueID error = %v, want ErrNotFound", err)
	}
	// Resolution comes first for exactly this reason: an id nobody recognises
	// must not reach the board at all.
	for _, req := range seen {
		if strings.HasPrefix(req, "PATCH ") || req == "POST /v1/pages" {
			t.Errorf("requests = %v, want no write after failing to resolve the id", seen)
			break
		}
	}
}
```

`contains(haystack []string, needle string) bool` esiste già in fondo a `service_test.go`.

- [ ] **Step 2: Esegui i test e verifica che falliscano**

Run: `go test ./internal/service/ -run TestSetByUniqueID -v`
Expected: FAIL alla compilazione, `svc.SetByUniqueID undefined`.

- [ ] **Step 3: Scrivi il metodo**

In `internal/service/service.go`, subito dopo `SetByID`:

```go
// SetByUniqueID updates the row carrying a board id.
//
// It resolves the id to a page and hands off to SetByID: the id is a way to
// find a row, not a second way to write one, so nothing about the write itself
// is duplicated here. Resolution happens first, which is what makes a wrong id
// fail before anything is sent to the board.
func (s *Service) SetByUniqueID(ctx context.Context, input string, f tracker.Fields, body *BodyRequest) (Result, error) {
	page, err := s.findByUniqueID(ctx, input)
	if err != nil {
		return Result{}, err
	}
	return s.SetByID(ctx, page.ID, f, body)
}
```

- [ ] **Step 4: Esegui i test e verifica che passino**

Run: `go test ./internal/service/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -race
git add internal/service/service.go internal/service/service_test.go
git commit -m "feat(service): update a row addressed by its board id"
```

---

### Task 10: Gli exit code dei nuovi errori

**Files:**
- Modify: `internal/cli/output.go:72-140` (`exitCodeFor`)
- Test: `internal/cli/output_test.go`

**Interfaces:**
- Consumes: `tracker.InvalidIDError` (Task 4), `service.ErrEmptyID` e `service.ErrNoIDProperty` (Task 8)
- Produces: niente di nuovo — solo il mapping

- [ ] **Step 1: Scrivi il test che fallisce**

In fondo a `internal/cli/output_test.go`:

```go
func TestExitCodeForIDErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"malformed id", &tracker.InvalidIDError{Value: "x", Reason: "expected a number"}, ExitUsage},
		{"empty id", service.ErrEmptyID, ExitUsage},
		{"id role not mapped", service.ErrNoIDProperty, ExitUsage},
		// Wrapped the way the service actually returns it.
		{"wrapped not-mapped", fmt.Errorf("%w: run init", service.ErrNoIDProperty), ExitUsage},
		{"unknown id", fmt.Errorf("%w: BDF-999", service.ErrNotFound), ExitNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exitCodeFor(tt.err); got != tt.want {
				t.Errorf("exitCodeFor(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}
```

Verifica che `fmt`, `tracker` e `service` siano fra gli import del file; aggiungili se mancano.

- [ ] **Step 2: Esegui il test e verifica che fallisca**

Run: `go test ./internal/cli/ -run TestExitCodeForIDErrors -v`
Expected: FAIL — i primi tre casi restituiscono `ExitError` (1) invece di `ExitUsage` (2). L'ultimo passa già.

- [ ] **Step 3: Aggiungi i casi**

In `internal/cli/output.go`, aggiungi `invalidID *tracker.InvalidIDError` al blocco `var (...)` delle variabili per `errors.As`, e nel primo `switch` un caso accanto a quelli di `invalid` e `ambiguous`:

```go
	case errors.As(err, &invalidID):
		return ExitUsage
```

E, insieme agli altri "valore mancante o non configurato", estendi il caso esistente:

```go
	// --id "" is a missing value wearing a passed flag, like --ticket "" and
	// --page-id "" before it. A profile with no id column mapped is the same
	// class of mistake as config.ErrNotConfigured: the invocation cannot work
	// as written, and the fix is the user's to make.
	case errors.Is(err, service.ErrEmptyID), errors.Is(err, service.ErrNoIDProperty):
		return ExitUsage
```

- [ ] **Step 4: Esegui i test e verifica che passino**

Run: `go test ./internal/cli/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -race
git add internal/cli/output.go internal/cli/output_test.go
git commit -m "feat(cli): map the id addressing errors onto exit code 2"
```

---

### Task 11: Il campo `id` nel JSON — e la sua gemella MCP

**Files:**
- Modify: `internal/cli/get.go:11-44` (`pageJSON`, `toPageJSON`)
- Modify: `internal/mcp/server.go:25-38` (`Row`)
- Test: `internal/cli/get_test.go`, `internal/cli/mcp_test.go`

**Interfaces:**
- Consumes: `config.Properties.ID` (Task 5), `PropertyValue.Text` per `unique_id` (Task 2)
- Produces: chiave JSON `id` in ogni riga stampata da `get`, `list`, `upsert` e `set`, e nei risultati dei tool MCP. **Non** in `apply`: il suo output è costruito da `applyOutcome` (indice, op, ticket, azione), che non contiene la pagina e non passa da `toPageJSON` — è un non-goal dichiarato dallo spec, non una dimenticanza

**⚠️ Vincolo bloccante:** `pageJSON` e `mcp.Row` si convertono direttamente con `mcp.Row(toPageJSON(...))` in `internal/cli/mcp.go`. La conversione compila **solo** finché le due struct sono identiche per nome, tipo e ordine dei campi. Le due modifiche devono stare nello **stesso commit**: separarle lascia l'albero non compilabile, che è esattamente il difetto intercettato nella review del ruolo `priority`.

- [ ] **Step 1: Scrivi i test che falliscono**

In fondo a `internal/cli/get_test.go`:

```go
func TestGetJSONCarriesTheBoardID(t *testing.T) {
	page := notion.Page{
		ID:  "page1",
		URL: "https://notion.so/page1",
		Properties: map[string]notion.PropertyValue{
			"ID":   {Type: "unique_id", Text: "BDF-271"},
			"Name": {Type: "title", Text: "Hardening"},
		},
	}
	got := toPageJSON(page, config.Properties{ID: "ID", Title: "Name"})
	if got.ID != "BDF-271" {
		t.Errorf("ID = %q, want %q", got.ID, "BDF-271")
	}
}

func TestGetJSONKeepsTheIDKeyWhenTheRoleIsUnmapped(t *testing.T) {
	// The key is always present so no script has to branch on it — the same
	// rule assignee and priority already follow.
	b, err := json.Marshal(toPageJSON(notion.Page{ID: "page1"}, config.Properties{}))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(b), `"id":""`) {
		t.Errorf("marshalled to %s, want an empty id key", b)
	}
}
```

In fondo a `internal/cli/mcp_test.go`:

```go
func TestMCPRowMirrorsPageJSON(t *testing.T) {
	// The two structs are converted with a direct type conversion, which only
	// compiles while they match field for field. This test states the contract
	// the compiler enforces, so a reviewer reading either file finds out why.
	pj := reflect.TypeOf(pageJSON{})
	row := reflect.TypeOf(mcp.Row{})
	if pj.NumField() != row.NumField() {
		t.Fatalf("pageJSON has %d fields, mcp.Row has %d", pj.NumField(), row.NumField())
	}
	for i := 0; i < pj.NumField(); i++ {
		a, b := pj.Field(i), row.Field(i)
		if a.Name != b.Name || a.Type != b.Type || a.Tag.Get("json") != b.Tag.Get("json") {
			t.Errorf("field %d: pageJSON has %s %s `%s`, mcp.Row has %s %s `%s`",
				i, a.Name, a.Type, a.Tag.Get("json"), b.Name, b.Type, b.Tag.Get("json"))
		}
	}
	if _, ok := pj.FieldByName("ID"); !ok {
		t.Error("pageJSON has no ID field")
	}
}
```

Aggiungi `reflect` agli import di `mcp_test.go` se manca.

- [ ] **Step 2: Esegui i test e verifica che falliscano**

Run: `go test ./internal/cli/ -run 'TestGetJSON|TestMCPRow' -v`
Expected: FAIL alla compilazione, `got.ID undefined (type pageJSON has no field or method ID)`.

- [ ] **Step 3: Aggiungi il campo a entrambe le struct**

In `internal/cli/get.go`, come **primo** campo di `pageJSON`:

```go
	// ID is the row's board id ("BDF-271"): the identifier a person reads and
	// says out loud, as opposed to PageID's UUID. First because it is the row's
	// identity, so the JSON reads in the order the board displays a row.
	//
	// Empty both when the row carries no value and when the id role is not
	// mapped, the same rule Assignee and Priority follow below.
	ID string `json:"id"`
```

e in `toPageJSON`, come primo campo del literal:

```go
		ID: p.Properties[props.ID].Text,
```

In `internal/mcp/server.go`, come **primo** campo di `Row`, nella stessa posizione:

```go
	// ID is the row's board id ("BDF-271"), the identifier a person uses.
	// Empty both when the row carries no value and when the id role is not
	// mapped, so an agent never has to branch on the key's presence.
	ID string `json:"id"`
```

- [ ] **Step 4: Esegui i test e verifica che passino**

Run: `go test ./internal/cli/ ./internal/mcp/ -v`
Expected: PASS. Se `internal/cli` non compila, le due struct sono disallineate: rileggile fianco a fianco.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -race
git add internal/cli/get.go internal/cli/get_test.go internal/mcp/server.go internal/cli/mcp_test.go
git commit -m "feat(json): expose the board id in every row, in the CLI and over MCP"
```

---

### Task 12: `get --id`

**Files:**
- Modify: `internal/cli/get.go:46-103`
- Test: `internal/cli/get_test.go`

**Interfaces:**
- Consumes: `service.GetByUniqueID` (Task 8)
- Produces: il flag `--id` su `get`, mutuamente esclusivo con `--ticket` e `--page-id`

- [ ] **Step 1: Scrivi i test che falliscono**

In fondo a `internal/cli/get_test.go`. Gli helper `withStubbedAPIProfile`, `executeArgs` e `captureStdout` esistono già; `cliSchemaWithIDJSON` arriva dal Task 6, stesso package:

```go
// idProfileYAML maps the id role on top of the common profile.
const idProfileYAML = `schema_version: 1
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
      id: ID
`

// cliRowWithIDJSON is a row carrying a board id.
const cliRowWithIDJSON = `{"id":"23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5",
	"url":"https://notion.so/23fb4e5c8a5f4d21b7c9d0e1f2a3b4c5",
	"last_edited_time":"2026-07-20T10:00:00.000Z",
	"parent":{"type":"data_source_id","data_source_id":"ds1"},
	"properties":{
	"Name":{"type":"title","title":[{"plain_text":"Hardening"}]},
	"Ticket":{"type":"rich_text","rich_text":[{"plain_text":"BDF-231"}]},
	"Stato":{"type":"status","status":{"name":"Fatto"}},
	"ID":{"type":"unique_id","unique_id":{"prefix":"BDF","number":271}}}}`

func TestGetByBoardIDReadsTheRow(t *testing.T) {
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaWithIDJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowWithIDJSON + `],"has_more":false}`))
	}, idProfileYAML)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--id", "BDF-271", "--json", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	if got["id"] != "BDF-271" {
		t.Errorf("id = %v, want %q", got["id"], "BDF-271")
	}
}

func TestGetRejectsTwoWaysOfAddressingAtOnce(t *testing.T) {
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaWithIDJSON))
	}, idProfileYAML)

	if code := executeArgs([]string{
		"get", "--id", "BDF-271", "--ticket", "Hardening", "--config", cfg,
	}); code != ExitUsage {
		t.Errorf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
}

func TestGetRejectsAnEmptyBoardID(t *testing.T) {
	// Passed but blank: cobra sees a flag, the service sees no value. It must
	// take the id path anyway and surface ErrEmptyID, not fall through to a
	// ticket lookup with a key it was never given.
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaWithIDJSON))
	}, idProfileYAML)

	var code int
	errOut := captureStderr(t, func() {
		code = executeArgs([]string{"get", "--id", "", "--config", cfg})
	})
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
	// The exit code alone proves nothing here: the regression this test exists
	// for — branching on the value instead of on Changed("id") — falls through
	// to a ticket lookup, raises ErrEmptyTicket, and exits 2 as well. The
	// message is what tells the two apart.
	if !strings.Contains(errOut, "id must not be empty") {
		t.Errorf("stderr = %q, want it to report an empty id", errOut)
	}
}
```

`captureStderr` esiste già in `get_test.go`.

- [ ] **Step 2: Esegui i test e verifica che falliscano**

Run: `go test ./internal/cli/ -run 'TestGetByBoardID|TestGetRejectsTwoWays|TestGetRejectsAnEmptyBoardID' -v`
Expected: FAIL con `unknown flag: --id`.

Regex quotata, e attenzione: `TestGetRejectsTwoWaysOfAddressingAtOnce` **passa già** prima dell'implementazione, perché un flag sconosciuto finisce comunque in `SetFlagErrorFunc` → exit 2. Il test che deve fallire qui è `TestGetByBoardIDReadsTheRow`.

- [ ] **Step 3: Aggiungi il flag e il ramo**

In `internal/cli/get.go`, dichiara `var boardID string` accanto a `ticket` e `pageID`, e aggiungi il ramo in testa alla catena dentro `RunE`:

```go
			var page notion.Page
			// Branch on Changed, not on the value: `--page-id ""` and `--id ""`
			// must still take their own path so they surface as
			// service.ErrEmptyPageID and service.ErrEmptyID rather than
			// silently falling through to a ticket lookup with an empty key
			// neither was ever given.
			switch {
			case cmd.Flags().Changed("id"):
				page, err = svc.GetByUniqueID(cmd.Context(), boardID)
			case cmd.Flags().Changed("page-id"):
				page, err = svc.GetByID(cmd.Context(), pageID)
			default:
				page, err = svc.Get(cmd.Context(), ticket)
			}
```

E in fondo, nella registrazione dei flag:

```go
	cmd.Flags().StringVar(&boardID, "id", "",
		"board id of the row, as Notion shows it (e.g. BDF-271, or just 271); "+
			"needs an id property mapped in the profile")
	cmd.MarkFlagsMutuallyExclusive("ticket", "page-id", "id")
	cmd.MarkFlagsOneRequired("ticket", "page-id", "id")
```

sostituendo le due chiamate `MarkFlags*` esistenti a due argomenti.

Aggiorna anche lo `Short` del comando:

```go
		Short: "Read the row for a ticket, a board id, or a Notion page id",
```

- [ ] **Step 4: Esegui i test e verifica che passino**

Run: `go test ./internal/cli/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -race
git add internal/cli/get.go internal/cli/get_test.go
git commit -m "feat(get): address a row by its board id"
```

---

### Task 13: `set --id`

**Files:**
- Modify: `internal/cli/upsert.go:10-24` (`writeFlags`), `:86-97` (`bindWithPageID`)
- Modify: `internal/cli/set.go:13-16` (`Long`), `:31-38` (il ramo)
- Test: `internal/cli/upsert_test.go` (è lì che stanno i test di `set`)

**Interfaces:**
- Consumes: `service.SetByUniqueID` (Task 9)
- Produces: il flag `--id` su `set`, mutuamente esclusivo con `--ticket` e `--page-id`

**Nota su dove sono i flag:** i flag di indirizzamento di `set` **non** stanno in `set.go`, ma in `bindWithPageID`, dentro `internal/cli/upsert.go` — è il binder condiviso, e `set.go` lo invoca in una riga sola.

- [ ] **Step 1: Scrivi i test che falliscono**

In `internal/cli/upsert_test.go` — **`set_test.go` non esiste**: il package ha `set.go` ma i suoi test vivono in `upsert_test.go` e `pageid_test.go`. Riusa `idProfileYAML`, `cliSchemaWithIDJSON` e `cliRowWithIDJSON` dei Task 6 e 12 — stesso package:

```go
func TestSetByBoardIDUpdatesTheRow(t *testing.T) {
	var patchedPath string
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaWithIDJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + cliRowWithIDJSON + `],"has_more":false}`))
		default: // PATCH /v1/pages/{id}
			patchedPath = r.URL.Path
			w.Write([]byte(cliRowWithIDJSON))
		}
	}, idProfileYAML)

	if code := executeArgs([]string{
		"set", "--id", "BDF-271", "--status", "Fatto", "--config", cfg,
	}); code != ExitOK {
		t.Fatalf("exit code = %d, want %d (ExitOK)", code, ExitOK)
	}
	// The write must land on the page the id resolved to.
	if patchedPath != "/v1/pages/23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5" {
		t.Errorf("patched %q, want the resolved page", patchedPath)
	}
}

func TestSetRejectsTwoWaysOfAddressingAtOnce(t *testing.T) {
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaWithIDJSON))
	}, idProfileYAML)

	if code := executeArgs([]string{
		"set", "--id", "BDF-271", "--page-id", "abc", "--status", "Fatto", "--config", cfg,
	}); code != ExitUsage {
		t.Errorf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
}

func TestUpsertHasNoBoardIDFlag(t *testing.T) {
	// upsert's key is the ticket, and a row being created has no board id yet:
	// Notion assigns it. Offering --id there would be offering to address
	// something that does not exist.
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaWithIDJSON))
	}, idProfileYAML)

	if code := executeArgs([]string{
		"upsert", "--id", "BDF-271", "--config", cfg,
	}); code != ExitUsage {
		t.Errorf("exit code = %d, want %d for an unknown flag", code, ExitUsage)
	}
}
```

- [ ] **Step 2: Esegui i test e verifica che falliscano**

Run: `go test ./internal/cli/ -run 'TestSetByBoardID|TestSetRejectsTwoWays|TestUpsertHasNoBoardID' -v`
Expected: FAIL su `TestSetByBoardIDUpdatesTheRow` con `unknown flag: --id`.

Gli altri due **passano già** e devono restare verdi: un flag sconosciuto arriva comunque a `SetFlagErrorFunc` → exit 2, quindi né `TestSetRejectsTwoWaysOfAddressingAtOnce` né `TestUpsertHasNoBoardIDFlag` distinguono il prima dal dopo. Il secondo diventa significativo solo **dopo** l'implementazione, quando `--id` esiste su `set` e deve continuare a non esistere su `upsert`.

- [ ] **Step 3: Aggiungi il campo e il flag**

In `internal/cli/upsert.go`, dentro `type writeFlags struct`, dopo `pageID`:

```go
	// boardID is the unique_id address ("BDF-271"). Deliberately absent from
	// fields() below: like pageID it says which row to write, never what to
	// write into it — a unique_id column is assigned by Notion and read-only.
	boardID string
```

E in `bindWithPageID`, aggiornando il commento della funzione:

```go
// bindWithPageID is set's binding: --ticket, --page-id and --id are alternate,
// mutually exclusive ways to address an existing row, and exactly one of them
// is required.
func (wf *writeFlags) bindWithPageID(cmd *cobra.Command) {
	// ... le righe già presenti per ticket e page-id ...
	cmd.Flags().StringVar(&wf.boardID, "id", "",
		"board id of the row, as Notion shows it (e.g. BDF-271, or just 271); "+
			"needs an id property mapped in the profile")
	wf.bindShared(cmd)
	cmd.MarkFlagsMutuallyExclusive("ticket", "page-id", "id")
	cmd.MarkFlagsOneRequired("ticket", "page-id", "id")
}
```

`writeFlags.fields()` **non** cambia: `boardID` non è un valore da scrivere.

- [ ] **Step 4: Aggiungi il ramo a `set`**

In `internal/cli/set.go`, sostituisci l'`if` sul `page-id` con:

```go
			var res service.Result
			// See get.go: branch on Changed, not on the value, so an empty
			// --page-id or --id still takes its own path.
			switch {
			case cmd.Flags().Changed("id"):
				res, err = svc.DryRun(wf.dryRun).SetByUniqueID(cmd.Context(), wf.boardID, wf.fields(), body)
			case cmd.Flags().Changed("page-id"):
				res, err = svc.DryRun(wf.dryRun).SetByID(cmd.Context(), wf.pageID, wf.fields(), body)
			default:
				res, err = svc.DryRun(wf.dryRun).Set(cmd.Context(), wf.fields(), body)
			}
```

E aggiorna il `Long`:

```go
		Long: "Update an existing row, addressed by --ticket, --id or --page-id; fail\n" +
			"if it does not exist.\n\n" +
			"Use this when a missing row is a symptom worth surfacing rather than\n" +
			"a row to create.",
```

- [ ] **Step 5: Esegui i test e verifica che passino**

Run: `go test ./internal/cli/ -v`
Expected: PASS, compreso `TestUpsertHasNoBoardIDFlag` — se fallisce, il flag è finito in `bindShared` (condiviso con `upsert`) invece che in `bindWithPageID` (solo di `set`).

- [ ] **Step 6: Commit**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -race
git add internal/cli/upsert.go internal/cli/set.go internal/cli/upsert_test.go
git commit -m "feat(set): update a row addressed by its board id"
```

---

### Task 14: L'id nell'output umano di `get` e `list`

**Files:**
- Modify: `internal/cli/output.go` (accanto a `assigneeSuffix` e `prioritySuffix`)
- Modify: `internal/cli/get.go` (le due `cmd.Printf` del ramo umano)
- Modify: `internal/cli/list.go:15-17` (i due formati) e il ciclo di stampa
- Test: `internal/cli/get_test.go`, `internal/cli/list_test.go`

**Interfaces:**
- Consumes: `config.Properties.ID` (Task 5), `PropertyValue.Text` per `unique_id` (Task 2)
- Produces: `idPrefix(p notion.Page, props config.Properties) string`

**Perché esiste questo task:** senza di esso `BDF-271` vive solo dentro `--json`, e `notion-track get --id BDF-271` risponderebbe senza mai mostrare `BDF-271`. Un identificatore che le persone si dicono a voce e che nessuna superficie umana stampa è una contraddizione con la ragione stessa della feature.

- [ ] **Step 1: Scrivi i test che falliscono**

In fondo a `internal/cli/get_test.go`:

```go
func TestGetPrintsTheBoardIDForHumans(t *testing.T) {
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaWithIDJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowWithIDJSON + `],"has_more":false}`))
	}, idProfileYAML)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	if !strings.Contains(out, "BDF-271") {
		t.Errorf("output = %q, want it to show the board id", out)
	}
}

func TestGetHumanOutputIsUnchangedWithoutTheIDRole(t *testing.T) {
	// The profile from withStubbedAPI maps no id role: the line must come out
	// byte-identical to what it was before this feature existed, which is the
	// rule the two suffixes in output.go already follow.
	cfg := withStubbedAPI(t, stubbedRow)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"get", "--ticket", "BDF-231", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	if strings.HasPrefix(out, " ") {
		t.Errorf("output = %q, want no leading padding when the id role is unmapped", out)
	}
}
```

Prima di scriverlo, apri `internal/cli/get_test.go` e guarda come i test umani già esistenti (`stubbedRow` a riga 386) montano il caso: riusa quello che c'è.

In fondo a `internal/cli/list_test.go`, allineandoti a `stubForList` che il file già ha:

```go
func TestListPrintsTheBoardIDForHumans(t *testing.T) {
	cfg := withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaWithIDJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowWithIDJSON + `],"has_more":false}`))
	}, idProfileYAML)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{"list", "--config", cfg}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})
	// The id leads the row: names read down the left edge of a list.
	if !strings.HasPrefix(out, "BDF-271") {
		t.Errorf("output = %q, want the row to start with the board id", out)
	}
}
```

- [ ] **Step 2: Esegui i test e verifica che falliscano**

Run: `go test ./internal/cli/ -run 'TestGetPrintsTheBoardID|TestListPrintsTheBoardID|TestGetHumanOutputIsUnchanged' -v`
Expected: FAIL sui primi due (l'id non compare); il terzo passa già ed è il guard-rail della non-regressione.

- [ ] **Step 3: Scrivi l'helper**

In `internal/cli/output.go`, subito dopo `prioritySuffix`:

```go
// idPrefix formats a row's board id for human-readable output.
//
// A prefix and not a suffix, unlike assigneeSuffix and prioritySuffix above: it
// is the row's name, and names read down the left edge of a list. Padded to a
// fixed width so list's columns stay aligned across rows whose ids differ in
// length ("BDF-9" and "BDF-1234"); get prints one row, where the padding costs
// nothing.
//
// Empty when the role is unmapped, which is what keeps every existing profile's
// output byte-identical — the same rule the two suffixes follow.
func idPrefix(p notion.Page, props config.Properties) string {
	if id := p.Properties[props.ID].Text; id != "" {
		return fmt.Sprintf("%-10s ", id)
	}
	return ""
}
```

Aggiungi `"fmt"` agli import di `internal/cli/output.go`.

- [ ] **Step 4: Usalo in `get`**

In `internal/cli/get.go`, nel ramo umano, entrambe le `cmd.Printf` guadagnano un `%s` iniziale alimentato da `idPrefix`:

```go
			id := idPrefix(page, profile.Properties)
			if ticketIsTitle(profile.Properties) {
				cmd.Printf("%s%s  [%s]%s%s\n  %s\n",
					id, page.Properties[profile.Properties.Title].Text, status,
					priority, assignee, page.URL)
				return nil
			}
			cmd.Printf("%s%s  %s  [%s]%s%s\n  %s\n",
				id,
				page.Properties[profile.Properties.Ticket].Text,
				page.Properties[profile.Properties.Title].Text,
				status, priority, assignee, page.URL)
			return nil
```

- [ ] **Step 5: Usalo in `list`**

In `internal/cli/list.go`, i due formati guadagnano un `%s` iniziale:

```go
const (
	listRowFormat       = "%s%-20s %-40s [%s]%s%s\n"
	listMergedRowFormat = "%s%-61s [%s]%s%s\n"
)
```

Estendi il commento sopra di essi, che già spiega la stessa regola per i due suffissi: il prefisso è un segmento `%s` iniziale, vuoto quando il ruolo non è mappato, così le colonne restano byte-identiche per i profili senza il ruolo.

E nel ciclo di stampa:

```go
			for _, p := range pages {
				status := p.Properties[profile.Properties.Status].Text
				priority := prioritySuffix(p, profile.Properties)
				assignee := assigneeSuffix(p, profile.Properties)
				id := idPrefix(p, profile.Properties)
				if merged {
					cmd.Printf(listMergedRowFormat, id,
						p.Properties[profile.Properties.Title].Text, status, priority, assignee)
					continue
				}
				cmd.Printf(listRowFormat, id,
					p.Properties[profile.Properties.Ticket].Text,
					p.Properties[profile.Properties.Title].Text,
					status, priority, assignee)
			}
```

- [ ] **Step 6: Esegui i test e verifica che passino**

Run: `go test ./internal/cli/ -v`
Expected: PASS, compresi tutti i test umani preesistenti di `get` e `list` — se uno di quelli fallisce, il prefisso non sta tornando `""` per un profilo che non mappa il ruolo.

- [ ] **Step 7: Commit**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -race
git add internal/cli/output.go internal/cli/get.go internal/cli/list.go internal/cli/get_test.go internal/cli/list_test.go
git commit -m "feat(output): show the board id in get and list"
```

---

### Task 15: Documentazione

**Files:**
- Modify: `README.md`
- Modify: `README.it.md`
- Modify: `skills/notion-track/SKILL.md`
- Modify: `skills/notion-track/README.md`

**Interfaces:**
- Consumes: tutta la superficie dei Task 6, 11, 12, 13, 14
- Produces: niente codice

**Dove sono i file:** la skill dell'agente sta in `skills/notion-track/`, alla radice del repo — **non** sotto `.claude/skills/`, che contiene solo `settings.local.json`. E `README.it.md` è la traduzione speculare di `README.md`, sezione per sezione: aggiornarne uno solo produce esattamente la documentazione che si contraddice, cioè il difetto che questo task esiste per evitare.

- [ ] **Step 1: Verifica cosa dicono oggi i quattro file**

```bash
grep -rn "page-id\|--ticket\|unique_id\|BDF-" README.md README.it.md skills/notion-track/
```

Serve a trovare **ogni** punto che elenca i modi di indirizzare una riga: mancarne uno lascia una documentazione che contraddice il codice, che è il difetto emerso nella review della action di release in questo repo.

- [ ] **Step 2: Aggiorna `README.md`**

Dove elenca i modi di indirizzare una riga, aggiungi `--id` come terzo, con esempi eseguibili:

```bash
# by exact title (or ticket key)
notion-track get --ticket "Sistemare visualizzazione da telefono"

# by board id, the one people say out loud
notion-track set --id BDF-271 --status "Fatto"

# by Notion page id or URL, stable across renames
notion-track get --page-id https://notion.so/...
```

Dove documenta `init`, aggiungi `--id-prop` fra i ruoli opzionali, accanto a `--due-prop`, `--assignee-prop` e `--priority-prop`.

Dove mostra l'output di `--json`, aggiungi la chiave `id` all'esempio, **in prima posizione** come nella struct.

Se afferma da qualche parte che l'id `BDF-NN` non è leggibile dalla CLI o che l'API Notion non consente di filtrare per `unique_id`, **correggi l'affermazione**: l'API filtra per `unique_id`, ed era la CLI a non contemplare il tipo.

- [ ] **Step 3: Riporta le stesse modifiche in `README.it.md`**

Le stesse sezioni, tradotte. `README.it.md` è speculare: se una sezione esiste in uno e non nell'altro, è già una divergenza da segnalare, non da allargare.

- [ ] **Step 4: Aggiorna la skill**

In `skills/notion-track/SKILL.md` (la sezione che documenta `--ticket`/`--page-id`) e in `skills/notion-track/README.md`: `--id` fra i modi di indirizzare, `--id-prop` fra i ruoli di `init`, la chiave `id` nell'esempio JSON. Il testo deve dire **la stessa cosa** dei README: una skill che descrive una superficie diversa da quella documentata è peggio di una skill non aggiornata, perché sembra autorevole.

- [ ] **Step 5: Rileggi le quattro modifiche fianco a fianco**

```bash
git diff README.md README.it.md skills/notion-track/
```

Verifica che non ci sia nessuna affermazione presente in uno e contraddetta in un altro, e che ogni comando negli esempi sia eseguibile così com'è scritto.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && go vet ./... && go build ./... && go test ./... -race
git add README.md README.it.md skills/notion-track/
git commit -m "docs: document addressing a row by its board id"
```

---

## Verifica finale

Prima di chiudere il branch, dalla radice del repo:

```bash
gofmt -l .                                          # nessun output
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go test ./... -race
go build ./...
```

Più le due verifiche che questo lavoro si è dato:

```bash
# §3 dello spec: il codice di scrittura non conosce il ruolo id.
# Deve stampare nulla — se stampa qualcosa, il ruolo è finito nel payload.
grep -n "unique_id\|Properties\.ID" internal/tracker/payload.go
```

E, come specchio, l'elenco dei file non-test che il tipo lo conoscono:

```bash
grep -rl "unique_id" --include="*.go" internal/ | grep -v _test | sort
```

Devono comparire almeno `internal/notion/datasource.go`, `internal/notion/query.go`, `internal/tracker/mapping.go`, `internal/cli/init.go`, `internal/tui/wizard.go`, `internal/service/doctor.go` e `internal/service/service.go`. **Non** deve comparire `internal/tracker/payload.go`.

Due avvertenze su questo controllo, perché è più grossolano di quanto sembri. `internal/tracker/uniqueid.go` **non** compare, pur essendo il cuore del parsing: parla di `BDF-271`, non del nome del tipo Notion. E compaiono file che il tipo lo nominano solo in un commento (`internal/notion/types.go`, `internal/cli/upsert.go`). Il grep è un promemoria, non un test: l'unica riga che vincola davvero è quella su `payload.go`.
