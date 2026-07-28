# Priority Role Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Aggiungere a notion-track un sesto ruolo mappato, `priority`, che legge, scrive e filtra l'urgenza di un task su una colonna Notion di tipo `select`.

**Architecture:** Il ruolo gemello di `assignee`, completato ieri: stesso tipo di colonna, stessa risoluzione dei valori parziali, stessi punti di innesto. Quasi tutto il lavoro è ricalcare un percorso già battuto e testato; le uniche due parti che richiedono attenzione sono la guardia di `GuessMapping` (che ora deve escludere due colonne invece di una) e le conversioni dirette fra `pageJSON`/`mcp.Row` e `tracker.Fields`/`mcp.Fields`, che compilano solo se le due facce restano identiche.

**Tech Stack:** Go, `github.com/spf13/cobra` (CLI), `gopkg.in/yaml.v3` (config), `github.com/charmbracelet/bubbletea` (TUI), `github.com/modelcontextprotocol/go-sdk/mcp` (MCP), `net/http/httptest` (test del client).

## Global Constraints

- **Spec di riferimento:** `docs/superpowers/specs/2026-07-28-notion-track-priority-design.md`, e per tutto ciò che i due ruoli condividono `docs/superpowers/specs/2026-07-27-notion-track-assignee-design.md`.
- **Il ruolo gemello è il modello:** ogni volta che una scelta non è specificata qui, la risposta è "come `assignee`". Una divergenza silenziosa fra i due è un difetto, non una variante.
- **Nessuna regressione:** con `priority` non mappato ogni comando produce l'output di oggi byte per byte — umano, JSON, exit code. Ogni task che tocca l'output ha un test che lo verifica.
- **Niente svuotamento, niente `me`, niente ordinamento:** non esistono `--unpriority`, `list --unprioritized`, `--sort`. Un `Unpriority bool` in un qualsiasi struct è fuori scope.
- **Le conversioni dirette si estendono in coppia:** `pageJSON`↔`mcp.Row` e `tracker.Fields`↔`mcp.Fields` compilano solo finché sono identiche per nomi, tipi e ordine. I campi nuovi vanno **in coda** e **nello stesso commit** su entrambe le facce.
- **Errori tipizzati, mai riconosciuti per prefisso di messaggio.** Valore non valido e valore ambiguo escono 2; ruolo non mappato esce 1, come per gli altri cinque ruoli.
- **Commenti:** spiegano il PERCHÉ. Un commento che riformula la riga sotto va tolto.
- **Lingua:** codice, commenti, messaggi ed errori in inglese; i documenti in `docs/superpowers/` e `README.it.md` in italiano.
- **Gate CI, tutti e cinque, prima della PR:** `gofmt -l .` (nulla), `staticcheck ./...`, `go vet ./...`, `go test ./...`, `go build ./...`.
- **Commit:** conventional commit minuscolo. **Mai** un trailer `Co-Authored-By` — il proprietario del repo lo vieta.
- **Branch:** `feat/priority-role`, già creato, con lo spec committato.

---

### Task 1: Il ruolo nel profilo

**Files:**
- Modify: `internal/config/config.go` (struct `Properties`)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Properties.Priority string`, tag `yaml:"priority,omitempty"`.

- [ ] **Step 1: Write the failing test**

In `internal/config/config_test.go`:

```go
func TestPriorityRoundTripsThroughTheConfigFile(t *testing.T) {
	// The role must survive a save/load cycle, and its absence must keep an
	// older config valid: both new roles are additive by design.
	path := filepath.Join(t.TempDir(), "config.yml")
	cfg := &Config{
		DefaultProfile: "default",
		Profiles: map[string]Profile{
			"default": {
				DataSourceID: "ds1",
				Properties: Properties{
					Ticket: "Nome task", Status: "Stato", Title: "Nome task",
					Assignee: "Referente", Priority: "Urgenza",
				},
			},
		},
	}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}

	read, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := read.Profiles["default"].Properties.Priority; got != "Urgenza" {
		t.Errorf("Priority = %q, want %q", got, "Urgenza")
	}

	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "priority: Urgenza") {
		t.Errorf("config file does not carry the priority key:\n%s", raw)
	}
}

func TestAnUnmappedPriorityIsOmittedFromTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	cfg := &Config{
		DefaultProfile: "default",
		Profiles: map[string]Profile{
			"default": {DataSourceID: "ds1", Properties: Properties{Ticket: "T", Status: "S", Title: "N"}},
		},
	}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "priority") {
		t.Errorf("an unmapped role must not appear in the file:\n%s", raw)
	}
}
```

Import necessari nel file di test: `os`, `path/filepath`, `strings` (verificare quali già ci sono).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestPriority -v` e `go test ./internal/config/ -run TestAnUnmapped -v`
Expected: FAIL, non compila — `unknown field Priority in struct literal of type Properties`.

- [ ] **Step 3: Write minimal implementation**

In `internal/config/config.go`, in `Properties`, subito dopo `Assignee`:

```go
	// Priority is the column ranking how urgent a row is. Optional, and
	// usually sparse: a board marks what is burning, not everything.
	Priority string `yaml:"priority,omitempty"`
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS, inclusi i test preesistenti.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): add the priority property"
```

---

### Task 2: Il payload

**Files:**
- Modify: `internal/tracker/payload.go` (struct `Fields`, `BuildProperties`)
- Test: `internal/tracker/payload_test.go`

**Interfaces:**
- Produces: `tracker.Fields.Priority string`, **in coda allo struct**, dopo `Unassign`. L'ordine è vincolante: `internal/cli` converte `mcp.Fields` in `tracker.Fields` direttamente, e la conversione compila solo se le due facce restano identiche.
- Consumes: `config.Properties.Priority` (Task 1).

