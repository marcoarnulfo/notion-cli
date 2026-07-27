# notion-track — Ruolo "assignee" (referente del task) — Design doc

> Data: 2026-07-27 · Stato: approvato in brainstorming, da implementare
> Repo: `notion-cli` · Modulo: `github.com/marcoarnulfo/notion-cli` · Binario: `notion-track`
> Estende il design principale `2026-07-23-notion-track-design.md` con un quinto ruolo mappato.

Documento in italiano (convenzione ereditata: i design doc restano in italiano, il resto
del repo è in inglese).

---

## 1. Obiettivo

Oggi notion-track conosce quattro ruoli — `ticket`, `status`, `title`, `due` — e non ha
alcun modo di leggere o scrivere chi è responsabile di un task. Su una board usata da più
persone è l'informazione più consultata dopo lo stato, e la sua assenza costringe a tornare
sull'interfaccia di Notion proprio nel flusso che il tool esiste per evitare.

Questo documento aggiunge il ruolo **`assignee`**: mapparlo su una colonna, scriverlo,
leggerlo, filtrarci sopra e svuotarlo.

### Non-goal dichiarati

- **Non tocca i quattro ruoli esistenti.** Nessun comando cambia comportamento quando
  `assignee` non è mappato: l'output umano e gli exit code restano identici byte per byte
  per ogni profilo scritto prima di questa feature. Il JSON guadagna esattamente una chiave
  additiva, `assignee` (§6.1), sempre presente e vuota quando il ruolo non è mappato — un
  consumatore `jq` che legge le chiavi esistenti non nota differenza.
- **Non gestisce colonne di tipo `people`.** Vedi §2: la decisione è motivata da un fatto
  del dominio, non da pigrizia, ed è pensata per essere estesa senza rotture (§14).
- **Non crea né modifica colonne su Notion.** Se la board non ha una colonna adatta, `init`
  lo dice; crearla resta lavoro da fare in Notion.
- **Nessuna nozione di team, gruppi o assegnazione multipla.** Una riga ha un referente.

---

## 2. Il fatto che decide il design

La board reale (`Panoramica Task`, data source `0ae39344-…`) ha già la colonna:

```
Referente    select -> Andrea Ghidara, Marco Arnulfo, Mirko Spinato
```

valorizzata su 37 righe su 40. Il ruolo `assignee` si mappa **su una colonna `select`**.

L'alternativa naturale — il tipo nativo `people` di Notion — è stata verificata contro il
workspace reale il 2026-07-27 ed è impraticabile qui:

| Verifica (token reale, `Notion-Version: 2026-03-11`) | Esito |
|---|---|
| `GET /v1/users` | HTTP 200: **1 sola persona** (`Boutique Del Fitness`) e 2 bot |
| `GET /v1/users/me` | `type: bot` — l'integrazione, non un umano |
| Le tre persone che usano davvero la board | **guest**, e i guest sono esclusi da `GET /v1/users` |
| `GET /v1/users/{id}` su un id guest | HTTP 200 con nome ed email: i guest sono risolvibili **uno per uno**, se l'id è già noto |

Una colonna `people` avrebbe quindi richiesto: una directory utenti costruita dalle righe
della board (`created_by`, `last_edited_by`, colonne people) per aggirare l'invisibilità dei
guest, la risoluzione nome→uuid, una cache, la gestione dei tre livelli di capability
"read user information" e la politica sui bot — per assegnare persone che, come guest,
potrebbero comunque non essere assegnabili. Contro una colonna che esiste già, è già
popolata, e i cui valori sono esattamente i tre nomi.

**Conseguenza pratica**: quasi tutto è già implementato e testato nel codebase.
`BuildProperties` ha il ramo `select` (`internal/tracker/payload.go:68`), `ValidateStatus`
valida i valori contro le opzioni lette dallo schema (`internal/tracker/validate.go:32`),
`EqualsFilter` filtra i select (`internal/notion/query.go:21`), `decodePage` li decodifica
(`internal/notion/query.go:114`). Questo design aggiunge un ruolo, non un sottosistema.

