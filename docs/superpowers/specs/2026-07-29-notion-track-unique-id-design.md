# notion-track — Indirizzamento per `unique_id` (BDF-NN) — Design doc

> Data: 2026-07-29 · Stato: approvato in brainstorming, rivisto dopo review, da implementare
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
nulla. Il tipo non è contemplato in nessuno dei punti che lo toccherebbero (§9).

### 1.2 Non-goal dichiarati

- **Niente scrittura.** `unique_id` è read-only per costruzione: il numero lo assegna
  Notion alla creazione della riga. Nessun percorso di questo design mette mai una colonna
  `unique_id` in un payload di scrittura, e §3 spiega perché ciò non richiede nemmeno
  un'esclusione esplicita.
- **Niente manifest, e l'id non compare nell'output di `apply`.** `apply` costruisce il suo
  output da `applyOutcome` (indice, op, ticket, azione, piano, errore): una struct che non
  contiene la pagina e non passa da `toPageJSON`. Dargli il campo `id` significherebbe
  cambiare quella struct, non ereditare qualcosa. Fuori scope in entrambi i sensi: niente
  colonna `id` nei manifest, e niente `id` in ciò che `apply` stampa.
- **Niente argomenti MCP.** I tool `get` e `set` non guadagnano un argomento `id`. Le righe
  che restituiscono lo portano (§6.2), perché quello è un cambio di `mcp.Row`, non della
  superficie dei tool.
- **Niente filtro su `list`.** Filtrare una lista per un identificatore unico restituisce
  al massimo una riga: è `get`, scritto peggio.
- **Niente id nella TUI di browse.** `internal/cli/browse.go` popola una `tui.Row` propria,
  che non passa da `toPageJSON`: mostrare l'id lì è un cambio separato, in un file che
  questo lavoro non tocca.
- **Niente id nel piano di `--dry-run`.** `set --id … --dry-run` continua a stampare il
  `page_id` che il piano già porta. Il piano descrive la scrittura, e la scrittura avviene
  per page-id qualunque sia il modo in cui la riga è stata trovata.
- **Nessun cambio a `--ticket`, `upsert`, o alla semantica di creazione.** Vedi §2.

---

## 2. La decisione centrale: ruolo separato, non estensione del ticket

La proposta di partenza era estendere la ticket-prop ad accettare anche `unique_id`. Oggi
il contratto "solo `rich_text` e `title`" è scritto in tre posti — `internal/cli/init.go:411`
(`check("ticket", …, "rich_text", "title")`), `internal/tui/wizard.go:57` e
`internal/service/doctor.go:70` — mentre `internal/tracker/mapping.go:69-70` è un quarto
posto diverso: lì si scelgono i *candidati* che `GuessMapping` propone, non ciò che la
configurazione accetta.

Estendere quel contratto suona additivo. Non lo è, per due ragioni.

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

**Requisito verificabile:** `internal/tracker/payload.go` non menziona `unique_id` né
`Properties.ID`.

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
ciò che evita **logica nuova** più a valle: `toPageJSON` guadagna un campo (§6.2), ma lo
riempie con la stessa riga con cui riempie tutti gli altri, e non ha bisogno di sapere che
`unique_id` esiste. È anche la forma che l'utente vede sulla board, quindi non c'è nessuna
traduzione mentale fra quello che legge nella UI e quello che gli restituisce la CLI.

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

Forma prodotta, verificata contro la reference dell'API per `Notion-Version: 2026-03-11`:

```json
{"property": "ID", "unique_id": {"equals": 271}}
```

L'API offre anche `does_not_equal`, `greater_than`, `greater_than_or_equal_to`,
`less_than`, `less_than_or_equal_to`. Non c'è `is_empty`, e non serve: un `unique_id` è
sempre valorizzato.

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
// Notion filters on. Callers map it onto the "invalid usage" exit code.
type InvalidIDError struct {
	Value  string // what the user typed, verbatim
	Reason string // the clause after the colon in Error()
}