- [ ] **Step 1: Write the failing test**

In `internal/tracker/payload_test.go`:

```go
func TestBuildPropertiesPriority(t *testing.T) {
	schema := &notion.Schema{Properties: map[string]notion.Property{
		"Nome task": {Name: "Nome task", Type: "title"},
		"Urgenza":   {Name: "Urgenza", Type: "select", Options: []string{"ALTA", "MEDIA", "NORMALE"}},
	}}
	props := config.Properties{Title: "Nome task", Priority: "Urgenza"}

	t.Run("writes the select", func(t *testing.T) {
		got, err := BuildProperties(Fields{Priority: "ALTA"}, props, schema)
		if err != nil {
			t.Fatalf("BuildProperties: %v", err)
		}
		want := map[string]any{"select": map[string]string{"name": "ALTA"}}
		if !reflect.DeepEqual(got["Urgenza"], want) {
			t.Errorf("Urgenza = %#v, want %#v", got["Urgenza"], want)
		}
	})

	t.Run("not passed leaves the column alone", func(t *testing.T) {
		got, err := BuildProperties(Fields{Status: ""}, props, schema)
		if err != nil {
			t.Fatalf("BuildProperties: %v", err)
		}
		if _, ok := got["Urgenza"]; ok {
			t.Error("Urgenza is in the payload but nothing asked to write it")
		}
	})

	t.Run("a value the column does not offer is rejected", func(t *testing.T) {
		_, err := BuildProperties(Fields{Priority: "URGENTISSIMA"}, props, schema)
		var invalid *ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("error = %v, want *ValidationError", err)
		}
		if invalid.Field != "priority" {
			t.Errorf("Field = %q, want %q", invalid.Field, "priority")
		}
	})

	t.Run("unmapped role with a value is an error, not a silent drop", func(t *testing.T) {
		unmapped := config.Properties{Title: "Nome task"}
		if _, err := BuildProperties(Fields{Priority: "ALTA"}, unmapped, schema); err == nil {
			t.Fatal("BuildProperties = nil error, want a failure naming --priority-prop")
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tracker/ -run TestBuildPropertiesPriority -v`
Expected: FAIL, non compila — `unknown field Priority in struct literal of type Fields`.

- [ ] **Step 3: Write minimal implementation**

In `internal/tracker/payload.go`, in `Fields`, **in coda**, dopo `Unassign`:

```go
	Priority string
```

E, in `BuildProperties`, una riga accanto alle altre `add`, dopo quella dell'assignee:

```go
	if err := add("priority", props.Priority, f.Priority); err != nil {
		return nil, err
	}
```

