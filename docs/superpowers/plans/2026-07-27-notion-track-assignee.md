# Assignee Role Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Aggiungere a notion-track un quinto ruolo mappato, `assignee`, che legge, scrive, filtra e svuota il referente di un task su una colonna Notion di tipo `select`.

**Architecture:** Il ruolo segue esattamente la strada dei quattro ruoli esistenti (`ticket`, `status`, `title`, `due`): un nome di colonna nel profilo, un campo in `tracker.Fields`, un ramo in `BuildProperties`, un flag nei comandi di scrittura. L'unico pezzo nuovo è `tracker.ResolveOption`, una funzione pura che trasforma un valore parziale (`mirko`) nel valore canonico dell'opzione (`Mirko Spinato`); la risoluzione avviene nel service prima di costruire il payload, così `--dry-run` e la scrittura reale vedono lo stesso valore.

**Tech Stack:** Go 1.x, `github.com/spf13/cobra` (CLI), `gopkg.in/yaml.v3` (config), `github.com/charmbracelet/bubbletea` (TUI), `github.com/modelcontextprotocol/go-sdk/mcp` (MCP), `net/http/httptest` (test del client).

## Global Constraints

- **Spec di riferimento:** `docs/superpowers/specs/2026-07-27-notion-track-assignee-design.md`. Ogni decisione non coperta dal piano si risolve leggendo lo spec, non improvvisando.
- **Versione API Notion:** `2026-03-11` (`internal/notion/version.go:19`). Non cambiarla.
- **Nessuna regressione per i profili esistenti:** con `assignee` non mappato, ogni comando deve produrre l'output di oggi byte per byte. Ogni task che tocca l'output ha un test di non-regressione che lo verifica.
- **Regola "vuoto = lascia stare":** un campo non passato non finisce mai nel payload (`internal/tracker/payload.go:31-36`). L'unica eccezione al mondo è `Unassign`, che è un campo booleano separato proprio per non violarla.
- **Errori tipizzati, mai stringhe:** `exitCodeFor` (`internal/cli/output.go:47`) è l'unico punto che decide l'exit code, e `apply`/MCP non passano da cobra. Ogni errore nuovo è un tipo o una sentinella, mai riconosciuto per prefisso di messaggio.
- **Lingua:** codice, commenti, messaggi d'errore e README in inglese. I documenti in `docs/superpowers/` in italiano.
- **Commenti:** questo repo spiega il *perché*, non il *cosa*. Un commento che riformula la riga sotto va tolto; una scelta non ovvia va motivata.
- **Gate CI, tutti e cinque, prima della PR:** `gofmt -l .` (deve stampare nulla), `staticcheck ./...`, `go vet ./...`, `go test ./...`, `go build ./...`.
- **Branch:** `feat/assignee-role`, già creato, con lo spec committato.

---

### Task 1: Il ruolo nel profilo

