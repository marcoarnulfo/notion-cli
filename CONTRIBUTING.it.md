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

Per provarlo contro un workspace reale, imposta il token via variabile d'ambiente (non scriverlo mai in `config.yml` — vedi le convenzioni di `internal/config` più sotto per il perché):

```bash
export NOTION_TOKEN=ntn_...
go run ./cmd/notion-track doctor
```

In alternativa, esegui `notion-track init` in un terminale interattivo senza `NOTION_TOKEN` impostata: chiederà il token e offrirà di salvarlo in `credentials.yml`, così non serve riesportarlo a ogni `go run` durante una sessione di sviluppo.

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
internal/config      config.yml (profili, mapping proprietà), credentials.yml (token + identità per profilo) + variabili NOTION_TOKEN / NOTION_TRACK_*
```

Due regole qui **non sono negoziabili**:

> `internal/tracker` and `internal/markdown` must stay pure: no I/O, and no dependency on `internal/service` or `internal/cli`. `internal/tracker` may import `internal/notion` and `internal/config` for their data types only — domain logic goes there so it can be tested without mocks.

> Only stdlib in tests. No testify, no gomock. Fake the API with `httptest.Server`.

Ovvero: `internal/tracker` e `internal/markdown` devono restare puri — niente I/O, nessuna dipendenza da `internal/service` o `internal/cli`. `internal/tracker` può importare `internal/notion` e `internal/config`, ma solo per i loro tipi di dato. La logica di dominio vive lì proprio perché possa essere testata senza mock. E: nei test si usa solo la libreria standard — niente testify, niente gomock. L'API si simula con `httptest.Server`.

Altre convenzioni utili da conoscere:

- `internal/cli` non chiama mai `os.Exit`; `Execute()` restituisce un intero come exit code, così l'intero albero dei comandi può essere esercitato in-process nei test. Solo `cmd/notion-track/main.go` può terminare il processo.
- Gli exit code sono un contratto pubblico (vedi la tabella [Exit code](README.it.md#exit-code) del README) — mappa una nuova modalità di fallimento su un codice esistente invece di inventarne uno con leggerezza, e aggiorna la tabella se ne aggiungi uno.
- Anche ogni chiave JSON stampata con `--json` è un contratto di scripting stabile — rinominarla o rimuoverla è un breaking change, da documentare come tale.
- `internal/config`: un token letto da `NOTION_TOKEN` non deve mai essere scritto su disco — `Save`/`SaveTo` (config.yml) devono restarne silenziosi, e `SaveToken` (credentials.yml) non va mai chiamata con un token proveniente dall'ambiente. `config.yml` non deve mai contenere un token, punto — è a questo che serve `credentials.yml`, e solo un opt-in esplicito e interattivo dell'utente (il prompt di salvataggio di `init`) può scriverci.
- `internal/config`: lo stesso ragionamento vale per l'identità di `--assignee me`, che è personale e vive in `credentials.yml`. Nessuno deve riscrivere `me:` dentro `config.yml` — quel campo è legacy in sola lettura, mantenuto perché le configurazioni esistenti continuino a funzionare, e `doctor` lo segnala. Nessun comando deve inoltre riscrivere `config.yml` come effetto collaterale: è pensato per essere committato, e un diff inspiegato nel `git status` di qualcuno è il modo in cui un valore personale torna a essere condiviso.

## Linee guida per commit e PR

- Usa i **[Conventional Commits](https://www.conventionalcommits.org) con scope**, ad es. `feat(cli): add list --status filter`, `fix(tracker): guard duplicate detection`, `docs(readme): document exit codes`. Guarda `git log` per gli scope già in uso (`cli`, `service`, `notion`, `tracker`, `config`, `docs`, ...).
- **Non aggiungere mai `Co-Authored-By`** ai messaggi di commit, a prescindere dallo strumento usato per scrivere la modifica.
- Ogni cambiamento di comportamento osservabile (un flag, un exit code, una chiave JSON, la forma del config) deve aggiornare **entrambi** `README.md` e `README.it.md` nella stessa PR — non solo quello principale.
- Mantieni le PR focalizzate; compila il template della PR e collega la issue (`Closes #N`).
- Assicurati che i controlli sopra passino prima di chiedere una review.

## Pubblicare una release

Per chi mantiene il progetto. Pubblicare è un comando solo; tutto ciò che viene dopo è automatico.

```bash
git checkout main && git pull
git status --short          # deve essere vuoto

gofmt -l .                  # non deve stampare niente
go vet ./... && go build ./... && go test ./... -race
go run honnef.co/go/tools/cmd/staticcheck@latest ./...

git tag -a v0.7.1 -m "cosa cambia"
git push origin v0.7.1
```

Esegui questi controlli anche se il workflow di release ne ripete quasi tutti: `staticcheck` lo salta di proposito, perché scaricare uno strumento non fissato dentro il job che pubblica eseguibili restituirebbe esattamente la proprietà di supply chain che i suoi SHA fissati servono a proteggere.

**Quale numero.** Finché il progetto è `0.x`: la terza cifra per le correzioni, la seconda per le funzionalità. `v0.6.0` → `v0.6.1` era una correzione; `v0.6.1` → `v0.7.0` ha aggiunto lo spostamento dell'identità.

**Il tag è ciò che fa partire tutto**, ed è l'unica cosa che lo fa — un merge su `main` non pubblica niente. Il workflow riconosce solo `vX.Y.Z` e `vX.Y.Z-suffisso`, così un tag maggiore mobile come `v1` (quello attraverso cui si consuma una composite action) può essere ripuntato senza far partire una release. Un suffisso con il trattino pubblica una prerelease, che `latest` salta — utile per esercitare la pipeline senza toccare nessuno.

Spingere il tag compila i sei archivi e il `checksums.txt`, pubblica la release con le note generate dai commit dal tag precedente, e poi installa quel tag esatto con la composite action su Linux, macOS e Windows. Un fallimento in quest'ultimo job significa che gli archivi sono pubblici e inutilizzabili — il run diventa rosso.

Poi conferma la strada che gli utenti percorrono davvero:

```bash
go install github.com/marcoarnulfo/notion-cli/cmd/notion-track@latest
notion-track --version
```

Se riporta ancora la versione precedente, la cache locale dei moduli sta tenendo una lista di versioni ferma; rilancia tra poco, oppure chiedi il tag esplicitamente.

Due cose non si possono disfare: le note di una release pubblicata sono immutabili, quindi una correzione al footer in `.goreleaser.yaml` arriva solo alla release *successiva*; e un tag che qualcuno ha scaricato come modulo Go resta risolvibile tramite `proxy.golang.org` anche dopo averlo cancellato qui. Nessuna delle due è un motivo per ri-taggare — pubblica la patch successiva.

Grazie per aiutare a migliorare notion-track!
