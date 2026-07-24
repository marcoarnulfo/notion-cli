# notion-track — Interactive init wizard (TUI) — Design doc

> Data: 2026-07-24 · Stato: approvato in brainstorming, da implementare
> Repo: `notion-cli` · Modulo: `github.com/marcoarnulfo/notion-cli` · Binario: `notion-track`
> Issue: [#3](https://github.com/marcoarnulfo/notion-cli/issues/3) · Milestone: v0.3
> Dettaglia la parte "TUI-first" del design principale `2026-07-23-notion-track-design.md`.

Documento in italiano (convenzione ereditata: i design doc restano in italiano, il resto
del repo è in inglese).

---

## 1. Obiettivo

`notion-track init` lanciato **senza flag di configurazione** e con **stdin a un terminale**
apre una procedura guidata: sceglie la data source fra quelle condivise con l'integrazione,
propone una mappatura delle proprietà, la fa confermare o correggere, e scrive il profilo.

Oggi `init` è solo flag-driven: senza `--data-source-id` esce con un errore d'uso. Chi
configura il tool per la prima volta deve lanciare `init --list`, copiare un id, e comporre
a mano cinque flag leggendo i nomi delle colonne da Notion. È il primo contatto con il tool
ed è la parte più ostile.

### Non-goal dichiarati

- **Non tocca il percorso a flag.** Con un qualsiasi flag di configurazione il comportamento
  è identico a oggi, byte per byte. La CI dipende da quel percorso.
- **Non crea né modifica nulla su Notion.** Il wizard legge (data source, schema) e scrive
  un solo file locale: il config.
- **Non gestisce più profili in un colpo solo.** Un giro del wizard scrive un profilo, quello
  indicato da `--profile` o `default`.
- **Niente creazione di proprietà mancanti.** Se la board non ha una colonna adatta a un ruolo
  obbligatorio, il wizard lo dice e si ferma; crearla è lavoro da fare in Notion.

---

## 2. Innesco

Il wizard parte se e solo se **entrambe** le condizioni valgono:

1. nessun flag di configurazione è stato passato: `--data-source-id`, `--database-id`,
   `--ticket-prop`, `--status-prop`, `--title-prop`, `--due-prop`, `--list`;
2. `isInteractive()` è vero (stdin è un terminale).

`--profile` e `--config` sono ammessi: non descrivono *cosa* configurare ma *dove* scriverlo,
e il wizard li rispetta.

| Invocazione | Comportamento |
|---|---|
| `init` a un TTY | **wizard** |
| `init --profile work` a un TTY | **wizard**, scrive nel profilo `work` |
| `init --data-source-id ds1 …` | percorso a flag, invariato |
| `init --list` | elenco, invariato |
| `init` in CI / pipe / agente | errore d'uso odierno: `--data-source-id is required` |

L'ultima riga è la garanzia che tiene: un ambiente non interattivo non può mai finire dentro
una TUI che nessuno può guidare.

---

## 3. Flusso

```
init senza flag di config, a un TTY
      │
      ├─ 1. resolveInitToken()          [ESISTENTE, fuori dalla TUI]
      │      token già presente → prosegue in silenzio
      │      token assente      → promptForToken: lettura senza eco, offerta di salvataggio
      │
      ├─ 2. client.ListDataSources()    [fuori dalla TUI]
      │      0 risultati → errore odierno "no data sources are shared with this integration"
      │
      ├─ 3. runWizard(model)            [TUI]
      │        pickSource   → lista bubbles, frecce, Enter
      │        loadSchema   → tea.Cmd asincrono, "loading…"
      │        confirm      → riepilogo della proposta di GuessMapping
      │                       Enter conferma (solo se i ruoli obbligatori sono pieni)
      │                       t/s/i/d aprono la modifica del ruolo corrispondente
      │        editRole     → lista filtrata per tipo, la proposta preselezionata
      │      → Result{Ref, Schema, Props} oppure Cancelled
      │
      └─ 4. validateMapping(...)        [ESISTENTE] → statusType
             scrittura del profilo e stessa riga di successo del percorso a flag
```

Il token si raccoglie **prima** e **fuori** da bubbletea. Motivo: `promptForToken` legge senza
eco con `term.ReadPassword` e ha già la propria gestione di Ctrl-C che ripristina il terminale
(`internal/cli/interrupt.go`); infilarlo in un `textinput` significherebbe riscrivere quella
logica dentro un contesto dove un Ctrl-C mal gestito lascia il terminale rotto. In più, chi ha
già un token non vede nessun passaggio in più.

---

## 4. Mappatura

La proposta iniziale viene da `tracker.GuessMapping(schema)`, che è già in uso e già testato.

La schermata di conferma mostra i quattro ruoli con la colonna proposta o `— not set —`:

```
  Data source: Tasks

  t  ticket    Ticket           (rich_text)
  s  status    Stato            (status)
  i  title     Name             (title)
  d  due       — not set —      (optional)

  enter  save    t/s/i/d  change    esc  cancel
```

`enter` è accettato solo se ticket, status e title sono valorizzati; con un ruolo obbligatorio
vuoto la conferma è bloccata e una riga spiega perché. Sono gli stessi tre ruoli che
`validateMapping` e `internal/service/doctor.go` considerano obbligatori: un profilo scritto
senza uno di essi è rotto al primo uso.

La modifica di un ruolo apre una lista con le **sole** colonne di tipo compatibile:

| Ruolo | Tipi ammessi |
|---|---|
| ticket | `rich_text`, `title` |
| title | `title` |
| status | `status`, `select` |
| due | `date` |

Filtrare per tipo invece di validare dopo la scelta rende impossibile per costruzione la
mappatura che `validateMapping` rifiuterebbe. Il ruolo `due` è opzionale, quindi la sua lista
contiene anche una voce esplicita `— none —`. Se per un ruolo non esiste **nessuna** colonna
compatibile, la lista lo dichiara e non c'è niente da scegliere: è il caso "la board non è
adatta", che va risolto in Notion.

`validateMapping` viene comunque rieseguito prima di scrivere. È ridondante per costruzione, e
va bene così: riusa codice testato, produce `statusType`, e se un giorno il wizard sbagliasse
sarebbe l'ultimo controllo a fermarlo invece di lasciar scrivere un profilo rotto.

`DataSourceRef` porta con sé `DatabaseID` oltre a `ID` e `Title`, quindi il wizard compila
entrambi i campi del profilo senza chiedere nulla.

---

## 5. Annullamento ed errori

- **Esc / Ctrl-C / `q`** in qualunque schermata: nessun file scritto, messaggio `init cancelled`
  su stderr, **exit 1**. Exit 1 e non 0 perché uno script che invoca `init` deve poter
  distinguere "profilo configurato" da "l'utente ha rinunciato".
- **`GetSchema` fallisce dentro la TUI**: schermata d'errore con il messaggio; `esc` torna alla
  scelta della data source, `q` esce con l'errore e il suo exit code abituale.
- **`ListDataSources` fallisce**: succede prima della TUI, quindi è un normale errore di
  comando.
- **Nessuna data source condivisa**: stesso errore che dà oggi `init --list`, con lo stesso
  suggerimento (aggiungere l'integrazione da ••• → Connections).

---

## 6. Architettura

Nuovo pacchetto **`internal/tui`**, casa di tutte le interfacce da terminale — la issue #4
(browsing TUI) ci aggiungerà il proprio Model condividendo stile e convenzioni dei tasti.

Dipendenze: `bubbletea` (runtime), `bubbles/list` (liste), `lipgloss` (stile).

Il Model **non conosce la rete**: riceve le data source già caricate e una funzione
`fetchSchema(id) (*notion.Schema, error)` iniettata. `internal/cli` fa da adattatore fra il
client HTTP e il Model. È lo stesso principio dei seam già presenti in `init.go`
(`isInteractive`, `readToken`, `readLine`).

`tea.NewProgram` sta dietro un seam `runWizard`, sostituibile nei test: un test non ha un
terminale vero e far partire il runtime reale lo bloccherebbe.

```
internal/cli/init.go ──▶ internal/tui.NewWizard(sources, fetchSchema) ──▶ tui.Result
        │                                    │
        │                                    └─▶ tracker.GuessMapping, config.Properties
        └──▶ validateMapping, config.Save
```

---

## 7. Test

**Model puro.** Si costruisce il Model, gli si spingono messaggi sintetici (`tea.KeyMsg`,
messaggi di schema caricato o fallito) e si asserisce sullo stato e su sottostringhe di
`View()`. Nessun terminale, nessuna rete, deterministico. Casi coperti:

- selezione con le frecce e Enter → si passa al caricamento dello schema;
- schema caricato → la proposta di `GuessMapping` compare nel riepilogo;
- conferma bloccata quando un ruolo obbligatorio è vuoto, e sbloccata dopo averlo mappato;
- la lista di un ruolo contiene solo le colonne del tipo giusto;
- `due` può essere azzerato con `— none —`;
- Esc → `Cancelled`, e il risultato non contiene nessun profilo;
- errore di schema → schermata d'errore, e si può tornare indietro.

**Wiring CLI.** Con `isInteractive` forzato a vero e `runWizard` sostituito da un finto, si
verifica che: l'invocazione nuda scelga il wizard, il profilo scritto corrisponda al `Result`,
l'annullamento non scriva nulla ed esca 1, e che ogni flag di configurazione continui a
prendere il percorso di oggi.

**Non verificabile automaticamente:** che la TUI sia *piacevole* da usare al terminale. Serve
una prova manuale su un workspace reale; è stato deciso di mergiare su test e CI verdi e
correggere l'ergonomia in una PR successiva.
