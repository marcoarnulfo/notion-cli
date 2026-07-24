# notion-track — Browsing TUI — Design doc

> Data: 2026-07-24 · Stato: approvato in brainstorming, da implementare
> Repo: `notion-cli` · Modulo: `github.com/marcoarnulfo/notion-cli` · Binario: `notion-track`
> Issue: [#4](https://github.com/marcoarnulfo/notion-cli/issues/4) · Milestone: v0.3
> Condivide il pacchetto `internal/tui` e le convenzioni con
> `2026-07-24-notion-track-init-wizard-design.md`.

Documento in italiano (convenzione ereditata: i design doc restano in italiano, il resto
del repo è in inglese).

---

## 1. Obiettivo

`notion-track` senza argomenti, a un terminale, apre una vista interattiva sulle righe
tracciate: elenco, filtro per stato, cambio di stato inline. Nessuna logica di business
nuova — è una vista sullo stesso `internal/service` che usa ogni comando.

### Non-goal dichiarati

- **Niente logica che i flag non abbiano già.** Se la TUI potesse fare qualcosa che la CLI
  non fa, quel qualcosa andrebbe prima nel service e poi esposto a entrambe.
- **Niente modifica del corpo della pagina.** `--body-file` resta il modo di scrivere un
  corpo; un editor Markdown dentro la TUI è un'altra feature.
- **Niente modifica di titolo o scadenza.** La v1 cambia lo stato, che è l'operazione
  quotidiana; il resto si fa con `set`.

---

## 2. Innesco

| Invocazione | Comportamento |
|---|---|
| `notion-track` a un TTY | **TUI di navigazione** |
| `notion-track` in pipe / CI | help ed exit 0, come oggi |
| `notion-track <qualsiasi cosa>` | errore d'uso `unknown command`, come oggi |

Una TUI a schermo pieno riversata dentro una pipe è spazzatura per chi legge dall'altra
parte: il controllo del TTY è ciò che lo impedisce.

---

## 3. Schermate e tasti

```
 notion-track — lista
┌────────────────────────────────────────────────────────────┐
│ ▸ BDF-1  Hardening              In corso     2026-07-30    │
│   BDF-2  Grafici                Da fare                    │
└────────────────────────────────────────────────────────────┘
 enter dettaglio · s stato · f filtro · n nuovo · o apri · y copia · r ricarica · q esci
```

Una riga per task — non due come farebbe il delegate di default di bubbles, che su una board
piena spenderebbe metà schermo in spaziatura. Il dettaglio è **a schermo intero su `enter`**,
non un pannello affiancato: sotto le ~100 colonne uno split diventa illeggibile e andrebbe
gestito il degrado.

| Tasto | Azione |
|---|---|
| `↑`/`↓` | naviga |
| `enter` | dettaglio (e `esc` per tornare) |
| `s` | cambia lo stato della riga selezionata |
| `f` | filtra per stato (con voce `— all —` per togliere il filtro) |
| `n` | crea una riga |
| `o` | apre la riga in Notion |
| `y` | copia l'URL negli appunti |
| `r` | ricarica |
| `/` | filtro testuale (bubbles) |
| `q`, `esc`, `Ctrl-C` | esce |

Il picker di stato offre **solo** i valori che lo schema ammette: è ciò che impedisce alla
TUI di scrivere uno stato che la board rifiuterebbe, la stessa garanzia che dà
`list --status`.

La creazione ha due campi (chiave ticket e titolo). La chiave è obbligatoria: è l'identità
della riga, e `upsert` si basa su di essa. Se è attivo un filtro di stato, la nuova riga nasce
con quello stato — così compare nella vista che si sta guardando.

---

## 4. Errori

Ogni scrittura riporta in una riga sotto l'elenco, successo o fallimento che sia. Un errore
**non smonta l'interfaccia**: l'elenco resta leggibile e resta corretto.

- **Primo caricamento fallito** → schermata d'errore: non c'è nulla a cui tornare.
- **Ricarica fallita** → le righe già a schermo restano, con una nota. Dati vecchi che si
  possono ancora leggere battono uno schermo vuoto.
- **Appunti irraggiungibili** (niente `xclip` su una macchina headless) → nota, non errore:
  è un posto normale in cui eseguire questo strumento, non uno rotto.

---

## 5. Architettura

`BrowseModel` in `internal/tui`, accanto al wizard. Il Model non conosce Notion: consuma
l'interfaccia **`Board`**, dichiarata in `internal/tui` dove viene usata.

```go
type Board interface {
	List(status string) ([]Row, error)
	SetStatus(pageID, status string) error
	Create(ticket, title, status string) error
	Statuses() ([]string, error)
}
```

`Row` è la riga appiattita (page id, ticket, titolo, stato, scadenza, URL): risolvere "quale
colonna è lo stato" è lavoro dell'adapter, fatto una volta sola, così nessuna schermata deve
conoscere `config.Properties`.

`boardAdapter` in `internal/cli` implementa `Board` sopra `*service.Service`. `SetStatus`
scrive per **page id**, non per chiave ticket: la riga è già in mano, e una chiave non è
garantita univoca — ripassare da una lookup potrebbe fallire su un duplicato che l'utente sta
guardando a schermo.

Appunti (`atotto/clipboard`, già nel grafo delle dipendenze via `bubbles`) e apertura nel
browser sono iniettati nel Model: un test asserisce cosa *sarebbe* stato copiato o aperto, e
nessun test tocca gli appunti veri o lancia un browser. L'apertura verifica lo schema
dell'URL prima di passarlo a `open`, che su macOS lancerebbe volentieri anche un file locale
o un'applicazione.

`tea.NewProgram` sta dietro il seam `runBrowser`, come per il wizard, e usa lo schermo
alternativo: la TUI occupa tutto il terminale e alla chiusura lo scrollback deve tornare
intatto.

---

## 6. Test

Model puro pilotato da messaggi sintetici, come per il wizard, con una `Board` finta che
registra le chiamate. Coperti: caricamento iniziale, fallimento terminale del primo
caricamento, ricarica fallita che conserva le righe, dettaglio e ritorno, cambio di stato
(con la ricarica che ne consegue), valori del picker presi dallo schema, filtro e sua
rimozione, creazione con e senza filtro attivo, chiave ticket obbligatoria, apertura, copia,
appunti irraggiungibili, board vuota, riga con ticket uguale al titolo, uscita.

L'adapter ha test propri contro un finto server HTTP: è il punto dove sei campi vengono
copiati a mano da una forma con colonne a una con ruoli, ed è esattamente il tipo di errore
che nient'altro coglierebbe.

**Non verificabile automaticamente:** l'ergonomia reale al terminale. Serve una prova manuale;
come concordato si merge su test e CI verdi e si corregge dopo l'uso sul campo.