**Files:**
- Modify: `internal/config/config.go:35-51` (struct `Properties`, struct `Profile`), `:26-31` (costanti env), `:133-159` (`Resolve`)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Properties.Assignee string`, `config.Profile.Me string`, `config.MeEnv = "NOTION_TRACK_ME"`. Ogni task successivo legge il nome della colonna da `profile.Properties.Assignee` e l'identità da `profile.Me`.

- [ ] **Step 1: Write the failing test**

In `internal/config/config_test.go`:

```go
func TestResolveMeFromEnv(t *testing.T) {
	cfg := &Config{
		DefaultProfile: "default",
		Profiles: map[string]Profile{
			"default": {
				DataSourceID: "ds1",
				Me:           "Marco Arnulfo",
				Properties:   Properties{Ticket: "Nome task", Status: "Stato", Title: "Nome task", Assignee: "Referente"},
			},
		},
	}

	t.Run("the file value is used when the env is unset", func(t *testing.T) {
		// Explicitly unset, not merely "not set here": whoever runs the suite
		// may well have exported it — the README tells them to.
		t.Setenv(MeEnv, "")
		p, err := cfg.Resolve("")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if p.Me != "Marco Arnulfo" {
			t.Errorf("Me = %q, want %q", p.Me, "Marco Arnulfo")
		}
		if p.Properties.Assignee != "Referente" {
			t.Errorf("Assignee = %q, want %q", p.Properties.Assignee, "Referente")
		}
	})

	t.Run("the env wins over the file", func(t *testing.T) {
		t.Setenv(MeEnv, "Mirko Spinato")
		p, err := cfg.Resolve("")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if p.Me != "Mirko Spinato" {
			t.Errorf("Me = %q, want the env value", p.Me)
		}
	})

	t.Run("an empty env does not blank the file value", func(t *testing.T) {
		t.Setenv(MeEnv, "")
		p, err := cfg.Resolve("")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if p.Me != "Marco Arnulfo" {
			t.Errorf("Me = %q, want the file value to survive an empty env", p.Me)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestResolveMeFromEnv -v`
Expected: FAIL, non compila — `unknown field Me in struct literal`, `unknown field Assignee`, `undefined: MeEnv`.

- [ ] **Step 3: Write minimal implementation**

In `internal/config/config.go`, aggiungere alle costanti env (dopo `DataSourceEnv`):

```go
	// MeEnv names the person "--assignee me" stands for. It is an environment
	// variable first and a profile field second on purpose: config.yml is meant
	// to be committed and shared (see Credentials), so an identity stored there
	// would be everyone's identity — and would silently assign tasks to whoever
	// committed the file.
	MeEnv = "NOTION_TRACK_ME"
```

In `Properties`:

```go
	// Assignee is the column naming who a row belongs to. Optional: a board
	// that tracks nobody in particular simply leaves it unmapped.
	Assignee string `yaml:"assignee,omitempty"`
```

In `Profile`:

```go
	// Me is the assignee value "--assignee me" resolves to, overridden by
	// MeEnv. Optional.
	Me string `yaml:"me,omitempty"`
```

In `Resolve`, accanto agli altri override d'ambiente:

```go
	if v := os.Getenv(MeEnv); v != "" {
		p.Me = v
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS, inclusi i test preesistenti (nessuno tocca i campi nuovi).

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): add the assignee property and the me identity"
```

---

### Task 2: `ResolveOption`, la risoluzione di un valore parziale

**Files:**
- Create: `internal/tracker/assignee.go`
- Test: `internal/tracker/assignee_test.go`

**Interfaces:**
- Produces:
  - `func ResolveOption(field, query string, options []string) (string, error)` — restituisce il valore canonico dell'opzione, o un errore.
  - `type AmbiguousOptionError struct { Field, Value string; Matches []string }` con metodo `Error() string`.
- Consumes: niente. È una funzione pura: nessuna rete, nessun tipo del progetto.

- [ ] **Step 1: Write the failing test**

Create `internal/tracker/assignee_test.go`:

```go
package tracker

import (
	"errors"
	"testing"
)

func TestResolveOption(t *testing.T) {
	options := []string{"Andrea Ghidara", "Marco Arnulfo", "Mirko Spinato"}

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"exact match", "Mirko Spinato", "Mirko Spinato"},
		{"case-insensitive exact match", "mirko spinato", "Mirko Spinato"},
		{"unique prefix", "mirko", "Mirko Spinato"},
		{"unique substring anywhere", "spinato", "Mirko Spinato"},
		{"substring in the middle of a word", "ghid", "Andrea Ghidara"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveOption("assignee", tt.query, options)
			if err != nil {
				t.Fatalf("ResolveOption(%q): %v", tt.query, err)
			}
			if got != tt.want {
				t.Errorf("ResolveOption(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestResolveOptionExactBeatsSubstring(t *testing.T) {
	// "Marco" is an option in its own right AND a substring of another one.
	// The exact match must win, or naming an option exactly would be
	// impossible whenever a longer option contains it.
	options := []string{"Marco", "Marco Arnulfo"}
	got, err := ResolveOption("assignee", "Marco", options)
	if err != nil {
		t.Fatalf("ResolveOption: %v", err)
	}
	if got != "Marco" {
		t.Errorf("ResolveOption = %q, want the exact option %q", got, "Marco")
	}
}

func TestResolveOptionAmbiguous(t *testing.T) {
	options := []string{"Andrea Ghidara", "Marco Arnulfo", "Mirko Spinato"}

	_, err := ResolveOption("assignee", "ar", options)
	var ambiguous *AmbiguousOptionError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("error = %v, want *AmbiguousOptionError", err)
	}
	if len(ambiguous.Matches) != 2 {
		t.Fatalf("Matches = %v, want the two names containing \"ar\"", ambiguous.Matches)
	}
	// The message must name every candidate: telling the user it is ambiguous
	// without saying between what leaves them guessing.
	for _, want := range []string{"Andrea Ghidara", "Marco Arnulfo"} {
		if !strings.Contains(ambiguous.Error(), want) {
			t.Errorf("Error() = %q, want it to name %q", ambiguous.Error(), want)
		}
	}
}

func TestResolveOptionUnknown(t *testing.T) {
	options := []string{"Andrea Ghidara", "Marco Arnulfo", "Mirko Spinato"}

	_, err := ResolveOption("assignee", "Marko", options)
	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *ValidationError so it exits 2", err)
	}
	if invalid.Field != "assignee" {
		t.Errorf("Field = %q, want %q", invalid.Field, "assignee")
	}
}

func TestResolveOptionEdgeCases(t *testing.T) {
	options := []string{"Andrea Ghidara"}

	t.Run("an empty query is not a match-anything wildcard", func(t *testing.T) {
		if _, err := ResolveOption("assignee", "", options); err == nil {
			t.Fatal("ResolveOption(\"\") = nil error, want a failure")
		}
	})

	t.Run("no options at all", func(t *testing.T) {
		if _, err := ResolveOption("assignee", "Andrea", nil); err == nil {
			t.Fatal("ResolveOption with no options = nil error, want a failure")
		}
	})
}
```

Import del file di test: `errors`, `strings`, `testing`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tracker/ -run TestResolveOption -v`
Expected: FAIL, non compila — `undefined: ResolveOption`, `undefined: AmbiguousOptionError`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/tracker/assignee.go`:

```go
package tracker

import (
	"fmt"
	"strings"
)

// AmbiguousOptionError marks a value that matched more than one option.
//
// It is a distinct type from ValidationError because the two failures deserve
// different fixes: an unknown value needs the list of what exists, an ambiguous
// one needs more characters of a name the user already got right.
type AmbiguousOptionError struct {
	Field   string
	Value   string
	Matches []string
}

func (e *AmbiguousOptionError) Error() string {
	return fmt.Sprintf("ambiguous %s %q: matches %s\n  fix: pass more of the name",
		e.Field, e.Value, strings.Join(e.Matches, ", "))
}

// ResolveOption turns a value the user typed into the exact option the data
// source carries, so that "mirko" reaches Notion as "Mirko Spinato".
//
// Three passes, each stricter than useful on its own, tried in order and
// stopping at the first that yields exactly one candidate: exact, exact
// case-insensitive, then substring case-insensitive. The order is what makes an
// option that is a substring of another one still reachable — without it,
// "Marco" could never be selected on a board that also has "Marco Arnulfo".
//
// Ambiguity is never resolved by picking one: on a column of people's names,
// guessing wrong assigns someone else's work to someone, and a second word of
// the name costs the user nothing.
func ResolveOption(field, query string, options []string) (string, error) {
	// An empty query would match every option as a substring. Everywhere else
	// in notion-track an empty value means "leave this alone" and never reaches
	// a resolver at all; reaching one is a caller's bug, and answering it with
	// an arbitrary option would be the worst possible recovery.
	if query == "" {
		return "", &ValidationError{Field: field, Value: query, Allowed: options}
	}

	for _, match := range []func(option string) bool{
		func(option string) bool { return option == query },
		func(option string) bool { return strings.EqualFold(option, query) },
		func(option string) bool {
			return strings.Contains(strings.ToLower(option), strings.ToLower(query))
		},
	} {
		var found []string
		for _, option := range options {
			if match(option) {
				found = append(found, option)
			}
		}
		switch len(found) {
		case 1:
			return found[0], nil
		case 0:
			continue // a looser pass may still find it
		default:
			return "", &AmbiguousOptionError{Field: field, Value: query, Matches: found}
		}
	}
	return "", &ValidationError{Field: field, Value: query, Allowed: options}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tracker/ -v`
Expected: PASS. Nota: il test usa `*ValidationError` con `Field` valorizzato, che il Task 3 renderà generico — a questo punto `ValidationError` esiste già con quel campo (`internal/tracker/validate.go:12-16`), quindi compila.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/assignee.go internal/tracker/assignee_test.go
git commit -m "feat(tracker): resolve a partial assignee value to the exact option"
```

---

### Task 3: `ValidateOption`, il campo non è più sempre "status"

**Files:**
- Modify: `internal/tracker/validate.go:23-37`
- Test: `internal/tracker/validate_test.go`

**Interfaces:**
- Produces: `func ValidateOption(field, value string, allowed []string) error`. `ValidateStatus(value, allowed)` resta come wrapper con `field = "status"`, così nessun chiamante esistente cambia.

- [ ] **Step 1: Write the failing test**

In `internal/tracker/validate_test.go`, aggiungere:

```go
func TestValidateOptionNamesTheField(t *testing.T) {
	err := ValidateOption("assignee", "Marko", []string{"Marco Arnulfo"})

	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *ValidationError", err)
	}
	if invalid.Field != "assignee" {
		t.Errorf("Field = %q, want %q", invalid.Field, "assignee")
	}
}

func TestValidateStatusStillSaysStatus(t *testing.T) {
	// The wrapper exists so that every existing caller and every existing
	// message stays exactly as it was.
	err := ValidateStatus("Nope", []string{"Da fare"})

	var invalid *ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *ValidationError", err)
	}
	if invalid.Field != "status" {
		t.Errorf("Field = %q, want %q", invalid.Field, "status")
	}
}
```

Se `errors` non è già importato nel file di test, aggiungerlo.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tracker/ -run TestValidateOption -v`
Expected: FAIL, non compila — `undefined: ValidateOption`.

- [ ] **Step 3: Write minimal implementation**

In `internal/tracker/validate.go`, sostituire `ValidateStatus` con:

```go
// ValidateOption checks a value against the options read from the server.
//
// This matters most for select properties: Notion creates an unknown select
// option on write, so an unchecked typo becomes a permanent bogus value in the
// database. Status properties reject unknown values server-side, but failing
// here still produces a far better message than the API's.
//
// field names the role in the message, and — more importantly — travels into
// ValidationError, which is what maps this failure onto exit code 2 for callers
// that never touch cobra (apply, the MCP server).
//
// An empty allowed list disables the check.
func ValidateOption(field, value string, allowed []string) error {
	if len(allowed) == 0 || slices.Contains(allowed, value) {
		return nil
	}
	return &ValidationError{Field: field, Value: value, Allowed: allowed}
}

// ValidateStatus is ValidateOption for the status role.
func ValidateStatus(value string, allowed []string) error {
	return ValidateOption("status", value, allowed)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tracker/ -v`
Expected: PASS, inclusi i test preesistenti di `ValidateStatus`.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/validate.go internal/tracker/validate_test.go
git commit -m "refactor(tracker): let the validation error name any role, not just status"
```

---

### Task 4: Il payload — scrivere e svuotare il referente

**Files:**
- Modify: `internal/tracker/payload.go:10-96`
- Test: `internal/tracker/payload_test.go`

**Interfaces:**
- Produces:
  - `tracker.Fields` guadagna, **in fondo e in quest'ordine**: `Assignee string`, `Unassign bool`. L'ordine conta: `internal/cli/mcp.go` converte `mcp.Fields` in `tracker.Fields` e il Task 17 rende quella conversione diretta, il che richiede struct identici campo per campo.
  - `var ErrConflictingAssignee = errors.New(...)`.
- Consumes: `ValidateOption` (Task 3).

- [ ] **Step 1: Write the failing test**

In `internal/tracker/payload_test.go`, aggiungere:

```go
func TestBuildPropertiesAssignee(t *testing.T) {
	schema := &notion.Schema{Properties: map[string]notion.Property{
		"Nome task": {Name: "Nome task", Type: "title"},
		"Referente": {Name: "Referente", Type: "select", Options: []string{"Marco Arnulfo", "Mirko Spinato"}},
	}}
	props := config.Properties{Title: "Nome task", Assignee: "Referente"}

	t.Run("writes the select", func(t *testing.T) {
		got, err := BuildProperties(Fields{Assignee: "Mirko Spinato"}, props, schema)
		if err != nil {
			t.Fatalf("BuildProperties: %v", err)
		}
		want := map[string]any{"select": map[string]string{"name": "Mirko Spinato"}}
		if !reflect.DeepEqual(got["Referente"], want) {
			t.Errorf("Referente = %#v, want %#v", got["Referente"], want)
		}
	})

	t.Run("unassign clears the select with an explicit null", func(t *testing.T) {
		got, err := BuildProperties(Fields{Unassign: true}, props, schema)
		if err != nil {
			t.Fatalf("BuildProperties: %v", err)
		}
		value, ok := got["Referente"]
		if !ok {
			t.Fatal("Referente is absent from the payload; clearing must be explicit")
		}
		want := map[string]any{"select": nil}
		if !reflect.DeepEqual(value, want) {
			t.Errorf("Referente = %#v, want %#v", value, want)
		}
	})

	t.Run("neither passed leaves the column alone", func(t *testing.T) {
		got, err := BuildProperties(Fields{Status: ""}, props, schema)
		if err != nil {
			t.Fatalf("BuildProperties: %v", err)
		}
		if _, ok := got["Referente"]; ok {
			t.Error("Referente is in the payload but nothing asked to write it")
		}
	})

	t.Run("both passed is a conflict", func(t *testing.T) {
		_, err := BuildProperties(Fields{Assignee: "Mirko Spinato", Unassign: true}, props, schema)
		if !errors.Is(err, ErrConflictingAssignee) {
			t.Fatalf("error = %v, want ErrConflictingAssignee", err)
		}
	})

	t.Run("unmapped role with a value is an error, not a silent drop", func(t *testing.T) {
		unmapped := config.Properties{Title: "Nome task"}
		_, err := BuildProperties(Fields{Assignee: "Mirko Spinato"}, unmapped, schema)
		if err == nil {
			t.Fatal("BuildProperties = nil error, want a failure naming --assignee-prop")
		}
	})

	t.Run("unmapped role with unassign is an error too", func(t *testing.T) {
		unmapped := config.Properties{Title: "Nome task"}
		_, err := BuildProperties(Fields{Unassign: true}, unmapped, schema)
		if err == nil {
			t.Fatal("BuildProperties = nil error, want a failure naming --assignee-prop")
		}
	})

	t.Run("a value the column does not offer is rejected", func(t *testing.T) {
		_, err := BuildProperties(Fields{Assignee: "Marko"}, props, schema)
		var invalid *ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("error = %v, want *ValidationError", err)
		}
		if invalid.Field != "assignee" {
			t.Errorf("Field = %q, want %q", invalid.Field, "assignee")
		}
	})
}
```

Import da verificare nel file di test: `errors` e `reflect` mancano entrambi oggi; `config` e `notion` ci sono già.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tracker/ -run TestBuildPropertiesAssignee -v`
Expected: FAIL, non compila — `unknown field Assignee in struct literal of type Fields`.

- [ ] **Step 3: Write minimal implementation**

In `internal/tracker/payload.go`, estendere `Fields`:

```go
// Fields are the values a user asked to write. Empty strings mean "leave this
// property alone", which is what makes `set --status` a partial update.
//
// The field order is load-bearing: internal/cli converts mcp.Fields to this
// type directly, which only compiles while the two stay identical.
type Fields struct {
	Ticket   string
	Title    string
	Status   string
	Due      string
	Assignee string
	// Unassign clears the assignee column. It is a separate field rather than a
	// reserved Assignee value ("none", "") because every empty value in this
	// struct already means "leave this alone", and one exception to that rule
	// would be one too many.
	Unassign bool
}
```

Aggiungere la sentinella, sopra `BuildProperties`:

```go
// ErrConflictingAssignee marks a request that both sets and clears the
// assignee. The CLI's flags already exclude each other, but apply and the MCP
// server never touch cobra: the rule has to live where every caller passes.
var ErrConflictingAssignee = errors.New("cannot set and clear the assignee in the same write")
```

(Aggiungere `"errors"` agli import.)

Nel corpo di `BuildProperties`, dopo la dichiarazione di `add` e prima delle chiamate, sostituire il blocco finale con:

```go
	if f.Assignee != "" && f.Unassign {
		return nil, ErrConflictingAssignee
	}

	// Title first: when the ticket key *is* the title column, the ticket value
	// must win over a separately supplied title.
	if err := add("title", props.Title, f.Title); err != nil {
		return nil, err
	}
	if err := add("ticket", props.Ticket, f.Ticket); err != nil {
		return nil, err
	}
	if err := add("status", props.Status, f.Status); err != nil {
		return nil, err
	}
	if err := add("due", props.Due, f.Due); err != nil {
		return nil, err
	}
	if err := add("assignee", props.Assignee, f.Assignee); err != nil {
		return nil, err
	}
	// Clearing is the one write that has to happen for an empty value, so it
	// cannot go through add, which is built around the opposite rule.
	if f.Unassign {
		if props.Assignee == "" {
			return nil, fmt.Errorf(
				"unassign was requested but no assignee property is mapped; " +
					"run 'notion-track init --assignee-prop <name>' to map it")
		}
		if _, ok := schema.Properties[props.Assignee]; !ok {
			return nil, fmt.Errorf(
				"property %q is configured but does not exist in the data source; "+
					"run 'notion-track doctor' to see the current schema", props.Assignee)
		}
		out[props.Assignee] = map[string]any{"select": nil}
	}
	return out, nil
```

Infine, dentro `add`, cambiare la validazione del ramo `select` (e solo quello) perché nomini il ruolo giusto:

```go
		case "select":
			if err := ValidateOption(role, value, prop.Options); err != nil {
				return err
			}
			out[propName] = map[string]any{"select": map[string]string{"name": value}}
```

Il ramo `status` resta su `ValidateStatus`. Aggiornare il commento di `role` in cima ad `add`, che oggi dice che il ruolo serve solo al messaggio d'errore: ora entra anche nella validazione.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tracker/ -v`
Expected: PASS, inclusi i test esistenti di `BuildProperties`.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/payload.go internal/tracker/payload_test.go
git commit -m "feat(tracker): build and clear the assignee select"
```

---

### Task 5: `GuessMapping` riconosce `Referente`

**Files:**
- Modify: `internal/tracker/mapping.go:11-70`
- Test: `internal/tracker/mapping_test.go`

**Interfaces:**
- Consumes: `config.Properties.Assignee` (Task 1).
- Produces: `GuessMapping` popola `out.Assignee` quando il nome è riconoscibile.

- [ ] **Step 1: Write the failing test**

In `internal/tracker/mapping_test.go`, aggiungere:

```go
func TestGuessMappingAssignee(t *testing.T) {
	t.Run("recognises a known name", func(t *testing.T) {
		schema := &notion.Schema{Properties: map[string]notion.Property{
			"Nome task": {Name: "Nome task", Type: "title"},
			"Stato":     {Name: "Stato", Type: "status"},
			"Referente": {Name: "Referente", Type: "select"},
			"Urgenza":   {Name: "Urgenza", Type: "select"},
		}}
		got := GuessMapping(schema)
		if got.Assignee != "Referente" {
			t.Errorf("Assignee = %q, want %q", got.Assignee, "Referente")
		}
		if got.Status != "Stato" {
			t.Errorf("Status = %q, want %q", got.Status, "Stato")
		}
	})

	t.Run("does not guess an unrecognisable lone select", func(t *testing.T) {
		// A wrong guess the user waves through is worse than a question, and
		// this role is optional: leaving it blank costs nothing.
		schema := &notion.Schema{Properties: map[string]notion.Property{
			"Nome task": {Name: "Nome task", Type: "title"},
			"Stato":     {Name: "Stato", Type: "status"},
			"Urgenza":   {Name: "Urgenza", Type: "select"},
		}}
		if got := GuessMapping(schema); got.Assignee != "" {
			t.Errorf("Assignee = %q, want no guess", got.Assignee)
		}
	})

	t.Run("never reuses the column taken by status", func(t *testing.T) {
		// Both roles draw from selects. With one select named like a status,
		// status claims it and assignee must not claim it too.
		schema := &notion.Schema{Properties: map[string]notion.Property{
			"Nome task": {Name: "Nome task", Type: "title"},
			"Stato":     {Name: "Stato", Type: "select"},
		}}
		got := GuessMapping(schema)
		if got.Status != "Stato" {
			t.Fatalf("Status = %q, want %q", got.Status, "Stato")
		}
		if got.Assignee == got.Status {
			t.Errorf("Assignee = %q, want it not to reuse the status column", got.Assignee)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tracker/ -run TestGuessMappingAssignee -v`
Expected: FAIL — `got.Assignee` è sempre `""` (il campo esiste dal Task 1, quindi compila e fallisce sulle assert).

- [ ] **Step 3: Write minimal implementation**

In `internal/tracker/mapping.go`, aggiungere ai nomi riconosciuti:

```go
	assigneeNames = []string{
		"assignee", "owner", "referente", "persona", "responsabile",
		"assegnatario", "incaricato",
	}
```

E, dopo il calcolo di `out.Ticket`:

```go
	// Assignee is guessed by name only: pick's "the only candidate wins" rule
	// is right for a required role, where one plausible column *is* the answer,
	// but wrong for an optional one — guessing "Urgenza" as the assignee is
	// worse than guessing nothing, and nothing is a perfectly good outcome
	// here. The status column is excluded because both roles draw from
	// selects, and a board with a single select must not end up with the same
	// column in two roles.
	for _, name := range byType["select"] {
		if name == out.Status {
			continue
		}
		for _, known := range assigneeNames {
			if strings.EqualFold(name, known) {
				out.Assignee = name
				break
			}
		}
		if out.Assignee != "" {
			break
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tracker/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/mapping.go internal/tracker/mapping_test.go
git commit -m "feat(tracker): guess the assignee column by name"
```

---

### Task 6: I filtri che mancano

**Files:**
- Modify: `internal/notion/query.go:12-26`
- Test: `internal/notion/query_test.go`

**Interfaces:**
- Produces:
  - `func IsEmptyFilter(property, propType string) Filter`
  - `func AndFilter(filters ...Filter) Filter` — restituisce il singolo filtro quando ne riceve uno solo, `nil` quando non ne riceve nessuno.

- [ ] **Step 1: Write the failing test**

In `internal/notion/query_test.go`, aggiungere:

```go
func TestIsEmptyFilter(t *testing.T) {
	got := IsEmptyFilter("Referente", "select")
	want := Filter{"property": "Referente", "select": map[string]bool{"is_empty": true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("IsEmptyFilter = %#v, want %#v", got, want)
	}
}

func TestAndFilter(t *testing.T) {
	status := EqualsFilter("Stato", "status", "Da fare")
	assignee := EqualsFilter("Referente", "select", "Mirko Spinato")

	t.Run("no filters at all", func(t *testing.T) {
		if got := AndFilter(); got != nil {
			t.Errorf("AndFilter() = %#v, want nil so QueryPages returns every row", got)
		}
	})

	t.Run("one filter is passed through unwrapped", func(t *testing.T) {
		// Wrapping a lone filter in {"and": [...]} would work, but it changes
		// the request every existing caller sends for no gain.
		if got := AndFilter(status); !reflect.DeepEqual(got, status) {
			t.Errorf("AndFilter(one) = %#v, want the filter itself", got)
		}
	})

	t.Run("two filters compound", func(t *testing.T) {
		got := AndFilter(status, assignee)
		clauses, ok := got["and"].([]Filter)
		if !ok {
			t.Fatalf("AndFilter(two)[\"and\"] = %#v, want []Filter", got["and"])
		}
		if len(clauses) != 2 {
			t.Fatalf("clauses = %d, want 2", len(clauses))
		}
	})

	t.Run("nil filters are skipped", func(t *testing.T) {
		if got := AndFilter(nil, status, nil); !reflect.DeepEqual(got, status) {
			t.Errorf("AndFilter(nil, one, nil) = %#v, want the filter itself", got)
		}
	})
}
```

Import da aggiungere al file di test: `reflect`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/notion/ -run 'TestIsEmptyFilter|TestAndFilter' -v`
Expected: FAIL, non compila — `undefined: IsEmptyFilter`, `undefined: AndFilter`.

- [ ] **Step 3: Write minimal implementation**

In `internal/notion/query.go`, dopo `EqualsFilter`:

```go
// IsEmptyFilter matches rows where a property carries no value. Like
// EqualsFilter, the body is keyed by property type, so the caller passes the
// type from the schema.
func IsEmptyFilter(property, propType string) Filter {
	return Filter{
		"property": property,
		propType:   map[string]bool{"is_empty": true},
	}
}

// AndFilter combines filters into a compound one, skipping the nil entries a
// caller building a filter from optional flags naturally produces.
//
// A lone filter is returned unwrapped, and no filters at all yield nil (which
// QueryPages reads as "every row"): both keep the request identical to what
// callers sent before compounding existed.
func AndFilter(filters ...Filter) Filter {
	var present []Filter
	for _, f := range filters {
		if f != nil {
			present = append(present, f)
		}
	}
	switch len(present) {
	case 0:
		return nil
	case 1:
		return present[0]
	default:
		return Filter{"and": present}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/notion/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/notion/query.go internal/notion/query_test.go
git commit -m "feat(notion): add the is_empty and compound and filters"
```

---

### Task 7: Il service risolve il valore, una volta sola, per tutti

**Files:**
- Modify: `internal/service/service.go:19-50` (sentinelle), `:216-267` (`Upsert`), `:273-311` (`Set`), `:405-431` (`SetByID`)
- Test: `internal/service/service_test.go`

**Interfaces:**
- Produces:
  - `var ErrEmptyAssignee = errors.New(...)`, `var ErrNoIdentity = errors.New(...)`
  - `func (s *Service) resolveAssignee(ctx context.Context, f tracker.Fields) (tracker.Fields, error)` — non esportata; sostituisce `f.Assignee` con il valore canonico e traduce `me`.
- Consumes: `tracker.ResolveOption` (Task 2), `config.Profile.Me` (Task 1).

- [ ] **Step 1: Write the failing test**

Prima le fixture. In `internal/service/service_test.go`, estendere le due costanti già presenti (`:19-33`) con la colonna:

- in `schemaJSON`, dentro `properties`:
  `"Referente":{"name":"Referente","type":"select","select":{"options":[{"name":"Andrea Ghidara"},{"name":"Marco Arnulfo"},{"name":"Mirko Spinato"}]}}`
- in `rowJSON`, dentro `properties`:
  `"Referente":{"type":"select","select":{"name":"Mirko Spinato"}}`

I test esistenti non leggono quella colonna, quindi l'aggiunta non ne tocca nessuno.

Poi i due helper e i test:

```go
// assigneeProfile is testProfile with the role mapped, and an optional identity.
func assigneeProfile(me string) config.Profile {
	p := testProfile()
	p.Properties.Assignee = "Referente"
	p.Me = me
	return p
}

// capturingRoutes is routes() plus a copy of the properties payload of the last
// write: an assignee test asserts on what was sent, not on what came back.
func capturingRoutes(t *testing.T, queryResults string, written *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/pages" ||
			r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/v1/pages/") {
			var body struct {
				Properties map[string]any `json:"properties"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding the write payload: %v", err)
			}
			*written = body.Properties
		}
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + queryResults + `],"has_more":false}`))
		default:
			w.Write([]byte(rowJSON))
		}
	}))
}

func TestUpsertResolvesAssignee(t *testing.T) {
	var written map[string]any
	srv := capturingRoutes(t, "", &written)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile(""))
	_, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231", Assignee: "mirko"}, nil)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, _ := json.Marshal(written["Referente"])
	if want := `{"select":{"name":"Mirko Spinato"}}`; string(got) != want {
		t.Errorf("Referente = %s, want %s", got, want)
	}
}

func TestSetByIDResolvesAssignee(t *testing.T) {
	// The third write path is the one a refactor forgets: it never queries by
	// ticket, so it does not share Set's code.
	var written map[string]any
	srv := capturingRoutes(t, rowJSON, &written)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile(""))
	_, err := s.SetByID(context.Background(), "page1", tracker.Fields{Assignee: "mirko"}, nil)
	if err != nil {
		t.Fatalf("SetByID: %v", err)
	}

	got, _ := json.Marshal(written["Referente"])
	if want := `{"select":{"name":"Mirko Spinato"}}`; string(got) != want {
		t.Errorf("Referente = %s, want %s", got, want)
	}
}

func TestResolveAssigneeMe(t *testing.T) {
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()
	ctx := context.Background()

	t.Run("uses the profile identity", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("Marco Arnulfo"))
		f, err := s.resolveAssignee(ctx, tracker.Fields{Assignee: "me"})
		if err != nil {
			t.Fatalf("resolveAssignee: %v", err)
		}
		if f.Assignee != "Marco Arnulfo" {
			t.Errorf("Assignee = %q, want %q", f.Assignee, "Marco Arnulfo")
		}
	})

	t.Run("a partial identity resolves too", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("mirko"))
		f, err := s.resolveAssignee(ctx, tracker.Fields{Assignee: "me"})
		if err != nil {
			t.Fatalf("resolveAssignee: %v", err)
		}
		if f.Assignee != "Mirko Spinato" {
			t.Errorf("Assignee = %q, want %q", f.Assignee, "Mirko Spinato")
		}
	})

	t.Run("no identity configured", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile(""))
		_, err := s.resolveAssignee(ctx, tracker.Fields{Assignee: "me"})
		if !errors.Is(err, ErrNoIdentity) {
			t.Fatalf("error = %v, want ErrNoIdentity", err)
		}
	})
}

