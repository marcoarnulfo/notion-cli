# notion-track — Indirizzamento per `unique_id` (BDF-NN) — Design doc

> Data: 2026-07-29 · Stato: approvato in brainstorming, da implementare
> Repo: `notion-cli` · Modulo: `github.com/marcoarnulfo/notion-cli` · Binario: `notion-track`
> Parente stretto di `2026-07-27-notion-track-assignee-design.md`, che resta il documento
> di riferimento per come un ruolo opzionale entra nel sistema.

Documento in italiano (convenzione ereditata: i design doc restano in italiano, il resto
del repo è in inglese).

---

## 1. Obiettivo

La board porta una colonna che notion-track ignora del tutto:

```
ID    unique_id -> prefisso "BDF", numeri assegnati da Notion
```

È il nome con cui le persone chiamano i task fra loro — *"guarda BDF-271"* — e l'unica cosa
che oggi la CLI non sa fare è proprio riferirsi a un task con quel nome. Oggi si indirizza
per titolo esatto (`--ticket`) o per page-id/URL (`--page-id`): il primo è fragile ai
rename e alle omonimie, il secondo è un UUID che nessuno tiene a mente.

Questo documento aggiunge un settimo ruolo, **`id`**, e con esso un terzo modo di
indirizzare una riga:

```
notion-track set --id BDF-271 --status "Fatto"
```

### 1.1 Il malinteso da cui parte

Il limite era stato attribuito all'API Notion: *"la colonna ID è di tipo `unique_id`, che
l'API non consente di filtrare"*. È falso. L'API filtra benissimo:

```json
{"filter": {"property": "ID", "unique_id": {"equals": 271}}}
```

Il muro è interamente nella CLI: `grep -rn "unique_id" --include="*.go" .` non restituisce
nulla. Il tipo non è contemplato in nessuno dei sette punti che lo toccherebbero (§9).

### 1.2 Non-goal dichiarati

- **Niente scrittura.** `unique_id` è read-only per costruzione: il numero lo assegna
  Notion alla creazione della riga. Nessun percorso di questo design mette mai una colonna
  `unique_id` in un payload di scrittura, e §3 spiega perché ciò non richiede nemmeno
  un'esclusione esplicita.
- **Niente manifest.** `apply` non guadagna una colonna `id`. Richiederebbe di allentare la
  regola *"ticket obbligatorio"* in *"ticket-o-id"*, che è la validazione più delicata del
  manifest, per un caso d'uso che nessuno ha chiesto. Il campo `id` compare comunque nelle
  righe che `apply` stampa, perché passano da `toPageJSON` (§6).
- **Niente argomenti MCP.** I tool `get` e `set` non guadagnano un argomento `id`. Le righe
  che restituiscono lo portano (§6.2), perché quello è un cambio di `mcp.Row`, non della
  superficie dei tool.
- **Niente filtro su `list`.** Filtrare una lista per un identificatore unico restituisce
  al massimo una riga: è `get`, scritto peggio.
- **Nessun cambio a `--ticket`, `upsert`, o alla semantica di creazione.** Vedi §2.

---

## 2. La decisione centrale: ruolo separato, non estensione del ticket

La proposta di partenza era estendere la ticket-prop ad accettare anche `unique_id`
(`internal/tracker/mapping.go:69-70` oggi ammette solo `rich_text` e `title`). Suona
additivo. Non lo è, per due ragioni.

**La ticket-prop è una sola.** Sulla board reale punta alla colonna titolo — è per questo
che oggi si indirizza per titolo esatto. Rimapparla su `ID` non aggiunge `BDF-271`: toglie
il titolo. L'utente dovrebbe *scegliere* quale dei due modi di indirizzare tenere, quando
li vuole entrambi.

**`upsert` perderebbe la creazione.** `--ticket` oggi è due cose insieme: chiave di ricerca
*e* valore scritto quando la riga viene creata. Con `unique_id` la seconda metà sparisce,
perché il numero lo assegna Notion. `upsert --ticket BDF-999` su un id inesistente non
potrebbe creare niente con quell'id, e il comando diventerebbe un update travestito da
upsert — la cosa peggiore che possa capitare a un comando idempotente.

**La riformulazione.** `unique_id` non è una chiave scrivibile: è un identificatore di sola
lettura. Cioè esattamente ciò che `--page-id` già è, con la differenza che un umano lo sa
leggere. Trattarlo come tale — un terzo modo di indirizzare, accanto agli altri due, non al
posto di uno — lascia tutto il resto intatto:

```
notion-track get    --ticket "Sistemare visualizzazione da telefono"   # invariato
notion-track set    --id BDF-271 --status "Fatto"                      # nuovo
notion-track upsert --ticket "Nuovo task"                              # invariato, crea
notion-track get    --ticket "Nuovo task" --json                       # → "id": "BDF-272"
```

L'ultima riga chiude da sola il fastidio *"dopo un upsert il numero va letto dalla board"*,
senza che nessuno cambi il modo in cui indirizza le righe.

### 2.1 Il costo, detto per intero

Il ruolo separato non è meno lavoro: è lo stesso lavoro speso in superficie (un flag, un
campo di config, un campo JSON) invece che in semantica (un `upsert` che a volte non crea).
Si sceglie la superficie perché è ispezionabile — un flag in più si vede nell'help — mentre
un comando che cambia significato a seconda di come è mappata la config non si vede da
nessuna parte finché non morde.

---

## 3. Perché il payload di scrittura non ha bisogno di difese

`tracker.BuildProperties` costruisce il payload iterando sui ruoli **scrivibili**:
`title`, `ticket`, `status`, `due`, `assignee`, `priority`. Il ruolo `id` non viene
aggiunto a quell'elenco, quindi non c'è nessun ramo da escludere e nessun `if` da
dimenticare: la colonna `unique_id` non è raggiungibile dal codice di scrittura.

Questa è la differenza pratica fra le due strade di §2. Estendere il ticket avrebbe
richiesto un'esclusione esplicita dentro `BuildProperties` — oggi un ticket di tipo
`unique_id` cadrebbe nel `default:` di `payload.go:91` e fallirebbe con *"unsupported
type"* — cioè un ramo condizionale che qualcuno, un giorno, avrebbe potuto rimuovere
"semplificando". Un ruolo che il codice di scrittura non conosce non ha questo problema.

**Requisito verificabile:** nessun file sotto `internal/tracker/payload.go` menziona
`unique_id` o `Properties.ID`.

---

## 4. `internal/notion` — tre aggiunte

Nessuna delle tre cambia il comportamento di ciò che esiste.

### 4.1 Il prefisso nello schema

`Property` guadagna un campo:

```go
type Property struct {
	Name    string
	Type    string
	Options []string
	// Prefix is the string Notion prepends to a unique_id column's numbers
	// ("BDF" in "BDF-271"). Empty for every other type, and also for a
	// unique_id column configured without one.
	Prefix string
}
```

`GetSchema` lo decodifica dalla risposta di `GET /v1/data_sources/{id}`:

```json
"ID": {"id": "abc", "name": "ID", "type": "unique_id", "unique_id": {"prefix": "BDF"}}
```

`prefix` può essere `null` (colonna senza prefisso) → `Prefix == ""`.

### 4.2 La lettura

`decodePage` guadagna il ramo mancante. La risposta di una query porta:

```json
"ID": {"id": "abc", "type": "unique_id", "unique_id": {"prefix": "BDF", "number": 271}}
```

e il decoder scrive in `PropertyValue.Text` la **forma leggibile**:

| `prefix` | `number` | `Text` |
|---|---|---|
| `"BDF"` | `271` | `"BDF-271"` |
| `null` | `271` | `"271"` |

Scrivere in `Text` — lo stesso campo che già porta title, rich_text, select e status — è
ciò che fa comparire l'id nel JSON senza toccare `toPageJSON`, che legge già
`Properties[nome].Text`. È anche la forma che l'utente vede sulla board, quindi non c'è
nessuna traduzione mentale fra quello che legge nella UI e quello che gli restituisce la
CLI.

### 4.3 Il filtro

Nuova funzione accanto a `EqualsFilter`, **non** un'estensione:

```go
// UniqueIDEqualsFilter matches the row carrying one unique_id number.
//
// Separate from EqualsFilter because unique_id is the one property notion-track
// matches on whose filter value is a number, not a string. EqualsFilter's
// signature says "string" on purpose — its doc comment already warns that other
// types need a different operator or a non-string value — and widening it to
// carry both would break the promise that comment makes.
func UniqueIDEqualsFilter(property string, number int64) Filter {
	return Filter{
		"property":  property,
		"unique_id": map[string]int64{"equals": number},
	}
}
```

Forma prodotta, verificata contro l'API:

```json
{"property": "ID", "unique_id": {"equals": 271}}
```

---

## 5. `internal/tracker/uniqueid.go` — il parsing, dominio puro

File nuovo. Nessun I/O, come tutto il resto di `internal/tracker`.

```go
// ParseUniqueID turns what the user typed into the number Notion filters on.
//
// prefix is the column's own prefix, read from the schema: it is what makes
// "ABC-271 does not belong to this board" a message we can produce before any
// request is sent, rather than a query that quietly returns nothing.
func ParseUniqueID(input, prefix string) (int64, error)
```

### 5.1 Regole, in ordine

1. `strings.TrimSpace` sull'input.
2. Input vuoto → errore.
3. Se l'input è **interamente cifre ASCII** → è il numero, nessun prefisso da controllare.
4. Altrimenti, se contiene `-`: taglio all'**ultimo** `-`. Entrambe le metà devono essere
   non vuote, e la destra interamente cifre ASCII. La sinistra è il prefisso dichiarato.
5. Altrimenti → errore "non è un numero".
6. Confronto prefisso: dichiarato vs schema, **case-insensitive** (`strings.EqualFold`).
7. Il numero deve essere `>= 1`.

### 5.2 Tabella dei casi

Con `prefix` di schema `"BDF"`, salvo dove indicato:

| Input | Esito | Motivo |
|---|---|---|
| `BDF-271` | `271` | forma canonica |
| `bdf-271` | `271` | confronto case-insensitive |
| `271` | `271` | prefisso opzionale in input |
| `  BDF-271  ` | `271` | trim |
| `ABC-271` | errore | prefisso di un'altra board |
| `BDF-271` con schema `prefix == ""` | errore | la colonna non ha prefisso |
| `271` con schema `prefix == ""` | `271` | caso normale della colonna senza prefisso |
| `BDF-` | errore | metà destra vuota |
| `BDF-abc` | errore | metà destra non numerica |
| `-271` | errore | metà sinistra vuota — **non** un prefisso vuoto valido |
| `BDF-0`, `0` | errore | gli id Notion partono da 1 |
| `` (vuoto) | errore | gestito a monte da `ErrEmptyID`, ma la funzione regge comunque |
| `٢٧١` (cifre arabo-indiane) | errore | vedi §5.3 |
| `99999999999999999999` | errore | overflow di `int64`, riportato come input non valido |

### 5.3 La trappola delle cifre

"Interamente cifre" va implementato con un controllo ASCII esplicito (`c >= '0' && c <= '9'`),
**non** con `unicode.IsDigit`: quest'ultimo accetta le cifre arabo-indiane e le decine di
altri sistemi numerici Unicode, che poi `strconv.ParseInt` rifiuta. Il risultato sarebbe un
errore di parsing generico al posto del messaggio giusto, per un input che nessuno ha
digitato di proposito ma che un copia-incolla può produrre.

Il piano include un test su `٢٧١` proprio per questo: è il caso che distingue le due
implementazioni.

### 5.4 Il tipo d'errore

```go
// InvalidIDError marks an --id value that cannot be turned into the number
// Notion filters on. Callers map it onto the "invalid usage" exit code, which is
// how apply and the MCP server — neither of which ever touches cobra — get the
// same exit 2 the CLI gives.
type InvalidIDError struct {
	Value  string // what the user typed, verbatim
	Reason string // the clause after the colon in Error()
}

func (e *InvalidIDError) Error() string {
	return fmt.Sprintf("invalid id %q: %s", e.Value, e.Reason)
}
```

Nessun campo `Prefix`: il prefisso della board è già dentro `Reason` in tutti i casi in cui è lui il problema, e un campo che nessun chiamante legge è solo una cosa in più da tenere allineata.

Messaggi prodotti:

| Caso | `Reason` | Messaggio completo |
|---|---|---|
| prefisso sbagliato | `this board's ids start with "BDF"` | `invalid id "ABC-271": this board's ids start with "BDF"` |
| prefisso su colonna senza | `this board's ids have no prefix, so a bare number is expected` | `invalid id "BDF-271": this board's ids have no prefix, so a bare number is expected` |
| malformato, con prefisso | `expected a number, optionally prefixed (e.g. "BDF-1" or "1")` | `invalid id "BDF-abc": expected a number, optionally prefixed (e.g. "BDF-1" or "1")` |
| malformato, senza prefisso | `expected a number (e.g. "1")` | `invalid id "x": expected a number (e.g. "1")` |
| numero < 1 | `ids start at 1` | `invalid id "0": ids start at 1` |

