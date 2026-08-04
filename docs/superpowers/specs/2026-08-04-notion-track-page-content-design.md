# notion-track — Accesso al contenuto della pagina (`get --body`, `--append-file`) — Design doc

> Data: 2026-08-04 · Stato: approvato in brainstorming, API verificata sul campo (§10), da implementare
> Repo: `notion-cli` · Modulo: `github.com/marcoarnulfo/notion-cli` · Binario: `notion-track`
> Estende `2026-07-23-notion-track-markdown-body-design.md` (`--body-file`), che resta
> valido e **non viene modificato**.

Documento in italiano (convenzione ereditata: i design doc restano in italiano, il resto
del repo è in inglese).

---

## 1. Obiettivo

Oggi il tool **scrive** il corpo di una pagina (`--body-file` su `upsert`/`set`) ma non lo
**legge mai**. L'asimmetria è dichiarata nel README:

> «no command in this tool reads a page body back, `get` included, so opening the page in
> Notion is the only way to see what a run would replace» — `README.md:272`

Il problema non è l'assenza di una lettura in sé, ma la sua combinazione con la semantica
di `--body-file`, che è **replace senza undo e senza lock**: l'unico modo di sapere cosa si
sta per distruggere è aprire il browser. La stessa regola di sicurezza della skill per gli
agenti — *«Read before you write»* (`skills/notion-track/SKILL.md:51`) — è oggi
**inapplicabile al corpo**: per le proprietà l'agente può fare `get` prima di `set`, per il
body no.

Questa spec aggiunge due capacità, entrambe **puramente additive**:

1. **Lettura** del corpo come Markdown — `get --body` / `--body-only`.
2. **Append non distruttivo** — `--append-file` su `upsert`/`set`.

### Perché ora è possibile

Il client è già pinnato su `Notion-Version: 2026-03-11` (`internal/notion/version.go`), che
espone un'API Markdown nativa finora inutilizzata:

| Endpoint | Uso qui |
|---|---|
| `GET /v1/pages/{id}/markdown` | Lettura dell'intera pagina in **una** chiamata, rendering lato Notion |
| `PATCH /v1/pages/{id}/markdown` (`insert_content`) | Append in coda, **senza** cancellare nulla |

L'alternativa (`GET /v1/blocks/{id}/children` ricorsivo + converter blocchi→Markdown
scritto a mano) è stata **scartata**: restituisce solo il primo livello, richiede N chiamate
per il contenuto annidato, e obbligherebbe a mantenere a mano la conversione di ogni tipo di
blocco — inclusi tabelle, callout e toggle che oggi il tool non sa produrre né leggere.

> Nota: `ListBlockChildren` in `internal/notion/blocks.go` **non** è una lettura di
> contenuto: restituisce solo `{ID, Type}`, gli id servono a cancellare i blocchi durante il
> replace. Non legge mai il testo.

### Non-goal (dichiarati, non silenziosi)

- **Round-trip** (leggi → modifica in locale → ripubblica). Non è lossless: vedi §6. La
  lettura è per **ispezione**, non per editing locale.
- **Modifica di `--body-file`**. Il percorso replace-a-blocchi resta identico: stesso
  codice, stessi test, stessa semantica di skip delle sub-page. Migrarlo all'API Markdown
  cambierebbe il comportamento sulle sub-page (§6.3) e toccherebbe codice funzionante →
  eventuale spec separato.
- **Edit chirurgico** (`update_content` search-and-replace, `replace_content_range`).
  Esistono nell'API ma non servono ai due casi d'uso scelti.
- **Ricerca dentro il contenuto** delle pagine.
- **Lettura del body in `list`**: sarebbe una chiamata per riga. Solo `get`, che agisce su
  una riga sola.

---

## 2. Architettura

Tre layer, seguendo la struttura esistente (`notion` → `service` → `cli`). Nessun file
esistente cambia comportamento; le aggiunte sono additive.

### 2.1 `internal/notion/markdown.go` (nuovo)

Due metodi sul `Client` esistente, che riusano l'infrastruttura di retry già scritta:

```go
// PageMarkdown è la risposta di GET e di PATCH /v1/pages/{id}/markdown:
// entrambi restituiscono la stessa forma (verificato, §10).
type PageMarkdown struct {
    Markdown        string
    Truncated       bool
    UnknownBlockIDs []string
    RequestID       string // utile nei messaggi d'errore: è l'id che il supporto Notion chiede
}

func (c *Client) GetPageMarkdown(ctx context.Context, pageID string) (PageMarkdown, error)

// AppendPageMarkdown restituisce il markdown risultante: il PATCH risponde con
// la pagina completa aggiornata, non con un ack (§10.d).
func (c *Client) AppendPageMarkdown(ctx context.Context, pageID, content string) (PageMarkdown, error)
```

`position: {"type":"end"}` viene inviato **esplicitamente** anche se ometterlo dà lo stesso
risultato (§10, caso 5): dipendere da un default non documentato nella reference è un
rischio gratuito.

La scelta del retry **non è cosmetica** e segue la distinzione già codificata in
`internal/notion/blocks.go`:

| Metodo | HTTP | Retry | Perché |
|---|---|---|---|
| `GetPageMarkdown` | GET | `c.do` | Idempotente: ritentare è sempre sicuro |
| `AppendPageMarkdown` | PATCH | `c.doRejectRetryable` | **Non** idempotente: un append ritentato alla cieca duplica il contenuto |

`doRejectRetryable` esiste già ed è esattamente il caso d'uso: ritenta solo su 429/503/529
(dove Notion ha certamente rifiutato la richiesta senza processarla), e marca 500/502/504 e
gli errori di trasporto con `ErrAmbiguousWrite` — così l'utente sa che l'append **potrebbe**
essere andato a segno e non deve rilanciarlo alla cieca.

Forma delle richieste (verificata sulla documentazione ufficiale):

```jsonc
// GET /v1/pages/{id}/markdown  →  200
{ "object": "page_markdown", "id": "...", "markdown": "# Titolo\n\n...",
  "truncated": false, "unknown_block_ids": [] }

// PATCH /v1/pages/{id}/markdown
{ "type": "insert_content",
  "insert_content": { "content": "## Nota\n...", "position": { "type": "end" } } }
```

**Confine importante**: `internal/markdown` (Markdown→blocchi via goldmark) **non viene
toccato**. La lettura non ci passa — il rendering lo fa Notion. L'append invia Markdown
grezzo all'API, non blocchi: quindi **niente `ValidateAppendable`**, che vale solo per il
percorso a blocchi (limiti 100 figli / 1000 blocchi / 450KB / 2 livelli). Applicarlo qui
sarebbe un errore: quei limiti non governano questo endpoint.

### 2.2 `internal/service`

- `GetBody(ctx, ...)` — risolve l'indirizzamento (ticket / `--page-id` / `--id`) riusando i
  percorsi esistenti (`GetByID`, `GetByUniqueID`, lookup per ticket), poi chiama
  `GetPageMarkdown`.
- `BodyRequest` acquisisce la modalità append. Un `BodyRequest` con `Append: true` chiama
  `AppendPageMarkdown` invece di `replaceBody`. `replaceBody` resta invariata.

### 2.3 `internal/cli`

Flag su `get` (lettura) e su `upsert`/`set` (append), via `writeFlags`.

`--body-file` e `--append-file` sono **mutuamente esclusivi**, dichiarati con
`cmd.MarkFlagsMutuallyExclusive("body-file", "append-file")` — lo stesso meccanismo già
usato per `--assignee`/`--unassign`. Rende esplicito il bivio replace-vs-append nel punto
esatto in cui si sbaglia, invece di lasciar vincere silenziosamente uno dei due.

---

## 3. Superficie CLI

### 3.1 Lettura — `get --body` / `--body-only`

```sh
notion-track get --ticket TASK-231 --body        # proprietà + corpo sotto
notion-track get --ticket TASK-231 --body-only   # solo il Markdown
notion-track get --ticket TASK-231 --body --json # corpo dentro il JSON
```

`--body-only` non è zucchero sintattico: serve perché `> note.md` produca un file Markdown
valido, non contaminato dalle righe di proprietà. Implica `--body` ed è con esso mutuamente
esclusivo.