func TestResolveAssigneeEdges(t *testing.T) {
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()
	ctx := context.Background()

	t.Run("an absent assignee is left alone", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile(""))
		if _, err := s.resolveAssignee(ctx, tracker.Fields{Status: "Fatto"}); err != nil {
			t.Fatalf("an absent assignee must not fail: %v", err)
		}
	})

	t.Run("an unknown name fails with the allowed values", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile(""))
		_, err := s.resolveAssignee(ctx, tracker.Fields{Assignee: "Marko"})
		var invalid *tracker.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("error = %v, want *tracker.ValidationError", err)
		}
	})

	t.Run("unmapped role with a value", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()) // no Assignee
		_, err := s.resolveAssignee(ctx, tracker.Fields{Assignee: "mirko"})
		if err == nil {
			t.Fatal("resolveAssignee = nil error, want a failure naming --assignee-prop")
		}
	})
}
```

Nota su `--assignee ""`: la stringa vuota non arriva mai qui come "svuota" (§4 dello spec), e `resolveAssignee` la lascia passare intatta perché per `BuildProperties` significa "lascia stare". Il rifiuto di `--assignee ""` avviene nel Task 10, dove cobra sa che il flag è stato passato.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/ -run 'Assignee' -v`
Expected: FAIL, non compila — `svc.resolveAssignee undefined`, `undefined: ErrNoIdentity`.

- [ ] **Step 3: Write minimal implementation**

In `internal/service/service.go`, accanto alle altre sentinelle:

```go
// ErrNoIdentity means "--assignee me" was used without anything saying who
// "me" is.
var ErrNoIdentity = errors.New(
	"--assignee me needs to know who you are\n" +
		"  fix: export NOTION_TRACK_ME=<name>, or run 'notion-track init --me <name>'")

// ErrEmptyAssignee mirrors ErrEmptyTicket: cobra reports that a flag was
// passed, never that it carries a value, so `--assignee ""` would otherwise
// reach BuildProperties and be read as "leave this alone" — silently doing
// nothing in a command the user wrote specifically to change something.
var ErrEmptyAssignee = errors.New("assignee must not be empty; use --unassign to clear it")
```

E il resolver:

```go
// resolveAssignee turns what the user typed into the exact option the column
// carries, and turns "me" into the configured identity.
//
// It runs in the service rather than inside BuildProperties because --dry-run
// builds its plan from the same Fields (see planFor): resolving deeper would
// make the plan print "mirko" while the write performs "Mirko Spinato" — a dry
// run that does not describe the write it is describing.
func (s *Service) resolveAssignee(ctx context.Context, f tracker.Fields) (tracker.Fields, error) {
	if f.Assignee == "" {
		return f, nil
	}

	query := f.Assignee
	if query == "me" {
		if s.profile.Me == "" {
			return f, ErrNoIdentity
		}
		query = s.profile.Me
	}

	name := s.profile.Properties.Assignee
	if name == "" {
		return f, fmt.Errorf(
			"assignee was set to %q but no assignee property is mapped; "+
				"run 'notion-track init --assignee-prop <name>' to map it", f.Assignee)
	}
	schema, err := s.Schema(ctx)
	if err != nil {
		return f, err
	}
	prop, ok := schema.Properties[name]
	if !ok {
		return f, fmt.Errorf(
			"property %q is configured but does not exist in the data source; "+
				"run 'notion-track doctor' to see the current schema", name)
	}

	resolved, err := tracker.ResolveOption("assignee", query, prop.Options)
	if err != nil {
		return f, err
	}
	f.Assignee = resolved
	return f, nil
}
```

Poi chiamarlo nei **tre** percorsi di scrittura, subito dopo il controllo del ticket vuoto e prima di qualunque altra cosa che usi `f`:

In `Upsert`, dopo `if f.Ticket == "" { … }`:

```go
	f, err := s.resolveAssignee(ctx, f)
	if err != nil {
		return Result{}, err
	}
```

Le righe che seguono restano invariate: `matches, err := s.findByTicket(...)` è ancora legale con `err` già dichiarato, perché `matches` è una variabile nuova e Go richiede solo che ce ne sia almeno una. Non convertirla in `=`, o `matches` risulta non dichiarata.

