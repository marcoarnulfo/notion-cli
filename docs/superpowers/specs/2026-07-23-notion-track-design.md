# notion-track — Design doc

> Data: 2026-07-23 · Stato: approvato in brainstorming, da implementare
> Repo: `notion-cli` · Modulo: `github.com/marcoarnulfo/notion-cli` · Binario: `notion-track`

Documento di design in italiano (convenzione ereditata da `clickup-cli`: i design doc in
`docs/superpowers/` restano in italiano, tutto il resto del repo è in inglese).

---

## 1. Obiettivo

CLI Go open source per il task tracking su un database Notion, autenticata **esclusivamente**
con un integration token, con tre superfici di pari dignità: una TUI interattiva, comandi
headless per la CI, e — dalla v0.5 — un server MCP locale per gli agenti AI.

La primitiva centrale è l'**upsert idempotente per chiave ticket**: data una chiave, trovare
la riga e aggiornarla, oppure crearla se non esiste. Eseguito due volte, produce una riga.

### Perché solo token, mai OAuth

Vincolo fondativo, non preferenza estetica:

- **OAuth è inaccessibile ai workspace guest.** Il consent flow richiede un'autorizzazione a
  livello di workspace che un guest non può concedere. Due dei tre membri del team accedono
  come guest.
- **L'endpoint MCP remoto di Notion è bloccato dai firewall aziendali.** Alcuni firewall (es.
  Cato Networks) lo categorizzano sotto "Generative AI Tools / Remote MCP" e lo bloccano.
- **`api.notion.com` passa gli stessi firewall.**
- **Un internal integration token (`ntn_...`) aggira tutto.** L'integrazione è un bot con
  permessi propri: funziona identica per guest e membri, non richiede login browser, e vede
  solo i database che un Workspace Owner le ha esplicitamente condiviso.

Conseguenza da dichiarare nel README come tradeoff accettato: **tutte le modifiche risultano
fatte dal bot** (`last_edited_by` = integrazione). L'informazione "chi ha messo Fatto" si perde
strutturalmente.

### Perimetro

Task tracking **generico**: il tool assume il modello "una riga = un task" ma non hardcoda
alcun nome di proprietà. Il mapping tra concetti (chiave, stato, titolo) e proprietà reali del
database viene scoperto e configurato da `init`. Chiunque abbia un database di task su Notion
può usarlo.

Non-goal: CLI Notion generalista (lo copre `4ier/notion-cli`), Notion Workers (li copre `ntn`),
sync engine bidirezionale, OAuth in qualunque forma.

---

## 2. Vincolo tecnico critico: versione API e data source

**Questa sezione ha priorità su tutto il resto: va risolta prima di scrivere il client.**

