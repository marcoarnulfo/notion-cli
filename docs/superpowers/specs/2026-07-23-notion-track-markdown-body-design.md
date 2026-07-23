# notion-track — Markdown page body (`--body-file`) — Design doc

> Data: 2026-07-23 · Stato: approvato in brainstorming (con review fable), da implementare
> Repo: `notion-cli` · Modulo: `github.com/marcoarnulfo/notion-cli` · Binario: `notion-track`
> Issue: [#5](https://github.com/marcoarnulfo/notion-cli/issues/5) · Milestone: v0.3
> Sostituisce e dettaglia il §10 ("Body Markdown") del design principale
> `2026-07-23-notion-track-design.md`.

Documento in italiano (convenzione ereditata: i design doc restano in italiano, il resto
del repo è in inglese).

---

## 1. Obiettivo

Aggiungere `--body-file <path>` a `upsert` e `set`: converte un file Markdown in blocchi
Notion e lo imposta come **corpo** della pagina (non le proprietà — quelle restano gestite
da `--title`/`--status`/`--due`). Semantica **replace**: eseguito due volte con lo stesso
file, il corpo della pagina è identico.

Fino a oggi il tool scrive solo le proprietà mappate; non c'è modo di allegare contenuto.
Questa è la feature singola più grande della roadmap: il parsing è dominio puro, quindi
sviluppabile in TDD table-driven senza rete.

### Non-goal della v1 (dichiarati, non silenziosi)

- Nesting Markdown oltre **2 livelli** materializzati (vedi §2, §5).
- Un singolo elemento con **>100 figli diretti** o un sottoalbero **>1000 blocchi**: non
  è affettabile in richieste valide → **errore pre-flight** (§5), non fallimento a metà.
- Tabelle, immagini, HTML raw come blocchi nativi Notion: **degradano** conservando il testo
  (§2). Il blocco `image` nativo è un follow-up.
- Svuotare il corpo (`--body-file` su file vuoto è un **errore**, non un wipe — §7). Un
  eventuale `--clear-body` è follow-up.
- `CreatePage` con `children` inline in una sola chiamata: si usa il percorso unificato
  crea-vuota-poi-append (§6).

---

## 2. Sottoinsieme Markdown

Il parsing usa **goldmark** con l'estensione **GFM** (per task list e strikethrough).
goldmark produce l'AST CommonMark; `internal/markdown` possiede **solo** il mapping AST →
blocchi Notion. È l'unica dipendenza di terze parti nuova; goldmark è puro Go, zero
dipendenze proprie, standard de-facto (usato da Hugo).

### Mapping

| Markdown | Blocco Notion |
|---|---|
| `# H1` / `## H2` / `### H3` | `heading_1` / `heading_2` / `heading_3` |
| `####`+ (h4-h6), heading setext (`===`/`---` sotto testo) | `heading_3` (Notion ha 3 livelli) |
| paragrafo | `paragraph` |
| `- ` / `* ` / `+ ` | `bulleted_list_item` |
| `1. ` | `numbered_list_item` |
| `- [ ]` / `- [x]` (GFM task) | `to_do` (`checked` = presenza di `x`) |
| ` ```lang ` fence | `code` (`language` normalizzato, vedi sotto) |
| `> ` (anche multi-paragrafo / con lista dentro) | `quote` (con `children` se ha contenuto annidato) |
| `---` / `***` (thematic break) | `divider` |

### Inline → rich text (annotations)

`**bold**`, `*italic*`, `` `code` ``, `~~strike~~` (GFM), `[testo](url)`. Le annotations si
combinano (bold+italic, ecc.). Un link diventa uno span con `text.link.url`. Hard break
(`\` a fine riga o due spazi) → `\n` nello stesso span.

### Linguaggio dei code fence

Notion valida `language` contro un enum chiuso e risponde **400** su valori ignoti. Quindi
`internal/markdown` mantiene:
- un **set** dei linguaggi accettati da Notion;
- una **mappa di alias** verso i nomi canonici (`js`→`javascript`, `ts`→`typescript`,
  `sh`/`bash`→`shell`, `py`→`python`, `yml`→`yaml`, `md`→`markdown`, `rb`→`ruby`, `golang`→`go`, …);
- fallback: linguaggio assente o non riconosciuto → `"plain text"` (il valore neutro
  accettato da Notion). Nessun 400 può originare da qui.

### Costrutti non mappati — degrado con warning (mai perdita silenziosa)

- **Tabella GFM**, **HTML raw**: si conserva il **sorgente Markdown/testo grezzo** dentro un
  blocco `code` (`language: "plain text"`), non celle concatenate. Un **warning su stderr**
  nomina il costrutto e la riga.
- **Immagine** `![alt](url)`: → `paragraph` con uno span-link `alt` → `url` (il blocco
  `image` nativo è follow-up: un URL esterno non raggiungibile farebbe 400, e la v1 resta
  robusta). Warning su stderr.
- **Nesting > 2 livelli**: gli elementi più profondi del livello 2 sono **promossi** al
  livello 2 (restano list item, testo e ordine preservati), con **un** warning su stderr che
  nomina il limite. Niente 400, niente testo perso.

I warning vanno su **stderr**, quindi non inquinano `--json` (stdout) né rompono l'uso in CI.

---

## 3. Il tipo `notion.Block` e il pacchetto `internal/markdown`

### `notion.Block` (nuovo file `internal/notion/block.go`)

Tipo-dato puro che si serializza nella forma JSON di Notion via `MarshalJSON`. Vive nel
package `notion` — stessa scelta già fatta per `tracker` che importa `notion.Schema`: i tipi
Notion stanno in un posto solo. È dato puro, nessun ciclo, nessuna dipendenza dal client HTTP
a livello di tipo. (Alternativa considerata e scartata per la v1: un sottopacchetto di soli
tipi separato dal client — churn non giustificato ora.)

```go
// Block is one Notion block, in a shape internal/markdown builds and the
// client serializes. MarshalJSON emits the nested {"type":X,"X":{...}} form.
type Block struct {
    Type     string     // "paragraph","heading_1..3","bulleted_list_item",
                         // "numbered_list_item","to_do","code","quote","divider"
    RichText []Span     // text content; nil for divider
    Checked  bool       // to_do only
    Language string     // code only
    Children []Block    // nested list/quote children; ≤2 levels materialized (§5)
}

// Span is a writable rich-text fragment with its annotations. Kept separate
// from the read-oriented RichText/Text types in types.go so the read path is
// untouched.
type Span struct {
    Content       string
    Link          string // url; "" when none
    Bold          bool
    Italic        bool
    Code          bool
    Strikethrough bool
}
```

`Block.MarshalJSON` produce, per esempio per un paragraph:
`{"type":"paragraph","paragraph":{"rich_text":[{"type":"text","text":{"content":"...","link":{"url":"..."}},"annotations":{"bold":true}}]}}`.
`divider` → `{"type":"divider","divider":{}}`. `to_do` aggiunge `"checked":bool`. `code`
aggiunge `"language":"..."`. Blocchi con `Children` emettono `"children":[...]` dentro
l'oggetto del tipo.

### `internal/markdown` (dominio puro)

```go
// ToBlocks parses Markdown and returns a flat slice of top-level Notion
// blocks (nested list/quote content lives in each block's Children, ≤2 levels).
// Warnings describe every graceful degradation (unmapped construct, promoted
// deep nesting) for the caller to print to stderr. err is non-nil only on an
// input the mapper cannot represent at all (today: none — goldmark accepts
// almost anything; reserved for future hard-fail cases).
func ToBlocks(src []byte) (blocks []notion.Block, warnings []string, err error)
```

Responsabilità che **restano** in `internal/markdown` (contenuto, non trasporto):
- **Split `rich_text` a 2000 caratteri**: uno span troppo lungo si spezza in più span a
  confine di parola quando possibile, preservando le annotations su ogni frammento.
- **Split di secondo livello**: se uno split produce **>100 span** in un singolo blocco
  (limite "array rich_text = 100 elementi"), il blocco si spezza in **più blocchi dello
  stesso tipo** (paragraph→paragraph, ecc.). Per i `code` block lo split in due blocchi
  `code` è accettato e documentato (cambia il rendering in due riquadri: è il male minore
  rispetto a un 400).
- Normalizzazione input: rimozione BOM iniziale; CRLF/CR → LF prima del parse.

Responsabilità che **NON** stanno qui (vedi §5): raggruppare i blocchi in richieste da ≤100 /
≤1000 / ≤500KB è trasporto → vive nel client.

---

## 4. Client `internal/notion`: metodi nuovi e politica di retry

Tre metodi nuovi (file `internal/notion/blocks.go`), più una **terza modalità di retry**.

### 4.1 La terza modalità di retry (il punto critico)

`AppendBlockChildren` **non è idempotente**: un append ripetuto duplica blocchi. La
duplicazione da retry ambiguo **non viene sanata dal delete nella stessa run** (la fotografia
dei figli è scattata *prima* dell'append; i duplicati sono blocchi "nuovi" non fotografati).
Quindi né `do` (ritenta troppo) né `doNonRetryable` (non ritenta mai → 429 quasi garantito su
documenti grossi a ~3 req/s) vanno bene. Serve:

```go
// rejectedByServer reports statuses where Notion certainly refused the request
// WITHOUT processing it — safe to retry an append on. 429 (rate limited),
// 503 (service unavailable), 529 (service overload). Deliberately excludes
// 502/504 and transport errors (timeout, reset): those are AMBIGUOUS — the
// request may have reached Notion and been applied, so retrying could duplicate.
func rejectedByServer(status int) bool // {429, 503, 529}

// doRejectRetryable retries ONLY on rejectedByServer statuses, honoring
// Retry-After exactly like do. On any other error — an ambiguous transport/
// gateway failure included — it returns immediately, wrapped so the caller can
// tell the user the body may be partially written and to re-run to converge.
func (c *Client) doRejectRetryable(ctx, method, path string, body, out any) error
```

Errore ambiguo restituito da `doRejectRetryable` → sentinel `ErrAmbiguousWrite` (nuovo in
`errors.go`), che la CLI trasforma nel messaggio "il corpo potrebbe essere scritto
parzialmente: ri-esegui lo stesso comando per convergere".

### 4.2 `AppendBlockChildren`

```go
// AppendBlockChildren appends blocks as children of blockID, in order.
//
// It first validates the whole slice against Notion's request limits
// (ValidateAppendable) and returns before any network I/O if a single element
// is too big to ever fit one request (§5) — so a destructive replace never
// starts on input that cannot finish. Then it splits the slice into sequential
// request-groups (≤100 top-level, ≤1000 total blocks counting Children,
// ≤ byte budget) and PATCHes each with position {"type":"end"}, STRICTLY
// SEQUENTIALLY: order is guaranteed only because each group lands at the end
// after the previous one. Each group goes through doRejectRetryable.
func (c *Client) AppendBlockChildren(ctx context.Context, blockID string, blocks []Block) error
```

`position: {"type":"end"}` è esplicito e autodocumentante. **Vincolo di design**: gli append
dei gruppi sono strettamente sequenziali — nessuna parallelizzazione, romperebbe l'ordine.

### 4.3 `ListBlockChildren`

```go
// ListBlockChildren returns the direct children of blockID. It is GET
// (idempotent) → goes through do. It paginates to has_more=false; a bug that
// stops early would silently leave stale trailing content, so a >100-child
// page is a dedicated test. Each child carries its id and its type, so the
// caller can skip child_page/child_database on delete (§6).
func (c *Client) ListBlockChildren(ctx context.Context, blockID string) ([]ChildBlock, error)

type ChildBlock struct {
    ID   string
    Type string // "paragraph", ..., "child_page", "child_database"
}
```

### 4.4 `DeleteBlock`

```go
// DeleteBlock archives a block (DELETE /v1/blocks/{id}). Idempotent for our
// purpose — deleting an already-gone block returns 404, which we treat as
// SUCCESS (the goal, "block absent", is met): a human may have removed it
// between snapshot and delete. Goes through do; 404 is swallowed here, every
// other error propagates.
func (c *Client) DeleteBlock(ctx context.Context, blockID string) error
```

### 4.5 `ValidateAppendable` (puro, pre-flight)

```go
// ValidateAppendable reports whether blocks can be materialized as valid Notion
// append requests at all, WITHOUT any network call. It fails when a SINGLE
// top-level element cannot fit one request: >100 direct children, or a subtree
// exceeding the 1000-block / 500KB per-payload caps. Called by the service
// right after parsing, before any destructive step. Pure → table-testable.
func ValidateAppendable(blocks []Block) error
```

---

## 5. Chunking e limiti (dove vivono)

Riepilogo della divisione di responsabilità decisa in review:

| Preoccupazione | Dove | Perché |
|---|---|---|
| Split rich_text a 2000 char + split blocco a >100 span | `internal/markdown` | manipolazione di **contenuto**, richiede consapevolezza semantica (confini di parola, annotations) |
| Raggruppare in richieste ≤100 top-level / ≤1000 blocchi totali / ≤~450KB | `internal/notion` (`AppendBlockChildren`) | **trasporto**: è una proprietà dell'API, non del dominio |
| Rifiuto pre-flight di elementi non affettabili | `internal/notion` (`ValidateAppendable`, puro) | limite API, ma verificabile senza rete |

I conteggi sono sull'**intero albero** (top-level **+** figli annidati) e sui **byte**
serializzati, non sui soli top-level. Budget byte con margine sotto i 500KB reali (es.
450KB) per lasciare spazio all'overhead JSON. Il limite "2 livelli di nesting per richiesta"
è già rispettato perché la v1 materializza al massimo 2 livelli (§2, nesting più profondo
promosso).

---

## 6. Semantica replace (l'ordine conta)

Un solo percorso di codice, un metodo `replaceBody` in `internal/service`, riusato da
`Upsert`/`Set`/`SetByID`. L'ordine è studiato per convergere su re-run e minimizzare il danno
su fallimento parziale:

1. **Fotografia**: `ListBlockChildren(pageID)` fino a `has_more=false` → lista degli id
   figli esistenti, con il loro tipo.
2. **Append**: `AppendBlockChildren(pageID, nuoviBlocchi)` — il nuovo corpo va **in coda** al
   vecchio (che è ancora lì).
3. **Delete**: per ogni id fotografato, `DeleteBlock(id)` — **saltando** i figli di tipo
   `child_page` / `child_database` (con un warning su stderr che li nomina): "possedere il
   corpo" non deve significare archiviare intere sotto-pagine/database. Un 404 in questo
   passo è successo (I1).

**Convergenza**: se il passo 2 fallisce a metà, il vecchio contenuto resta (più un pezzo di
nuovo); un re-run rifotografa **tutto** (vecchio + avanzi) e riparte → converge sul corpo
corretto. Su una pagina **appena creata** (percorso create di `upsert`) la fotografia è vuota
→ è un semplice append: stesso codice per create e update.

**Percorso create di `upsert`**: `CreatePage` crea la riga con le sole **proprietà** (nessun
`children`), poi `replaceBody` appende il corpo. Trade-off accettato e documentato: per un
istante la pagina esiste senza corpo. È la semplificazione necessaria comunque oltre 100
blocchi / 2 livelli, e tiene `CreatePage` non-ritentabile com'è.

**Ordine proprietà → corpo**: la scrittura delle proprietà (o la create) avviene **prima**
del corpo, perché il percorso create ha bisogno che la pagina esista per ottenerne l'id. Se
il corpo fallisce, le proprietà sono già applicate: vedi §8 per come questo appare in output.

---

## 7. Superficie CLI

`--body-file <path>` aggiunto a `upsert` e `set` (quest'ultimo anche in combinazione con
`--page-id`). Non è mutuamente esclusivo con gli altri flag di scrittura: si possono
aggiornare proprietà **e** corpo nello stesso comando.

- **Pre-flight, prima di ogni chiamata di rete**, in quest'ordine:
  1. leggere il file (`path == "-"` → **stdin**, comodo per agenti/pipe CI);
  2. file **inesistente/illeggibile** → errore, **exit 2**;
  3. contenuto **vuoto o solo-whitespace** → errore `"body file is empty"`, **exit 2** (evita
     svuotamenti accidentali; per svuotare davvero servirà un futuro `--clear-body`);
  4. `markdown.ToBlocks` → blocchi + warning;
  5. `notion.ValidateAppendable` → un elemento non affettabile (§5) → errore, **exit 2**.

  Solo se tutti passano si tocca la rete. Un Markdown "malformato" non è un caso reale
  (goldmark accetta quasi tutto); i veri errori pre-flight sono quelli sopra.
- I **warning** di degrado/nesting vanno su **stderr**.
- `--json` invariato nella forma esistente; §8 definisce i campi additivi.

---

## 8. Contratto `--json` e fallimento parziale

`--json` è un contratto pubblico stabile: i campi per il corpo sono **additivi** e compaiono
**solo** quando `--body-file` è passato.

**Successo** (exit 0), stdout:
```json
{
  "action": "created" | "updated",
  "page": { …forma esistente… },
  "body": { "blocks_written": 12, "blocks_deleted": 5 }
}
```
(`blocks_written` = blocchi top-level appesi; `blocks_deleted` = figli vecchi archiviati,
esclusi i `child_page`/`child_database` saltati.)

**Fallimento parziale** — proprietà scritte, corpo fallito (exit **1**):
- con `--json`, stdout resta parsabile e distingue lo stato:
  ```json
  {
    "action": "updated",
    "page": { … },
    "body": { "written": false, "error": "<messaggio>" }
  }
  ```
- senza `--json`, messaggio su stderr. Se l'errore è ambiguo (`ErrAmbiguousWrite`), il
  messaggio dice esplicitamente di **ri-eseguire per convergere**.

La presenza di `body.error` + exit code ≠ 0 è il segnale machine-readable che le proprietà
sono passate ma il corpo no. Nessun campo esistente cambia significato.

---

## 9. Costo, progresso, limiti dimensionali

Non esiste bulk delete: un replace di una pagina con N blocchi vecchi costa N `DELETE`
sequenziali + il list paginato + K append. A ~3 req/s può durare minuti su pagine grandi.

- Il README dichiara il costo **O(n)** dell'operazione.
- Con corpo/pagina non banali, la CLI stampa un **progresso su stderr** (es. "appending
  2/3…", "deleting 40/120…") — mai su stdout, per non rompere `--json`.
- `ValidateAppendable` più i limiti per-richiesta già impediscono payload assurdi; un tetto
  esplicito sulla dimensione del file evita di partire e morire a metà. **Soglia: 1 MiB**
  (un corpo-task oltre 1 MiB di Markdown è fuori dallo scopo del tool) — file più grande →
  errore pre-flight, exit 2.

**Concorrenza**: due run in parallelo sulla stessa pagina (es. CI ri-triggerata) possono
produrre corpo duplicato — nessun lock è possibile su Notion. Documentato come limite noto.

---

## 10. Testing

Solo stdlib: `testing` + `net/http/httptest`. Nessun framework esterno (goldmark è dipendenza
di produzione, non di test).

- **`internal/markdown`** — il grosso, table-driven puro (millisecondi, zero rete): ogni
  costrutto del mapping; inline con annotations combinate; split a 2000 char a confine di
  parola; blocco che supera 100 span → split in più blocchi; degrado di tabella/immagine/HTML
  con warning atteso; nesting a 3 livelli → promozione a 2 + warning; alias e fallback
  linguaggi fence; casi di contorno **golden**: heading setext, blockquote multi-paragrafo /
  con lista annidata, BOM, CRLF, task list checked/unchecked.
- **`internal/notion`** — `httptest.Server`:
  - `AppendBlockChildren`: chunking su >100 blocchi → **più** richieste, **sequenziali**, con
    `position:end`; verifica del conteggio sull'albero+byte; `doRejectRetryable` ritenta su
    429/503/529 (con `Retry-After`) e **non** su 502/504/timeout → `ErrAmbiguousWrite`.
  - `ListBlockChildren`: paginazione oltre 100 figli fino a `has_more=false` (test dedicato).
  - `DeleteBlock`: 404 trattato come successo; altri errori propagati.
  - `ValidateAppendable`: puro, table-driven (>100 figli, subtree >1000 blocchi, >500KB).
- **`internal/service`** — `replaceBody` con client finto: verifica dell'**ordine**
  snapshot → append → delete; skip di `child_page`/`child_database` con warning; convergenza
  su pagina vuota (create) = solo append; fallimento append lascia le proprietà applicate e
  produce l'output di §8.
- **`internal/cli`** — seam di package esistenti (`loadConfig`, `newClient`): pre-flight
  (file inesistente/vuoto → exit 2; stdin `-`), forma `--json` additiva, exit code 1 su
  fallimento parziale, warning su stderr non su stdout.

---

## 11. Documentazione da aggiornare

- **`README.md` / `README.it.md`**: nuova sottosezione Usage per `--body-file` (subset
  supportato, semantica **replace**/proprietario del corpo, costo O(n), limite nesting,
  degrado dei costrutti non mappati, concorrenza).
- **`skills/notion-track/SKILL.md`**: il corpo pagina **non** è più fuori scope. La sezione
  "When NOT to reach for this skill" va corretta (oggi dice "page bodies … out of scope");
  aggiungere la regola read-before-write anche per il corpo (replace è distruttivo) e come
  leggere `body.error` nel `--json`.

---

## 12. Decisioni chiuse in questo design

1. **Parser**: goldmark + GFM; possediamo solo il mapping.
2. **Retry append**: terza modalità `doRejectRetryable`, ritenta solo su rifiuto certo
   (429/503/529); ambiguo → `ErrAmbiguousWrite` + "ri-esegui per convergere".
3. **Delete distruttivo**: salta `child_page`/`child_database`, 404 = successo.
4. **Chunking**: conteggio su albero+byte; gruppi nel client; split contenuto nel markdown;
   elemento non affettabile → errore pre-flight.
5. **File vuoto** → errore (no wipe). **Non mappato** → degrada a testo + warning. **Nesting
   v1 = 2 livelli**, più profondo promosso + warning.
6. **`--json`**: campi `body` additivi; fallimento parziale distinguibile; exit 1.
7. **Tipo `Block`** nel package `notion` (dato puro con `MarshalJSON`); `Span` separato dai
   tipi read-oriented.

## 13. Rischi aperti

- **Enum linguaggi Notion**: il set/alias è mantenuto a mano; un linguaggio nuovo non ancora
  in whitelist degrada a plain text (non rompe, ma non colora). Aggiornabile.
- **Byte budget**: la stima della dimensione serializzata per il chunking è un'euristica;
  il margine sotto 500KB assorbe l'incertezza. Se un payload reale sfora, si abbassa il
  budget.
- **Fallimento ambiguo**: `ErrAmbiguousWrite` sposta sull'utente la convergenza (re-run). È
  il compromesso corretto per un'API senza transazioni, ma va comunicato bene nei messaggi.