In `Set`, nello stesso punto. In `SetByID`, come prima istruzione del corpo (è il terzo percorso di scrittura, e l'unico che non passa mai da una ricerca per ticket: è quello che una modifica frettolosa dimentica).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/service/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/
git commit -m "feat(service): resolve the assignee before every write, including me"
```

---

### Task 8: Il dry-run dice anche cosa svuota

**Files:**
- Modify: `internal/service/plan.go:16-57`, `internal/cli/body.go:86-110` (`emitPlan`)
- Test: `internal/service/plan_test.go`

Le tre chiamate a `planFor` in `service.go` non cambiano: la firma resta la stessa, e `props`/`f` che già riceve bastano per entrambe le aggiunte.

**Interfaces:**
- Produces: `Plan.Cleared []string` con tag `json:"cleared,omitempty"`; `planFor` guadagna il parametro `props config.Properties` già presente e usa `f.Unassign`.

- [ ] **Step 1: Write the failing test**

In `internal/service/plan_test.go`:

```go
func TestPlanForAssignee(t *testing.T) {
	props := config.Properties{
		Ticket: "Nome task", Title: "Nome task", Status: "Stato", Assignee: "Referente",
	}

	t.Run("a set names the column and the canonical value", func(t *testing.T) {
		plan := planFor("updated", "page-1", "https://notion.so/x",
			tracker.Fields{Assignee: "Mirko Spinato"}, props, 0)

		var found bool
		for _, p := range plan.Properties {
			if p.Column == "Referente" && p.Value == "Mirko Spinato" {
				found = true
			}
		}
		if !found {
			t.Errorf("Properties = %#v, want Referente -> Mirko Spinato", plan.Properties)
		}
	})

	t.Run("a clear is reported, not silently dropped", func(t *testing.T) {
		// Without this the most destructive write in the feature produces an
		// empty plan: "would update", and nothing else.
		plan := planFor("updated", "page-1", "", tracker.Fields{Unassign: true}, props, 0)

		if len(plan.Cleared) != 1 || plan.Cleared[0] != "Referente" {
			t.Errorf("Cleared = %#v, want [Referente]", plan.Cleared)
		}
	})

	t.Run("nothing cleared when nothing asked", func(t *testing.T) {
		plan := planFor("updated", "page-1", "", tracker.Fields{Status: "Fatto"}, props, 0)
		if len(plan.Cleared) != 0 {
			t.Errorf("Cleared = %#v, want empty", plan.Cleared)
		}
	})
}
```

Nessun test CLI in questo task: il flag `--unassign` non esiste ancora (nasce nel Task 10), quindi un test che lo invoca qui fallirebbe con `unknown flag` qualunque cosa faccia l'implementazione. Il caso end-to-end del dry-run vive nel Task 10, dove il flag esiste.

Import da aggiungere a `plan_test.go`: `config` (e `tracker`, se non c'è già).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/ -run TestPlanForAssignee -v`
Expected: FAIL — `plan.Cleared undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/service/plan.go`, aggiungere il campo a `Plan`:

```go
	// Cleared names the columns a write would empty. Properties cannot carry
	// them: it reports what would be *written*, and skips empty values by
	// design, which would make a clear invisible in the one command that exists
	// to make writes visible.
	Cleared []string `json:"cleared,omitempty"`
```

In `planFor`, aggiungere l'assignee alla slice dei valori che verrebbero scritti (`plan.go:44-49`) — senza questa riga `set --assignee X --dry-run` stampa un piano vuoto, che è lo stesso difetto di `--unassign` visto dall'altro lato:

```go
	for _, p := range []PlannedProperty{
		{Column: props.Ticket, Value: f.Ticket},
		{Column: props.Title, Value: f.Title},
		{Column: props.Status, Value: f.Status},
		{Column: props.Due, Value: f.Due},
		{Column: props.Assignee, Value: f.Assignee},
	} {
```

e, prima del `return`:

```go
	if f.Unassign && props.Assignee != "" {
		plan.Cleared = append(plan.Cleared, props.Assignee)
	}
```

In `internal/cli/body.go`, dentro `emitPlan`, dopo il ciclo su `plan.Properties`:

```go
	for _, column := range plan.Cleared {
		cmd.Printf("  %-20s %s\n", "clear", column)
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/service/ -run TestPlanForAssignee -v && go test ./... `
Expected: PASS. `emitPlan` non ha test propri che cambino: la riga nuova stampa solo quando `Cleared` non è vuoto, e nessun test esistente produce un piano con quel campo.

- [ ] **Step 5: Commit**

```bash
git add internal/service/plan.go internal/service/plan_test.go internal/cli/body.go
git commit -m "feat(service): report cleared columns in the dry-run plan"
```

---

### Task 9: `List` prende un filtro, non uno status

**Files:**
- Modify: `internal/service/service.go:433-453` (`List`)
- Test: `internal/service/service_test.go`

**Interfaces:**
- Produces:
  - `type ListFilter struct { Status, Assignee string; Unassigned bool }`
  - `func (s *Service) List(ctx context.Context, f ListFilter) ([]notion.Page, error)` — **firma cambiata**: ogni chiamante (CLI `list`, adapter MCP, adapter TUI browse) va aggiornato nello stesso commit o il pacchetto non compila.
- Consumes: `notion.AndFilter`, `notion.IsEmptyFilter` (Task 6), `tracker.ResolveOption` (Task 2).

- [ ] **Step 1: Write the failing test**

In `internal/service/service_test.go`, un helper che cattura il filtro spedito, e i casi:

```go
// filterRoutes answers schema and query, keeping the raw filter of the last
// query so a test can assert on the request rather than on canned rows.
func filterRoutes(t *testing.T, sent *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1/query" {
			var body struct {
				Filter map[string]any `json:"filter"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding the query: %v", err)
			}
			*sent = body.Filter
			w.Write([]byte(`{"results":[],"has_more":false}`))
			return
		}
		w.Write([]byte(schemaJSON))
	}))
}

func TestListFilters(t *testing.T) {
	var sent map[string]any
	srv := filterRoutes(t, &sent)
	defer srv.Close()
	client := notion.New("t", notion.WithBaseURL(srv.URL))
	s := New(client, assigneeProfile("Marco Arnulfo"))
	ctx := context.Background()

	t.Run("no filter at all", func(t *testing.T) {
		sent = nil
		if _, err := s.List(ctx, ListFilter{}); err != nil {
			t.Fatalf("List: %v", err)
		}
		if sent != nil {
			t.Errorf("filter = %#v, want none so every row comes back", sent)
		}
	})

	t.Run("status only is unchanged", func(t *testing.T) {
		sent = nil
		if _, err := s.List(ctx, ListFilter{Status: "Fatto"}); err != nil {
			t.Fatalf("List: %v", err)
		}
		if sent["property"] != "Stato" {
			t.Errorf("filter = %#v, want the plain status filter", sent)
		}
	})

	t.Run("assignee compounds with status", func(t *testing.T) {
		sent = nil
		if _, err := s.List(ctx, ListFilter{Status: "Fatto", Assignee: "mirko"}); err != nil {
			t.Fatalf("List: %v", err)
		}
		clauses, ok := sent["and"].([]any)
		if !ok || len(clauses) != 2 {
			t.Fatalf("filter = %#v, want a compound of two", sent)
		}
	})

	t.Run("a partial name is resolved before it is sent", func(t *testing.T) {
		sent = nil
		if _, err := s.List(ctx, ListFilter{Assignee: "mirko"}); err != nil {
			t.Fatalf("List: %v", err)
		}
		got := sent["select"].(map[string]any)["equals"]
		if got != "Mirko Spinato" {
			t.Errorf("filter value = %v, want the canonical option", got)
		}
	})

	t.Run("me resolves in a filter too", func(t *testing.T) {
		sent = nil
		if _, err := s.List(ctx, ListFilter{Assignee: "me"}); err != nil {
			t.Fatalf("List: %v", err)
		}
		got := sent["select"].(map[string]any)["equals"]
		if got != "Marco Arnulfo" {
			t.Errorf("filter value = %v, want the configured identity", got)
		}
	})

	t.Run("unassigned", func(t *testing.T) {
		sent = nil
		if _, err := s.List(ctx, ListFilter{Unassigned: true}); err != nil {
			t.Fatalf("List: %v", err)
		}
		if got := sent["select"].(map[string]any)["is_empty"]; got != true {
			t.Errorf("filter = %#v, want is_empty", sent)
		}
	})

	t.Run("assignee and unassigned together is a conflict", func(t *testing.T) {
		_, err := s.List(ctx, ListFilter{Assignee: "mirko", Unassigned: true})
		if !errors.Is(err, ErrConflictingListFilter) {
			t.Fatalf("error = %v, want ErrConflictingListFilter", err)
		}
	})

	t.Run("filtering on an unmapped role fails clearly", func(t *testing.T) {
		unmapped := New(client, testProfile())
		if _, err := unmapped.List(ctx, ListFilter{Assignee: "mirko"}); err == nil {
			t.Fatal("List = nil error, want a failure naming --assignee-prop")
		}
	})
}
```

Nota sui tipi nelle assert: il filtro viene riletto da JSON, quindi ogni valore annidato è `map[string]any` e ogni lista è `[]any` — non i tipi Go con cui è stato costruito.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/ -run TestListFilters -v`
Expected: FAIL, non compila — `undefined: ListFilter`, e `List` accetta ancora una stringa.

- [ ] **Step 3: Write minimal implementation**

In `internal/service/service.go`, sostituire `List`:

```go
// ListFilter is what List narrows on. Every field is optional; the zero value
// returns every row.
//
// A struct rather than a growing parameter list: the CLI, the MCP server and
// the browsing TUI all call List, and each new way to narrow a listing would
// otherwise change three signatures.
type ListFilter struct {
	Status     string
	Assignee   string
	Unassigned bool
}

// ErrConflictingListFilter marks a listing narrowed both to somebody and to
// nobody.
var ErrConflictingListFilter = errors.New("cannot filter by assignee and by unassigned at the same time")

// List returns rows matching f.
func (s *Service) List(ctx context.Context, f ListFilter) ([]notion.Page, error) {
	if f.Assignee != "" && f.Unassigned {
		return nil, ErrConflictingListFilter
	}
	schema, err := s.Schema(ctx)
	if err != nil {
		return nil, err
	}

	var clauses []notion.Filter

	if f.Status != "" {
		name := s.profile.Properties.Status
		prop, ok := schema.Properties[name]
		if !ok {
			return nil, fmt.Errorf(
				"status property %q does not exist in the data source; run 'notion-track doctor'", name)
		}
		if err := tracker.ValidateStatus(f.Status, prop.Options); err != nil {
			return nil, err
		}
		clauses = append(clauses, notion.EqualsFilter(name, prop.Type, f.Status))
	}

	if f.Assignee != "" || f.Unassigned {
		name := s.profile.Properties.Assignee
		if name == "" {
			return nil, fmt.Errorf(
				"cannot filter by assignee: no assignee property is mapped; " +
					"run 'notion-track init --assignee-prop <name>' to map it")
		}
		prop, ok := schema.Properties[name]
		if !ok {
			return nil, fmt.Errorf(
				"assignee property %q does not exist in the data source; run 'notion-track doctor'", name)
		}
		if f.Unassigned {
			clauses = append(clauses, notion.IsEmptyFilter(name, prop.Type))
		} else {
			// Reuse resolveAssignee so that "me" and partial names mean exactly
			// the same thing when reading as when writing.
			resolved, err := s.resolveAssignee(ctx, tracker.Fields{Assignee: f.Assignee})
			if err != nil {
				return nil, err
			}
			clauses = append(clauses, notion.EqualsFilter(name, prop.Type, resolved.Assignee))
		}
	}

	return s.client.QueryPages(ctx, s.profile.DataSourceID, notion.AndFilter(clauses...))
}
```

Aggiornare **tutti e quattro** i chiamanti, o il pacchetto non compila (elenco verificato con `grep -rn "\.List(" internal/`):

| Chiamante | Come diventa |
|---|---|
| `internal/cli/list.go:28` | `svc.List(cmd.Context(), service.ListFilter{Status: status})` — provvisorio, il Task 12 aggiunge i flag |
| `internal/cli/mcp.go:80` | `a.svc.List(ctx, service.ListFilter{Status: status})` — provvisorio, il Task 17 cambia la firma dell'adapter |
| `internal/cli/browse.go:48` | idem; la firma di `boardAdapter.List` resta `(status string)`, quindi `browse_test.go` non si tocca |
| `internal/service/service_test.go:358` | `s.List(ctx, ListFilter{Status: "In corso"})` — **test esistente**, si romperebbe in silenzio |

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... `
Expected: PASS su tutti i pacchetti.

- [ ] **Step 5: Commit**

```bash
git add internal/service/ internal/cli/
git commit -m "feat(service): filter listings by assignee, or by having none"
```

---

### Task 10: I flag di scrittura

**Files:**
- Modify: `internal/cli/upsert.go:9-78` (`writeFlags`, `bindShared`, `fields`), `internal/cli/output.go:47-123` (`exitCodeFor`)
- Test: `internal/cli/get_test.go:71-80` (le fixture condivise dello Step 1), `internal/cli/upsert_test.go`
- **Non** modificare `internal/cli/set.go`: eredita tutto da `bindShared`.

**Interfaces:**
- Produces: `--assignee`, `--unassign` su `upsert` e `set` (quest'ultimo li eredita da `bindShared` senza modifiche a `set.go`).
- Consumes: `tracker.Fields.Assignee/Unassign` (Task 4), `service.ErrEmptyAssignee`, `ErrNoIdentity` (Task 7), `tracker.AmbiguousOptionError` (Task 2), `tracker.ErrConflictingAssignee` (Task 4), `service.ErrConflictingListFilter` (Task 9).

- [ ] **Step 1: Write the failing test**

Prima le fixture CLI, in `internal/cli/get_test.go` accanto a quelle esistenti (`:71-80`), perché i Task 10-13 le condividono tutti.

Estendere `cliSchemaJSON` con la colonna:
`"Referente":{"name":"Referente","type":"select","select":{"options":[{"name":"Andrea Ghidara"},{"name":"Marco Arnulfo"},{"name":"Mirko Spinato"}]}}`

ed estendere `cliRowJSON` con:
`"Referente":{"type":"select","select":{"name":"Mirko Spinato"}}`

Poi due profili, accanto a `titleKeyedProfile`:

```go
// assigneeProfile maps the role and configures an identity, for the tests that
// exercise --assignee, --unassign and "me".
const assigneeProfile = `schema_version: 1
default_profile: work
profiles:
  work:
    database_id: db1
    data_source_id: ds1
    status_type: status
    me: Marco Arnulfo
    properties:
      ticket: Ticket
      status: Stato
      title: Name
      assignee: Referente
`