---

## 6. La superficie

### 6.1 I flag

`--id` su `get` e `set`, mutuamente esclusivo con gli altri due modi di indirizzare:

```go
cmd.MarkFlagsMutuallyExclusive("ticket", "page-id", "id")
cmd.MarkFlagsOneRequired("ticket", "page-id", "id")
```

`get` e `set` già dichiarano la coppia `ticket`/`page-id` in entrambi i modi: si aggiunge
il terzo nome a chiamate esistenti. Attenzione a dove sono: quelle di `set` **non** stanno
in `set.go`, ma in `bindWithPageID`, dentro `internal/cli/upsert.go` — è il binder condiviso
dei flag di scrittura, e `set.go` lo invoca in una riga sola.

`writeFlags` guadagna quindi un campo `id string`. Ciò che **non** deve guadagnare è
`writeFlags.fields()`: quel metodo costruisce `tracker.Fields`, cioè i valori da scrivere,
e l'id non è un valore da scrivere. È la stessa separazione che `pageID` già rispetta.

Come per `--page-id`, il ramo si sceglie su `cmd.Flags().Changed("id")` e non sul valore:
`--id ""` deve fallire come flag vuoto, non ricadere silenziosamente sul percorso del
ticket.

Testo dell'help:

```
--id string   board id of the row (e.g. "BDF-271", or just "271"); requires an
              id property mapped in the profile
```

`upsert` **non** guadagna `--id`: la sua chiave è il ticket, e la creazione di una riga non
può indirizzare un id che ancora non esiste.

### 6.2 Il campo nel JSON, e il vincolo che ha già morso

`pageJSON` guadagna un campo, **in prima posizione**:

```go
type pageJSON struct {
	ID             string `json:"id"`
	Ticket         string `json:"ticket"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	PageID         string `json:"page_id"`
	URL            string `json:"url"`
	LastEditedTime string `json:"last_edited_time"`
	Assignee       string `json:"assignee"`
	Priority       string `json:"priority"`
}
```

Prima posizione perché è l'identità della riga così come la vede un umano, e mettendola in
testa l'ordine di lettura del JSON (`id, ticket, title, status`) è lo stesso in cui la
board mostra una riga.

`toPageJSON` la riempie come tutte le altre: `p.Properties[props.ID].Text`. Come per
assignee e priority, il campo è vuoto sia quando il ruolo non è mappato sia quando la riga
non porta valore — la chiave c'è sempre, così nessuno script deve ramificare.

**Vincolo bloccante.** `pageJSON` e `mcp.Row` si convertono direttamente
(`mcp.Row(toPageJSON(...))` in `internal/cli/mcp.go`), il che compila solo finché le due
struct restano identiche per nome, tipo **e ordine** dei campi. `mcp.Row` deve quindi
guadagnare lo stesso campo nella stessa posizione **nello stesso commit**. Non è una
raccomandazione di stile: è esattamente il difetto che la review del ruolo `priority` ha
intercettato come BLOCKER, dove estendere una struct senza la gemella avrebbe lasciato
`internal/cli` non compilabile per sei task.

Lo stesso vale per il campo che il ruolo aggiunge a `config.Properties`, ma lì non c'è
conversione diretta e quindi nessun vincolo di ordine.

### 6.3 Cosa lo eredita gratis

`list --json`, l'output JSON di `upsert`/`set`/`apply` e le righe restituite dai tool MCP
passano tutti da `toPageJSON`: guadagnano il campo `id` senza una riga di codice
dedicata. È il motivo per cui §1.2 può escludere manifest e argomenti MCP senza che
l'informazione resti irraggiungibile da quei percorsi.

---

## 7. Il ruolo `id` nella configurazione

Entra sui binari già battuti da `due`, `assignee` e `priority`: opzionale ovunque.

### 7.1 `internal/config`

```go
// ID is the column carrying Notion's own row identifier ("BDF-271"). Optional,
// and read-only by nature: it is an addressing mode, never a value to write.
ID string `yaml:"id,omitempty"`
```

Nessuna migrazione e nessun bump di `CurrentSchemaVersion`: un campo `omitempty` assente da
un profilo esistente si legge come `""`, che è già il valore "non mappato" con cui gli
altri ruoli opzionali convivono.

### 7.2 `init`

Flag `--id-prop`, e una riga nella validazione dei tipi di `internal/cli/init.go`:

```go
if _, err := check("id", "id-prop", id, false, "unique_id"); err != nil {
	return "", err
}
```

`false` = opzionale, come `due`/`assignee`/`priority`.

### 7.3 Il wizard TUI

Voce nuova in fondo a `roles`, dove stanno gli opzionali:

```go
{name: "id", key: "u", types: []string{"unique_id"}, optional: true},
```

Il tasto è `u`, di `unique_id`: `i` è del titolo, `d` della scadenza. È la stessa collisione
che il codice già commenta per `title` (*"title's key is 'i', not 't': ticket claimed 't'"*),
e va commentata allo stesso modo.

### 7.4 `GuessMapping`

La colonna `unique_id` unica vince **per tipo, non per nome**:

```go
if ids := byType["unique_id"]; len(ids) == 1 {
	out.ID = ids[0]
}
```

Qui la regola "unico candidato di tipo adatto" è sicura, al contrario che per `assignee`:
nessun altro ruolo accetta `unique_id`, quindi non c'è nessun ruolo a cui rubare la
colonna. E il fatto che `"id"` sia già in `ticketNames` non crea conflitto, perché i
candidati del ticket si pescano solo fra `rich_text` e `title`.

Con due o più colonne `unique_id` la guess resta vuota, come per ogni altro ruolo: una
guess sbagliata che l'utente conferma distrattamente è peggio di una domanda.

### 7.5 `doctor`

`"id"` entra in tre punti di `internal/service/doctor.go`:

- `wantType["id"] = []string{"unique_id"}`
- l'elenco `roles`
- `optionalRoles`, con la stessa motivazione degli altri tre: una board può legittimamente
  non avere una colonna id, e non averla non è un guasto.

---

## 8. `internal/service` — l'indirizzamento e i suoi errori

### 8.1 La risoluzione

```go
// findByUniqueID resolves a board id ("BDF-271") to the single row carrying it.
func (s *Service) findByUniqueID(ctx context.Context, input string) (notion.Page, error)
```

Sequenza, con l'errore di ciascun passo:

1. `s.profile.Properties.ID == ""` → `ErrNoIDProperty`, wrappato con il suggerimento:
   `id addressing was requested but no id property is mapped; run 'notion-track init --id-prop <name>' to map it`
2. La colonna non esiste nello schema → `property %q is configured but does not exist in the data source; run 'notion-track doctor' to see the current schema` (la stessa frase che `BuildProperties` già usa per lo stesso guasto).
3. Il tipo non è `unique_id` → `id property %q has type %q, not unique_id; run 'notion-track doctor'`
4. `ParseUniqueID(input, prop.Prefix)` → `*InvalidIDError`
5. `UniqueIDEqualsFilter` → `QueryPages`
6. Zero righe → `fmt.Errorf("%w: %s", ErrNotFound, input)`, come fa già `Get`
7. Più di una riga → errore esplicito. È impossibile per costruzione (un `unique_id` è
   unico), ma "impossibile" e "silenziosamente sbagliato" non sono la stessa cosa:
   prendere la prima nasconderebbe un guasto che nessun altro noterebbe. Testabile con un
   server finto che ne restituisce due.

### 8.2 I due punti d'ingresso

```go
func (s *Service) GetByUniqueID(ctx context.Context, input string) (notion.Page, error)
func (s *Service) SetByUniqueID(ctx context.Context, input string, f tracker.Fields, body *BodyRequest) (Result, error)
```

`SetByUniqueID` risolve l'id in page-id e **delega a `SetByID`**, che già esiste. La
scrittura continua ad avere un percorso solo: il nuovo modo di indirizzare finisce nel
vecchio prima che qualcosa venga scritto.

### 8.3 `ErrEmptyID`

```go
// ErrEmptyID means --id was passed with a blank value, the same shape of
// mistake ErrEmptyTicket and ErrEmptyPageID already describe.
var ErrEmptyID = errors.New("id must not be empty")
```

Controllato nel service e non nella CLI, per la ragione già scritta su `ErrEmptyTicket`: i
percorsi che non toccano cobra devono ereditare la guardia.

### 8.4 Exit code

Tre casi nuovi in `exitCodeFor` (`internal/cli/output.go`); il quarto è già coperto:

| Errore | Codice | Perché |
|---|---|---|
| `*tracker.InvalidIDError` | 2 (`ExitUsage`) | un id malformato è un comando da riscrivere, come `*ValidationError` |
| `service.ErrEmptyID` | 2 (`ExitUsage`) | stesso ragionamento di `ErrEmptyTicket`/`ErrEmptyPageID` |
| `service.ErrNoIDProperty` | 2 (`ExitUsage`) | "non ancora configurato", stessa classe di `config.ErrNotConfigured` |
| id inesistente sulla board | 3 (`ExitNotFound`) | già coperto: wrappa `service.ErrNotFound` |

---

## 9. Inventario dei file

L'analisi iniziale del problema aveva individuato quattro punti da toccare. Sono
quattordici più la documentazione — e uno dei quattro originali (`get`/`list --json`) si è
rivelato gratuito, perché §4.2 lo copre già.

| # | File | Cosa cambia |
|---|---|---|
| 1 | `internal/notion/types.go` | `Property.Prefix` |
| 2 | `internal/notion/datasource.go` | `GetSchema` decodifica `unique_id.prefix` |
| 3 | `internal/notion/query.go` | `decodePage` → `case "unique_id"`; `UniqueIDEqualsFilter` |
| 4 | `internal/tracker/uniqueid.go` | **nuovo** — `ParseUniqueID`, `InvalidIDError` |
| 5 | `internal/tracker/mapping.go` | `GuessMapping` per il ruolo `id` |
| 6 | `internal/config/config.go` | `Properties.ID` |
| 7 | `internal/service/service.go` | `findByUniqueID`, `GetByUniqueID`, `SetByUniqueID`, `ErrEmptyID`, `ErrNoIDProperty` |
| 8 | `internal/service/doctor.go` | `wantType`, `roles`, `optionalRoles` |
| 9 | `internal/cli/init.go` | `--id-prop` + `check(...)` |
| 10 | `internal/tui/wizard.go` | voce `roles` |
| 11 | `internal/cli/get.go` | `--id`, `pageJSON.ID`, `toPageJSON` |
| 12 | `internal/cli/set.go` | `--id` |
| 13 | `internal/cli/output.go` | `exitCodeFor` |
| 14 | `internal/mcp/server.go` | `Row.ID`, stessa posizione di `pageJSON.ID` |
| 15 | `README.md`, skill dell'agente | documentazione |

Non toccati, e la loro assenza da questo elenco è una verifica: `internal/tracker/payload.go`
(§3), `internal/manifest/`, `internal/cli/upsert.go`, `internal/cli/apply.go`.

---

## 10. Test

Il pattern è quello dei due ruoli precedenti: dominio puro a tabella, HTTP contro
`httptest`, e mutation testing sui test che storicamente non mordono.

**A tabella, senza rete** — `ParseUniqueID` con tutti i casi di §5.2, compresi `٢٧١` e
l'overflow.

**Contro `httptest`:**
- `GetSchema` estrae `prefix`, e lo lascia vuoto quando è `null`
- `decodePage` produce `"BDF-271"` con prefisso e `"271"` senza
- `findByUniqueID` invia esattamente `{"property":"ID","unique_id":{"equals":271}}` — il
  corpo della richiesta va ispezionato, non solo la risposta
- zero righe → `ErrNotFound`; due righe → errore, non la prima

**Superficie:**
- `get --id BDF-271` e `set --id BDF-271 --status X` percorrono la strada nuova
- i tre flag di indirizzamento sono mutuamente esclusivi, e almeno uno è richiesto
- `--id ""` esce 2, non ricade sul ticket
- `get --json` porta la chiave `id`, e `mcp.Row` la stessa

**Mutation testing obbligatorio** su:
- il `case "unique_id"` di `decodePage` — un test che verifica solo "non va in panic"
  passerebbe anche con `Text` vuoto: deve asserire la stringa esatta
- il confronto del prefisso in `ParseUniqueID` — un test che passa solo il caso canonico
  passerebbe anche senza il confronto
- la guardia `len(ids) == 1` in `GuessMapping`

---

## 11. Documentazione

`README.md` guadagna `--id` fra i modi di indirizzare, e la skill dell'agente la riga
corrispondente. Entrambe devono dire la stessa cosa: la review della action di release ha
già prodotto, in questo repo, una documentazione che contraddiceva il codice due righe più
sotto.

Va corretta anche l'affermazione sbagliata di §1.1 ovunque compaia — l'API Notion filtra
per `unique_id`, il limite era della CLI.
