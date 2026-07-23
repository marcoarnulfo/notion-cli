[English](CONTRIBUTING.md) · **Italiano**

# Contribuire a notion-track

Grazie per il tuo interesse — contributi di ogni dimensione sono benvenuti! Segnalazioni di bug, documentazione e codice sono tutti apprezzati. Questo progetto è libero e open-source (MIT).

Partecipando accetti il nostro [Codice di condotta](CODE_OF_CONDUCT.md).

## Modi per contribuire

- 🐛 **Segnala un bug** o 💡 **proponi una funzionalità** tramite le [Issues](https://github.com/marcoarnulfo/notion-cli/issues) (template forniti).
- 🧑‍💻 **Invia una PR** — per qualsiasi cosa non banale, **apri prima una issue** così possiamo allinearci sull'approccio prima che tu ci investa tempo.
- 📖 Migliora la documentazione — il README è bilingue: inglese `README.md` + `README.it.md`.

Hai trovato un problema di sicurezza? Vedi [SECURITY.md](SECURITY.md) — non aprire una issue pubblica per quelli.

## Prerequisiti

- **[Go](https://go.dev/dl/) 1.26+** (`go version` per verificare).
- [`staticcheck`](https://staticcheck.dev) per il linting (opzionale in locale, eseguito in CI):
  `go install honnef.co/go/tools/cmd/staticcheck@latest`.
- Un token di integrazione interna Notion se vuoi provare lo strumento su dati reali — vedi il [README](README.it.md#avvio-rapido) principale per come crearne uno. Non serve per compilare, vettare o eseguire la suite di test: ogni test simula l'API Notion con `httptest.Server`.

## Configurazione dell'ambiente di sviluppo

```bash
git clone https://github.com/marcoarnulfo/notion-cli.git
cd notion-cli
go build ./...
go run ./cmd/notion-track --help
```

Per provarlo contro un workspace reale, imposta il token via variabile d'ambiente (non scriverlo mai nel file di configurazione):

```bash
export NOTION_TOKEN=ntn_...
go run ./cmd/notion-track doctor
```

## Prima di aprire una PR

Esegui gli stessi controlli della CI — devono essere tutti puliti/verdi:

```bash
gofmt -l .                                          # nessun output = formattato
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go test ./... -race
go build ./...
```

## Struttura del progetto e convenzioni

```
cmd/notion-track     entry point (binario: notion-track); l'unico punto autorizzato a chiamare os.Exit
internal/cli         albero dei comandi cobra, parsing dei flag, rendering JSON, mapping degli exit code
internal/service     orchestra client + config + dominio per un profilo (Upsert/Set/Get/List/Doctor)
internal/notion      client per l'API Notion (solo net/http — nessun SDK), retry/backoff, errori tipizzati
internal/tracker     dominio PURO: decisioni crea-o-aggiorna, costruzione payload, validazione stato
internal/config      config YAML (profili, mapping proprietà) + variabili NOTION_TOKEN / NOTION_TRACK_*
```

Due regole qui **non sono negoziabili**:

> `internal/tracker` and `internal/markdown` must stay pure: no I/O, no imports of `internal/notion` or `internal/config`. Domain logic goes there so it can be tested without mocks.

> Only stdlib in tests. No testify, no gomock. Fake the API with `httptest.Server`.

Ovvero: `internal/tracker` e `internal/markdown` devono restare puri — niente I/O, nessun import di `internal/notion` o `internal/config`. La logica di dominio vive lì proprio perché possa essere testata senza mock. E: nei test si usa solo la libreria standard — niente testify, niente gomock. L'API si simula con `httptest.Server`.

Altre convenzioni utili da conoscere:

- `internal/cli` non chiama mai `os.Exit`; `Execute()` restituisce un intero come exit code, così l'intero albero dei comandi può essere esercitato in-process nei test. Solo `cmd/notion-track/main.go` può terminare il processo.
- Gli exit code sono un contratto pubblico (vedi la tabella [Exit code](README.it.md#exit-code) del README) — mappa una nuova modalità di fallimento su un codice esistente invece di inventarne uno con leggerezza, e aggiorna la tabella se ne aggiungi uno.
- Anche ogni chiave JSON stampata con `--json` è un contratto di scripting stabile — rinominarla o rimuoverla è un breaking change, da documentare come tale.
- `internal/config`: un token letto da `NOTION_TOKEN` non deve mai essere riscritto nel file di configurazione — `Save`/`SaveTo` devono restarne silenziosi. Non aggiungere un percorso di codice che persiste un token.

## Linee guida per commit e PR

- Usa i **[Conventional Commits](https://www.conventionalcommits.org) con scope**, ad es. `feat(cli): add list --status filter`, `fix(tracker): guard duplicate detection`, `docs(readme): document exit codes`. Guarda `git log` per gli scope già in uso (`cli`, `service`, `notion`, `tracker`, `config`, `docs`, ...).
- **Non aggiungere mai `Co-Authored-By`** ai messaggi di commit, a prescindere dallo strumento usato per scrivere la modifica.
- Ogni cambiamento di comportamento osservabile (un flag, un exit code, una chiave JSON, la forma del config) deve aggiornare **entrambi** `README.md` e `README.it.md` nella stessa PR — non solo quello principale.
- Mantieni le PR focalizzate; compila il template della PR e collega la issue (`Closes #N`).
- Assicurati che i controlli sopra passino prima di chiedere una review.

Grazie per aiutare a migliorare notion-track!