Funziona con **tutte** le forme di indirizzamento già supportate da `get` (`--ticket`,
`--page-id`, `--id`): nessuna logica nuova di risoluzione.

### 3.2 Append — `--append-file`

```sh
notion-track set --ticket TASK-231 --append-file nota.md
notion-track set --ticket TASK-231 --append-file -          # stdin
notion-track upsert --ticket TASK-231 --append-file n.md --expand
```

Riusa integralmente `loadBody` per lettura/validazione dell'input: `-` per stdin, cap di
**1 MiB** (`maxBodyFileBytes`), file vuoto = errore d'uso, ed `--expand` per
`{{ticket}}`/`{{date}}`. L'unica differenza è che il testo espanso viene inviato **come
Markdown**, senza passare da `markdown.ToBlocks`.

---

## 4. Output e forma JSON

### 4.1 Lettura

Chiave **additiva** `body`. Nessun campo di `pageJSON` cambia: il commento in `get.go:11`
lo dichiara public API («Renaming a key here breaks every script and agent that consumes
it»), e questa spec lo rispetta.

```json
{
  "page": { "...invariato..." },
  "body": { "markdown": "# ...", "truncated": false, "unknown_block_ids": [] }
}
```

`truncated` e `unknown_block_ids` sono **esposti, non nascosti**. Una pagina troncata (il
limite è ~20.000 blocchi) o con blocchi non resi produrrebbe altrimenti un output che
*sembra* completo: chi legge deve poterlo sapere. In forma umana → warning su stderr via
`printWarnings`, coerente con il resto del tool (stdout = risposta, stderr = diagnostica).

Con `--body-only` stdout contiene **esclusivamente** il Markdown; gli eventuali warning
restano su stderr, così la redirezione su file resta pulita.

`--body-only --json` segue la stessa regola: emette **il solo oggetto body**, senza il
wrapper `page`. Degradarlo all'output di `--body` renderebbe il flag inefficace proprio
dove uno script ci conta.

**`--dry-run` con `--append-file`** riporta l'append che verrebbe fatto (in byte di
Markdown, dato che nel percorso append non esistono blocchi da contare). Un dry-run che
tace un'operazione richiesta è peggio dell'assenza di dry-run: è il comando che esiste per
dire cosa succederebbe.

### 4.2 Append

```json
{ "action": "updated", "page": {...}, "body": { "appended": true } }
```

`appended` è deliberatamente **distinto** da `blocks_written`/`blocks_deleted` del replace:
sono operazioni diverse con conseguenze diverse, e uno script non deve poterle confondere.

Vale anche sul **fallimento parziale** (proprietà scritte, append fallito): l'output porta
`body: {"written": false, "error": "...", "appended": false}` e **non** i contatori del
replace, che direbbero «0 blocchi scritti» di un'operazione che blocchi non ne conta.
Distinguere «quale modalità è stata eseguita» da «è andata a buon fine» richiede due
booleani separati nel risultato, non uno.

---

## 5. Errori

Nessun exit code nuovo; si riusano quelli documentati:

| Situazione | Exit | Note |
|---|---|---|
| Token assente/rifiutato | `5` | invariato |
| Pagina inesistente / non condivisa con l'integrazione | `3` | `ErrNotFound` |
| `--body-file` + `--append-file` insieme | `2` | intercettato da cobra |
| `--append-file` vuoto, oltre 1 MiB, placeholder non risolto | `2` | pre-flight in `loadBody` |
| Append fallito dopo la scrittura delle proprietà | `1` | pattern `BodyWriteError` esistente |
| Append con esito ambiguo (500/502/504, timeout) | `1` | `ErrAmbiguousWrite`: il messaggio deve dire che **potrebbe** essere stato applicato e che rilanciarlo può duplicare |

L'ultimo caso è il più delicato: un append non idempotente con esito ignoto **non** va
ripetuto automaticamente, e il messaggio deve dirlo esplicitamente all'utente.

---

## 6. Rischi verificati

Tre limiti reali dell'API, verificati sulla documentazione ufficiale. Vanno **documentati**,
non scoperti dagli utenti.

### 6.1 `insert_content` marcato "legacy" — VERIFICATO, rischio chiuso