---

## 3. Configurazione

```yaml
profiles:
    default:
        properties:
            ticket: Nome task
            status: Stato
            title: Nome task
            due: Deadline
            assignee: Referente      # nuovo, opzionale
        me: Marco Arnulfo            # nuovo, opzionale
        status_type: status
```

Entrambi i campi sono `omitempty`. **Nessun bump di `schema_version`**: un campo assente è
lo zero value, che `migrate` già tratta correttamente, e un binario vecchio che legge un
config nuovo ignora le chiavi che non conosce (`gopkg.in/yaml.v3` non è strict qui). Un
profilo scritto prima di questa feature resta valido e si comporta come prima.

### 3.1 `me` e la variabile d'ambiente

`NOTION_TRACK_ME` sovrascrive `me:`, applicata in `config.Resolve` accanto a
`NOTION_TRACK_DB` e `NOTION_TRACK_DATA_SOURCE` — stesso meccanismo, stessa precedenza
(env batte file), nessuna regola nuova da imparare.

**Perché l'env è la fonte raccomandata**: `config.yml` è progettato per essere committato in
un repo di progetto e condiviso (`internal/config/config.go:172-179` lo dichiara: è il motivo
per cui il token vive in un file separato). Un `me:` scritto lì vale per **tutti** i
collaboratori: chi non esporta la variabile risolverebbe `me` nell'identità di chi ha
committato il file, assegnando task alla persona sbagliata in silenzio. Perciò:

- `init --me <valore>` stampa un avviso esplicito quando scrive `me:` in un config
  condiviso, e suggerisce `NOTION_TRACK_ME`;
- `doctor` segnala (warn, non fail) un profilo che ha `me:` mentre `NOTION_TRACK_ME` è
  assente.

---

## 4. Scrittura

```bash
notion-track set    --ticket BDF-1 --assignee "Mirko Spinato"
notion-track set    --ticket BDF-1 --assignee mirko          # match univoco, §5
notion-track set    --ticket BDF-1 --assignee me             # §3.1
notion-track set    --ticket BDF-1 --unassign
notion-track upsert --ticket BDF-2 --title "Nuovo" --assignee me
notion-track set    --page-id <url|uuid> --assignee andrea
```

- `--assignee` **non passato** = colonna non toccata. È la regola "stringa vuota = lascia
  stare" che governa già ogni altro campo (`internal/tracker/payload.go:31-36`), applicata
  senza eccezioni.
- `--assignee ""` è un errore d'uso, non un modo per svuotare: `ErrEmptyAssignee`, sullo
  stesso modello di `ErrEmptyTicket` (`internal/service/service.go:23-32`), perché cobra
  verifica che un flag sia passato ma non che porti un valore.
- `--unassign` svuota la colonna. Mutuamente esclusivo con `--assignee`.
- Un solo referente per riga: un `select` tiene un valore solo, quindi `--assignee` **non è
  ripetibile**.

### 4.1 Payload

`tracker.Fields` guadagna due campi:

```go
type Fields struct {
	Ticket   string
	Title    string
	Status   string
	Due      string
	Assignee string // valore canonico dell'opzione, già risolto (§5)
	Unassign bool   // true: svuota la colonna, ignorando Assignee (che dev'essere vuoto)
}
```

`BuildProperties` scrive `{"<colonna>": {"select": {"name": "Mirko Spinato"}}}` attraverso
il ramo `select` esistente, e per `Unassign` emette `{"<colonna>": {"select": null}}` — la
forma documentata per svuotare un select, e l'unico caso in cui `BuildProperties` produce un
valore per un campo vuoto. Il ramo `unassign` vive **fuori** dalla closure `add`, che è
costruita attorno alla regola opposta.

La mutua esclusione `Assignee`/`Unassign` è verificata **dentro `tracker`**, non solo dai
flag cobra: `apply` e il server MCP non passano da cobra e devono ereditare la stessa regola
dallo stesso punto.

