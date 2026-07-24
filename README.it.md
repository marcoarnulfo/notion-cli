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
- **Navigazione interattiva** (`notion-track` senza argomenti, a un terminale) — una TUI sulle righe tracciate: filtro per stato, cambio di stato inline, apertura della riga in Notion, creazione senza uscire dalla vista.
- **Diagnostica** (`doctor`) — verifica il token, l'accesso alla data source, il mapping delle proprietà (compreso il drift di tipo rispetto a quando `init` è stato eseguito), scansiona l'intera data source alla ricerca di chiavi ticket duplicate e avvisa se un file tracciato da git sembra contenere il tuo token di integrazione.
- **Configurazione guidata** (`init`) — un `notion-track init` nudo in un terminale apre una procedura guidata che sceglie la data source e ti propone il mapping delle proprietà; la forma a flag scrive lo stesso profilo in modo non interattivo, validato contro lo schema live della data source prima di salvare qualsiasi cosa. `init --list` scopre gli id delle data source visibili alla tua integrazione. In un terminale interattivo offre anche di raccogliere e salvare il token di integrazione se non ne trova uno (vedi [Configurazione](#configurazione)).
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
3. **Fornisci il token a `notion-track`.** Esportalo tu stesso:
   ```bash
   export NOTION_TOKEN=ntn_...
   ```
   oppure salta questo passaggio e lascia che `init` (passaggio 5) lo chieda interattivamente — in un terminale vero lo chiede senza fare echo del token, e offre di salvarlo in `credentials.yml` così non serve riesportarlo alla sessione successiva. Vedi [Configurazione](#configurazione) per dove vive questo file e come si differenzia da `config.yml`.
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

### `notion-track` — la TUI di navigazione

```
notion-track
```

Senza argomenti e a un terminale, `notion-track` apre una vista interattiva sulle righe tracciate: una riga ciascuna, con chiave del ticket, titolo, stato e scadenza. `enter` apre il dettaglio a schermo intero; `s` sposta la riga selezionata in un altro stato, scelto fra i valori che lo schema ammette davvero; `f` restringe l'elenco a un solo stato; `n` crea una riga senza uscire dalla vista; `o` la apre in Notion, `y` ne copia l'URL, `r` ricarica, `/` filtra per testo, `q` esce.

È una vista sullo stesso layer `internal/service` che usa ogni comando — nessuna logica separata, e niente che possa fare più dei flag.

Creare una riga mentre è attivo un filtro di stato le assegna quello stato, così la nuova riga compare nella vista che stai guardando. Una scrittura fallita lascia l'elenco a schermo e riporta il motivo in una riga, invece di smontare l'interfaccia: le righe restano leggibili e restano corrette.

Senza un terminale — in pipe, con redirezione, in CI — `notion-track` senza argomenti stampa l'help ed esce, esattamente come prima.

### `init` — configura un profilo

Due forme. In un terminale, senza nient'altro sulla riga di comando:

```
notion-track init
```

apre una procedura guidata: recupera il tuo token (chiedendolo solo se non ne esiste ancora uno), elenca le data source condivise con la tua integrazione e ti fa scegliere con le frecce. Poi propone un mapping delle proprietà, dedotto dai nomi e dai tipi delle tue colonne, da confermare o correggere — ogni ruolo offre soltanto le colonne che può davvero usare, quindi un mapping che si romperebbe al primo utilizzo non è nemmeno selezionabile. `enter` salva; `esc` o `Ctrl-C` annullano, senza scrivere nulla e uscendo con codice diverso da zero, così uno script può distinguere i due casi. Anche `--profile` e `--config` funzionano qui: dicono dove finisce il profilo, non cosa contiene.

La procedura guidata richiede un terminale **e** una riga di comando altrimenti nuda. Passare un qualsiasi flag di configurazione, o eseguire il comando senza un TTY — CI, una pipe, un agente — porta alla forma esplicita qui sotto, invariata:

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

**Richiesta del token.** Se non trova nessun token né in `NOTION_TOKEN` né in `credentials.yml`, `init` si comporta diversamente a seconda di come viene eseguito:

- **In un terminale interattivo**, chiede il token (l'input non viene mostrato a schermo) e offre di salvarlo in `credentials.yml` — premere Invio a vuoto accetta il default raccomandato. Se rifiuti, stampa la riga `export NOTION_TOKEN=...` da eseguire per la sessione corrente, senza mai stampare il token stesso: un processo figlio non ha modo di modificare l'ambiente della shell del genitore, quindi questo è il massimo che può fare per aiutarti.
- **In modo non interattivo** — CI, una pipe, uno script, un agente — non chiede mai nulla. Come ogni altro comando: exit code 5 e un messaggio che punta a `NOTION_TOKEN`.

### `upsert` — crea o aggiorna una riga per chiave ticket

```
notion-track upsert --ticket <chiave> [--title <titolo>] [--status <stato>] [--due YYYY-MM-DD] [--json]
```

Il comando principale. Interroga la data source per la riga la cui proprietà ticket è uguale a `--ticket`: la aggiorna se la trova, altrimenti la crea. `0` corrispondenze → crea, `1` corrispondenza → aggiorna, `>1` corrispondenze → fallisce con exit code 4 (vedi [Limitazioni](#limitazioni)). Silenzioso in caso di successo; con `--json` stampa `{"action": "created"|"updated", "page": {...}}`.

### `set` — aggiorna solo una riga esistente

```
notion-track set (--ticket <chiave> | --page-id <id>) [--title <titolo>] [--status <stato>] [--due YYYY-MM-DD] [--json]
```

Stessi campi di `upsert`, ma fallisce con exit code 3 se la riga non esiste ancora, invece di crearla. Usalo dove una riga mancante è un sintomo da far emergere, non un dettaglio da ignorare.

`--ticket` e `--page-id` sono mutuamente esclusivi ed è obbligatorio esattamente uno dei due. `--page-id` indirizza una riga direttamente tramite il suo page id di Notion — nessuna query per chiave ticket — il che è più rapido e privo di ambiguità quando lo si ha già a disposizione (ad es. dal `page_id` restituito da una precedente chiamata `--json`, vedi [Output JSON](#output-json)). Accetta l'URL completo della pagina copiato dalla barra degli indirizzi di Notion, un id esadecimale nudo di 32 caratteri, o un UUID con trattini; qualsiasi altro input fallisce immediatamente con exit code 2, prima di qualunque chiamata di rete. Poiché leggere una pagina per id funziona per qualsiasi pagina condivisa con l'integrazione — non solo per le righe della data source configurata — un page id che risolve verso una data source *diversa* da quella del profilo attivo viene rifiutato con exit code 2 invece di fallire più avanti con un criptico errore sui nomi delle proprietà da parte di Notion. Anche `set --page-id` rifiuta, con lo stesso exit code, una pagina il cui parent non riporta alcuna data source — la sua appartenenza non può mai essere confermata, e una scrittura non deve procedere su una pagina che non può dimostrare di appartenere a questo profilo.

### `--body-file` — scrive il corpo della pagina da Markdown

```
notion-track upsert --ticket <chiave> --body-file notes.md
notion-track set --page-id <id> --body-file -
```

Disponibile sia su `upsert` sia su `set`. `--body-file` accetta il percorso di un file Markdown, oppure `-` per leggere da stdin; il suo contenuto diventa il corpo della pagina Notion della riga, convertito in blocchi nativi. Le proprietà (`--title`, `--status`, `--due`) e il corpo sono indipendenti — puoi passare entrambi, uno solo, o nessuno dei due.

**Semantica di sostituzione.** `--body-file` **possiede il corpo della pagina**: ogni esecuzione rende il corpo uguale al contenuto del file, cancellando qualunque blocco fosse già presente — incluso ciò che è stato aggiunto a mano in Notion dall'ultima esecuzione. Eseguirlo due volte sullo stesso file produce lo stesso corpo, non un duplicato. Non esiste una modalità append né un annulla, quindi tratta il file come l'unica fonte di verità per quella pagina e leggi prima la pagina (`get`) se non sei sicuro di cosa contenga. Le sotto-pagine e i database annidati sotto la pagina non vengono mai toccati — vengono saltati invece che archiviati, e un warning su stderr nomina ciascuno di quelli mantenuti.

**Markdown supportato.** Titoli (`#`/`##`/`###`, i livelli più profondi si appiattiscono su h3), paragrafi, liste puntate e numerate, checkbox di attività (`- [ ]` / `- [x]`), blocchi di codice con o senza fence, citazioni, divisori `---`, e formattazione inline **grassetto**, *corsivo*, `codice`, ~~barrato~~ e link. L'annidamento di liste e citazioni è supportato fino a 2 livelli. Tabelle, immagini, HTML grezzo e annidamento oltre i 2 livelli non vengono scartati — ciascuno **degrada** verso il blocco supportato più vicino (una tabella diventa un blocco di codice in testo semplice, un'immagine diventa un link, l'annidamento più profondo viene promosso di un livello) e stampa un warning su stderr che indica cosa è successo, così niente sparisce silenziosamente ma niente blocca la scrittura. Un file oltre 1 MiB viene rifiutato prima di qualunque chiamata di rete (exit code 2).

**Costo.** L'API di Notion non offre un endpoint di cancellazione massiva, quindi sostituire un corpo costa `O(n)` nel numero di blocchi già presenti sulla pagina: si aggiunge il nuovo contenuto, poi si cancellano i vecchi blocchi uno alla volta. Una pagina con molto contenuto esistente richiede proporzionalmente più tempo, e `notion-track` stampa righe di avanzamento su stderr (blocchi aggiunti, blocchi cancellati finora) così un'esecuzione lunga non sembra bloccata.

**Concorrenza.** Due esecuzioni di `--body-file` in corsa sulla stessa pagina possono entrambe aggiungere contenuto prima che una delle due cancelli il vecchio, duplicando il corpo — non c'è alcun lock da acquisire su una pagina Notion. Non eseguire scritture concorrenti del corpo sulla stessa pagina.

Con `--json`, una scrittura riuscita aggiunge un oggetto `body`: `{"blocks_written": N, "blocks_deleted": N}`. Se la scrittura delle proprietà riesce ma la sostituzione del corpo fallisce a metà, il comando esce comunque con codice 1 (non 0), e `--json` stampa `body: {"written": false, "error": "...", "blocks_written": N, "blocks_deleted": N}` — `written` indica che il corpo *non* è nello stato descritto dal file, mentre `page` nello stesso output riflette comunque le proprietà effettivamente applicate, perché sono due chiamate API Notion separate e la prima può riuscire anche se la seconda fallisce.

### `get` — legge una riga

```
notion-track get (--ticket <chiave> | --page-id <id>) [--json]
```

Stampa ticket, titolo, stato e URL della riga. `--ticket` e `--page-id` sono mutuamente esclusivi ed è obbligatorio esattamente uno dei due — vedi `set` sopra per cosa accetta `--page-id` e come viene validato. Fallisce con exit code 3 se non trovata (il 404 di Notion non distingue "pagina inesistente" da "mai condivisa con questa integrazione" — il messaggio d'errore lo dice esplicitamente), 4 se una chiave ticket corrisponde a più di una riga (vedi [Limitazioni](#limitazioni)), o 2 per un page id malformato o esterno alla data source del profilo attivo. A differenza di `set`, `get --page-id` accetta una pagina il cui parent non riporta alcuna data source — una lettura non può fare danni con una pagina non confermata, a differenza di una scrittura.

### `list` — legge più righe

```
notion-track list [--status <stato>] [--json]
```

Elenca tutte le righe, oppure solo quelle che corrispondono a `--status`. Un valore di stato sconosciuto fallisce subito con exit code 2, indicando i valori realmente ammessi da Notion per quella proprietà.

Quando non corrisponde nulla, la forma leggibile stampa `no matching tasks` **su stderr** ed esce con 0 — stdout resta vuoto, così `list | wc -l` conta righe e nient'altro. `list --json` stampa `[]` e non dice nulla su stderr.

### `doctor` — verifica la configurazione

```
notion-track doctor [--json]
```

Esegue cinque controlli — `token`, `data_source`, `properties`, `duplicates`, `secrets` — e stampa ciascuno come `ok`, `warn` o `fail` con un messaggio di dettaglio azionabile. Un `warn` (ad es. il tipo della proprietà stato è cambiato da quando `init` è stato eseguito) non fa fallire il comando; qualsiasi `fail` lo fa uscire con codice diverso da zero. `properties` e `duplicates` vengono eseguiti anche quando `data_source` fallisce, così una configurazione rotta viene diagnosticata in un solo passaggio invece che un sintomo alla volta.

`secrets` è l'unico controllo che guarda la tua macchina invece che Notion: scansiona i file **tracciati** dal repository git corrente cercando qualcosa che abbia la forma di un token di integrazione, e avvisa indicando file e numero di riga — mai il testo trovato, che equivarrebbe a far trapelare il segreto una seconda volta, in scrollback e log di CI. I file non tracciati vengono lasciati stare: un token in un `.env` ignorato non è l'errore che questo controllo cerca. Fuori da un repository, o senza git installato, riporta `ok` con la motivazione invece di un avviso su cui nessuno può agire.

## Configurazione

`notion-track` tiene due file affiancati in `os.UserConfigDir()/notion-track/` — rispettando `$XDG_CONFIG_HOME` su Linux:

| File | Contiene | Sicuro da committare? |
|---|---|---|
| `config.yml` | profili: id della data source, mapping delle proprietà | Sì — nessun segreto |
| `credentials.yml` | il token di integrazione | **No — non committarlo mai** |

Sono due file separati, non uno solo, esattamente per questa ragione: `config.yml` è pensato per essere committato in un repository di progetto così che la CI e ogni collega condividano lo stesso mapping delle proprietà (vedi [Uso in CI](#uso-in-ci)); `credentials.yml` contiene l'unica cosa che non deve mai finire in quel repository. Separarli rende "il token non può trapelare attraverso il config committato" una proprietà della struttura dei file, non una regola che qualcuno deve ricordarsi mentre modifica lo YAML.

| Sistema operativo | Directory predefinita |
|---|---|
| macOS | `~/Library/Application Support/notion-track/` |
| Linux | `~/.config/notion-track/` |
| Windows | `%AppData%\notion-track\` |

Passa `--config /percorso/al/file.yml` per puntare `config.yml` a un file diverso — è così che si usa un file di configurazione **committato in un repository di progetto** invece del default per-utente (vedi [Uso in CI](#uso-in-ci)). Non esiste un flag equivalente per `credentials.yml`: resta deliberatamente sempre il percorso predefinito per-utente e per-macchina, mai qualcosa a cui punta un repository di progetto.

```yaml
# config.yml
schema_version: 1        # scritto da `init`; non modificarlo a mano
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

```yaml
# credentials.yml — non committare mai questo file
schema_version: 1
token: ntn_...
```

Entrambi i file sono sostituiti in modo atomico (un file temporaneo nella stessa directory, poi un rename), ma solo `credentials.yml` è garantito a `0600`: il suo file temporaneo ha un suffisso casuale e i permessi vengono impostati esplicitamente, immune a qualunque cosa sia già presente a un percorso temporaneo prevedibile. Il file temporaneo di `config.yml` ha invece un nome fisso e i suoi permessi non vengono forzati su un file già esistente in quel punto, quindi un `config.yml.tmp` residuo di un'esecuzione precedente può lasciarlo con qualunque permesso avesse quel residuo (es. `0644`) — accettabile solo perché, a differenza di `credentials.yml`, non contiene segreti propri.

`credentials.yml` viene scritto in un solo punto: `init`, quando gira in un terminale interattivo, non trova nessun token né in `NOTION_TOKEN` né nel file già esistente, e accetti il prompt "salvarlo?" (il default — vedi [Avvio rapido](#avvio-rapido)). Nulla scrive mai un token in `config.yml`.

### Variabili d'ambiente

| Variabile | Effetto |
|---|---|
| `NOTION_TOKEN` | il token di integrazione. Vince sempre su `credentials.yml` quando è impostata — è ciò che permette alla CI di passare un token che non tocca mai il disco. |
| `NOTION_TRACK_PROFILE` | quale profilo risolvere, a meno che `--profile` non sia anch'esso specificato |
| `NOTION_TRACK_DB` | sovrascrive il `database_id` del profilo risolto |
| `NOTION_TRACK_DATA_SOURCE` | sovrascrive il `data_source_id` del profilo risolto |

Precedenza:

- **Selezione del profilo:** flag `--profile` → `NOTION_TRACK_PROFILE` → `default_profile` nel file di configurazione.
- **`database_id` / `data_source_id`:** le variabili d'ambiente sopra sovrascrivono sempre ciò che il profilo risolto ha su file, indipendentemente da come quel profilo è stato scelto — è ciò che permette a un job CI di puntare un profilo esistente verso un'altra data source senza toccare il file committato.
- **Token:** `NOTION_TOKEN` → `credentials.yml`. Un token letto dall'ambiente non viene mai riscritto in `credentials.yml` — un secret della CI non può mai trapelare su disco durante un'esecuzione normale. Esegui `notion-track doctor` se hai bisogno di vedere quale fonte ha effettivamente vinto.
- **Percorso del file di configurazione:** flag `--config` → il percorso predefinito del sistema operativo sopra. Non esiste una variabile d'ambiente per il percorso stesso, né un flag equivalente per `credentials.yml`.

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

`doctor --json` — un array di check, uno per `token` / `data_source` / `properties` / `duplicates` / `secrets`:

```json
[
  { "name": "token", "status": "ok", "detail": "token from environment\n  authenticated as notion-track" },
  { "name": "data_source", "status": "ok", "detail": "reachable: Tasks" },
  { "name": "properties", "status": "ok", "detail": "all mapped properties exist with the expected types" },
  { "name": "duplicates", "status": "ok", "detail": "42 rows, no repeated ticket keys" },
  { "name": "secrets", "status": "ok", "detail": "37 tracked files scanned, no token-looking strings" }
]
```

`status` vale `"ok"`, `"warn"` o `"fail"`; `detail` è omesso solo quando vuoto, cosa che in pratica non accade.

## Exit code

Le pipeline possono ramificare su questi valori senza fare parsing di alcun messaggio:

| Codice | Nome | Significato |
|---|---|---|
| `0` | OK | successo |
| `1` | Error | un errore generico — un errore di rete/API, oppure `doctor` che segnala un check fallito diverso da `token` |
| `2` | Usage | l'invocazione non può funzionare così com'è: un flag mancante o non valido, `--ticket` e `--page-id` passati insieme o nessuno dei due, un comando sconosciuto, nessuna configurazione ancora presente (`notion-track init` non è mai stato eseguito), un valore di stato che la data source non ammette, un `--page-id` malformato, o un `--page-id` che risolve fuori dalla data source del profilo attivo |
| `3` | Not found | il ticket richiesto non ha una riga corrispondente, oppure il page id non corrisponde a nessuna pagina (o a una non condivisa con questa integrazione) (`get`, `set`) |
| `4` | Duplicate | la chiave del ticket corrisponde a più di una riga (`upsert`, `set`, `get`) |
| `5` | Auth | nessun token trovato (incluso il caso in cui `credentials.yml` esiste ma non è leggibile), oppure Notion lo ha rifiutato (401/403) — incluso `doctor`, quando il suo check `token` è l'unico fallito |

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
5. **`--body-file` sostituisce l'intero corpo della pagina, senza lock e senza annulla.** Possiede il corpo: ogni esecuzione sovrascrive tutto ciò che c'è, incluso il contenuto modificato a mano, e due esecuzioni in corsa sulla stessa pagina possono duplicarlo. Vedi `--body-file` sotto [Uso](#uso) sopra.

## Usarlo da un agente AI

Poiché il tool è muto in caso di successo, parla `--json` con uno schema stabile e restituisce [exit code differenziati](#exit-code), un agente può guidarlo con la stessa affidabilità di uno script — senza interpretare l'output umano. Nel repo, in **[`skills/notion-track/`](skills/notion-track/)**, c'è una skill pronta per [Claude Code](https://claude.com/claude-code): insegna all'agente quale comando usare e come restare al sicuro (leggere prima di scrivere, non inventare stati, ramificare sugli exit code). Si installa copiando il suo `SKILL.md` in `~/.claude/skills/notion-track/`, poi basta chiedere all'agente di "segnare quel task come fatto su Notion". Un server `notion-track mcp` è nella [roadmap](#roadmap) per gli host che parlano MCP invece della shell.

## Contribuire

I contributi sono benvenuti — questo è un progetto libero e open-source. Vedi **[CONTRIBUTING.it.md](CONTRIBUTING.it.md)** per la configurazione dell'ambiente di sviluppo, i controlli da eseguire prima di aprire una PR, e le regole architetturali non negoziabili del progetto. Leggi anche il [Codice di condotta](CODE_OF_CONDUCT.md). Hai trovato un problema di sicurezza? Vedi [SECURITY.md](SECURITY.md) invece di aprire una issue pubblica.

## Roadmap

Implementato oggi: `init` (procedura guidata interattiva e forma a flag, con `--list`), la TUI di navigazione, `upsert`, `set`, `get`, `list`, `doctor`; `--body-file` su `upsert`/`set` per scrivere il corpo della pagina da Markdown; `--json` su ogni comando che produce output; profili; retry con backoff.

Non ancora costruito:

- **Binari precompilati** — una pipeline GoReleaser che pubblica GitHub Releases per macOS/Linux/Windows; oggi le uniche opzioni sono `go install` o compilare da sorgente.
- **Una composite GitHub Action** che avvolge il binario, così uno step di workflow non ha bisogno di un proprio `go install`.
- **Un adapter server MCP** sopra lo stesso livello `internal/service` usato oggi dalla CLI.

## Licenza

[MIT](LICENSE)