// assigneeProfileNoIdentity maps the role but says nothing about who "me" is:
// what a teammate who never exported NOTION_TRACK_ME has.
const assigneeProfileNoIdentity = `schema_version: 1
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
      assignee: Referente
`
```

Il test, in `internal/cli/upsert_test.go`:

```go
// stubForAssignee answers schema, query and write, keeping the properties
// payload of the write so a test can assert on what reached Notion.
//
// NOTION_TRACK_ME is cleared deliberately: config.Resolve lets it override the
// profile, so a developer who followed the README and exported it would
// otherwise have their own identity leak into every test here — and the one
// test that asserts "me" is *not* configured would silently pass for the wrong
// reason.
func stubForAssignee(t *testing.T, profile string, written *map[string]any) string {
	t.Helper()
	t.Setenv(config.MeEnv, "")
	return withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/data_sources/ds1":
			w.Write([]byte(cliSchemaJSON))
		case r.URL.Path == "/v1/data_sources/ds1/query":
			w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
		default:
			var body struct {
				Properties map[string]any `json:"properties"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			*written = body.Properties
			w.Write([]byte(cliRowJSON))
		}
	}, profile)
}

func TestSetWritesTheResolvedAssignee(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--assignee", "mirko", "--config", cfg,
	}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	got, _ := json.Marshal(written["Referente"])
	if want := `{"select":{"name":"Mirko Spinato"}}`; string(got) != want {
		t.Errorf("Referente = %s, want %s", got, want)
	}
}

func TestSetUnassignClearsTheColumn(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--unassign", "--config", cfg,
	}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	got, _ := json.Marshal(written["Referente"])
	if want := `{"select":null}`; string(got) != want {
		t.Errorf("Referente = %s, want %s", got, want)
	}
}

func TestSetMeUsesTheConfiguredIdentity(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--assignee", "me", "--config", cfg,
	}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	got, _ := json.Marshal(written["Referente"])
	if want := `{"select":{"name":"Marco Arnulfo"}}`; string(got) != want {
		t.Errorf("Referente = %s, want %s", got, want)
	}
}

func TestAssigneeUsageErrorsAllExitTwo(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		args    []string
	}{
		{"both set and clear", assigneeProfile, []string{"--assignee", "mirko", "--unassign"}},
		{"an empty value", assigneeProfile, []string{"--assignee", ""}},
		{"an unknown name", assigneeProfile, []string{"--assignee", "Marko"}},
		{"an ambiguous name", assigneeProfile, []string{"--assignee", "ar"}},
		{"me with no identity", assigneeProfileNoIdentity, []string{"--assignee", "me"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var written map[string]any
			cfg := stubForAssignee(t, tt.profile, &written)

			args := append([]string{"set", "--ticket", "BDF-231"}, tt.args...)
			args = append(args, "--config", cfg)
			if code := executeArgs(args); code != ExitUsage {
				t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
			}
			if written != nil {
				t.Errorf("a usage error still wrote %v", written)
			}
		})
	}
}
```

Poi il caso che esce **1** e non 2, asserito perché sia una scelta e non una svista (spec §11, ultime due righe):

```go
func TestAssigneeOnAnUnmappedRoleExitsOne(t *testing.T) {
	// Not ExitUsage: the "role not mapped" message is the same untyped one the
	// other four roles have always produced, and typing it for assignee alone
	// would either change --due's exit code too or treat one role differently
	// from the rest for the identical condition. Both are worse than a 1.
	t.Setenv(config.MeEnv, "")
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--assignee", "mirko", "--config", cfg,
	}); code != ExitError {
		t.Fatalf("exit code = %d, want %d (ExitError)", code, ExitError)
	}
}
```

E il caso end-to-end del dry-run, che vive qui perché è qui che nasce `--unassign`:

```go
func TestUnassignDryRunSaysWhatItWouldClear(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{
			"set", "--ticket", "BDF-231", "--unassign", "--dry-run", "--config", cfg,
		}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	if !strings.Contains(out, "Referente") {
		t.Errorf("output = %q, want it to name the column it would clear", out)
	}
	if written != nil {
		t.Errorf("a dry run wrote %v", written)
	}
}
```

Import da aggiungere al file di test: `encoding/json`, `strings`, e `config` per `config.MeEnv`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'Assignee|Unassign' -v`
Expected: FAIL — `unknown flag: --assignee`.

- [ ] **Step 3: Write minimal implementation**

In `internal/cli/upsert.go`, aggiungere a `writeFlags`:

```go
	assignee string
	unassign bool
```

In `bindShared`:

```go
	cmd.Flags().StringVar(&wf.assignee, "assignee", "",
		"who the row belongs to; a partial name is enough when it is unambiguous, "+
			"and 'me' stands for NOTION_TRACK_ME")
	cmd.Flags().BoolVar(&wf.unassign, "unassign", false, "clear the assignee")
	cmd.MarkFlagsMutuallyExclusive("assignee", "unassign")
```

In `fields`:

```go
func (wf *writeFlags) fields() tracker.Fields {
	return tracker.Fields{
		Ticket: wf.ticket, Title: wf.title, Status: wf.status, Due: wf.due,
		Assignee: wf.assignee, Unassign: wf.unassign,
	}
}
```

Il rifiuto di `--assignee ""` va dove cobra sa che il flag è stato **passato** — `MarkFlagRequired` e le regole dei gruppi verificano che ci sia, mai che porti un valore. Va registrato in `bindShared`, che riceve `cmd` ed è l'unico punto attraversato sia da `upsert` sia da `set`: così `set.go` resta invariato, come lo spec §15 dichiara.

```go
	// A PreRunE rather than a check inside each RunE: bindShared is the one
	// place both write commands pass through, and duplicating the guard is how
	// one of the two eventually loses it.
	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Flags().Changed("assignee") && wf.assignee == "" {
			return service.ErrEmptyAssignee
		}
		return nil
	}
```

Verificare, prima di scrivere: nessuno dei due comandi definisce già un `PreRunE` (oggi non lo fa nessuno) — se in futuro ne comparisse uno, questo lo sovrascriverebbe in silenzio.

In `internal/cli/output.go`, dentro `exitCodeFor`: aggiungere `ambiguous` al blocco `var (…)` che dichiara `dup`, `invalid` e `apiErr` (`:58-62`),

```go
	var (
		dup       *tracker.DuplicateError
		invalid   *tracker.ValidationError
		ambiguous *tracker.AmbiguousOptionError
		apiErr    *notion.APIError
	)
```

e due `case` nello `switch` che segue, subito dopo `case errors.As(err, &invalid):` — vicini al ruolo che condividono, così un lettore trova insieme tutte le "il valore passato non va bene":

```go
	case errors.As(err, &ambiguous):
		return ExitUsage
	// Every way of getting the assignee wrong is a mistake the user can fix by
	// rewriting the command, which is exactly what exit code 2 means.
	case errors.Is(err, service.ErrEmptyAssignee),
		errors.Is(err, service.ErrNoIdentity),
		errors.Is(err, service.ErrConflictingListFilter),
		errors.Is(err, tracker.ErrConflictingAssignee):
		return ExitUsage
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/upsert.go internal/cli/output.go internal/cli/upsert_test.go
git commit -m "feat(cli): add --assignee and --unassign to upsert and set"
```

---

### Task 11: Leggere il referente

**Files:**
- Modify: `internal/cli/get.go:18-93`
- Test: `internal/cli/get_test.go`

**Interfaces:**
- Produces: `pageJSON.Assignee string` con tag `json:"assignee"`. **Vincolo:** `mcp.Row` dovrà restare identico (Task 17), perché `internal/cli/mcp.go:60` converte l'uno nell'altro direttamente.

- [ ] **Step 1: Write the failing test**

In `internal/cli/get_test.go`:

```go
// stubForGet answers schema and query with the shared fixtures.
func stubForGet(t *testing.T, profile string) string {
	t.Helper()
	return withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	}, profile)
}

func TestGetJSONCarriesTheAssignee(t *testing.T) {
	cfg := stubForGet(t, assigneeProfile)

	out := captureStdout(t, func() {
		if code := executeArgs([]string{
			"get", "--ticket", "BDF-231", "--json", "--config", cfg,
		}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	var row map[string]any
	if err := json.Unmarshal([]byte(out), &row); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	if row["assignee"] != "Mirko Spinato" {
		t.Errorf("assignee = %v, want %q", row["assignee"], "Mirko Spinato")
	}
}

func TestGetJSONAlwaysCarriesTheAssigneeKey(t *testing.T) {
	// A script reading .assignee must not have to branch on the key existing,
	// so it is present even for a profile that never mapped the role.
	cfg := stubForGet(t, assigneeProfileNoIdentity)

	out := captureStdout(t, func() {
		executeArgs([]string{"get", "--ticket", "BDF-231", "--json", "--config", cfg})
	})

	var row map[string]any
	if err := json.Unmarshal([]byte(out), &row); err != nil {
		t.Fatalf("output is not JSON: %s", out)
	}
	if _, ok := row["assignee"]; !ok {
		t.Errorf("the assignee key is missing from %v", row)
	}
}

func TestGetHumanOutputShowsTheAssignee(t *testing.T) {
	cfg := stubForGet(t, assigneeProfile)

	out := captureStdout(t, func() {
		executeArgs([]string{"get", "--ticket", "BDF-231", "--config", cfg})
	})

	if !strings.Contains(out, "@Mirko Spinato") {
		t.Errorf("output = %q, want it to name the assignee", out)
	}
}

func TestGetHumanOutputIsUnchangedWithoutTheRole(t *testing.T) {
	// Non-regression: a profile written before this feature must print exactly
	// what it printed before, down to the byte.
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})

	out := captureStdout(t, func() {
		executeArgs([]string{"get", "--ticket", "BDF-231", "--config", cfg})
	})

	want := "BDF-231  Hardening  [Fatto]\n  https://notion.so/page1\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}
```

`strings` e `json` vanno negli import se non ci sono già.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'TestGetJSON|TestGetHumanOutput' -v`
Expected: FAIL — la chiave `assignee` non esiste nel JSON.

- [ ] **Step 3: Write minimal implementation**

In `internal/cli/get.go`, aggiungere il campo a `pageJSON` **in fondo**, per non spostare le chiavi esistenti nell'output indentato:

```go
type pageJSON struct {
	Ticket         string `json:"ticket"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	PageID         string `json:"page_id"`
	URL            string `json:"url"`
	LastEditedTime string `json:"last_edited_time"`
	// Assignee is empty both when nobody is assigned and when the role is not
	// mapped: the key is always present so a script never has to branch on it.
	Assignee string `json:"assignee"`
}
```

In `toPageJSON`:

```go
		Assignee:       p.Properties[props.Assignee].Text,
```

E l'output umano, sostituendo i due `cmd.Printf` finali:

```go
			assignee := ""
			if name := page.Properties[profile.Properties.Assignee].Text; name != "" {
				assignee = "  @" + name
			}
			if ticketIsTitle(profile.Properties) {
				cmd.Printf("%s  [%s]%s\n  %s\n",
					page.Properties[profile.Properties.Title].Text, status, assignee, page.URL)
				return nil
			}
			cmd.Printf("%s  %s  [%s]%s\n  %s\n",
				page.Properties[profile.Properties.Ticket].Text,
				page.Properties[profile.Properties.Title].Text,
				status, assignee, page.URL)
```

Nota: `p.Properties[""]` su una mappa restituisce lo zero value, quindi un ruolo non mappato dà `""` senza controlli aggiuntivi — è la stessa proprietà su cui si reggono già gli altri campi (`get.go:14-17`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/get.go internal/cli/get_test.go
git commit -m "feat(cli): read the assignee in get, in both output forms"
```

---

### Task 12: `list --assignee` e `list --unassigned`

**Files:**
- Modify: `internal/cli/list.go:6-70`
- Test: `internal/cli/list_test.go` (o il file di test che già copre `list`)

**Interfaces:**
- Consumes: `service.ListFilter` (Task 9), `pageJSON.Assignee` (Task 11).

- [ ] **Step 1: Write the failing test**

```go
// stubForList answers schema and query, keeping the filter of the last query.
func stubForList(t *testing.T, profile string, sent *map[string]any) string {
	t.Helper()
	return withStubbedAPIProfile(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		var body struct {
			Filter map[string]any `json:"filter"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		*sent = body.Filter
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	}, profile)
}

