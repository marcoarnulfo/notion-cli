[English](README.md) · **Italiano**

# notion-track

[![CI](https://github.com/marcoarnulfo/notion-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/marcoarnulfo/notion-cli/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/marcoarnulfo/notion-cli)](https://github.com/marcoarnulfo/notion-cli/releases)
[![Go version](https://img.shields.io/github/go-mod/go-version/marcoarnulfo/notion-cli)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)

> Una piccola CLI Go, volutamente opinionata, per tenere sincronizzato un database Notion di task tracking dal terminale — e dalla CI. Libera e open-source (MIT).

`notion-track` conosce una cosa sola: un database Notion in cui ogni riga è un ticket, identificato da una chiave (ticket key), con uno stato e poche altre proprietà. La sua operazione centrale è un **upsert idempotente** — data una chiave, trova la riga e la aggiorna, oppure la crea se non esiste. Eseguito due volte, produce una riga sola.

L'autenticazione avviene solo con un **token di integrazione interna** di Notion — nessun OAuth da browser. Quel token è un bot con permessi propri, ristretti ai soli database condivisi con lui: è ciò che lo fa funzionare identicamente per membri del workspace e guest, e ciò che lo rende utilizzabile dietro firewall che bloccano l'endpoint MCP ospitato di Notion.

## Funzionalità

- **Upsert idempotente** (`upsert`) — crea o aggiorna la riga di un ticket in base alla sua chiave. Due esecuzioni, una riga sola.
- **Scrittura solo in aggiornamento** (`set`) — fallisce con un exit code dedicato se il ticket non esiste ancora, invece di crearlo silenziosamente.
- **Lettura** (`get`, `list`) — una riga o molte, filtrabili per stato, in forma leggibile o `--json`.
- **Diagnostica** (`doctor`) — verifica il token, l'accesso alla data source, il mapping delle proprietà (compreso il drift di tipo rispetto a quando `init` è stato eseguito) e scansiona l'intera data source alla ricerca di chiavi ticket duplicate.
- **Configurazione guidata** (`init`) — scrive un profilo a partire dai flag, validato contro lo schema live della data source prima di salvare qualsiasi cosa; `init --list` scopre gli id delle data source visibili alla tua integrazione.
- **Profili** — più configurazioni di database, con nome, in un solo file YAML, selezionabili via flag, variabile d'ambiente o un default configurato.
- **`--json` ovunque** — ogni comando che produce output (`get`, `list`, `doctor`, `upsert`, `set`) può emettere JSON leggibile da macchina, con una forma documentata e stabile.
- **Pensato per la CI** — silenzioso in caso di successo, un exit code distinto per ogni classe di errore (auth, non trovato, duplicato, uso scorretto, generico), nessun prompt interattivo.
- **Retry con backoff** sul rate limiting di Notion (429) e sulle risposte transitorie 502/503/504/529, rispettando `Retry-After` quando Notion lo invia.
- **Un unico binario Go statico** — niente runtime Node, niente venv Python.

## Requisiti

- **[Go](https://go.dev/dl/) 1.26 o successivo** — necessario per compilare o installare da sorgente; non esistono ancora binari precompilati (vedi [Roadmap](#roadmap)).
- Un **token di integrazione interna** Notion (`ntn_...`), creato da un **Workspace Owner** su <https://www.notion.so/my-integrations>.
- Un database Notion **condiviso con quell'integrazione**.

## Installazione

```bash
go install github.com/marcoarnulfo/notion-cli/cmd/notion-track@latest
```

Installa il binario `notion-track` in `$(go env GOPATH)/bin` (assicurati che sia nel tuo `PATH`).

<details>
<summary>Compilare da sorgente</summary>

```bash
git clone https://github.com/marcoarnulfo/notion-cli.git
cd notion-cli
go build -o notion-track ./cmd/notion-track
./notion-track --help
```
</details>

Non esiste ancora una release con binari precompilati — GoReleaser e le GitHub Releases sono nella [roadmap](#roadmap).

## Avvio rapido

1. **Crea l'integrazione.** Un Workspace Owner va su <https://www.notion.so/my-integrations>, crea una nuova integrazione **interna** e copia il token (`ntn_...`). Solo un Workspace Owner può fare questo passaggio.
2. **Condividi il database con l'integrazione.** Sempre come Workspace Owner, apri il database di tracking in Notion → **•••** (in alto a destra) → **Connessioni** → aggiungi l'integrazione. Senza questo passaggio ogni richiesta di `notion-track` risponderà 404, token o non token.
3. **Esporta il token** (non scriverlo mai in un file di configurazione):
   ```bash
   export NOTION_TOKEN=ntn_...
   ```
4. **Trova l'id della data source.** Un database può contenere più di una data source, quindi chiedi all'integrazione cosa vede:
   ```bash
   notion-track init --list
   ```
5. **Configura un profilo**, mappando i concetti di notion-track sui nomi reali delle proprietà del tuo database — `init` valida ogni proprietà contro lo schema live prima di scrivere qualsiasi cosa:
   ```bash
   notion-track init \
     --data-source-id <id> \
     --ticket-prop Ticket \
     --status-prop Stato \
     --title-prop Name
   ```
6. **Verifica la configurazione:**
   ```bash
   notion-track doctor
   ```
7. **Crea o aggiorna una riga** — questo è il comando che userai davvero ogni giorno:
   ```bash
   notion-track upsert --ticket BDF-231 --title "Hardening" --status "In corso"
   notion-track upsert --ticket BDF-231 --status "Fatto"   # aggiorna la stessa riga, nessun duplicato
   ```

## Uso

Flag globali, disponibili su ogni comando:

| Flag | Significato |
|---|---|
| `--profile string` | profilo di configurazione da usare (vedi [Configurazione](#configurazione)) |
| `--config string` | percorso di un file di configurazione esplicito, al posto della posizione predefinita del sistema operativo |

### `init` — configura un profilo

```
notion-track init --data-source-id <id> --ticket-prop <nome> --status-prop <nome> --title-prop <nome> [--due-prop <nome>] [--database-id <id>] [--list]
```

| Flag | Significato |
|---|---|
| `--data-source-id string` | id della data source (obbligatorio, salvo `--list`) |
| `--ticket-prop string` | proprietà che contiene la chiave del ticket — deve essere `rich_text` o `title` (obbligatorio) |
| `--status-prop string` | proprietà che contiene lo stato — deve essere `status` o `select` (obbligatorio) |
| `--title-prop string` | proprietà titolo (obbligatorio) |
| `--due-prop string` | proprietà data (opzionale) |
| `--database-id string` | id del database, registrato solo come riferimento — ogni lettura/scrittura usa `--data-source-id`, non questo |
| `--list` | elenca gli id delle data source condivise con l'integrazione, ed esce |

Ogni proprietà mappata viene verificata contro lo schema live della data source; `init` rifiuta di scrivere un profilo che si romperebbe al primo uso (tipo sbagliato, o proprietà inesistente). `--ticket-prop`, `--status-prop` e `--title-prop` sono di fatto obbligatori — `init` restituisce un errore di uso indicando quale manca — anche se `--due-prop` è l'unico realmente opzionale. Il profilo viene scritto con il nome passato tramite `--profile` (default `"default"`); se è il primo profilo nel file diventa anche `default_profile`. Rilanciare `init` con lo stesso nome di `--profile` sovrascrive quel profilo senza toccare gli altri.

### `upsert` — crea o aggiorna una riga per chiave ticket

```
notion-track upsert --ticket <chiave> [--title <titolo>] [--status <stato>] [--due YYYY-MM-DD] [--json]
```

Il comando principale. Interroga la data source per la riga la cui proprietà ticket è uguale a `--ticket`: la aggiorna se la trova, altrimenti la crea. `0` corrispondenze → crea, `1` corrispondenza → aggiorna, `>1` corrispondenze → fallisce con exit code 4 (vedi [Limitazioni](#limitazioni)). Silenzioso in caso di successo; con `--json` stampa `{"action": "created"|"updated", "page": {...}}`.

### `set` — aggiorna solo una riga esistente

```
notion-track set --ticket <chiave> [--title <titolo>] [--status <stato>] [--due YYYY-MM-DD] [--json]
```

Stessi campi di `upsert`, ma fallisce con exit code 3 se il ticket non esiste ancora, invece di crearlo. Usalo dove un ticket mancante è un sintomo da far emergere, non un dettaglio da ignorare.

### `get` — legge una riga

```
notion-track get --ticket <chiave> [--json]
```

Stampa ticket, titolo, stato e URL della riga. Fallisce con exit code 3 se non trovata, o 4 se la chiave corrisponde a più di una riga (vedi [Limitazioni](#limitazioni)).

### `list` — legge più righe

```
notion-track list [--status <stato>] [--json]
```

Elenca tutte le righe, oppure solo quelle che corrispondono a `--status`. Un valore di stato sconosciuto fallisce subito con exit code 2, indicando i valori realmente ammessi da Notion per quella proprietà.

### `doctor` — verifica la configurazione

```
notion-track doctor [--json]
```

Esegue quattro controlli — `token`, `data_source`, `properties`, `duplicates` — e stampa ciascuno come `ok`, `warn` o `fail` con un messaggio di dettaglio azionabile. Un `warn` (ad es. il tipo della proprietà stato è cambiato da quando `init` è stato eseguito) non fa fallire il comando; qualsiasi `fail` lo fa uscire con codice diverso da zero. `properties` e `duplicates` vengono eseguiti anche quando `data_source` fallisce, così una configurazione rotta viene diagnosticata in un solo passaggio invece che un sintomo alla volta.

## Configurazione

Il file di configurazione vive in `os.UserConfigDir()/notion-track/config.yml` — rispettando `$XDG_CONFIG_HOME` su Linux:

| Sistema operativo | Percorso predefinito |
|---|---|
| macOS | `~/Library/Application Support/notion-track/config.yml` |
| Linux | `~/.config/notion-track/config.yml` |
| Windows | `%AppData%\notion-track\config.yml` |

Passa `--config /percorso/al/file.yml` per puntare a un file diverso — è così che si usa un file di configurazione **committato in un repository di progetto** invece del default per-utente (vedi [Uso in CI](#uso-in-ci)).

```yaml
schema_version: 1        # scritto automaticamente da `init`/`upsert`/`set`; non modificarlo a mano
default_profile: work    # usato quando --profile e NOTION_TRACK_PROFILE sono entrambi assenti
profiles:
  work:
    database_id: "1a2b3c4d..."     # opzionale, solo informativo — nessuna operazione lo legge
    data_source_id: "5e6f7a8b..."  # obbligatorio — ogni query, creazione e aggiornamento usa questo id
    status_type: status            # "status" o "select", registrato da `init`; vedi il check "properties" di doctor
    properties:
      ticket: Ticket   # proprietà rich_text o title che contiene la chiave del ticket
      status: Stato    # proprietà status o select
      title: Name      # proprietà titolo
      due: Scadenza    # opzionale: proprietà data
```

Il file **non contiene alcun segreto** ed è sicuro da committare in un repository — è proprio questo il punto: permette alla CI (e a ogni collega) di condividere lo stesso mapping delle proprietà senza rilanciare `init`. Il token non viene mai letto da questo file; `init` non ce lo scrive mai.

### Variabili d'ambiente

| Variabile | Effetto |
|---|---|
| `NOTION_TOKEN` | il token di integrazione. È l'**unica** fonte da cui `notion-track` legge un token — non esiste un fallback sul file di configurazione né un flag. |
| `NOTION_TRACK_PROFILE` | quale profilo risolvere, a meno che `--profile` non sia anch'esso specificato |
| `NOTION_TRACK_DB` | sovrascrive il `database_id` del profilo risolto |
| `NOTION_TRACK_DATA_SOURCE` | sovrascrive il `data_source_id` del profilo risolto |

Precedenza:

- **Selezione del profilo:** flag `--profile` → `NOTION_TRACK_PROFILE` → `default_profile` nel file di configurazione.
- **`database_id` / `data_source_id`:** le variabili d'ambiente sopra sovrascrivono sempre ciò che il profilo risolto ha su file, indipendentemente da come quel profilo è stato scelto — è ciò che permette a un job CI di puntare un profilo esistente verso un'altra data source senza toccare il file committato.
- **Token:** `NOTION_TOKEN`, punto.
- **Percorso del file di configurazione:** flag `--config` → il percorso predefinito del sistema operativo sopra. Non esiste una variabile d'ambiente per il percorso stesso.

## Output JSON

Ogni forma `--json` qui sotto è un **contratto di scripting documentato e stabile**: una chiave non viene mai rinominata o rimossa senza un annuncio di breaking change.

Una riga (`get --json`, e ogni elemento di `list --json`):

```json
{
  "ticket": "BDF-231",
  "title": "Hardening",
  "status": "In corso",
  "page_id": "1a2b3c4d-...",
  "url": "https://www.notion.so/...",
  "last_edited_time": "2026-07-23T10:15:00Z"
}
```

Se il mapping configurato indica una colonna che la riga non porta davvero, il campo corrispondente torna come stringa vuota invece che come errore — segnalare un mapping rotto è compito di `doctor`, non un motivo per far fallire ogni lettura.

`upsert --json` / `set --json`:

```json
{
  "action": "created",
  "page": { "ticket": "BDF-231", "title": "Hardening", "status": "In corso", "page_id": "...", "url": "...", "last_edited_time": "..." }
}
```

`action` vale `"created"` o `"updated"`.

`doctor --json` — un array di check, uno per `token` / `data_source` / `properties` / `duplicates`:

```json
[
  { "name": "token", "status": "ok", "detail": "authenticated as notion-track" },
  { "name": "data_source", "status": "ok", "detail": "reachable: Tasks" },
  { "name": "properties", "status": "ok", "detail": "all mapped properties exist with the expected types" },
  { "name": "duplicates", "status": "ok", "detail": "42 rows, no repeated ticket keys" }
]
```

`status` vale `"ok"`, `"warn"` o `"fail"`; `detail` è omesso solo quando vuoto, cosa che in pratica non accade.

## Exit code

Le pipeline possono ramificare su questi valori senza fare parsing di alcun messaggio:

| Codice | Nome | Significato |
|---|---|---|
| `0` | OK | successo |
| `1` | Error | un errore generico — un errore di rete/API, oppure `doctor` che segnala un check fallito |
| `2` | Usage | l'invocazione non può funzionare così com'è: un flag mancante o non valido, un comando sconosciuto, nessuna configurazione ancora presente (`notion-track init` non è mai stato eseguito), o un valore di stato che la data source non ammette |
| `3` | Not found | il ticket richiesto non ha una riga corrispondente (`get`, `set`) |
| `4` | Duplicate | la chiave del ticket corrisponde a più di una riga (`upsert`, `get`) |
| `5` | Auth | nessun token trovato, oppure Notion lo ha rifiutato (401/403) |

## Uso in CI

Poiché il file di configurazione non contiene segreti, lo schema comune è **committarlo nel repository** e puntarci esplicitamente con `--config`, mentre il token arriva da un secret CI:

```yaml
# .github/workflows/notion.yml
- name: Install notion-track
  run: go install github.com/marcoarnulfo/notion-cli/cmd/notion-track@latest

- name: Segna il ticket come fatto
  run: notion-track upsert --ticket "$TICKET" --status "Fatto" --config notion-track.yml
  env:
    NOTION_TOKEN: ${{ secrets.NOTION_TOKEN }}
    TICKET: ${{ github.event.inputs.ticket }}
```

Una composite GitHub Action che avvolga questo binario è nella [roadmap](#roadmap); oggi, `go install` più le due righe sopra è l'intera integrazione.

## Limitazioni

Sono tradeoff attuali e deliberati — non bug di cui sorprendersi:

1. **Ogni modifica risulta attribuita all'integrazione, non a te.** Notion registra le modifiche fatte via API come effettuate dall'identità bot dell'integrazione. Se controlli la cronologia modifiche di una pagina in Notion, vedrai il nome dell'integrazione, mai la persona o il job CI che ha effettivamente lanciato il comando.
2. **`upsert` e `get` falliscono sui ticket duplicati invece di sceglierne uno.** Se più righe condividono la stessa chiave ticket, `notion-track` rifiuta di indovinare quale intendessi — esce con codice 4 ed elenca le righe in conflitto. Esegui `notion-track doctor` per trovarle e ripulirle.
3. **Due job concorrenti che creano lo stesso ticket nuovo possono generare un duplicato per race condition.** La decisione crea-o-aggiorna di `upsert` legge le righe correnti e poi scrive; l'API di Notion non offre un vincolo di unicità né un compare-and-swap per chiudere quella finestra. Non è prevenibile lato client — la scansione dei duplicati di `doctor` è la mitigazione, non una soluzione.
4. **Solo un Workspace Owner può fare questa configurazione.** Creare l'integrazione e condividere un database con essa richiedono entrambi permessi da Workspace Owner in Notion. Un guest del workspace — una delle ragioni per cui questo strumento esiste — non può fare nessuno dei due passaggi, ma può usare liberamente lo strumento una volta che qualcuno con permessi da Owner li ha completati.
5. **Nessun body Markdown, e nessuna TUI interattiva ancora.** `upsert`/`set` toccano solo le proprietà documentate sopra — non esiste un `--body-file` per scrivere il contenuto della pagina, e non esiste un wizard o un'interfaccia di navigazione: ogni comando qui è guidato da flag. Entrambi sono tracciati nella [Roadmap](#roadmap).

## Contribuire

I contributi sono benvenuti — questo è un progetto libero e open-source. Vedi **[CONTRIBUTING.it.md](CONTRIBUTING.it.md)** per la configurazione dell'ambiente di sviluppo, i controlli da eseguire prima di aprire una PR, e le regole architetturali non negoziabili del progetto. Leggi anche il [Codice di condotta](CODE_OF_CONDUCT.md). Hai trovato un problema di sicurezza? Vedi [SECURITY.md](SECURITY.md) invece di aprire una issue pubblica.

## Roadmap

Implementato oggi: `init` (guidato da flag, con `--list`), `upsert`, `set`, `get`, `list`, `doctor`; `--json` su ogni comando che produce output; profili; retry con backoff.

Non ancora costruito:

- **Wizard interattivo per `init`** — un'alternativa TUI guidata all'attuale forma solo a flag.
- **TUI di navigazione** — una vista interattiva sulle righe tracciate.
- **Body Markdown della pagina** (`--body-file` su `upsert`/`set`) — oggi si possono scrivere solo le proprietà elencate in [Uso](#uso).
- **Binari precompilati** — una pipeline GoReleaser che pubblica GitHub Releases per macOS/Linux/Windows; oggi le uniche opzioni sono `go install` o compilare da sorgente.
- **Una composite GitHub Action** che avvolge il binario, così uno step di workflow non ha bisogno di un proprio `go install`.
- **Un adapter server MCP** sopra lo stesso livello `internal/service` usato oggi dalla CLI.

## Licenza

[MIT](LICENSE)