Non serve altro: il ramo `select` di `add` costruisce già il payload e valida il valore contro le opzioni dello schema, nominando il ruolo che gli viene passato.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tracker/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/payload.go internal/tracker/payload_test.go
git commit -m "feat(tracker): build the priority select"
```

---

### Task 3: `GuessMapping` con due ruoli sullo stesso tipo

**Files:**
- Modify: `internal/tracker/mapping.go`
- Test: `internal/tracker/mapping_test.go`

**Interfaces:**
- Produces: `GuessMapping` popola `out.Priority`, e la guardia di esclusione copre ora sia la colonna di `status` sia quella di `assignee`.

- [ ] **Step 1: Write the failing test**

In `internal/tracker/mapping_test.go`:

```go
func TestGuessMappingPriority(t *testing.T) {
	t.Run("recognises the real board", func(t *testing.T) {
		// Referente and Urgenza are both selects: the shape that makes the
		// exclusion guard load-bearing.
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
		if got.Priority != "Urgenza" {
			t.Errorf("Priority = %q, want %q", got.Priority, "Urgenza")
		}
	})

	t.Run("never reuses the column taken by assignee", func(t *testing.T) {
		// A board with one select that both roles would recognise: assignee
		// claims it first, and priority must not claim it too.
		schema := &notion.Schema{Properties: map[string]notion.Property{
			"Nome task": {Name: "Nome task", Type: "title"},
			"Stato":     {Name: "Stato", Type: "status"},
			"Referente": {Name: "Referente", Type: "select"},
		}}
		got := GuessMapping(schema)
		if got.Assignee != "Referente" {
			t.Fatalf("Assignee = %q, want %q", got.Assignee, "Referente")
		}
		if got.Priority == got.Assignee {
			t.Errorf("Priority = %q, want it not to reuse the assignee column", got.Priority)
		}
	})

	t.Run("does not guess an unrecognisable lone select", func(t *testing.T) {
		schema := &notion.Schema{Properties: map[string]notion.Property{
			"Nome task": {Name: "Nome task", Type: "title"},
			"Stato":     {Name: "Stato", Type: "status"},
			"Colore":    {Name: "Colore", Type: "select"},
		}}
		if got := GuessMapping(schema); got.Priority != "" {
			t.Errorf("Priority = %q, want no guess", got.Priority)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tracker/ -run TestGuessMappingPriority -v`
Expected: FAIL — `got.Priority` è sempre `""` (il campo esiste dal Task 1, quindi compila e fallisce sulle assert).

- [ ] **Step 3: Write minimal implementation**

In `internal/tracker/mapping.go`, accanto ad `assigneeNames`:

```go
	priorityNames = []string{"priority", "urgenza", "priorità", "priorita", "importanza", "severity"}
```

Poi, dopo il blocco che calcola `out.Assignee`, il blocco gemello. La differenza rispetto a quello dell'assignee è una sola: esclude **due** colonne invece di una, perché ora sono due i ruoli che pescano dai `select`, e una board con un solo select riconoscibile deve vederselo assegnare una volta sola.

```go
	// Same rule as assignee — name only, no "the only candidate wins"
	// fallback — with one more column to skip: with two optional roles drawing
	// from the same pool, a board holding a single recognisable select must not
	// see it claimed twice.
	for _, name := range byType["select"] {
		if name == out.Status || name == out.Assignee {
			continue
		}
		for _, known := range priorityNames {
			if strings.EqualFold(name, known) {
				out.Priority = name
				break
			}
		}
		if out.Priority != "" {
			break
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tracker/ -v`
Expected: PASS, inclusi i test dell'assignee.

- [ ] **Step 5: Verify the guard actually bites**

La guardia va provata, non solo scritta: sostituire temporaneamente la riga della condizione con `if name == out.Status {`, rieseguire `go test ./internal/tracker/ -run TestGuessMappingPriority -v` e verificare che il sottotest "never reuses the column taken by assignee" **fallisca**; poi ripristinarla e verificare che torni verde. Riportare entrambi gli output.

- [ ] **Step 6: Commit**

```bash
git add internal/tracker/mapping.go internal/tracker/mapping_test.go
git commit -m "feat(tracker): guess the priority column, never reusing another role's"
```

---

### Task 4: La risoluzione, condivisa fra i due ruoli

**Files:**
- Modify: `internal/service/service.go` (`resolveAssignee`, più il nuovo helper e `resolvePriority`)
- Test: `internal/service/service_test.go`

**Interfaces:**
- Produces:
  - `func (s *Service) resolveOption(ctx context.Context, role, query, column string) (string, error)` — non esportata: cerca la colonna nello schema e risolve il valore contro le sue opzioni.
  - `func (s *Service) resolvePriority(ctx context.Context, f tracker.Fields) (tracker.Fields, error)`
- Consumes: `tracker.ResolveOption`, `config.Properties.Priority`.

**Perché un helper e non una copia:** `resolveAssignee` fa tre cose — traduce `me`, cerca la colonna nello schema, risolve il valore. Solo la prima è specifica dell'assignee. Copiare le altre due per il terzo caso significherebbe tre punti da correggere quando il messaggio d'errore cambia; il reviewer del ruolo gemello aveva già segnalato la duplicazione fra `resolveAssignee` e `List`. Questo NON è la generalizzazione dei ruoli che lo spec §6 scarta: i ruoli restano campi espliciti, è solo il lookup a smettere di essere copiato.

- [ ] **Step 1: Write the failing test**

In `internal/service/service_test.go`, estendere prima le due costanti condivise (`schemaJSON`, `rowJSON`) con la colonna:

- in `schemaJSON`, dentro `properties`:
  `"Urgenza":{"name":"Urgenza","type":"select","select":{"options":[{"name":"ALTA"},{"name":"MEDIA"},{"name":"NORMALE"}]}}`
- in `rowJSON`, dentro `properties`:
  `"Urgenza":{"type":"select","select":{"name":"ALTA"}}`

e l'helper del profilo:

```go
// priorityProfile is assigneeProfile with the priority role mapped too, which
// is what the real board looks like.
func priorityProfile() config.Profile {
	p := assigneeProfile("")
	p.Properties.Priority = "Urgenza"
	return p
}
```

Poi i test:

```go
func TestUpsertResolvesPriority(t *testing.T) {
	var written map[string]any
	srv := capturingRoutes(t, "", &written)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), priorityProfile())
	_, err := s.Upsert(context.Background(), tracker.Fields{Ticket: "BDF-231", Priority: "alta"}, nil)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, _ := json.Marshal(written["Urgenza"])
	if want := `{"select":{"name":"ALTA"}}`; string(got) != want {
		t.Errorf("Urgenza = %s, want %s", got, want)
	}
}

func TestSetByIDResolvesPriority(t *testing.T) {
	// The third write path, the one that shares no code with Set.
	var written map[string]any
	srv := capturingRoutes(t, rowJSON, &written)
	defer srv.Close()

	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), priorityProfile())
	_, err := s.SetByID(context.Background(), testPageID, tracker.Fields{Priority: "media"}, nil)
	if err != nil {
		t.Fatalf("SetByID: %v", err)
	}

	got, _ := json.Marshal(written["Urgenza"])
	if want := `{"select":{"name":"MEDIA"}}`; string(got) != want {
		t.Errorf("Urgenza = %s, want %s", got, want)
	}
}

func TestResolvePriorityEdges(t *testing.T) {
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()
	ctx := context.Background()

	t.Run("an absent priority is left alone", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), priorityProfile())
		if _, err := s.resolvePriority(ctx, tracker.Fields{Status: "Fatto"}); err != nil {
			t.Fatalf("an absent priority must not fail: %v", err)
		}
	})

	t.Run("an unknown value fails with the allowed ones", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), priorityProfile())
		_, err := s.resolvePriority(ctx, tracker.Fields{Priority: "URGENTISSIMA"})
		var invalid *tracker.ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("error = %v, want *tracker.ValidationError", err)
		}
	})

	t.Run("unmapped role with a value", func(t *testing.T) {
		s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("")) // no Priority
		if _, err := s.resolvePriority(ctx, tracker.Fields{Priority: "ALTA"}); err == nil {
			t.Fatal("resolvePriority = nil error, want a failure naming --priority-prop")
		}
	})
}