func TestListFiltersByAssignee(t *testing.T) {
	var sent map[string]any
	cfg := stubForList(t, assigneeProfile, &sent)

	captureStdout(t, func() {
		if code := executeArgs([]string{
			"list", "--assignee", "mirko", "--config", cfg,
		}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	if sent["property"] != "Referente" {
		t.Fatalf("filter = %#v, want it on the Referente column", sent)
	}
	if got := sent["select"].(map[string]any)["equals"]; got != "Mirko Spinato" {
		t.Errorf("filter value = %v, want the canonical option", got)
	}
}

func TestListUnassigned(t *testing.T) {
	var sent map[string]any
	cfg := stubForList(t, assigneeProfile, &sent)

	captureStdout(t, func() {
		executeArgs([]string{"list", "--unassigned", "--config", cfg})
	})

	if got := sent["select"].(map[string]any)["is_empty"]; got != true {
		t.Errorf("filter = %#v, want is_empty", sent)
	}
}

func TestListAssigneeAndUnassignedAreExclusive(t *testing.T) {
	var sent map[string]any
	cfg := stubForList(t, assigneeProfile, &sent)

	if code := executeArgs([]string{
		"list", "--assignee", "mirko", "--unassigned", "--config", cfg,
	}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
}

func TestListHumanRowsShowTheAssignee(t *testing.T) {
	var sent map[string]any
	cfg := stubForList(t, assigneeProfile, &sent)

	out := captureStdout(t, func() {
		executeArgs([]string{"list", "--config", cfg})
	})

	if !strings.Contains(out, "@Mirko Spinato") {
		t.Errorf("output = %q, want the assignee in the row", out)
	}
}

func TestListHumanRowsAreUnchangedWithoutTheRole(t *testing.T) {
	// Non-regression on the row format: the columns must not shift for a
	// profile that never mapped the role.
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/data_sources/ds1" {
			w.Write([]byte(cliSchemaJSON))
			return
		}
		w.Write([]byte(`{"results":[` + cliRowJSON + `],"has_more":false}`))
	})

	out := captureStdout(t, func() {
		executeArgs([]string{"list", "--config", cfg})
	})

	want := "BDF-231              Hardening                                [Fatto]\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}
```

Il valore atteso dell'ultimo test va copiato da ciò che il comando stampa oggi, prima di toccare `list.go`: eseguire `go test ./internal/cli/ -run TestListHumanRowsAreUnchangedWithoutTheRole -v` sul codice attuale, leggere la stringa dal messaggio di fallimento e incollarla. È l'unico modo onesto di scrivere un test di non-regressione sul formato.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'TestList' -v`
Expected: FAIL — `unknown flag: --assignee`.

- [ ] **Step 3: Write minimal implementation**

In `internal/cli/list.go`:

```go
func newListCmd() *cobra.Command {
	var status string
	var assignee string
	var unassigned bool
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List rows, optionally filtered by status or assignee",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := buildService(cmd)
			if err != nil {
				return err
			}
			pages, err := svc.List(cmd.Context(), service.ListFilter{
				Status: status, Assignee: assignee, Unassigned: unassigned,
			})
			if err != nil {
				return err
			}
			profile := svc.Profile()
```

Il resto del corpo (il ramo `--json`, la notice "no matching tasks" su stderr) resta esattamente com'è: cambia solo la chiamata a `List` e il ciclo di stampa qui sotto.

e, nel ciclo di stampa, il segmento in coda:

```go
			merged := ticketIsTitle(profile.Properties)
			for _, p := range pages {
				status := p.Properties[profile.Properties.Status].Text
				assignee := ""
				if name := p.Properties[profile.Properties.Assignee].Text; name != "" {
					assignee = "  @" + name
				}
				if merged {
					cmd.Printf(listMergedRowFormat, p.Properties[profile.Properties.Title].Text, status, assignee)
					continue
				}
				cmd.Printf(listRowFormat,
					p.Properties[profile.Properties.Ticket].Text,
					p.Properties[profile.Properties.Title].Text,
					status, assignee)
			}
```

con i formati aggiornati a un `%s` finale, che resta vuoto quando non c'è nulla da mostrare:

```go
const (
	listRowFormat       = "%-20s %-40s [%s]%s\n"
	listMergedRowFormat = "%-61s [%s]%s\n"
)
```

E i flag:

```go
	cmd.Flags().StringVar(&assignee, "assignee", "",
		"only rows assigned to this person; a partial name is enough, and 'me' stands for NOTION_TRACK_ME")
	cmd.Flags().BoolVar(&unassigned, "unassigned", false, "only rows with no assignee")
	cmd.MarkFlagsMutuallyExclusive("assignee", "unassigned")
```

`internal/cli/list.go` importa oggi solo `cobra`: aggiungere `internal/service` per il tipo `ListFilter`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/list.go internal/cli/list_test.go
git commit -m "feat(cli): filter and display the assignee in list"
```

---

### Task 13: `init --assignee-prop` e `init --me`

**Files:**
- Modify: `internal/cli/init.go:138-141` (`configFlags`), `:255-344` (flag e `RunE`), `:354-390` (`validateMapping`), `:220-226` e `:325-332` (le due chiamate a `saveInitProfile`)
- Test: `internal/cli/init_test.go`

**Interfaces:**
- Produces: `validateMapping(schema, ticket, status, title, due, assignee string) (string, error)` — **firma cambiata**, entrambi i chiamanti vanno aggiornati.

- [ ] **Step 1: Write the failing test**

```go
// initArgs is the flag form of init for the shared fixture board, with
// whatever the test adds on top.
//
// --profile work is not decoration: withStubbedAPI writes a config whose
// default_profile is already "work", and saveInitProfile writes to "default"
// when no profile is named, leaving default_profile untouched because it is not
// empty. Without this, the test reads back the OLD profile and fails against a
// perfectly correct implementation.
func initArgs(cfg string, extra ...string) []string {
	args := []string{
		"init", "--data-source-id", "ds1", "--profile", "work",
		"--ticket-prop", "Ticket", "--status-prop", "Stato", "--title-prop", "Name",
	}
	args = append(args, extra...)
	return append(args, "--config", cfg)
}

// writtenProfile reads back the profile init just wrote.
//
// The env is cleared first: Resolve applies NOTION_TRACK_ME over whatever the
// file says, so without this the assertion on Me would read the developer's
// shell instead of the file under test.
func writtenProfile(t *testing.T, path string) config.Profile {
	t.Helper()
	t.Setenv(config.MeEnv, "")
	cfg, err := config.LoadFrom(path)
	if err != nil {
		t.Fatalf("reading back the config: %v", err)
	}
	p, err := cfg.Resolve("")
	if err != nil {
		t.Fatalf("resolving the written profile: %v", err)
	}
	return p
}

func TestInitMapsTheAssigneeColumn(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	if code := executeArgs(initArgs(cfg, "--assignee-prop", "Referente")); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if got := writtenProfile(t, cfg).Properties.Assignee; got != "Referente" {
		t.Errorf("assignee = %q, want %q", got, "Referente")
	}
}

func TestInitRejectsAnAssigneeColumnOfTheWrongType(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	// Name is the title column: usable as a title, never as an assignee.
	if code := executeArgs(initArgs(cfg, "--assignee-prop", "Name")); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
}

func TestAssigneeFlagsAreConfigFlags(t *testing.T) {
	// A config flag means "the caller knows their answers", which is what keeps
	// init out of the wizard. Forgetting to register one makes
	// `init --assignee-prop X` at a terminal open the TUI instead.
	for _, name := range []string{"assignee-prop", "me"} {
		var found bool
		for _, f := range configFlags {
			if f == name {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is missing from configFlags", name)
		}
	}
}

func TestInitMeStoresTheCanonicalValue(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	code := executeArgs(initArgs(cfg, "--assignee-prop", "Referente", "--me", "mirko"))
	if code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if got := writtenProfile(t, cfg).Me; got != "Mirko Spinato" {
		t.Errorf("me = %q, want the canonical option %q", got, "Mirko Spinato")
	}
}

func TestInitMeNeedsTheAssigneeColumn(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	if code := executeArgs(initArgs(cfg, "--me", "mirko")); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
}

func TestInitMeWarnsThatTheConfigIsShared(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	errOut := captureStderr(t, func() {
		executeArgs(initArgs(cfg, "--assignee-prop", "Referente", "--me", "mirko"))
	})
	if !strings.Contains(errOut, config.MeEnv) {
		t.Errorf("stderr = %q, want it to point at %s", errOut, config.MeEnv)
	}
}
```

Nota: `--me` va risolto contro le opzioni **del profilo che si sta scrivendo**, quindi il test usa `Referente` dalla fixture estesa nel Task 10.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'TestInit|TestAssigneeFlagsAreConfigFlags' -v`
Expected: FAIL — `unknown flag: --assignee-prop`.

- [ ] **Step 3: Write minimal implementation**

In `internal/cli/init.go`:

```go
var configFlags = []string{
	"data-source-id", "database-id", "ticket-prop", "status-prop",
	"title-prop", "due-prop", "assignee-prop", "me", "list",
}
```

Le due variabili, nel blocco `var (…)` del comando (`init.go:256-264`), e i due flag:

```go
	var (
		databaseID   string
		dataSourceID string
		ticketProp   string
		statusProp   string
		titleProp    string
		dueProp      string
		assigneeProp string
		me           string
		list         bool
	)

	cmd.Flags().StringVar(&assigneeProp, "assignee-prop", "", "select property holding the assignee (optional)")
	cmd.Flags().StringVar(&me, "me", "", "the assignee value '--assignee me' stands for (optional)")
```

`internal/cli/init.go` va importato `internal/tracker` per `ResolveOption`.

`validateMapping` guadagna il parametro e il controllo:

```go
	if _, err := check("assignee", "assignee-prop", assignee, false, "select"); err != nil {
		return "", err
	}
```

Nel `RunE`, dopo `validateMapping` e prima di `saveInitProfile`:

```go
			resolvedMe := ""
			if me != "" {
				if assigneeProp == "" {
					return Errorf(ExitUsage,
						"--me needs an assignee column to resolve against\n"+
							"  fix: pass --assignee-prop <name> as well")
				}
				resolvedMe, err = tracker.ResolveOption("me", me, schema.Properties[assigneeProp].Options)
				if err != nil {
					return Errorf(ExitUsage, "%v", err)
				}
				// config.yml is meant to be committed and shared, so an identity
				// written there is everyone's identity: say so at the one moment
				// the user is choosing to write it.
				cmd.PrintErrf(
					"warning: %q is stored in the config file, which is meant to be shared.\n"+
						"  For a personal identity, export %s instead.\n",
					resolvedMe, config.MeEnv)
			}
```

e passare `Me: resolvedMe` nel `config.Profile` costruito. Aggiornare anche il percorso wizard (`runInitWizard`), che chiama `validateMapping` con quattro ruoli: passare `res.Props.Assignee` come quinto argomento e `Me: ""`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/init.go internal/cli/init_test.go
git commit -m "feat(cli): map the assignee column and the me identity in init"
```

---

### Task 14: `doctor` conosce il ruolo

**Files:**
- Modify: `internal/service/doctor.go:27-50` (`Doctor`), `:54-80` (`checkProperties`)
- Test: `internal/service/doctor_test.go`

**Interfaces:**
- Consumes: `config.Properties.Assignee`, `config.Profile.Me`, `tracker.ResolveOption`.

- [ ] **Step 1: Write the failing test**

```go
// doctorRoutes answers the three endpoints Doctor touches.
func doctorRoutes(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/users/me":
			w.Write([]byte(`{"name":"notion-track","type":"bot"}`))
		case "/v1/data_sources/ds1":
			w.Write([]byte(schemaJSON))
		default:
			w.Write([]byte(`{"results":[],"has_more":false}`))
		}
	}))
}

func TestDoctorTreatsTheAssigneeAsOptional(t *testing.T) {
	srv := doctorRoutes(t)
	defer srv.Close()

	// testProfile leaves the role unmapped, the way every profile written
	// before this feature does.
	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), testProfile()).
		Doctor(context.Background())

	props, ok := findCheck(checks, "properties")
	if !ok {
		t.Fatal("no properties check")
	}
	if props.Status == "fail" {
		t.Errorf("properties = fail (%s), want the optional role to be skipped", props.Detail)
	}
	if _, ok := findCheck(checks, "assignee"); ok {
		t.Error("an assignee check ran for a profile that does not map the role")
	}
}

func TestDoctorWarnsWhenTheIdentityNoLongerResolves(t *testing.T) {
	srv := doctorRoutes(t)
	defer srv.Close()

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("Someone Who Left")).
		Doctor(context.Background())

	check, ok := findCheck(checks, "assignee")
	if !ok {
		t.Fatal("no assignee check")
	}
	if check.Status != "warn" {
		t.Errorf("assignee = %s (%s), want warn", check.Status, check.Detail)
	}
}