---

## 5. Risoluzione del valore

Digitare `"Mirko Spinato"` per intero a ogni comando è il tipo di attrito che fa smettere di
usare un tool. `tracker.ResolveOption` risolve un valore parziale contro le opzioni lette
dallo schema, in quest'ordine, fermandosi al primo che produce **esattamente un** candidato:

1. match esatto (`Mirko Spinato`);
2. match esatto case-insensitive (`mirko spinato`);
3. match per sottostringa case-insensitive (`mirko`, `spinato`, `ghid`).

Zero candidati e più candidati sono due errori distinti e diversamente utili:

```
error: unknown assignee "Marko"; allowed values are: Andrea Ghidara, Marco Arnulfo, Mirko Spinato

error: ambiguous assignee "ar": matches Andrea Ghidara, Marco Arnulfo
  fix: pass more of the name
```

La funzione è **pura** e vive in `internal/tracker/assignee.go`: nessuna rete, tutto il
comportamento è esercitabile in test da tabella.

`me` è un valore **riservato** che viene sostituito prima della risoluzione con `me:` /
`NOTION_TRACK_ME`, e il risultato passa poi per le stesse tre regole (quindi
`NOTION_TRACK_ME=marco` funziona quanto il nome completo). Un'opzione che si chiamasse
letteralmente `me` non sarebbe raggiungibile: documentato, e accettabile su una colonna che
contiene nomi di persone.

### 5.1 Dove avviene la risoluzione

**Nel service**, prima di costruire il payload, in un helper unico
`resolveAssignee(ctx, f) (tracker.Fields, error)` chiamato dai **tre** punti di scrittura —
`Upsert`, `Set`, `SetByID` — e, nella sua forma di sola lettura, da `List`.

La ragione è il dry-run. `planFor` riceve gli stessi `Fields` di `BuildProperties`
(`internal/service/service.go:253`, `:302`, `:422`): se la risoluzione avvenisse dentro
`BuildProperties`, il piano mostrerebbe `mirko` mentre la scrittura reale metterebbe
`Mirko Spinato` — un dry-run che non descrive la scrittura che sta descrivendo. Risolvendo
prima, entrambi vedono il valore canonico.

### 5.2 Generalizzazione di `ValidationError`

`ValidateStatus` produce oggi un `*tracker.ValidationError` con `Field` fisso a `"status"`
(`internal/tracker/validate.go:36`). Diventa `ValidateOption(field, value, allowed)`, con
`ValidateStatus` come wrapper che passa `"status"`; `assignee` passa `"assignee"`.

Questo non è cosmetico: `*ValidationError` è ciò che `exitCodeFor` mappa su `ExitUsage`
(`internal/cli/output.go:67`), ed è l'**unico** canale per cui `apply` e MCP — che non
passano da cobra — escono con il codice giusto invece del generico `ExitError`. Ogni errore
di questa feature (valore sconosciuto, ambiguità, `me` non configurato, `--assignee ""`)
dev'essere tipizzato per questo motivo.

---

## 6. Lettura e output

### 6.1 `--json` (contratto pubblico)

```json
{
  "ticket": "BDF-1",
  "title": "Hardening",
  "status": "In corso",
  "page_id": "…",
  "url": "…",
  "last_edited_time": "2026-07-27T…",
  "assignee": "Mirko Spinato"
}
```

La chiave nuova va **in coda** allo struct, non in mezzo: l'ordine delle chiavi non fa
parte del contratto, ma spostare quelle esistenti cambierebbe l'output indentato di ogni
comando per nessun motivo.

Una **stringa**, sempre presente, vuota quando la riga non ha referente o il ruolo non è
mappato — esattamente la regola già documentata per gli altri campi
(`internal/cli/get.go:14-17`: una mappatura rotta non fa fallire una lettura, è `doctor` a
segnalarla).