func TestAssigneeResolutionIsUnchanged(t *testing.T) {
	// The helper extraction must not move the assignee's behaviour: same
	// canonical value, same error type for an unknown name.
	var seen []string
	srv := routes(t, "", &seen)
	defer srv.Close()
	ctx := context.Background()
	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("Marco Arnulfo"))

	f, err := s.resolveAssignee(ctx, tracker.Fields{Assignee: "me"})
	if err != nil {
		t.Fatalf("resolveAssignee: %v", err)
	}
	if f.Assignee != "Marco Arnulfo" {
		t.Errorf("Assignee = %q, want %q", f.Assignee, "Marco Arnulfo")
	}

	_, err = s.resolveAssignee(ctx, tracker.Fields{Assignee: "Marko"})
	var invalid *tracker.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want *tracker.ValidationError", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/ -run 'Priority' -v`
Expected: FAIL, non compila — `s.resolvePriority undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/service/service.go`, estrarre il nucleo comune e aggiungere il gemello:

```go
// resolveOption turns what the user typed into the exact option a mapped
// column carries. Shared by the roles that live on a select, so the schema
// lookup and its two failure messages exist once rather than once per role.
func (s *Service) resolveOption(ctx context.Context, role, query, column string) (string, error) {
	if column == "" {
		return "", fmt.Errorf(
			"%s was set to %q but no %s property is mapped; "+
				"run 'notion-track init --%s-prop <name>' to map it", role, query, role, role)
	}
	schema, err := s.Schema(ctx)
	if err != nil {
		return "", err
	}
	prop, ok := schema.Properties[column]
	if !ok {
		return "", fmt.Errorf(
			"property %q is configured but does not exist in the data source; "+
				"run 'notion-track doctor' to see the current schema", column)
	}
	return tracker.ResolveOption(role, query, prop.Options)
}

// resolvePriority turns what the user typed into the exact option the priority
// column carries. Unlike the assignee there is no identity to translate first:
// a priority is nobody's.
func (s *Service) resolvePriority(ctx context.Context, f tracker.Fields) (tracker.Fields, error) {
	if f.Priority == "" {
		return f, nil
	}
	resolved, err := s.resolveOption(ctx, "priority", f.Priority, s.profile.Properties.Priority)
	if err != nil {
		return f, err
	}
	f.Priority = resolved
	return f, nil
}
```

e riscrivere il corpo di `resolveAssignee` perché usi lo stesso helper, lasciando intatta la sola parte che gli è propria — la traduzione di `me` e `ErrNoIdentity`:

```go
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
	resolved, err := s.resolveOption(ctx, "assignee", query, s.profile.Properties.Assignee)
	if err != nil {
		return f, err
	}
	f.Assignee = resolved
	return f, nil
}
```

Infine chiamare `resolvePriority` nei **tre** percorsi di scrittura, subito dopo la chiamata a `resolveAssignee` già presente in `Upsert`, `Set` e `SetByID`:

```go
	f, err = s.resolvePriority(ctx, f)
	if err != nil {
		return Result{}, err
	}
```