func TestDoctorAcceptsAnIdentityFromTheEnvironment(t *testing.T) {
	srv := doctorRoutes(t)
	defer srv.Close()
	t.Setenv(config.MeEnv, "mirko")

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("mirko")).
		Doctor(context.Background())

	check, ok := findCheck(checks, "assignee")
	if !ok {
		t.Fatal("no assignee check")
	}
	if check.Status != "ok" {
		t.Errorf("assignee = %s (%s), want ok", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "Mirko Spinato") {
		t.Errorf("detail = %q, want it to name who me resolves to", check.Detail)
	}
}

func TestDoctorWarnsWhenTheIdentityLivesOnlyInTheSharedConfig(t *testing.T) {
	// The failure this catches is silent by nature: everything resolves, and
	// every teammate assigns work to whoever ran init.
	srv := doctorRoutes(t)
	defer srv.Close()
	t.Setenv(config.MeEnv, "")

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("mirko")).
		Doctor(context.Background())

	check, ok := findCheck(checks, "assignee")
	if !ok {
		t.Fatal("no assignee check")
	}
	if check.Status != "warn" {
		t.Errorf("assignee = %s (%s), want warn", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, config.MeEnv) {
		t.Errorf("detail = %q, want it to point at the environment variable", check.Detail)
	}
}
```

Nota: `TestDoctorWarnsWhenTheIdentityNoLongerResolves` va anch'esso preceduto da `t.Setenv(config.MeEnv, "")`, così l'ambiente di chi lancia i test non ne cambia l'esito. Import da aggiungere a `doctor.go`: `os`, `config`, `tracker`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/ -run TestDoctor -v`
Expected: FAIL — nessun check chiamato `assignee`.

- [ ] **Step 3: Write minimal implementation**

In `checkProperties` (`internal/service/doctor.go:54-77`), quattro punti, tutti nella stessa funzione:

```go
	mapped := map[string]string{
		"ticket":   s.profile.Properties.Ticket,
		"status":   s.profile.Properties.Status,
		"title":    s.profile.Properties.Title,
		"due":      s.profile.Properties.Due,
		"assignee": s.profile.Properties.Assignee,
	}
	wantType := map[string][]string{
		"ticket":   {"rich_text", "title"},
		"status":   {"status", "select"},
		"title":    {"title"},
		"due":      {"date"},
		"assignee": {"select"},
	}

	// A board may legitimately track nobody, so an unmapped assignee is a
	// skip, not a failure — the same judgement already made for due.
	optionalRoles := map[string]bool{"due": true, "assignee": true}

	// …
	roles := []string{"ticket", "status", "title", "due", "assignee"}
```

E, in `Doctor`, dopo `checkProperties` (dentro il ramo in cui lo schema è stato letto):

```go
		if s.profile.Properties.Assignee != "" {
			checks = append(checks, s.checkAssignee(schema))
		}
```

con:

```go
// checkAssignee verifies that the configured identity still names an option the
// column offers. An option renamed in Notion turns every "--assignee me" into a
// runtime failure, and this is the place to find that out first.
func (s *Service) checkAssignee(schema *notion.Schema) Check {
	if s.profile.Me == "" {
		return Check{"assignee", "ok", "mapped; no identity configured (--assignee me is unavailable)"}
	}
	prop := schema.Properties[s.profile.Properties.Assignee]
	resolved, err := tracker.ResolveOption("me", s.profile.Me, prop.Options)
	if err != nil {
		return Check{"assignee", "warn", fmt.Sprintf(
			"the configured identity %q no longer resolves: %v\n"+
				"  fix: export %s=<name>, or rerun 'notion-track init --me <name>'",
			s.profile.Me, err, config.MeEnv)}
	}

	// The identity resolves — but where did it come from? config.yml is meant
	// to be committed and shared, so an identity that lives only in the file is
	// every teammate's identity: theirs resolves to whoever ran init, and their
	// "--assignee me" quietly assigns work to that person. os.Getenv rather
	// than the profile field, because Resolve has already folded the override
	// in and the two are indistinguishable by then.
	if os.Getenv(config.MeEnv) == "" {
		return Check{"assignee", "warn", fmt.Sprintf(
			"--assignee me resolves to %s, from the config file rather than the environment\n"+
				"  fix: export %s=<name>; a shared config gives everyone the same identity",
			resolved, config.MeEnv)}
	}
	return Check{"assignee", "ok", "--assignee me resolves to " + resolved}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/service/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/doctor.go internal/service/doctor_test.go
git commit -m "feat(service): check the assignee mapping and identity in doctor"
```

---

### Task 15: Il ruolo nel wizard

**Files:**
- Modify: `internal/tui/wizard.go:56-61` (`roles`), `:432-460` (`roleValue`, `setRole`)
- Test: `internal/tui/wizard_test.go`

**Interfaces:**
- Consumes: `config.Properties.Assignee`.

- [ ] **Step 1: Write the failing test**

```go
func TestWizardAssigneeRole(t *testing.T) {
	t.Run("the role is offered and optional", func(t *testing.T) {
		var spec roleSpec
		var found bool
		for _, r := range roles {
			if r.name == "assignee" {
				spec, found = r, true
			}
		}
		if !found {
			t.Fatal("no assignee role in the wizard")
		}
		if !spec.optional {
			t.Error("the assignee role must be optional: a board may track nobody")
		}
		if len(spec.types) != 1 || spec.types[0] != "select" {
			t.Errorf("types = %v, want [select]", spec.types)
		}
		if spec.key == "" {
			t.Error("the role has no shortcut key")
		}
	})

	t.Run("roleValue and setRole round-trip", func(t *testing.T) {
		var p config.Properties
		setRole(&p, "assignee", "Referente")
		if p.Assignee != "Referente" {
			t.Errorf("setRole left Assignee = %q", p.Assignee)
		}
		if got := roleValue(p, "assignee"); got != "Referente" {
			t.Errorf("roleValue = %q, want %q", got, "Referente")
		}
	})

	t.Run("no two roles share a shortcut key", func(t *testing.T) {
		seen := map[string]string{}
		for _, r := range roles {
			if other, dup := seen[r.key]; dup {
				t.Errorf("key %q is used by both %q and %q", r.key, other, r.name)
			}
			seen[r.key] = r.name
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tui/ -run TestWizardAssigneeRole -v`
Expected: FAIL — nessun ruolo `assignee`.

- [ ] **Step 3: Write minimal implementation**

In `internal/tui/wizard.go`, aggiungere a `roles`:

```go
	{name: "assignee", key: "a", types: []string{"select"}, optional: true},
```

e un `case` in ciascuna delle due funzioni di fondo (`wizard.go:432-460`), accanto a quello di `due`:

```go
func roleValue(p config.Properties, role string) string {
	switch role {
	// … ticket, status, title, due invariati …
	case "assignee":
		return p.Assignee
	}
	return ""
}

func setRole(p *config.Properties, role, value string) {
	switch role {
	// … ticket, status, title, due invariati …
	case "assignee":
		p.Assignee = value
	}
}
```