func (e *InvalidIDError) Error() string {
	return fmt.Sprintf("invalid id %q: %s", e.Value, e.Reason)
}
```

Nessun campo `Prefix`: il prefisso della board è già dentro `Reason` in tutti i casi in cui
è lui il problema, e un campo che nessun chiamante legge è solo una cosa in più da tenere
allineata.

Il tipo vive in `internal/tracker` perché lì vive il parsing, non perché un chiamante
non-cobra debba produrlo: con manifest e tool MCP fuori scope (§1.2), oggi l'unico percorso
che lo genera è la CLI. La collocazione resta quella giusta il giorno in cui uno degli altri
due entrerà, ma non va motivata con un chiamante che non esiste.

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
in `set.go`, ma in `bindWithPageID`, dentro `internal/cli/upsert.go`. Quel binder è di
`set` soltanto — `upsert` usa `bind`, che richiede `--ticket` — e i due condividono
`bindShared`, che invece porta i flag senza semantica di indirizzamento e **non** va
toccato.

`writeFlags` guadagna quindi un campo `boardID string`. Ciò che **non** deve guadagnare è
`writeFlags.fields()`: quel metodo costruisce `tracker.Fields`, cioè i valori da scrivere,
e l'id non è un valore da scrivere. È la stessa separazione che `pageID` già rispetta.

Come per `--page-id`, il ramo si sceglie su `cmd.Flags().Changed("id")` e non sul valore:
`--id ""` deve fallire come flag vuoto, non ricadere silenziosamente sul percorso del
ticket.

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
board mostra una riga. L'ordine delle chiavi JSON non è osservabile da nessun parser
corretto; a doversi aggiornare sono solo gli esempi nella documentazione, che §11 copre.

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

### 6.3 L'output umano

Un id che esiste solo dentro `--json` sarebbe incoerente con §1: se `BDF-271` è il nome con
cui le persone chiamano i task, deve comparire dove le persone guardano. Oggi
`notion-track get --id BDF-271` risponderebbe senza mai mostrare `BDF-271`.

`get` e `list` guadagnano quindi un prefisso, sul modello dei due suffissi che già esistono
in `internal/cli/output.go`:

```go
// idPrefix formats a row's board id for human-readable output.
//
// A prefix and not a suffix, unlike assigneeSuffix and prioritySuffix: it is
// the row's name, and names read down the left edge of a list. Padded to a
// fixed width so list's columns stay aligned across rows whose ids differ in
// length; get prints one row, where the padding costs nothing.
//
// Empty when the role is unmapped, which is what keeps every existing
// profile's output byte-identical — the same rule the two suffixes follow.
func idPrefix(p notion.Page, props config.Properties) string {
	if id := p.Properties[props.ID].Text; id != "" {
		return fmt.Sprintf("%-10s ", id)
	}
	return ""
}
```

`listRowFormat` e `listMergedRowFormat` guadagnano un `%s` iniziale, e le due `Printf` di
`get` altrettanto. Per un profilo che non mappa il ruolo il prefisso è `""` e l'output
resta identico byte per byte, che è la regola già scritta nel commento sopra quei formati.

### 6.4 Cosa lo eredita gratis

`list --json` e l'output JSON di `upsert` e `set` passano da `toPageJSON`, e i tool MCP da
`mcp.Row`: guadagnano il campo `id` senza una riga di codice dedicata. `apply` **no** (§1.2).

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

Quattro punti, non uno. Ometterne uno produce un bug reale, non un'omissione cosmetica:

1. **Il flag**: `--id-prop`, opzionale.
2. **`configFlags`** (`internal/cli/init.go:139-142`): è l'elenco che decide se
   un'invocazione "sa già le risposte" o apre il wizard. Senza `"id-prop"` lì dentro,
   `notion-track init --id-prop ID` da terminale **ignorerebbe il flag e aprirebbe il
   wizard**.
3. **`validateMapping`**: una riga di validazione del tipo,
   `check("id", "id-prop", …, false, "unique_id")`. La funzione ha già sei parametri
   stringa posizionali e il settimo sarebbe la settima occasione di invertirne due senza
   che il compilatore dica niente: passa a prendere `config.Properties`. Entrambi i
   chiamanti — `init.go:215` (ramo wizard) e `init.go:327` (ramo flag) — vanno aggiornati,
   e il primo ha già la struct in mano.
4. **Il literal del profilo** (`init.go:356-359`): senza `ID: idProp` lì, la validazione
   passa e il profilo salvato non porta l'id.

### 7.3 Il wizard TUI

Anche qui più di un punto. La voce nuova in fondo a `roles`:

```go
{name: "id", key: "u", types: []string{"unique_id"}, optional: true},
```

Il tasto è `u`, di `unique_id`: `i` è del titolo, `d` della scadenza, e `u` è libero. È la
stessa collisione che il codice già commenta per `title` (*"title's key is 'i', not 't':
ticket claimed 't'"*), e va commentata allo stesso modo.

E i due accessori `roleValue` e `setRole` (`wizard.go:433-467`), che indirizzano i ruoli per
nome perché è così che l'utente li sceglie dallo schermo. **Senza il caso `"id"` in
entrambi, il tasto `u` è un no-op silenzioso**: il picker si apre, `enter` non scrive
niente, e la riga resta "not set" per sempre — anche quando `GuessMapping` aveva indovinato.

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

La guardia `len(ids) == 1` è **difensiva**, non un comportamento osservabile: Notion permette
una sola proprietà ID per database, quindi il caso "due o più" non è producibile dalla UI.
Sta lì per la stessa ragione del caso "due righe" di §8.1 — impossibile non è la stessa cosa
di silenziosamente sbagliato — ma non merita un mutation test, perché l'input che lo
eserciterebbe non esiste nel mondo reale.

### 7.5 `doctor`

`"id"` entra in **quattro** punti di `internal/service/doctor.go`:

- la mappa `mapped` (`doctor.go:61-68`): `"id": s.profile.Properties.ID`
- `wantType["id"] = []string{"unique_id"}`
- l'elenco `roles`
- `optionalRoles`

Il primo è quello che si dimentica, ed è quello che rompe tutto in silenzio: il loop legge
il nome configurato da `mapped[role]` (`doctor.go:94-95`). Con `"id"` nei `roles` ma non in
`mapped`, il nome è sempre `""`, il ruolo è opzionale, e `doctor` fa `continue` — non
verificherebbe **mai** né l'esistenza né il tipo della colonna, senza dire niente a nessuno.

`optionalRoles` per la stessa ragione degli altri tre: una board può legittimamente non
avere una colonna id, e non averla non è un guasto — è semplicemente una board che si
indirizza negli altri due modi.

---

## 8. `internal/service` — l'indirizzamento e i suoi errori

### 8.1 La risoluzione

```go
// findByUniqueID resolves a board id ("BDF-271") to the single row carrying it.
func (s *Service) findByUniqueID(ctx context.Context, input string) (notion.Page, error)
```

Sequenza, con l'errore di ciascun passo:

1. Input vuoto o soli spazi → `ErrEmptyID`
2. `s.profile.Properties.ID == ""` → `ErrNoIDProperty`, wrappato con il suggerimento:
   `id addressing was requested but no id property is mapped; run 'notion-track init --id-prop <name>' to map it`
3. La colonna non esiste nello schema → `property %q is configured but does not exist in the data source; run 'notion-track doctor' to see the current schema` (la stessa frase che `BuildProperties` già usa per lo stesso guasto).
4. Il tipo non è `unique_id` → `id property %q has type %q, not unique_id; run 'notion-track doctor'`
5. `ParseUniqueID(input, prop.Prefix)` → `*InvalidIDError`
6. `UniqueIDEqualsFilter` → `QueryPages`
7. Zero righe → `fmt.Errorf("%w: no row has id %s", ErrNotFound, input)`. Il testo esplicito
   serve: `ErrNotFound` è `errors.New("ticket not found")`, e wrapparlo come fa `Get`
   produrrebbe *"ticket not found: BDF-271"* per un comando che `--ticket` non l'ha mai
   usato. `errors.Is` e l'exit 3 restano intatti.
8. Più di una riga → errore esplicito. È impossibile per costruzione (un `unique_id` è
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

Il costo, dichiarato: `SetByID` richiama `resolvePage`, cioè un `GET /v1/pages/{id}` più il
controllo di appartenenza al data source — su una riga che `findByUniqueID` ha appena
ottenuto dalla query del profilo, e che quindi appartiene già a quel data source per
costruzione. È un round-trip in più per ogni `set --id`. Si accetta: un secondo percorso di
scrittura che salta `resolvePage` è esattamente il tipo di scorciatoia che poi diverge dal
primo, e il guadagno è una richiesta su un comando interattivo.

### 8.3 `ErrEmptyID`

```go
// ErrEmptyID means --id was passed with a blank value, the same shape of
// mistake ErrEmptyTicket and ErrEmptyPageID already describe.
var ErrEmptyID = errors.New("id must not be empty")
```

Controllato nel service e non nella CLI per la stessa ragione già scritta su
`ErrEmptyTicket`: la guardia sta dove sta la logica, così un secondo chiamante non deve
ricordarsi di riscriverla. Oggi il chiamante è uno solo.

### 8.4 Exit code

Tre casi nuovi in `exitCodeFor` (`internal/cli/output.go`); il quarto è già coperto:

| Errore | Codice | Perché |
|---|---|---|
| `*tracker.InvalidIDError` | 2 (`ExitUsage`) | un id malformato è un comando da riscrivere, come `*ValidationError` |
| `service.ErrEmptyID` | 2 (`ExitUsage`) | stesso ragionamento di `ErrEmptyTicket`/`ErrEmptyPageID` |
| `service.ErrNoIDProperty` | 2 (`ExitUsage`) | "non ancora configurato", stessa classe di `config.ErrNotConfigured` |
| id inesistente sulla board | 3 (`ExitNotFound`) | già coperto: wrappa `service.ErrNotFound` |

**Una divergenza dichiarata.** Gli altri errori "ruolo non mappato" — quelli di
`payload.go:59-62`, `service.go:242-244` e `service.go:586-588` per assignee e priority —
sono `fmt.Errorf` semplici che cadono nel default di `exitCodeFor` e **escono 1**. Il ruolo
`id` esce 2 perché è la risposta giusta secondo il significato documentato del codice: il
comando non può funzionare come scritto, e la correzione è dell'utente. Che gli altri tre
escano 1 è, con ogni probabilità, un difetto latente di quel codice; allinearli è un cambio
a sé, fuori dallo scope di questo lavoro. Va sistemato, non copiato.

---

## 9. Inventario dei file

L'analisi iniziale del problema aveva individuato quattro punti da toccare.

| # | File | Cosa cambia |
|---|---|---|
| 1 | `internal/notion/types.go` | `Property.Prefix` |
| 2 | `internal/notion/datasource.go` | `GetSchema` decodifica `unique_id.prefix` |
| 3 | `internal/notion/query.go` | `decodePage` → `case "unique_id"`; `UniqueIDEqualsFilter` |
| 4 | `internal/tracker/uniqueid.go` | **nuovo** — `ParseUniqueID`, `InvalidIDError` |
| 5 | `internal/tracker/mapping.go` | `GuessMapping` per il ruolo `id` |
| 6 | `internal/config/config.go` | `Properties.ID` |
| 7 | `internal/service/service.go` | `findByUniqueID`, `GetByUniqueID`, `SetByUniqueID`, `ErrEmptyID`, `ErrNoIDProperty` |
| 8 | `internal/service/doctor.go` | `mapped`, `wantType`, `roles`, `optionalRoles` (§7.5) |
| 9 | `internal/cli/init.go` | flag, `configFlags`, `validateMapping` + 2 chiamanti, literal del profilo (§7.2) |
| 10 | `internal/tui/wizard.go` | voce in `roles`, casi in `roleValue` e `setRole` (§7.3) |
| 11 | `internal/cli/get.go` | `pageJSON.ID`, `toPageJSON`, flag `--id`, riga umana |
| 12 | `internal/cli/list.go` | i due formati di riga guadagnano il prefisso |
| 13 | `internal/cli/output.go` | `idPrefix`, `exitCodeFor` |
| 14 | `internal/cli/upsert.go` | `writeFlags.boardID`, `bindWithPageID` a tre vie |
| 15 | `internal/cli/set.go` | il ramo `--id` |
| 16 | `internal/mcp/server.go` | `Row.ID`, stessa posizione di `pageJSON.ID` |
| 17 | `README.md`, `README.it.md`, `skills/notion-track/` | documentazione (§11) |

Non toccati, e la loro assenza da questo elenco è una verifica: `internal/tracker/payload.go`
(§3), `internal/manifest/`, `internal/cli/apply.go`, `internal/cli/browse.go` (§1.2).

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
- `--id ""` esce 2 **e dice "id must not be empty"** — il solo exit code non basta, perché
  la regressione temuta (ramo scelto sul valore invece che su `Changed`) porterebbe a
  `ErrEmptyTicket`, che esce 2 anche lui
- `get --json` porta la chiave `id`, e `mcp.Row` la stessa
- l'output umano di `get` e `list` mostra l'id quando il ruolo è mappato, e resta identico
  byte per byte quando non lo è

**Mutation testing obbligatorio** su:
- il `case "unique_id"` di `decodePage` — un test che verifica solo "non va in panic"
  passerebbe anche con `Text` vuoto: deve asserire la stringa esatta
- il confronto del prefisso in `ParseUniqueID` — un test che passa solo il caso canonico
  passerebbe anche senza il confronto

Non su `GuessMapping`: la guardia `len(ids) == 1` copre un input che Notion non permette di
costruire (§7.4).

---

## 11. Documentazione

Quattro file, non due, e devono dire tutti la stessa cosa:

- `README.md` e **`README.it.md`**, che sono traduzioni speculari sezione per sezione:
  aggiornarne uno solo produce esattamente la documentazione che si contraddice.
- `skills/notion-track/SKILL.md` e `skills/notion-track/README.md` — la skill dell'agente
  sta lì, alla radice del repo, **non** sotto `.claude/skills/`.

In ciascuno: `--id` fra i modi di indirizzare una riga, `--id-prop` fra i ruoli opzionali di
`init`, e la chiave `id` negli esempi di output `--json`.

Va corretta anche l'affermazione sbagliata di §1.1 ovunque compaia — l'API Notion filtra
per `unique_id`, il limite era della CLI.