`pageJSON` e `mcp.Row` devono restare **convertibili l'uno nell'altro**: la conversione
diretta in `internal/cli/mcp.go:48-60` è dichiarata "deliberate and load-bearing". Un
campo `Assignee string` in entrambi lo preserva senza sforzo. Nella stessa direzione,
`fieldsFromMCP` (`internal/cli/mcp.go:92`) copia campo per campo: dimenticarci `Assignee` o
`Unassign` compilerebbe e li scarterebbe in silenzio, quindi diventa anch'essa una
conversione diretta.

### 6.2 Output umano

Additivo e in coda, così le colonne esistenti non si spostano di un carattere e chi non
mappa il ruolo non vede alcuna differenza:

```
$ notion-track list --status "Da fare"
BDF-1                Hardening delle rotte                    [Da fare]  @Mirko Spinato
BDF-2                Cleanup S3                               [Da fare]

$ notion-track get --ticket BDF-1
BDF-1  Hardening delle rotte  [Da fare]  @Mirko Spinato
  https://notion.so/…
```

Il segmento `@…` compare se e solo se il ruolo è mappato **e** la riga ha un valore.

---

## 7. Filtro

```bash
notion-track list --assignee me
notion-track list --assignee mirko --status "Da fare"
notion-track list --unassigned
```

`Service.List(ctx, status string)` diventa `List(ctx, ListFilter{Status, Assignee, Unassigned})`:
un tipo invece di tre parametri posizionali, che è anche ciò che serve al server MCP per non
duplicare la logica.

- solo status → filtro singolo, **identico a oggi**;
- status + assignee → compound `{"and": [ …status…, …select… ]}`;
- `--unassigned` → `{"property": "<colonna>", "select": {"is_empty": true}}`;
- `--assignee` e `--unassigned` sono mutuamente esclusivi (flag cobra **e** controllo nel
  service, per il percorso MCP);
- `--assignee`/`--unassigned` con il ruolo non mappato → errore esplicito
  (`assignee is not mapped; run 'notion-track init --assignee-prop <name>'`), non un filtro
  su una colonna chiamata `""`. Il ramo status non ha questo caso perché `status` è
  obbligatorio; `assignee` è opzionale, quindi il caso esiste e va gestito.

Come per `--status`, il valore passa per `ResolveOption`: `list --assignee marko` fallisce
con l'elenco delle opzioni valide invece di restituire zero righe senza spiegazione.

---

## 8. `--dry-run`

`Plan` guadagna un campo:

```go
type Plan struct {
	Action     string            `json:"action"`
	PageID     string            `json:"page_id,omitempty"`
	URL        string            `json:"url,omitempty"`
	Properties []PlannedProperty `json:"properties"`
	Cleared    []string          `json:"cleared,omitempty"`   // colonne che verrebbero svuotate
	BodyBlocks int               `json:"body_blocks,omitempty"`
}
```

`planFor` scarta i valori vuoti (`internal/service/plan.go:52`), il che è giusto per
"campo non passato" ma renderebbe `--unassign --dry-run` un piano **completamente vuoto**:
"would update" senza dire cosa — l'operazione più distruttiva della feature, invisibile
proprio nel comando che esiste per mostrarla. `Cleared` è il minimo che ripara questo, ed è
generico per qualunque futuro svuotamento.

```
$ notion-track set --ticket BDF-1 --unassign --dry-run
would update page1
  clear                Referente
```

Il padding è quello che `emitPlan` usa già per le altre righe (`%-20s %s`): le colonne di
un piano devono incolonnarsi, che si stia scrivendo o svuotando.

---

## 9. `init`, wizard, `doctor`

### 9.1 `init`

- `--assignee-prop <colonna>` — opzionale, accetta **solo** colonne di tipo `select`,
  attraverso lo stesso `check(...)` che governa `--due-prop`
  (`internal/cli/init.go:354-390`). Va aggiunta a `configFlags` (`init.go:138`), altrimenti
  `init --assignee-prop X` a un terminale aprirebbe il wizard invece di configurare.