Le due funzioni sono uno switch esaustivo sui ruoli: dimenticarne uno non è un errore di compilazione, è un ruolo che il wizard mostra ma non salva — che è esattamente ciò che il round-trip test dello Step 1 verifica.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tui/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/wizard.go internal/tui/wizard_test.go
git commit -m "feat(tui): offer the assignee role in the init wizard"
```

---

### Task 16: Il manifest

**Files:**
- Modify: `internal/manifest/manifest.go:24-47` (`Entry`, `fieldNames`), `:62-89` (`parseJSON`), `:124-158` (`assign`, `validate`), `internal/cli/apply.go:153-155`
- Test: `internal/manifest/manifest_test.go`

**Interfaces:**
- Produces: `manifest.Entry.Assignee string`, `manifest.Entry.Unassign bool`.
- Consumes: `tracker.Fields` (Task 4).

- [ ] **Step 1: Write the failing test**

```go
func TestManifestAssignee(t *testing.T) {
	t.Run("csv", func(t *testing.T) {
		data := []byte("op,ticket,assignee,unassign\nset,BDF-1,Mirko Spinato,\nset,BDF-2,,true\n")
		entries, err := Parse("tasks.csv", data)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if entries[0].Assignee != "Mirko Spinato" {
			t.Errorf("Assignee = %q", entries[0].Assignee)
		}
		if !entries[1].Unassign {
			t.Error("Unassign = false, want true")
		}
	})

	t.Run("json", func(t *testing.T) {
		data := []byte(`[{"op":"set","ticket":"BDF-1","assignee":"mirko"},
		                 {"op":"set","ticket":"BDF-2","unassign":"true"}]`)
		entries, err := Parse("tasks.json", data)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if entries[0].Assignee != "mirko" {
			t.Errorf("Assignee = %q", entries[0].Assignee)
		}
		if !entries[1].Unassign {
			t.Error("Unassign = false, want true")
		}
	})

	t.Run("a bad boolean is a parse error naming the entry", func(t *testing.T) {
		data := []byte("op,ticket,unassign\nset,BDF-1,perhaps\n")
		_, err := Parse("tasks.csv", data)
		if err == nil {
			t.Fatal("Parse = nil error, want a failure")
		}
		if !strings.Contains(err.Error(), "1") {
			t.Errorf("error = %q, want it to name entry 1", err)
		}
	})

	t.Run("an unknown field still fails", func(t *testing.T) {
		data := []byte("op,ticket,assigne\nset,BDF-1,Mirko\n")
		if _, err := Parse("tasks.csv", data); err == nil {
			t.Fatal("a typo in a column name must not be ignored")
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/manifest/ -run TestManifestAssignee -v`
Expected: FAIL — `unknown field "assignee"`.

- [ ] **Step 3: Write minimal implementation**

In `internal/manifest/manifest.go`, aggiungere a `Entry`:

```go
	Assignee string `json:"assignee,omitempty"`
	// Unassign clears the assignee. It is a string in the file and a bool here
	// because CSV has no way to say "an explicitly empty list": "true"/"false"
	// is the only form both formats can express identically.
	Unassign bool `json:"unassign,omitempty"`
```

A `fieldNames`: `"assignee", "unassign"`. `fieldNames` è condiviso fra CSV e JSON, quindi entrambe le forme accettano entrambi i campi — un'asimmetria fra i due formati costerebbe un caso speciale e sarebbe più difficile da spiegare di un campo in più.

In `assign`:

```go
	case "assignee":
		e.Assignee = value
	case "unassign":
		if value == "" {
			return nil
		}
		parsed, err := strconv.ParseBool(strings.ToLower(value))
		if err != nil {
			return fmt.Errorf("unassign must be true or false, got %q", value)
		}
		e.Unassign = parsed
```

(aggiungere `"strconv"` agli import).

E in `parseJSON` (`manifest.go:74-78`), che oggi rifiuta ogni valore non-stringa: un `unassign` in JSON si scrive `true`, non `"true"`, e rifiutare la forma naturale del formato sarebbe una trappola gratuita. Il resto dei campi resta come prima — un `title` booleano è ancora un errore.

```go
			for key, value := range obj {
				text, ok := value.(string)
				if !ok {
					// JSON has a boolean; unassign is the one field where using
					// it is the obvious thing to write.
					if b, isBool := value.(bool); isBool && key == "unassign" {
						text = strconv.FormatBool(b)
					} else {
						return nil, fmt.Errorf("entry %d: %q must be a string", i+1, key)
					}
				}
				if err := assign(&entry, key, text); err != nil {
					return nil, fmt.Errorf("entry %d: %w", i+1, err)
				}
			}
```

Il test JSON dello Step 1 va allora esteso con la forma booleana:

```go
	t.Run("json accepts a real boolean for unassign", func(t *testing.T) {
		data := []byte(`[{"op":"set","ticket":"BDF-2","unassign":true}]`)
		entries, err := Parse("tasks.json", data)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if !entries[0].Unassign {
			t.Error("Unassign = false, want true")
		}
	})

	t.Run("a boolean anywhere else is still an error", func(t *testing.T) {
		data := []byte(`[{"op":"set","ticket":"BDF-2","title":true}]`)
		if _, err := Parse("tasks.json", data); err == nil {
			t.Fatal("Parse = nil error, want the non-string check to still bite")
		}
	})
```

In `internal/cli/apply.go`, estendere la costruzione dei campi:

```go
	fields := tracker.Fields{
		Ticket: entry.Ticket, Title: entry.Title, Status: entry.Status, Due: entry.Due,
		Assignee: entry.Assignee, Unassign: entry.Unassign,
	}
```

E aggiornare `applyExample` (`internal/cli/apply.go:39-48`) perché mostri le nuove colonne.

Infine i due test end-to-end, in `internal/cli/apply_test.go`, sul percorso che non passa da cobra — che è la ragione per cui `ErrConflictingAssignee` è un tipo e non un messaggio:

```go
func TestApplyWritesTheAssignee(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "tasks.csv")
	os.WriteFile(manifestPath, []byte("op,ticket,unassign\nset,BDF-231,true\n"), 0o600)

	captureStdout(t, func() {
		if code := executeArgs([]string{
			"apply", "--file", manifestPath, "--config", cfg,
		}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	got, _ := json.Marshal(written["Referente"])
	if want := `{"select":null}`; string(got) != want {
		t.Errorf("Referente = %s, want %s", got, want)
	}
}

func TestApplyRejectsSettingAndClearingTheSameEntry(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "tasks.csv")
	os.WriteFile(manifestPath, []byte("op,ticket,assignee,unassign\nset,BDF-231,mirko,true\n"), 0o600)

	captureStdout(t, func() {
		// Exit 2, not 1: apply never touches cobra, so the typed error is the
		// only thing carrying the usage verdict out of the domain layer.
		if code := executeArgs([]string{
			"apply", "--file", manifestPath, "--config", cfg,
		}); code != ExitUsage {
			t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
		}
	})
	if written != nil {
		t.Errorf("a rejected entry still wrote %v", written)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/manifest/ ./internal/cli/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/manifest/ internal/cli/apply.go
git commit -m "feat(manifest): carry the assignee and unassign through apply"
```

---

### Task 17: Gli agent vedono il ruolo

**Files:**
- Modify: `internal/mcp/server.go:22-72` (`Row`, `Fields`, `Tracker`, args), `:131-140` (`list_tasks`), `internal/cli/mcp.go:55-94`
- Test: `internal/mcp/server_test.go`, `internal/cli/mcp_test.go`

**Interfaces:**
- Produces: `mcp.Row.Assignee string` (in fondo, **identico a `pageJSON`**), `mcp.Fields.Assignee/Unassign` (in fondo, **identici a `tracker.Fields`**), `Tracker.List(ctx, service.ListFilter)`.

- [ ] **Step 1: Write the failing test**

Prima adeguare il fake e i test che si rompono. In `internal/mcp/server_test.go`: `fakeTracker.listed` passa da `[]string` a `[]ListFilter` (`:24`) e la firma di `List` cambia di conseguenza (`:51-57`); `testRow()` guadagna `Assignee: "Mirko Spinato"` (`:59-65`).

Tre test esistenti vanno aggiornati insieme, altrimenti il pacchetto non compila:

| Test | Come diventa |
|---|---|
| `internal/mcp/server_test.go:212` `TestListToolPassesTheStatusFilter` | `tracker.listed[0].Status != "Fatto"` |
| `internal/mcp/server_test.go:233` `TestListToolWithNoFilterAsksForEverything` | `tracker.listed[0] != (ListFilter{})` |
| `internal/cli/mcp_test.go:62` | `adapter.List(context.Background(), mcp.ListFilter{})` |

Poi:

```go
func TestUpsertToolCarriesTheAssignee(t *testing.T) {
	tracker := &fakeTracker{row: testRow()}
	session := connect(t, tracker)

	call(t, session, "upsert_task", map[string]any{
		"ticket": "BDF-231", "assignee": "mirko",
	}, nil)

	if len(tracker.upserted) != 1 {
		t.Fatalf("upsert calls = %d, want 1", len(tracker.upserted))
	}
	if got := tracker.upserted[0].Assignee; got != "mirko" {
		t.Errorf("Assignee = %q, want %q", got, "mirko")
	}
}

func TestSetToolCarriesUnassign(t *testing.T) {
	tracker := &fakeTracker{row: testRow()}
	session := connect(t, tracker)

	call(t, session, "set_task", map[string]any{
		"ticket": "BDF-231", "unassign": true,
	}, nil)

	if len(tracker.set) != 1 {
		t.Fatalf("set calls = %d, want 1", len(tracker.set))
	}
	if !tracker.set[0].Unassign {
		t.Error("Unassign = false, want true")
	}
}

func TestListToolFiltersByAssignee(t *testing.T) {
	tracker := &fakeTracker{rows: []Row{testRow()}}
	session := connect(t, tracker)

	call(t, session, "list_tasks", map[string]any{"assignee": "me"}, nil)

	if len(tracker.listed) != 1 || tracker.listed[0].Assignee != "me" {
		t.Fatalf("list calls = %v, want one filtered by me", tracker.listed)
	}
}

func TestListToolFiltersByUnassigned(t *testing.T) {
	tracker := &fakeTracker{rows: []Row{testRow()}}
	session := connect(t, tracker)

	call(t, session, "list_tasks", map[string]any{"unassigned": true}, nil)

	if len(tracker.listed) != 1 || !tracker.listed[0].Unassigned {
		t.Fatalf("list calls = %v, want one filtered by unassigned", tracker.listed)
	}
}

func TestGetToolExposesTheAssignee(t *testing.T) {
	tracker := &fakeTracker{row: testRow()}
	session := connect(t, tracker)

	var row Row
	call(t, session, "get_task", map[string]any{"ticket": "BDF-231"}, &row)

	if row.Assignee != "Mirko Spinato" {
		t.Errorf("Assignee = %q, want %q", row.Assignee, "Mirko Spinato")
	}
}
```

E, in `internal/cli/mcp_test.go`, il test che protegge le tre conversioni:

```go
func TestTheMCPConversionsStayDirect(t *testing.T) {
	// A compile-time guarantee turned into a test: these conversions only
	// compile while the structs stay identical, which is what keeps the
	// documented --json contract and what an agent sees from drifting apart.
	// If this file stops compiling, that is the feature working.
	var p pageJSON
	_ = mcp.Row(p)

	var f mcp.Fields
	_ = tracker.Fields(f)

	var lf mcp.ListFilter
	_ = service.ListFilter(lf)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ ./internal/cli/ -run 'Assignee|Unassign|TestTheMCPConversions' -v`
Expected: FAIL — i campi non esistono; `tracker.Fields(f)` non compila.

- [ ] **Step 3: Write minimal implementation**

In `internal/mcp/server.go`:

```go
type Row struct {
	Ticket         string `json:"ticket"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	PageID         string `json:"page_id"`
	URL            string `json:"url"`
	LastEditedTime string `json:"last_edited_time"`
	Assignee       string `json:"assignee"`
}

// Fields are the writable values of a row. An empty field means "leave this
// alone", which is the same rule the CLI's flags follow.
//
// The field order mirrors tracker.Fields exactly: internal/cli converts one
// into the other directly, which only compiles while they stay identical.
type Fields struct {
	Ticket   string
	Title    string
	Status   string
	Due      string
	Assignee string
	Unassign bool
}

type upsertArgs struct {
	Ticket   string `json:"ticket" jsonschema:"the ticket key identifying the row"`
	Title    string `json:"title,omitempty" jsonschema:"the row's title; omit to leave it unchanged"`
	Status   string `json:"status,omitempty" jsonschema:"the status to set; must be one the board already offers"`
	Due      string `json:"due,omitempty" jsonschema:"the due date as YYYY-MM-DD; omit to leave it unchanged"`
	Assignee string `json:"assignee,omitempty" jsonschema:"who the row belongs to; a partial name is enough when unambiguous, and 'me' means the configured identity; omit to leave it unchanged"`
	Unassign bool   `json:"unassign,omitempty" jsonschema:"clear the assignee; cannot be combined with assignee"`
}

type listArgs struct {
	Status     string `json:"status,omitempty" jsonschema:"only return rows with this status; omit for all rows"`
	Assignee   string `json:"assignee,omitempty" jsonschema:"only return rows assigned to this person; a partial name is enough, and 'me' means the configured identity"`
	Unassigned bool   `json:"unassigned,omitempty" jsonschema:"only return rows with no assignee; cannot be combined with assignee"`
}
```

Il filtro di `list_tasks` viaggia su un tipo **locale** al pacchetto:

```go
// ListFilter mirrors service.ListFilter field for field, so internal/cli can
// convert one into the other directly.
//
// A local type rather than an import of internal/service: this package declares
// the slice of the service layer it consumes (see Tracker) and depends on none
// of it, which is what lets a test drive the whole protocol with a fake and no
// network.
type ListFilter struct {
	Status     string
	Assignee   string
	Unassigned bool
}
```

`Tracker.List` diventa `List(ctx context.Context, f ListFilter) ([]Row, error)`. Il pacchetto `internal/mcp` oggi non importa nulla del progetto (solo l'SDK), e questa è una proprietà da conservare: importare `internal/service` non creerebbe un ciclo, ma accoppierebbe l'adapter al layer che esiste per disaccoppiare.

In `internal/cli/mcp.go`:

```go
func (a trackerAdapter) List(ctx context.Context, f mcp.ListFilter) ([]mcp.Row, error) {
	pages, err := a.svc.List(ctx, service.ListFilter(f))
	if err != nil {
		return nil, err
	}
	props := a.svc.Profile().Properties
	rows := make([]mcp.Row, 0, len(pages))
	for _, p := range pages {
		rows = append(rows, mcp.Row(toPageJSON(p, props)))
	}
	return rows, nil
}

// fieldsFromMCP is a direct conversion for the same reason mcp.Row is: copying
// field by field compiles happily while silently dropping whatever was added
// on one side and forgotten on the other.
func fieldsFromMCP(f mcp.Fields) tracker.Fields { return tracker.Fields(f) }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/ internal/cli/mcp.go internal/cli/mcp_test.go
git commit -m "feat(mcp): expose the assignee to agents"
```

---

### Task 18: Documentazione, e i cinque gate

**Files:**
- Modify: `README.md`, `README.it.md`, `skills/notion-track/SKILL.md`

**Interfaces:**
- Consumes: tutto quanto sopra. Nessun codice nuovo.

- [ ] **Step 1: Aggiornare i due README**

Le sezioni da toccare, in entrambi i file e con lo stesso contenuto:

- la tabella dei flag di `upsert`/`set`: `--assignee`, `--unassign`;
- i flag di `list`: `--assignee`, `--unassigned`;
- i flag di `init`: `--assignee-prop`, `--me`;
- il contratto `--json`: la chiave `assignee`, sempre presente, vuota quando nessuno è assegnato o il ruolo non è mappato;
- la sezione sulle variabili d'ambiente: `NOTION_TRACK_ME`, con la ragione per cui è preferibile a `me:` nel config condiviso;
- il formato del manifest: colonne `assignee` e `unassign` in CSV e JSON;
- gli strumenti MCP: i nuovi argomenti;
- la sezione `doctor`: il check `assignee`;
- un esempio realistico nel flusso principale, ad esempio:

```bash
# quello che ognuno mette una volta nel proprio shell profile
export NOTION_TRACK_ME="Marco Arnulfo"

notion-track set --ticket BDF-1 --status "In corso" --assignee me
notion-track list --assignee me --status "Da fare"
notion-track list --unassigned
```

- [ ] **Step 2: Aggiornare la skill**

In `skills/notion-track/SKILL.md`: i nuovi flag, e soprattutto **quando** un agente deve usarli — "assegna a Mirko", "prendi in carico", "chi ha in mano X", "cosa devo fare io", "task senza referente". La skill è ciò che fa scattare l'uso: un flag documentato ma non collegato a una frase che l'utente pronuncia non verrà mai usato.

- [ ] **Step 3: I cinque gate**

```bash
gofmt -l .            # deve stampare nulla
staticcheck ./...
go vet ./...
go test ./...
go build ./...
```

Ogni comando deve passare pulito. `gofmt -l .` che stampa un file è un fallimento, non un avviso.

- [ ] **Step 4: Smoke manuale sulla board reale**

```bash
notion-track get --ticket "<un task esistente>" --json | jq .assignee
notion-track list --assignee me --status "Da fare"
notion-track set --ticket "<un task di prova>" --assignee mirko --dry-run
notion-track set --ticket "<un task di prova>" --assignee mirko
notion-track set --ticket "<un task di prova>" --unassign
notion-track doctor
```

Il primo comando è read-only e va eseguito per primo: se la lettura non mostra i referenti già presenti su 37 righe, niente di ciò che segue ha senso.

- [ ] **Step 5: Commit**

```bash
git add README.md README.it.md skills/
git commit -m "docs: document the assignee role across the READMEs and the skill"
```

---

## Ordine e dipendenze

```
1 config
├── 2 ResolveOption ──┐
├── 3 ValidateOption ─┼── 4 payload ── 7 resolveAssignee ── 8 dry-run
├── 5 GuessMapping    │                └── 9 ListFilter
└── 6 filtri ─────────┘

10 flag di scrittura  (introduce le fixture CLI condivise)
├── 11 get/json
├── 12 list
├── 13 init ── 14 doctor
└── 16 manifest

15 wizard      (indipendente da tutto tranne il Task 1)
17 mcp         (dipende da 9 e 11)
18 docs        (ultimo, sempre)
```

**I task 1-10 vanno in ordine stretto.** Dal 10 in poi la struttura si apre, con un vincolo che non è negoziabile: **10 viene prima di 11, 12, 13 e 16**, perché il suo Step 1 introduce le fixture CLI condivise (`cliSchemaJSON`/`cliRowJSON` estese, `assigneeProfile`, `assigneeProfileNoIdentity`, `stubForAssignee`) che i test di quei task usano. Eseguirli in parallelo con il 10 significa non compilare.

Fra loro, 11/12/13/16 sono indipendenti; 14 segue il 13 solo per comodità di lettura (usa `assigneeProfile` del pacchetto `service`, introdotta nel Task 7, non le fixture CLI). Il 15 dipende solo dal Task 1. Il 17 ha bisogno di `ListFilter` (9) e del campo in `pageJSON` (11).