(in `Upsert` e `Set` `err` è già dichiarata dalla riga dell'assignee, quindi `=`; in `SetByID` seguire la forma che quel corpo già usa.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/service/ -v`
Expected: PASS, inclusi tutti i test dell'assignee — che è il punto del test di non-regressione.

- [ ] **Step 5: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "feat(service): resolve the priority on every write, sharing the lookup"
```

---

### Task 5: Il filtro e il piano

**Files:**
- Modify: `internal/service/service.go` (`ListFilter`, `List`), `internal/service/plan.go` (`planFor`)
- Test: `internal/service/service_test.go`, `internal/service/plan_test.go`

**Interfaces:**
- Produces: `service.ListFilter.Priority string`, **in coda** allo struct (dopo `Unassigned`), perché `mcp.ListFilter` gli si converte direttamente.

- [ ] **Step 1: Write the failing test**

In `internal/service/plan_test.go`:

```go
func TestPlanForPriority(t *testing.T) {
	// Without this the plan for `set --priority ALTA --dry-run` is empty: it
	// would report a write that changes nothing, which is worse than silence.
	props := config.Properties{
		Ticket: "Nome task", Title: "Nome task", Status: "Stato",
		Assignee: "Referente", Priority: "Urgenza",
	}
	plan := planFor("updated", "page-1", "", tracker.Fields{Priority: "ALTA"}, props, 0)

	var found bool
	for _, p := range plan.Properties {
		if p.Column == "Urgenza" && p.Value == "ALTA" {
			found = true
		}
	}
	if !found {
		t.Errorf("Properties = %#v, want Urgenza -> ALTA", plan.Properties)
	}
}
```

In `internal/service/service_test.go`:

```go
func TestListFiltersByPriority(t *testing.T) {
	var sent map[string]any
	srv := filterRoutes(t, &sent)
	defer srv.Close()
	s := New(notion.New("t", notion.WithBaseURL(srv.URL)), priorityProfile())
	ctx := context.Background()

	t.Run("alone", func(t *testing.T) {
		sent = nil
		if _, err := s.List(ctx, ListFilter{Priority: "alta"}); err != nil {
			t.Fatalf("List: %v", err)
		}
		if sent["property"] != "Urgenza" {
			t.Fatalf("filter = %#v, want it on Urgenza", sent)
		}
		if got := sent["select"].(map[string]any)["equals"]; got != "ALTA" {
			t.Errorf("filter value = %v, want the canonical option", got)
		}
	})

	t.Run("three clauses compound", func(t *testing.T) {
		sent = nil
		_, err := s.List(ctx, ListFilter{Status: "Fatto", Assignee: "mirko", Priority: "ALTA"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		clauses, ok := sent["and"].([]any)
		if !ok || len(clauses) != 3 {
			t.Fatalf("filter = %#v, want a compound of three", sent)
		}
		// Which three, not just how many.
		byProperty := map[string]bool{}
		for _, c := range clauses {
			byProperty[c.(map[string]any)["property"].(string)] = true
		}
		for _, want := range []string{"Stato", "Referente", "Urgenza"} {
			if !byProperty[want] {
				t.Errorf("no clause on %s in %#v", want, clauses)
			}
		}
	})

	t.Run("filtering on an unmapped role fails clearly", func(t *testing.T) {
		unmapped := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile(""))
		if _, err := unmapped.List(ctx, ListFilter{Priority: "ALTA"}); err == nil {
			t.Fatal("List = nil error, want a failure naming --priority-prop")
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/service/ -run 'TestPlanForPriority|TestListFiltersByPriority' -v`
Expected: FAIL, non compila — `unknown field Priority in struct literal of type ListFilter`.

- [ ] **Step 3: Write minimal implementation**

In `internal/service/service.go`, in `ListFilter`, in coda:

```go
	Priority   string
```

e in `List`, dopo il blocco dell'assignee:

```go
	if f.Priority != "" {
		name := s.profile.Properties.Priority
		resolved, err := s.resolveOption(ctx, "priority", f.Priority, name)
		if err != nil {
			return nil, err
		}
		prop := schema.Properties[name] // resolveOption has already proven it exists
		clauses = append(clauses, notion.EqualsFilter(name, prop.Type, resolved))
	}
```

In `internal/service/plan.go`, in `planFor`, una riga nella slice delle `PlannedProperty`:

```go
		{Column: props.Priority, Value: f.Priority},
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/service/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/
git commit -m "feat(service): filter listings by priority, and plan it in dry runs"
```

---

### Task 6: I flag e l'output

**Files:**
- Modify: `internal/cli/upsert.go` (`writeFlags`, `bindShared`, `fields`), `internal/cli/list.go`, `internal/cli/get.go`, `internal/cli/output.go`
- Test: `internal/cli/get_test.go` (fixture condivise), `internal/cli/upsert_test.go`, `internal/cli/list_test.go`

**Interfaces:**
- Produces: `--priority` su `upsert`/`set`, `list --priority`, `pageJSON.Priority` (in coda), `prioritySuffix` in `output.go`.
- **Non** modificare `internal/cli/set.go`: eredita tutto da `bindShared` e `wf.fields()`.

- [ ] **Step 1: Write the failing test**

Estendere prima le fixture condivise in `internal/cli/get_test.go`:

- in `cliSchemaJSON`: `"Urgenza":{"name":"Urgenza","type":"select","select":{"options":[{"name":"ALTA"},{"name":"MEDIA"},{"name":"NORMALE"}]}}`
- in `cliRowJSON`: `"Urgenza":{"type":"select","select":{"name":"ALTA"}}`
- in `assigneeProfile` e `assigneeProfileNoIdentity`, sotto `properties:`: `      priority: Urgenza`

Poi i test. In `internal/cli/upsert_test.go`:

```go
func TestSetWritesTheResolvedPriority(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--priority", "alta", "--config", cfg,
	}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}

	got, _ := json.Marshal(written["Urgenza"])
	if want := `{"select":{"name":"ALTA"}}`; string(got) != want {
		t.Errorf("Urgenza = %s, want %s", got, want)
	}
}

func TestPriorityUsageErrorsExitTwo(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--priority", "URGENTISSIMA", "--config", cfg,
	}); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
	if written != nil {
		t.Errorf("a usage error still wrote %v", written)
	}
}

func TestPriorityAndAssigneeTogether(t *testing.T) {
	// Two roles, one write: both columns must reach the payload.
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	if code := executeArgs([]string{
		"set", "--ticket", "BDF-231", "--priority", "media", "--assignee", "mirko", "--config", cfg,
	}); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	urgenza, _ := json.Marshal(written["Urgenza"])
	referente, _ := json.Marshal(written["Referente"])
	if string(urgenza) != `{"select":{"name":"MEDIA"}}` {
		t.Errorf("Urgenza = %s", urgenza)
	}
	if string(referente) != `{"select":{"name":"Mirko Spinato"}}` {
		t.Errorf("Referente = %s", referente)
	}
}
```

In `internal/cli/get_test.go`:

```go
func TestGetJSONCarriesThePriority(t *testing.T) {
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
	if row["priority"] != "ALTA" {
		t.Errorf("priority = %v, want %q", row["priority"], "ALTA")
	}
}

func TestGetHumanOutputShowsPriorityBeforeTheAssignee(t *testing.T) {
	cfg := stubForGet(t, assigneeProfile)

	out := captureStdout(t, func() {
		executeArgs([]string{"get", "--ticket", "BDF-231", "--config", cfg})
	})

	if !strings.Contains(out, "!ALTA  @Mirko Spinato") {
		t.Errorf("output = %q, want the priority before the assignee", out)
	}
}
```

In `internal/cli/list_test.go`:

```go
func TestListFiltersByPriorityFlag(t *testing.T) {
	var sent map[string]any
	cfg := stubForList(t, assigneeProfile, &sent)

	captureStdout(t, func() {
		if code := executeArgs([]string{
			"list", "--priority", "alta", "--config", cfg,
		}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	if sent["property"] != "Urgenza" {
		t.Fatalf("filter = %#v, want it on Urgenza", sent)
	}
}

func TestListRowsShowThePriority(t *testing.T) {
	var sent map[string]any
	cfg := stubForList(t, assigneeProfile, &sent)

	out := captureStdout(t, func() {
		executeArgs([]string{"list", "--config", cfg})
	})

	if !strings.Contains(out, "!ALTA") {
		t.Errorf("output = %q, want the priority in the row", out)
	}
}
```

Il test di non-regressione `TestListHumanRowsAreUnchangedWithoutTheRole` esiste già e usa il profilo di default: deve continuare a passare **senza modifiche**. Se fallisce, il segmento nuovo non è additivo come deve essere.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'Priority' -v`
Expected: FAIL — `unknown flag: --priority`.

- [ ] **Step 3: Write minimal implementation**

In `internal/cli/output.go`, accanto ad `assigneeSuffix`:

```go
// prioritySuffix is assigneeSuffix's twin, and exists for the same reason: the
// two-space separator and the sigil must not drift between get and list.
//
// A different sigil from the assignee's "@" on purpose — two marks for two
// different things, readable in a row that carries both and has no header.
func prioritySuffix(p notion.Page, props config.Properties) string {
	if value := p.Properties[props.Priority].Text; value != "" {
		return "  !" + value
	}
	return ""
}
```

In `internal/cli/upsert.go`: il campo `priority string` in `writeFlags`, il flag in `bindShared`

```go
	cmd.Flags().StringVar(&wf.priority, "priority", "",
		"how urgent the row is; a partial value is enough when it is unambiguous")
```

e `Priority: wf.priority` in `fields()`.

In `internal/cli/get.go`: `Priority string \`json:"priority"\`` in coda a `pageJSON`, `Priority: p.Properties[props.Priority].Text` in `toPageJSON`, e nella riga umana il nuovo segmento **prima** di quello dell'assignee:

```go
			priority := prioritySuffix(page, profile.Properties)
			assignee := assigneeSuffix(page, profile.Properties)
			if ticketIsTitle(profile.Properties) {
				cmd.Printf("%s  [%s]%s%s\n  %s\n",
					page.Properties[profile.Properties.Title].Text, status,
					priority, assignee, page.URL)
				return nil
			}
			cmd.Printf("%s  %s  [%s]%s%s\n  %s\n",
				page.Properties[profile.Properties.Ticket].Text,
				page.Properties[profile.Properties.Title].Text,
				status, priority, assignee, page.URL)
```

In `internal/cli/list.go`: il flag `--priority`, `Priority: priority` nel `service.ListFilter`, e lo stesso segmento nelle due righe di stampa, sempre prima dell'assignee. I formati diventano `"%-20s %-40s [%s]%s%s\n"` e `"%-61s [%s]%s%s\n"`.

**`mcp.Row` va esteso nello stesso commit** (`Priority string \`json:"priority"\`` in coda), o la conversione diretta in `internal/cli/mcp.go` non compila. Il resto della superficie MCP è il Task 8.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cli/ -v`
Expected: PASS, incluso il test di non-regressione sul formato delle righe.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/ internal/mcp/server.go
git commit -m "feat(cli): add --priority, and show it in get and list"
```

---

### Task 7: `init`, `doctor`, wizard

**Files:**
- Modify: `internal/cli/init.go`, `internal/service/doctor.go`, `internal/tui/wizard.go`
- Test: `internal/cli/init_test.go`, `internal/service/doctor_test.go`, `internal/tui/wizard_test.go`

**Interfaces:**
- Produces: `init --priority-prop`; `validateMapping` guadagna un sesto parametro (entrambi i chiamanti vanno aggiornati); il ruolo `priority` in `checkProperties`; la riga nel wizard.

- [ ] **Step 1: Write the failing test**

In `internal/cli/init_test.go`:

```go
func TestInitMapsThePriorityColumn(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	if code := executeArgs(initArgs(cfg, "--priority-prop", "Urgenza")); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if got := writtenProfile(t, cfg).Properties.Priority; got != "Urgenza" {
		t.Errorf("priority = %q, want %q", got, "Urgenza")
	}
}

func TestInitRejectsAPriorityColumnOfTheWrongType(t *testing.T) {
	cfg := withStubbedAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(cliSchemaJSON))
	})
	withIsolatedUserConfigDir(t)
	withInteractivePrompt(t, false, nil, nil)

	// Name is the title column: never usable as a priority.
	if code := executeArgs(initArgs(cfg, "--priority-prop", "Name")); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}
}

func TestPriorityPropIsAConfigFlag(t *testing.T) {
	// Missing from configFlags, `init --priority-prop X` at a terminal opens
	// the wizard instead of configuring — silently, and only interactively.
	var found bool
	for _, f := range configFlags {
		if f == "priority-prop" {
			found = true
		}
	}
	if !found {
		t.Error("priority-prop is missing from configFlags")
	}
}
```

In `internal/service/doctor_test.go`:

```go
func TestDoctorTreatsThePriorityAsOptional(t *testing.T) {
	srv := doctorRoutes(t)
	defer srv.Close()
	t.Setenv(config.MeEnv, "")

	// assigneeProfile maps no priority, the way every profile written before
	// this feature does.
	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), assigneeProfile("")).
		Doctor(context.Background())

	props, ok := findCheck(checks, "properties")
	if !ok {
		t.Fatal("no properties check")
	}
	if props.Status == "fail" {
		t.Errorf("properties = fail (%s), want the optional role to be skipped", props.Detail)
	}
}

func TestDoctorChecksAMappedPriority(t *testing.T) {
	srv := doctorRoutes(t)
	defer srv.Close()
	t.Setenv(config.MeEnv, "")

	profile := assigneeProfile("")
	profile.Properties.Priority = "Urgenza rinominata"

	checks := New(notion.New("t", notion.WithBaseURL(srv.URL)), profile).
		Doctor(context.Background())

	props, ok := findCheck(checks, "properties")
	if !ok {
		t.Fatal("no properties check")
	}
	if props.Status != "fail" {
		t.Errorf("properties = %s (%s), want fail for a column that is gone", props.Status, props.Detail)
	}
}
```

In `internal/tui/wizard_test.go`:

```go
func TestWizardPriorityRole(t *testing.T) {
	var spec roleSpec
	var found bool
	for _, r := range roles {
		if r.name == "priority" {
			spec, found = r, true
		}
	}
	if !found {
		t.Fatal("no priority role in the wizard")
	}
	if !spec.optional {
		t.Error("the priority role must be optional")
	}
	if len(spec.types) != 1 || spec.types[0] != "select" {
		t.Errorf("types = %v, want [select]", spec.types)
	}

	var p config.Properties
	setRole(&p, "priority", "Urgenza")
	if p.Priority != "Urgenza" {
		t.Errorf("setRole left Priority = %q", p.Priority)
	}
	if got := roleValue(p, "priority"); got != "Urgenza" {
		t.Errorf("roleValue = %q, want %q", got, "Urgenza")
	}

	seen := map[string]string{}
	for _, r := range roles {
		if other, dup := seen[r.key]; dup {
			t.Errorf("key %q is used by both %q and %q", r.key, other, r.name)
		}
		seen[r.key] = r.name
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'Priority' -v`, `go test ./internal/service/ -run 'TestDoctor.*Priority' -v`, `go test ./internal/tui/ -run TestWizardPriorityRole -v`
Expected: FAIL — `unknown flag: --priority-prop`, nessun ruolo `priority` nel wizard.

- [ ] **Step 3: Write minimal implementation**

`internal/cli/init.go`:
- la variabile `priorityProp string` nel blocco `var` del comando;
- il flag: `cmd.Flags().StringVar(&priorityProp, "priority-prop", "", "select property holding the priority (optional)")`;
- `"priority-prop"` in `configFlags`;
- in `validateMapping`, il parametro `priority string` e il controllo `if _, err := check("priority", "priority-prop", priority, false, "select"); err != nil { return "", err }`;
- **entrambi** i chiamanti: il percorso a flag passa `priorityProp`, `runInitWizard` passa `res.Props.Priority`;
- `Priority: priorityProp` nel `config.Properties` scritto.

`internal/service/doctor.go`, in `checkProperties`: `"priority": s.profile.Properties.Priority` in `mapped`, `"priority": {"select"}` in `wantType`, `"priority": true` in `optionalRoles`, e `"priority"` in fondo alla slice `roles`. Nessun check dedicato: non c'è identità da risolvere.

`internal/tui/wizard.go`: una riga in `roles`

```go
	{name: "priority", key: "p", types: []string{"select"}, optional: true},
```

più i due `case "priority":` in `roleValue` (che restituisce `p.Priority`) e `setRole` (che assegna `p.Priority = value`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -v`
Expected: PASS su tutti i pacchetti.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/init.go internal/cli/init_test.go internal/service/doctor.go internal/service/doctor_test.go internal/tui/
git commit -m "feat: map the priority column in init, doctor and the wizard"
```

---

### Task 8: Manifest e MCP

**Files:**
- Modify: `internal/manifest/manifest.go`, `internal/cli/apply.go`, `internal/mcp/server.go`, `internal/cli/mcp.go`
- Test: `internal/manifest/manifest_test.go`, `internal/cli/apply_test.go`, `internal/mcp/server_test.go`

**Interfaces:**
- Produces: `manifest.Entry.Priority string`; `mcp.Fields.Priority` e `mcp.ListFilter.Priority` (entrambi **in coda**, allineati alle loro controparti); `priority` in `upsertArgs` e `listArgs`.
- Consumes: `tracker.Fields.Priority` (Task 2), `service.ListFilter.Priority` (Task 5), `mcp.Row.Priority` (già aggiunto nel Task 6).

- [ ] **Step 1: Write the failing test**

In `internal/manifest/manifest_test.go`:

```go
func TestManifestPriority(t *testing.T) {
	t.Run("csv", func(t *testing.T) {
		data := []byte("op,ticket,priority\nset,BDF-1,ALTA\n")
		entries, err := Parse("tasks.csv", data)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if entries[0].Priority != "ALTA" {
			t.Errorf("Priority = %q", entries[0].Priority)
		}
	})

	t.Run("json", func(t *testing.T) {
		data := []byte(`[{"op":"set","ticket":"BDF-1","priority":"alta"}]`)
		entries, err := Parse("tasks.json", data)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if entries[0].Priority != "alta" {
			t.Errorf("Priority = %q", entries[0].Priority)
		}
	})

	t.Run("a typo in the column name is still an error", func(t *testing.T) {
		data := []byte("op,ticket,prioriti\nset,BDF-1,ALTA\n")
		if _, err := Parse("tasks.csv", data); err == nil {
			t.Fatal("a typo must not be silently ignored")
		}
	})
}
```

In `internal/cli/apply_test.go`:

```go
func TestApplyWritesThePriority(t *testing.T) {
	var written map[string]any
	cfg := stubForAssignee(t, assigneeProfile, &written)

	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "tasks.csv")
	os.WriteFile(manifestPath, []byte("op,ticket,priority\nset,BDF-231,alta\n"), 0o600)

	captureStdout(t, func() {
		if code := executeArgs([]string{
			"apply", "--file", manifestPath, "--config", cfg,
		}); code != ExitOK {
			t.Fatalf("exit code = %d", code)
		}
	})

	got, _ := json.Marshal(written["Urgenza"])
	if want := `{"select":{"name":"ALTA"}}`; string(got) != want {
		t.Errorf("Urgenza = %s, want %s", got, want)
	}
}
```

In `internal/mcp/server_test.go` (`testRow()` guadagna `Priority: "ALTA"`):

```go
func TestUpsertToolCarriesThePriority(t *testing.T) {
	tracker := &fakeTracker{row: testRow()}
	session := connect(t, tracker)

	call(t, session, "upsert_task", map[string]any{
		"ticket": "BDF-231", "priority": "alta",
	}, nil)

	if len(tracker.upserted) != 1 || tracker.upserted[0].Priority != "alta" {
		t.Fatalf("upsert calls = %v, want one carrying the priority", tracker.upserted)
	}
}

func TestListToolFiltersByPriority(t *testing.T) {
	tracker := &fakeTracker{rows: []Row{testRow()}}
	session := connect(t, tracker)

	call(t, session, "list_tasks", map[string]any{"priority": "ALTA"}, nil)

	if len(tracker.listed) != 1 || tracker.listed[0].Priority != "ALTA" {
		t.Fatalf("list calls = %v, want one filtered by priority", tracker.listed)
	}
}

func TestGetToolExposesThePriority(t *testing.T) {
	tracker := &fakeTracker{row: testRow()}
	session := connect(t, tracker)

	var row Row
	call(t, session, "get_task", map[string]any{"ticket": "BDF-231"}, &row)

	if row.Priority != "ALTA" {
		t.Errorf("Priority = %q, want %q", row.Priority, "ALTA")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/manifest/ ./internal/cli/ ./internal/mcp/ -run 'Priority' -v`
Expected: FAIL — `unknown field "priority"`, e i campi mancanti in `mcp`.

- [ ] **Step 3: Write minimal implementation**

`internal/manifest/manifest.go`: `Priority string \`json:"priority,omitempty"\`` in `Entry`, `"priority"` in `fieldNames`, e il `case "priority": e.Priority = value` in `assign`.

`internal/cli/apply.go`: `Priority: entry.Priority` nel `tracker.Fields` costruito da `applyOne`, e la colonna nuova in `applyExample`.

`internal/mcp/server.go`: `Priority string` in coda a `Fields` e a `ListFilter`; `priority` in `upsertArgs` e `listArgs`, con un `jsonschema` che dica cosa accetta — è la documentazione che l'agente legge per decidere come chiamare il tool:

```go
	Priority string `json:"priority,omitempty" jsonschema:"how urgent the row is; must be one of the values the board offers, and a partial value is enough when unambiguous; omit to leave it unchanged"`
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -v`
Expected: PASS. In particolare `TestTheMCPConversionsStayDirect` in `internal/cli/mcp_test.go` deve continuare a compilare: è la prova che le tre coppie di struct sono rimaste identiche.

- [ ] **Step 5: Commit**

```bash
git add internal/manifest/ internal/cli/apply.go internal/cli/apply_test.go internal/mcp/
git commit -m "feat: carry the priority through apply and the MCP tools"
```

---

### Task 9: Documentazione e i cinque gate

**Files:**
- Modify: `README.md`, `README.it.md`, `skills/notion-track/SKILL.md`

- [ ] **Step 1: Aggiornare i due README**

Ogni punto dove l'assignee compare oggi ha un gemello da scrivere, e i due file restano paralleli: qualunque cosa si aggiunga a uno va aggiunta all'altro, nello stesso posto, dicendo la stessa cosa, ciascuno nella propria lingua.

- la tabella dei flag di `upsert`/`set`: `--priority`;
- i flag di `list`: `--priority`;
- i flag di `init`: `--priority-prop`;
- il contratto `--json`: la chiave `priority`, sempre presente, vuota quando la colonna non ha valore o il ruolo non è mappato;
- il formato del manifest: la colonna `priority` in CSV e la chiave in JSON;
- gli strumenti MCP: il nuovo argomento;
- la riga "Implemented today" della roadmap;
- un esempio nel flusso principale:

```bash
notion-track list --priority ALTA --status "Da fare"
notion-track list --priority ALTA --assignee me
notion-track set --ticket BDF-1 --priority alta --assignee mirko
```

Dire esplicitamente ciò che il ruolo **non** ha, perché chi conosce l'assignee se lo aspetta: nessun `--unpriority`, nessun `list --unprioritized`, nessun `me`.

- [ ] **Step 2: Aggiornare la skill**

In `skills/notion-track/SKILL.md`, i trigger — è ciò che fa scattare l'uso, in entrambe le lingue: "è urgente", "priorità alta", "cosa c'è di urgente", "cosa faccio prima", "mettilo in alta", e gli equivalenti inglesi. Più i flag e l'esempio combinato con l'assignee.

- [ ] **Step 3: I cinque gate**

```bash
gofmt -l .            # deve stampare nulla
staticcheck ./...
go vet ./...
go test ./...
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add README.md README.it.md skills/
git commit -m "docs: document the priority role across the READMEs and the skill"
```

---

## Ordine e dipendenze

```
1 config
└── 2 payload ── 3 mapping
    └── 4 resolveOption + resolvePriority
        └── 5 ListFilter + planFor
            └── 6 flag e output  (introduce le fixture CLI estese e mcp.Row.Priority)
                ├── 7 init/doctor/wizard
                └── 8 manifest/MCP
                    └── 9 docs
```

I task 1-6 vanno in ordine stretto. Il 7 e l'8 dipendono entrambi dal 6 (che estende le fixture CLI condivise) ma non l'uno dall'altro. Il 9 è l'ultimo, sempre.