La pagina `/reference/update-page-markdown` marca `insert_content` (e
`replace_content_range`) come **legacy/deprecated**, mentre la guida
`/guides/data-apis/working-with-markdown-content` lo documenta come corrente. Le due pagine
ufficiali si contraddicono.

→ **Risolto con chiamate reali il 2026-08-04** contro il workspace configurato
(`Notion-Version: 2026-03-11`), su una pagina di prova creata e archiviata subito dopo.
Esito: **`insert_content` è operativo.** Vedi §10 per i risultati completi. La guida ha
ragione; il piano procede come progettato.

### 6.2 Round-trip non lossless

Gli URL dei file nel Markdown restituito sono **pre-signed e scadono**. Rileggere e
riscrivere non è un'identità. Per questo `get --body` va documentato come lettura e
ispezione, mai come primo passo di un ciclo "scarica → modifica → ricarica".

Inoltre i blocchi non supportati (bookmark, embed, link preview, breadcrumb, template
button) arrivano come `<unknown url="..." alt="tipo"/>`. Va detto in README e skill, così
nessuno crede che quel contenuto sia sparito dalla pagina.

### 6.3 Semantica sulle sub-page (motivo per non migrare `--body-file`)

`PATCH .../markdown` rifiuta di cancellare sub-page e child-database salvo
`allow_deleting_content: true`. È vicino ma **non identico** allo skip che `--body-file`
implementa oggi. È la ragione tecnica per cui il replace esistente resta intoccato: la
migrazione cambierebbe comportamento su codice funzionante e coperto da test.

L'append (`insert_content`) non cancella nulla, quindi il tema non lo riguarda.

---

## 7. Test

La suite usa `httptest` con `BaseURL` sovrascritto: le nuove chiamate si testano allo stesso
modo, **senza rete**.

**`internal/notion`**
- `GetPageMarkdown`: risposta normale; `markdown` vuoto (pagina senza corpo);
  `truncated: true`; `unknown_block_ids` non vuoto; 404 → `ErrNotFound`; 401 → auth.
- `AppendPageMarkdown`: forma esatta del JSON inviato (`type`, `insert_content.content`,
  `position.type == "end"`); 429 → ritenta; **500 → `ErrAmbiguousWrite` e nessun retry**
  (la garanzia anti-duplicazione, il test più importante di questo gruppo).

**`internal/cli`**
- `--body` e `--body-only` in forma umana e `--json`; `--body-only` non emette proprietà su
  stdout; warning di truncation su stderr, non su stdout.
- `--body-file` + `--append-file` → exit 2.
- `--append-file` con `--expand`; da stdin.
- **`--append-file` con file vuoto → exit 2, e nessuna chiamata HTTP** (test di
  regressione su §10.e: Notion accetterebbe un content vuoto con `200` senza fare nulla, e
  l'utente crederebbe di aver appeso qualcosa).
- Invarianza: le chiavi di `pageJSON` restano identiche senza `--body`.

---

## 8. Documentazione

### 8.1 README (`README.md` e `README.it.md`)

- Nuova sezione lettura, accanto a `--body-file`.
- **Correggere `README.md:272`**, che oggi afferma che nessun comando rilegge il corpo: con
  questa spec diventa falso. Va aggiornato in entrambe le lingue.
- Documentare `truncated`, `unknown_block_ids`, il round-trip non lossless, e la
  distinzione replace-vs-append.

### 8.2 Skill per gli agenti (`skills/notion-track/SKILL.md`)

La feature non è completa finché l'agente non sa usarla. Tre modifiche, la prima è la più
importante concettualmente:

1. **`get --body` chiude la regola «Read before you write»** per il corpo, oggi
   inapplicabile. Va scritto come **istruzione esplicita**, non come nota a margine: prima
   di un `--body-file` su una pagina che potrebbe avere contenuto, l'agente ispeziona con
   `get --body`.
2. **`--append-file` come default consigliato** quando l'intento è *aggiungere* (una nota di
   avanzamento, un esito di CI) invece di *sostituire*. È non distruttivo, quindi va
   preferito a `--body-file` ogni volta che l'utente non ha chiesto esplicitamente di
   riscrivere tutto.