Dalla versione API `2025-09-03` Notion ha introdotto i database multi-source come breaking
change ([upgrade guide](https://developers.notion.com/guides/get-started/upgrade-guide-2025-09-03),
[FAQ](https://developers.notion.com/docs/upgrade-faqs-2025-09-03)):

| Operazione | Pre-2025 | Da `2025-09-03` |
|---|---|---|
| Leggere lo schema | `GET /v1/databases/{id}` → `properties` | `GET /v1/databases/{id}` → lista `data_sources`; poi `GET /v1/data_sources/{id}` → `properties` |
| Query righe | `POST /v1/databases/{id}/query` | `/v1/data_sources/{data_source_id}/query` |
| Filtro search | `filter.value = "database"` | `filter.value = "data_source"` |
| Parent pagina | `{"type": "database_id", ...}` | `{"type": "data_source_id", ...}` |

**Restare pinnati a `2022-06-28` non è un'opzione sicura.** Se un utente aggiunge un secondo
data source a un database dalla UI, falliscono create page, read, query e le proprietà relation.
È un click di distanza.

Decisioni conseguenti:

1. Il client fissa la **versione stabile più recente** dell'header `Notion-Version`, mai
   inferiore a `2025-09-03`. Il numero esatto va verificato sul
   [changelog ufficiale](https://developers.notion.com/page/changelog) come **primo task
   dell'implementazione**, non assunto da questo documento.
2. Il config memorizza **`data_source_id` accanto a `database_id`** dal giorno 1.
3. `init` fa discovery in **due passi**: database → data source → schema. Se un database espone
   più data source, il wizard chiede quale.
4. La versione API è una costante in un solo punto di `internal/notion`, così l'upgrade è una
   riga più i test.

Metodo dell'endpoint di query: **`POST /v1/data_sources/{id}/query`**, verificato sul reference
ufficiale. Resta da fissare in fase di implementazione solo il numero esatto della versione
stabile più recente.

---

## 3. Architettura

Sei livelli, regola di dipendenza rigida: si guarda solo verso il basso.

```
cmd/notion-track/main.go            os.Exit(cli.Execute())
        │
internal/cli    internal/tui                      ← gusci sottili
        └───────────┬──────────┘
            internal/service                      ← orchestrazione
                    │
   ┌────────────────┼─────────────────┬──────────────────┐
internal/tracker  internal/notion  internal/config  internal/markdown
 (PURO, no I/O)    (net/http)       (YAML, profili)  (PURO, no I/O)
```

**`internal/tracker`** — dominio puro, non importa né `notion` né `config`. Riceve dati,
restituisce decisioni: dato l'insieme di righe trovate decide create/update/errore; dato un
mapping e dei flag costruisce il payload delle proprietà; valida uno stato contro le opzioni
ammesse. Testabile senza rete e senza mock.

**`internal/markdown`** — dominio puro. Markdown in, albero di blocchi Notion out. Ci vive tutto
il chunking: massimo 100 blocchi per chiamata, massimo 2000 caratteri per `rich_text`. Nessuna
conoscenza di HTTP.

**`internal/notion`** — solo HTTP: bearer token, header `Notion-Version`, paginazione, retry con
backoff, unmarshalling nei tipi API. Nessuna nozione di "ticket" o "stato".

**`internal/config`** — lettura/scrittura YAML, profili, `schema_version` con `migrate()`,
precedenza delle sorgenti.

**`internal/service`** — l'unico punto dove le cose si incontrano: carica il profilo, chiama il
client, passa i risultati al tracker, applica la decisione. CLI, TUI e (in v0.5) MCP invocano le
stesse funzioni con gli stessi argomenti.

**`internal/cli`** e **`internal/tui`** — gusci sottili. Un comando cobra è parsing dei flag più
una chiamata al service più formattazione dell'output.

`internal/mcp` **non** compare nell'architettura v0.1: arriva in v0.5 come adapter sopra
`internal/service`.

### Stack

- **Go**, versione allineata a `clickup-cli` (`go 1.26.x`).
- **`spf13/cobra`** per la CLI. Divergenza consapevole dal README originale, che proponeva
  `urfave/cli`: il progetto gemello usa cobra in due file piatti senza scaffolding per comando,
  il che falsifica l'argomento anti-cobra del README. Un solo framework nei due progetti gemelli.
- **`charmbracelet/bubbletea` + `bubbles` + `lipgloss`** per la TUI.
- **`net/http` stdlib** per l'API: nessun SDK di terze parti, come `internal/clickup` nel gemello.
  Ci servono cinque endpoint; gli struct dei property value sono lavoro contenuto e in cambio
  controlliamo la `Notion-Version` (vedi §2).
- **`gopkg.in/yaml.v3`** per il config.

Il rate limiting è **reattivo** (retry con backoff sui `429`, §9) e non richiede dipendenze. Un
limiter proattivo tipo `golang.org/x/time/rate` si aggiunge solo se l'uso reale dimostra che
serve.

---

## 4. Config e profili

Percorso: `os.UserConfigDir()/notion-track/config.yml`, permessi file `0600`, directory `0755`.
`schema_version` con `migrate()`: versione assente → migrazione silenziosa; versione futura →
warning su stderr ma il caricamento procede. Regola ereditata: *Load può emettere warning, Save
deve restare silenzioso.*

```yaml
schema_version: 1
default_profile: work
profiles:
  work:
    database_id: 1a2b3c...
    data_source_id: 9x8y7z...
    properties:
      ticket: Ticket          # rich_text | title
      status: Stato           # status | select
      title:  Name            # title
      due:    Scadenza        # date (opzionale)
    status_type: status       # determina il comportamento di validazione, vedi §6
```

### Token

**Mai scritto su file di default.** In v0.1 il token viene **solo** da `NOTION_TOKEN`: il
fallback su file arriva insieme al wizard TUI, che è l'unico contesto in cui un utente potrebbe
volerlo salvare. Come nel fix documentato di `clickup-cli`, un **flag di provenienza** (non un
confronto di valore) garantisce che un token letto da env non venga mai riscritto su disco.

Il token non compare **mai** in output, log, messaggi d'errore o dump di debug delle request.

### Precedenza delle sorgenti

Uniforme su ogni valore: **flag → env → config → default**.

Il profilo si seleziona con `--profile` o con `NOTION_TRACK_PROFILE`. Le env var dei singoli
valori (`NOTION_TRACK_DB`, `NOTION_TRACK_DATA_SOURCE`, …) **scavalcano** il profilo selezionato,
coerentemente con la regola generale.

### Uso in CI

Il file di config **non contiene il token**, quindi è committabile nel repo. In CI si usa
`--config ./notion-track.yml` (o il file scoperto nella working directory) più `NOTION_TOKEN`
dai secret. Questo chiude un buco del README originale, il cui esempio CI passava solo token e
database ID e non avrebbe avuto modo di conoscere il mapping delle proprietà.

---

## 5. Schema discovery e `init`

È ciò che rende il tool open source invece che interno.

**Passo 1 — elenco dei data source.** `POST /v1/search` con `filter.value = "data_source"`
elenca ciò che è condiviso con l'integrazione. L'utente sceglie da una lista, non incolla un ID.

Caveat documentati ([search limitations](https://developers.notion.com/reference/search-optimizations-and-limitations)):
l'indice è eventually consistent, ma ciò che è condiviso **direttamente** con la connection è
garantito nei risultati — il caso di `init` è coperto. Il wizard offre comunque un "riprova" per
lo scenario "l'owner ha appena condiviso il database". Più data source dello stesso database
appaiono con lo stesso titolo: il wizard deve mostrare il nome del data source per distinguerli.

Se la lista è vuota, il messaggio dice testualmente che il Workspace Owner non ha ancora
aggiunto la connection al database, con i passi per farlo. Senza questo, è la prima issue che
riceveremo.

**Passo 2 — schema.** `GET /v1/data_sources/{id}` restituisce ogni proprietà con nome, tipo e —
per `select` e `status` — l'elenco delle opzioni.

**Passo 3 — mapping.** Il wizard propone un mapping per euristica (l'unica proprietà `title` è il
titolo; una proprietà `status`/`select` chiamata Status/Stato è lo stato; una `rich_text` chiamata
Ticket/Key è la chiave) e chiede conferma.

`init` esiste in due forme: **wizard TUI** (bubbletea, come `internal/tui/setup.go` del gemello)
e **headless a flag** (`init --data-source-id X --ticket-prop Ticket --status-prop Stato ...`)
per CI e agenti, con `init --list` a stampare gli id disponibili. È l'unico comando interattivo
del tool.

---

## 6. `select` contro `status`: comportamento di validazione

Distinzione con conseguenze pratiche opposte a quanto si intuisce
([property object](https://developers.notion.com/reference/property-object),
[update property schema](https://developers.notion.com/reference/update-property-schema-object)):

- Una proprietà **`status`** si scrive via API, ma le sue opzioni **non si creano** via API. Un
  valore sconosciuto produce un errore di validazione lato Notion. L'API ci protegge già.
- Una proprietà **`select`** invece **auto-crea l'opzione** se il nome non esiste. `--status
  "Fattto"` con un typo crea silenziosamente un valore spazzatura nel database.

Quindi il tool valida il valore **contro le opzioni lette dal server**, e lo fa soprattutto per
proteggere le `select`. Non teniamo una cache di `status_options` nel config: una cache deriva
dallo schema reale e introduce falsi negativi (rifiuta valori nuovi legittimi) e falsi positivi
(accetta valori rimossi).

La forma del payload la decide sempre lo **schema live**, non il config. `status_type` registra
invece cosa fosse la proprietà quando `init` è stato eseguito, e serve a `doctor`: se una
proprietà passa da `select` a `status` o viceversa, cambia il modello di sicurezza — una
`select` accetta e crea valori sconosciuti, una `status` li rifiuta — e l'utente merita di
saperlo.

Un valore rifiutato è **uso errato**, quindi exit code 2, non 1: è un errore dell'invocazione,
non un guasto.

`doctor` resta il posto dove lo schema live viene confrontato col config.

---

## 7. Comandi

Root senza argomenti apre la TUI, come `clup`. I subcomandi sono headless e muti in caso di
successo; nessuno chiede input interattivo, eccetto `init`.

```sh
notion-track                          # TUI: sfoglia, filtra, cambia stato
notion-track init                     # wizard TUI
notion-track init --list              # elenca i data source condivisi con l'integrazione
notion-track init --data-source-id X \
  --ticket-prop Ticket --status-prop Stato --title-prop Name    # forma headless

notion-track upsert --ticket BDF-231-SubD \
  --title "Hardening rotaia AI" --status "In corso" --body-file notes.md
notion-track set    --ticket BDF-231-SubD --status Fatto
notion-track get    --ticket BDF-231-SubD [--json]
notion-track list   --status "In corso"   [--json]
notion-track doctor [--json]
```

`--profile` e `--config` sono flag globali.

**`upsert` contro `set`** è una distinzione deliberata: `upsert` crea se la riga manca, `set`
fallisce. In CI si usa il primo dopo un merge, il secondo quando un ticket inesistente è il
sintomo di un errore che non va mascherato creando una riga fantasma.

### `doctor`

Verifica, in ordine, emettendo un esito per ciascun check:

1. token presente e valido (chiamata `GET /v1/users/me`);
2. `database_id` e `data_source_id` raggiungibili con la connection corrente;
3. ogni proprietà mappata nel config esiste ancora, con il tipo atteso — segnala le derive
   ("`Stato` non esiste più; il database ha `Status` di tipo status: forse è stata rinominata");
4. **duplicati**: righe che condividono la stessa chiave ticket, elencate con i loro URL;
5. warning se trova una stringa che sembra un token in un file tracciato dal repo.

Esce 0 se solo warning, non-zero al primo errore.

---

## 8. Output, exit code, errori

Dati su **stdout**, errori e warning su **stderr**. `cli.Execute()` non chiama mai `os.Exit`:
ritorna un `int`, stampa l'errore una sola volta come `error: <msg>`, e `main()` fa
`os.Exit(cli.Execute())` — pattern preso da `internal/cli/cli.go` del gemello.

`--json` è disponibile su tutti i comandi di lettura e scrittura fin dalla v0.1. Lo schema JSON
è dichiarato nel README **stabile per lo scripting** (chiavi `snake_case`, timestamp RFC3339):
cambiarlo è un breaking change.

| Codice | Significato |
|---|---|
| 0 | successo |
| 1 | errore generico |
| 2 | uso errato (flag mancante, valore non ammesso) |
| 3 | riga non trovata (`set`, `get`) |
| 4 | ticket duplicato: più righe corrispondono |
| 5 | autenticazione o autorizzazione fallita |

Divergenza dichiarata dal gemello, che usa solo 0/1: `clup` è un tool interattivo, `notion-track`
gira in pipeline che devono distinguere "non trovato" da "token scaduto" senza fare parsing di
stringhe.

### Errori azionabili

Ogni errore dice **cosa è successo, perché, e cosa fare**. È ciò che rende la CLI usabile da un
agente quanto da un umano: un agente che legge `error: unauthorized` si blocca, uno che legge il
messaggio qui sotto sa come procedere.

```
error: database not accessible (401)
  the integration token is valid but has no access to database 1a2b3c
  fix: open the database in Notion → ••• → Connections → add your integration
```

### Duplicati

L'upsert su più di un match **fallisce** (codice 4) e stampa gli URL di tutte le righe
corrispondenti. Non aggiorna "la più recente": un duplicato è un problema di dati e il tool non
lo peggiora scegliendo silenziosamente.

Rischio noto e accettato: due job CI concorrenti sullo stesso ticket possono entrambi non
trovare la riga e crearla, generando il duplicato. L'API Notion non offre transazioni né vincoli
di unicità, quindi non è prevenibile lato client. La via d'uscita è `doctor`, che elenca i
duplicati con i loro URL perché l'utente li risolva.

---

## 9. Rate limit e resilienza

Notion limita a circa **3 richieste al secondo per integrazione**, con burst, e risponde `429`
con header `Retry-After` in secondi ([request limits](https://developers.notion.com/reference/request-limits)).

Il vincolo è più stretto di quanto sembri: un solo token è condiviso tra tre persone, i job CI e
la TUI che pagina. È **un unico bucket**.

Esiste un secondo codice da trattare allo stesso modo: **`529` (`service_overload`)**, che la
documentazione ufficiale accosta esplicitamente al `429` — "handling HTTP 429 and 529 responses
and respecting the `Retry-After` response header value".

`internal/notion` implementa dal giorno 1: retry con backoff esponenziale sui `429` e sui `529`
rispettando `Retry-After`, retry sui `502`/`503`/`504`, numero massimo di tentativi
configurabile, timeout e `context.Context` propagato su ogni chiamata. Senza questo, la promessa
"safe to put in a retried CI job" è falsa.

---

## 10. Body Markdown

`internal/markdown` converte Markdown in blocchi Notion in v0.1, completo: heading, liste
puntate e numerate, checkbox, code block, quote, testo con formattazione inline.

È il pacchetto più grosso della v0.1 e ne allunga i tempi più di ogni altra parte; è una scelta
consapevole. In compenso è dominio puro (Markdown in, albero di blocchi out), quindi si sviluppa
in TDD table-driven senza toccare la rete.

Vincoli che vivono in questo pacchetto: massimo 100 blocchi per chiamata di append, massimo 1000
blocchi per payload, massimo 2000 caratteri per `rich_text`. La semantica di aggiornamento del
body è **replace**: `upsert --body-file` sostituisce il contenuto della pagina, non accoda.
Scelta coerente con l'idempotenza — eseguire due volte lo stesso comando deve produrre lo stesso
risultato.

---

## 11. Testing

Solo stdlib: `testing` + `net/http/httptest`. Nessun testify, gomock o framework esterno, come
nel gemello. TDD dichiarato (RED → GREEN), `go test ./... -race` sempre, un `*_test.go` accanto a
ogni file di produzione.

- **`internal/tracker` e `internal/markdown`** — il grosso dei test, table-driven puri: zero
  righe → create, una → update, due → errore; stato valido e non valido; ogni costrutto Markdown
  e ogni limite di chunking. Nessun mock, millisecondi.
- **`internal/notion`** — `httptest.Server` con risposte JSON reali catturate dall'API, inclusi i
  casi sgradevoli: `429` con `Retry-After`, `401`, `404`, paginazione su più pagine.
- **`internal/cli`** — seam a livello di package come nel gemello (`var loadConfig = config.Load`,
  `var newClient = ...`) che i test sostituiscono.
- **`internal/config`** — path come `var` iniettabili verso `t.TempDir()`.
- **`internal/tui`** — `Update()` invocato con messaggi simulati, senza snapshot.

---

## 12. Roadmap

**v0.1 — MVP.** TUI di browsing; `init` (wizard TUI + forma headless); `upsert`, `set`, `get`,
`list`, `doctor`; `--json` ovunque; profili nel config; `internal/markdown` completo; retry con
backoff; exit code differenziati.

**v0.2 — Distribuzione e agenti.** GoReleaser con binari macOS/Linux/Windows su amd64/arm64;
GitHub Action composite (`uses: marcoarnulfo/notion-cli/action@v1`) con versione pinnata e
verifica del checksum; `AGENTS.md` e una skill che insegnano i comandi a un agente.

**v0.3 — Ergonomia.** Body templating (placeholder per chiave ticket e data); operazioni bulk da
manifest CSV/JSON; `--dry-run`.

**v0.5 — `notion-track mcp`.** Server MCP su stdio che espone `upsert`/`set`/`get`/`list` come
tool, sopra lo stesso `internal/service`.

Il README deve spiegare in una riga perché l'MCP in roadmap non contraddice il pitch: l'MCP
**remoto** di Notion è bloccato dai firewall aziendali, un server MCP **locale** che parla con
`api.notion.com` no. `notion-track` porta Notion agli agenti proprio dove l'MCP ufficiale non
arriva.

---

## 13. Distribuzione

Divergenza consapevole dal gemello, che distribuisce solo via `go install`: un runner CI non ha
una toolchain Go e non deve compilare nulla per cambiare uno stato. Quindi binari precompilati
via GoReleaser dalla v0.2, più `go install github.com/marcoarnulfo/notion-cli/cmd/notion-track@latest`
per chi ha Go. Homebrew tap solo se qualcuno lo chiede.

---

## 14. Impianto open source

Identico al gemello: `README.md` in inglese (primario) e `README.it.md` in italiano, entrambi col
selettore lingua in prima riga e da aggiornare **insieme** (voce della checklist PR); badge CI,
release, Go version, MIT, PRs welcome; `CONTRIBUTING.md` + `CONTRIBUTING.it.md`; Contributor
Covenant; issue e PR template bilingui; CI con `go vet`, `staticcheck` via `go run`,
`go test ./... -race`, `go build ./...`.

Licenza **MIT**.

Commit in **Conventional Commits** con scope (`feat(tracker):`, `fix(notion):`), **mai**
`Co-Authored-By`. Branch di feature descrittivi → PR → merge su `main`.

**Lingua**: tutto ciò che vive nel repo è in **inglese** — codice, identificatori, commenti,
messaggi di errore, stringhe della TUI, commit. Eccezioni: `README.it.md`, `CONTRIBUTING.it.md`,
i design doc in `docs/superpowers/`, e le issue GitHub (bilingui, sezione 🇬🇧 poi 🇮🇹).

Due aggiunte rispetto al gemello, giustificate dal fatto che il tool maneggia un segreto
condiviso tra più persone e la CI:

- **SECURITY.md** (assente in `clickup-cli`).
- Sezione sulla **rotazione del token** nel README: come ruotarlo, cosa aggiornare (secret CI,
  config locali), e il fatto che il blast radius di un token compromesso è limitato ai soli
  database condivisi con l'integrazione.

---

## 15. Riscritture necessarie del README esistente

L'attuale `README.md` descrive scelte superate da questo design e va riscritto di conseguenza:

| Punto | README attuale | Design |
|---|---|---|
| Framework CLI | `urfave/cli` v3 | **cobra** |
| Client API | SDK `jomei/notionapi` | **`net/http`** stdlib |
| Versione API | pin `2022-06-28` accettato | **≥ `2025-09-03`**, data source |
| Config | TOML | **YAML** con profili |
| `--json` | v0.2 | **v0.1** |
| `list` | v0.2 | **v0.1** |
| TUI | non menzionata | **feature v0.1** |
| Duplicati | domanda aperta | **errore, confermato** |
| Attribuzione bot | non menzionata | **tradeoff dichiarato** |

---

## 16. Domande chiuse in questo design

- **Nome binario**: `notion-track` (repo `notion-cli`, modulo `github.com/marcoarnulfo/notion-cli`).
- **Duplicati**: `upsert` fallisce con exit code 4, `doctor` li elenca.
- **Licenza**: MIT.
- **Chiave ticket**: il tipo (`title` o `rich_text`) non è fissato dal tool — lo scopre `init` e
  lo registra nel config. La domanda aperta del README decade.
- **Homebrew**: rimandato finché qualcuno non lo chiede.

## 17. Rischi aperti

1. **Versione API esatta e metodo dell'endpoint di query** — da verificare sul changelog e sul
   reference ufficiale come primo task dell'implementazione (§2).
2. **`internal/markdown`** — è la parte con il rapporto ambizione/costo più alto della v0.1; se
   dovesse dilatare i tempi oltre il tollerabile, il fallback è degradare il body a blocchi
   paragraph in v0.1 e completare il Markdown in v0.4.
3. **Duplicati da concorrenza** — non prevenibili lato client, mitigati da `doctor` (§8).
4. **Attrito operativo dei profili** — ogni nuovo database richiede che un Workspace Owner
   aggiunga la connection; i guest non possono farlo da soli.