- `--me <valore>` — risolve subito contro le opzioni della colonna (fallendo se non è
  mappata) e salva il valore canonico, così un `me: mirko` non risolvibile non finisce nel
  file. Stampa l'avviso di §3.1.

### 9.2 `GuessMapping`

Nomi riconosciuti: `referente`, `assignee`, `owner`, `persona`, `responsabile`,
`assegnatario`, `incaricato`.

Due accortezze, entrambe conseguenze della struttura attuale
(`internal/tracker/mapping.go:39-51`):

1. **nessun fallback "unico candidato"** per questo ruolo. Il fallback esiste perché per
   `status` una sola colonna plausibile *è* la risposta; per un ruolo opzionale, indovinare
   `Urgenza` come referente è peggio che non indovinare nulla — ed è esattamente ciò che il
   commento di `GuessMapping` già raccomanda ("a wrong guess the user waves through is worse
   than a question");
2. **la colonna scelta per `status` è esclusa** dai candidati `assignee`: entrambi i ruoli
   pescano dai `select`, e senza l'esclusione una board con un solo select finirebbe con lo
   stesso nome in due ruoli.

### 9.3 Wizard TUI

Una riga in `internal/tui/wizard.go:56`:

```go
{name: "assignee", key: "a", types: []string{"select"}, optional: true},
```

più i due `case` in `roleValue`/`setRole` (`wizard.go:432-460`). La struttura a ruoli
generici, la schermata di conferma, il picker filtrato per tipo e la gestione degli
opzionali esistono già e non vanno toccate.

### 9.4 `doctor`

- `checkProperties`: `assignee` entra in `mapped`, `wantType` (`select`) e `optionalRoles`
  (`internal/service/doctor.go:55-73`) — ruolo opzionale come `due`, quindi non mapparlo non
  è un fail.
- nuovo check `assignee` (solo quando il ruolo è mappato): verifica che `me:` /
  `NOTION_TRACK_ME`, se presente, risolva ancora a un'opzione esistente — un'opzione
  rinominata in Notion rende `--assignee me` un errore a runtime, e questo è il posto dove
  scoprirlo prima. Include l'avviso di §3.1 su `me:` in un config condiviso.

---

## 10. `apply` e MCP

### 10.1 Manifest

```csv
op,ticket,assignee,unassign
set,BDF-1,Mirko Spinato,
set,BDF-2,,true
```

```json
[
  {"op": "set", "ticket": "BDF-1", "assignee": "mirko"},
  {"op": "set", "ticket": "BDF-2", "unassign": "true"}
]
```

`manifest.Entry` guadagna `Assignee string` e `Unassign string`, entrambi registrati in
`fieldNames` (`internal/manifest/manifest.go:47`). `fieldNames` è **condiviso** fra CSV e
JSON, quindi `unassign` è legale in entrambi i formati: renderlo legale solo nel CSV
richiederebbe un caso speciale, e due formati che accettano campi diversi sono più difficili
da spiegare di un campo in più.

`unassign` accetta `true`/`false`/vuoto (case-insensitive); qualunque altro valore è un
errore di parsing con il numero di entry, come ogni altro problema di manifest. Passare
`assignee` e `unassign: true` nella stessa entry è rifiutato dal controllo in `tracker`
(§4.1), che è il punto dove la regola vive una volta sola.

### 10.2 MCP

- `upsertArgs` (usato da `upsert_task` e `set_task`): `assignee`, `unassign`;
- `listArgs`: `assignee`, `unassigned`;
- `Row`: `assignee` (§6.1);
- `mcp.Fields`: `Assignee`, `Unassign`;
- `Tracker.List` passa da `status string` al `ListFilter` di §7.

I `jsonschema` tag sono documentazione per l'agente, non decorazione
(`internal/mcp/server.go:56-57`): quello di `assignee` deve dire che accetta un nome
parziale e che `me` è riservato.

---

## 11. Errori ed exit code

| Situazione | Errore | Exit |
|---|---|---|
| valore che non è un'opzione | `*tracker.ValidationError` (`Field: "assignee"`) | 2 `ExitUsage` |
| valore ambiguo | `*tracker.AmbiguousOptionError` | 2 `ExitUsage` |
| `--assignee ""` | `ErrEmptyAssignee` | 2 `ExitUsage` |
| `me` senza `me:` né env | `ErrNoIdentity` | 2 `ExitUsage` |
| `--assignee` + `--unassign` | `ErrConflictingAssignee` | 2 `ExitUsage` |
| `--assignee` + `--unassigned` su `list` | `ErrConflictingListFilter` | 2 `ExitUsage` |
| ruolo non mappato, valore passato | messaggio esistente di `add()`, non tipizzato | 1 `ExitError` |
| colonna mappata ma sparita da Notion | messaggio esistente di `BuildProperties`, non tipizzato | 1 `ExitError` |

Le prime sei sono tipizzate, mai identificate per prefisso di messaggio, e ognuna ha una
riga in `exitCodeFor` (`internal/cli/output.go:47`) o è coperta da un tipo che ne ha già
una. Questo non è un dettaglio di eleganza: `apply` e il server MCP non passano da cobra,
e un errore non tipizzato è indistinguibile da un guasto qualsiasi quando esce da lì.

Le ultime due escono **1**, non 2, e la scelta è deliberata. Quei due messaggi esistono già
oggi per i ruoli `ticket`, `status`, `title` e `due`, non sono tipizzati, e cadono nel
generico `ExitError`. Tipizzarli per `assignee` significherebbe o cambiare l'exit code
anche per `--due` su un profilo che non lo mappa — una regressione silenziosa per chi ha
script che distinguono 1 da 2, e il primo non-goal di questo documento dice il contrario —
oppure trattare `assignee` diversamente dagli altri quattro ruoli per la stessa identica
condizione. Nessuna delle due vale il guadagno. Restano 1, e i test lo asseriscono
esplicitamente perché sia una scelta e non una svista.

I messaggi seguono la forma già in uso nel repo: cosa è successo, poi `fix:` con il comando
esatto da eseguire.

---

## 12. Test

TDD, con i seam che il progetto usa già (`httptest` per il client, funzioni pure per il
dominio, `executeArgs` in-process per la CLI).

**Puri, senza rete** — `internal/tracker`:
- `ResolveOption`: match esatto; case-insensitive; sottostringa; ambiguità a 2 e a 3
  candidati; zero match; opzioni vuote; query vuota; precedenza fra i tre livelli (un match
  esatto vince su una sottostringa che ne includerebbe altri).
- `BuildProperties`: `assignee` su `select`; `Unassign` → `{"select": null}`; `Assignee` e
  `Unassign` insieme → errore; ruolo non mappato con valore passato → errore; nessuno dei
  due passato → la colonna non compare nel payload.
- `GuessMapping`: riconosce `Referente`; non indovina con un solo select non riconoscibile;
  non riusa la colonna presa da `status`.
- `ValidateOption`/`ValidateStatus`: il wrapper non cambia il messaggio esistente.

**Con `httptest`** — `internal/notion`, `internal/service`:
- filtro select semplice, compound `and` con status, `is_empty`;
- `resolveAssignee` chiamato su tutti e tre i percorsi di scrittura (`Upsert`, `Set`,
  `SetByID`);
- `List` con ogni combinazione di `ListFilter`.

**End-to-end sulla CLI** — `internal/cli`:
- `--assignee` e `--unassign` su `upsert`/`set`, con e senza `--json`;
- `--dry-run` mostra il valore canonico e `clear <colonna>`;
- `list --assignee`/`--unassigned`, umano e JSON;
- output umano invariato per un profilo senza `assignee` (test di non-regressione);
- exit code di ogni riga della tabella §11;
- `apply` con manifest CSV e JSON, incluso il conflitto `assignee`+`unassign`;
- MCP: i nuovi campi passano fino al service e tornano nella `Row`.

**Gate CI** (tutti e cinque, prima della PR): `gofmt -l`, `staticcheck`, `go vet`,
`go test ./...`, build.

**Smoke manuale** sulla board reale, a implementazione finita: `--dry-run`, poi una
scrittura vera su un task di prova, poi `list --assignee me`.

---

## 13. Fuori scope, dichiarato

- Colonne `people` (§2, §14), `multi_select`, `rich_text` come referente.
- Assegnazione multipla, `--add-assignee`/`--remove-assignee`.
- `created_by` / `last_edited_by` come sorgente del referente.
- Notifiche o menzioni Notion (impossibili su un `select`: nessun utente reale è coinvolto).
- La directory utenti costruita dalle righe della board (§2): verificata come praticabile,
  non serve a questo design.
- Il ruolo nella TUI di browsing (`internal/tui/browse.go`), che resta com'è.

---

## 14. Estendere a `people` in futuro

Se un domani i tre diventassero membri veri del workspace e la board passasse a una colonna
`people`, l'estensione è **additiva** e non rompe nulla di quanto scritto qui:

- `validateMapping` accetterebbe anche `people` per il ruolo;
- `BuildProperties` guadagnerebbe il ramo `people` accanto a quello `select`;
- `ResolveOption` verrebbe affiancata da una risoluzione nome→uuid basata sulla directory
  di §2, dietro la stessa firma "query → valore canonico";
- il JSON `"assignee": "<stringa>"` è l'unico punto che andrebbe rinegoziato, ed è un
  contratto pubblico: la scelta di oggi è di non pagarne il costo finché non serve.

---

## 15. File toccati

| File | Modifica |
|---|---|
| `internal/config/config.go` | `Properties.Assignee`, `Profile.Me`, `MeEnv` in `Resolve` |
| `internal/tracker/assignee.go` | **nuovo** — `ResolveOption`, `AmbiguousOptionError` |
| `internal/tracker/validate.go` | `ValidateOption` generico, `ValidateStatus` come wrapper |
| `internal/tracker/payload.go` | `Fields.Assignee`/`Unassign`, ramo `unassign`, mutua esclusione |
| `internal/tracker/mapping.go` | nomi riconosciuti, niente fallback, esclusione del select di status |
| `internal/notion/query.go` | `IsEmptyFilter`, `AndFilter` |
| `internal/service/service.go` | `resolveAssignee` sui 3 percorsi di scrittura, `ListFilter` |
| `internal/service/plan.go` | `Plan.Cleared`, `planFor` esteso |
| `internal/service/doctor.go` | ruolo opzionale, check `assignee` |
| `internal/cli/upsert.go` | `--assignee`, `--unassign` in `bindShared`, mutua esclusione, `fields()` |
| ~~`internal/cli/set.go`~~ | **invariato**: usa `writeFlags.bindWithPageID` e `wf.fields()`, che ereditano i nuovi flag da `bindShared` |
| `internal/cli/list.go` | `--assignee`, `--unassigned`, colonna in coda |
| `internal/cli/get.go` | `pageJSON.Assignee`, riga umana |
| `internal/cli/output.go` | `exitCodeFor` per i nuovi tipi |
| `internal/cli/body.go` | `emitPlan` stampa le colonne di `Cleared` |
| `internal/cli/init.go` | `--assignee-prop`, `--me`, `configFlags`, `validateMapping` |
| `internal/cli/apply.go` | `Fields` dall'entry |
| `internal/cli/mcp.go` | conversioni dirette invece di copie campo per campo |
| `internal/manifest/manifest.go` | `Assignee`, `Unassign`, `fieldNames`, parsing booleano |
| `internal/mcp/server.go` | args, `Row`, `Fields`, `Tracker.List` |
| `internal/tui/wizard.go` | una riga in `roles`, due `case` |
| `README.md`, `README.it.md` | flag, JSON, manifest, `NOTION_TRACK_ME`, exit code |
| `skills/notion-track/SKILL.md` | i nuovi flag, perché l'agente li usi |

Più i rispettivi `_test.go`. Nessun file nuovo oltre a `internal/tracker/assignee.go`.