3. **`description:` del frontmatter** — aggiungere i trigger sul contenuto, in italiano e
   inglese, coerenti con lo stile esistente: «leggi la pagina», «cosa c'è scritto nel
   ticket», «aggiungi una nota», «append a note».

Va inoltre menzionato che `<unknown .../>` nel corpo letto significa "blocco non
rappresentabile in Markdown", non "contenuto perso" — altrimenti un agente potrebbe
tentare di "riparare" la pagina distruggendola.

---

## 9. Ordine di implementazione

La verifica dell'API (ex punto 1) è **già stata eseguita**: vedi §10.

1. **Lettura**: `GetPageMarkdown` + `GetBody` + `get --body`/`--body-only`. Valore
   immediato, rischio nullo (sola lettura).
2. **Append**: `AppendPageMarkdown` + `--append-file`, con la mutua esclusione.
3. **Documentazione**: README (entrambe le lingue, inclusa la correzione di `README.md:272`)
   e `SKILL.md`.

Ogni passo in TDD, come il resto del repo.

---

## 10. Verifica sull'API reale (2026-08-04)

Eseguita contro il workspace configurato con `Notion-Version: 2026-03-11`, bot
`BDF Automation`. Le scritture sono avvenute su una pagina di prova creata apposta e
**archiviata al termine** (`in_trash: true`): nessun ticket reale è stato toccato.

| # | Chiamata | Esito |
|---|---|---|
| 1 | `GET /v1/users/me` | `200` — auth valida |
| 2 | `GET /v1/pages/{id}/markdown` su un ticket reale | `200`, `object: page_markdown` |
| 3 | `PATCH .../markdown` `insert_content` + `position:{type:"end"}` | **`200` — funziona** |
| 4 | Stesso PATCH, ispezione header | **nessun `Warning`/`Sunset`/deprecazione** |
| 5 | `insert_content` **senza** `position` | `200` — default = append in coda |
| 6 | `PATCH` su page id inesistente | `404`, `code: object_not_found` |
| 7 | `insert_content` con `content: ""` | **`200`, nessun errore** (no-op silenzioso) |

### Conseguenze sul design

**a) Il rischio §6.1 è chiuso.** `insert_content` è operativo e non emette segnali di
deprecazione. L'append procede come progettato; nessun ripiego necessario.

**b) Append non distruttivo confermato sul campo.** Dopo il PATCH il markdown della pagina
era:

```
Contenuto iniziale della prova.
## Sezione appesa
Riga aggiunta via insert_content.
```

Il contenuto preesistente è **preservato** e il nuovo testo aggiunto in coda: esattamente la
semantica che questa spec vuole offrire in alternativa al replace di `--body-file`.

**c) La risposta contiene un campo `request_id` non previsto in §2.1.** La struttura reale è
`{object, id, markdown, truncated, unknown_block_ids, request_id}`. `PageMarkdown` può
ignorarlo (Go scarta i campi JSON non mappati), ma vale la pena catturarlo: è l'id che il
supporto Notion chiede per diagnosticare una richiesta, quindi è utile includerlo nei
messaggi d'errore.

**d) Il PATCH restituisce il markdown completo aggiornato**, non un semplice ack. Quindi
`AppendPageMarkdown` **può confermare l'esito senza una GET aggiuntiva**. Utile soprattutto
nel caso `ErrAmbiguousWrite`: quando una risposta arriva, dice con certezza qual è lo stato
finale della pagina. Firma rivista:

```go
func (c *Client) AppendPageMarkdown(ctx context.Context, pageID, content string) (PageMarkdown, error)
```

**e) Il caso 7 è il più importante da difendere.** Un `content` vuoto **non** è un errore per
Notion: risponde `200` e non fa nulla. Un utente che appende per sbaglio un file vuoto
riceverebbe quindi un successo silenzioso. La validazione pre-flight di `loadBody` (file
vuoto → exit 2, §5) **non è ridondante**: è l'unica difesa. Va mantenuta ed esplicitamente
testata.

**f) Il caso 2 ha restituito `markdown: ""`** su un ticket reale privo di corpo, confermando
che "pagina senza contenuto" è un caso normale e non un errore — `get --body` deve produrre
output vuoto, non fallire.
