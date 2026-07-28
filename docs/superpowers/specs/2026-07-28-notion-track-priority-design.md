# notion-track — Ruolo "priority" (urgenza del task) — Design doc

> Data: 2026-07-28 · Stato: approvato in brainstorming, da implementare
> Repo: `notion-cli` · Modulo: `github.com/marcoarnulfo/notion-cli` · Binario: `notion-track`
> Gemello di `2026-07-27-notion-track-assignee-design.md`, che resta il documento di
> riferimento per tutto ciò che i due ruoli condividono.

Documento in italiano (convenzione ereditata: i design doc restano in italiano, il resto
del repo è in inglese).

---

## 1. Obiettivo

La board porta una seconda colonna `select` che notion-track ignora:

```
Urgenza    select -> ALTA, MEDIA, NORMALE
```

Oggi non c'è modo di chiedere dal terminale la domanda che un elenco di task esiste per
farsi porre: *cosa devo fare per primo*. Questo documento aggiunge il sesto ruolo,
**`priority`**, mappato su quella colonna.

È il ruolo gemello di `assignee` (§2): stessa meccanica, stesso tipo di colonna, stessa
risoluzione dei valori parziali. Ciò che segue dice **solo** dove i due divergono; per
tutto il resto vale il design dell'assignee, e ogni divergenza silenziosa fra i due sarebbe
un difetto.

### Non-goal dichiarati

- **Niente svuotamento.** Nessun `--unpriority`, nessun `list --unprioritized`. Le due
  colonne si usano in modo opposto (§2) e togliere un'urgenza non è un'operazione che
  qualcuno ha chiesto.
- **Niente ordinamento.** `list --sort priority` è il caso d'uso naturale di una priorità,
  ma non fa parte del pattern del ruolo: richiede i `sorts` nella query Notion, che la CLI
  non supporta, più una decisione su quale ordine seguire (quello delle opzioni della board
  o l'alfabeto). Feature separata, se e quando servirà.
- **Niente identità.** `--assignee me` esiste perché una persona può essere "io". Una
  priorità no.
- **Niente generalizzazione dei ruoli select.** Vedi §6.

---

## 2. Come le due colonne differiscono

Rilevato sulla board reale il 2026-07-28, su 62 righe:

| Colonna | Valorizzata | Vuota | Distribuzione |
|---|---|---|---|
| `Referente` | 58 | 4 | Marco 27, Mirko 24, Andrea 7 |
| `Urgenza` | 25 | **37** | ALTA 11, MEDIA 10, NORMALE 4 |

Il referente è la norma e la sua assenza un'anomalia; l'urgenza è l'eccezione che marca ciò
che scotta. Da qui i due non-goal sullo svuotamento: `--unassign` serviva a rimettere una
riga nello stato anomalo, mentre l'assenza di urgenza è già lo stato dei due terzi della
board, e `list --unprioritized` elencherebbe la maggioranza dei task senza dire nulla.

---

## 3. Cosa cambia, in concreto

```bash
notion-track set    --ticket BDF-1 --priority ALTA
notion-track set    --ticket BDF-1 --priority alta        # match parziale, §5 del gemello
notion-track upsert --ticket BDF-2 --title "Nuovo" --priority MEDIA
notion-track list   --priority ALTA --status "Da fare"
notion-track list   --priority ALTA --assignee me         # compound di tre clausole
```

| Punto | Trattamento |
|---|---|
| `config.Properties.Priority` | nuovo campo, `yaml:"priority,omitempty"`, nessun bump di `schema_version` |
| `tracker.Fields.Priority` | in **coda** allo struct, dopo `Unassign` |
| `BuildProperties` | una riga: `add("priority", props.Priority, f.Priority)`, che raggiunge il ramo `select` esistente e la sua validazione contro le opzioni |
| `service.ListFilter.Priority` | terza clausola, composta dall'`AndFilter` già in uso |
| `planFor` (`--dry-run`) | una riga in più fra le `PlannedProperty`. **Non dimenticarla**: sul ruolo gemello questa omissione fu l'unico BLOCKER della review del piano — un `--priority X --dry-run` che stampa un piano vuoto è peggio di nessun dry-run, perché sembra dire che non cambierebbe nulla |
| `--priority` | registrato in `bindShared`, quindi `upsert` e `set` lo ereditano e **`set.go` non si tocca** |
| `list --priority` | filtro, valore risolto con la stessa `ResolveOption` della scrittura |
| `pageJSON.Priority` / `mcp.Row.Priority` | in coda a **entrambi**, insieme (§4) |
| `mcp.Fields.Priority` | in coda, allineato a `tracker.Fields` (§4) |
| `init --priority-prop` | opzionale, accetta solo colonne `select` |
| `doctor` | ruolo opzionale in `checkProperties`; **nessun** check dedicato: non c'è identità da risolvere |
| wizard | una riga in `roles` (tasto `p`, libero) più i due `case` in `roleValue`/`setRole` |
| manifest | campo `priority`, in `Entry` e in `fieldNames`, valido in CSV e JSON |
| MCP | `priority` in `upsertArgs` e `listArgs`, `Priority` in `Row` e `Fields` |

Errori ed exit code sono quelli del gemello, senza eccezioni nuove: valore che la colonna
non offre e valore ambiguo escono 2; ruolo non mappato esce 1.

---

## 4. I due punti dove sbagliare è facile

### 4.1 Le conversioni dirette vanno estese in coppia

`internal/cli/mcp.go` converte `pageJSON` in `mcp.Row`, `mcp.Fields` in `tracker.Fields` e
`mcp.ListFilter` in `service.ListFilter` con conversioni dirette di tipo, deliberatamente:
compilano solo finché le due facce restano identiche per nomi, tipi e **ordine**, ed è
questo che impedisce al contratto `--json` documentato e a ciò che vede un agente di
divergere in silenzio.

Conseguenza pratica: `Priority` va aggiunto **in coda** e **nello stesso commit** su
entrambe le facce di ogni coppia. Il test `TestTheMCPConversionsStayDirect` esiste per
fallire in compilazione se una delle due viene dimenticata.

### 4.2 `GuessMapping` ora ha due ruoli che pescano dagli stessi candidati

La guardia introdotta con l'assignee esclude dai candidati la colonna già scelta per
`status`. Con un sesto ruolo sullo stesso tipo, l'esclusione deve coprire **anche** la
colonna scelta per `assignee`: su questa board `Referente` e `Urgenza` sono entrambe
`select`, ed è esattamente la configurazione in cui una guardia incompleta assegnerebbe la
stessa colonna a due ruoli.

Nomi riconosciuti: `urgenza`, `priority`, `priorità`, `priorita`, `importanza`, `severity`.
Come per l'assignee, **solo per nome**: nessun fallback "unico candidato", perché per un
ruolo opzionale indovinare male è peggio che non indovinare.

---

## 5. Output umano

Additivo in coda, come l'assignee, così le colonne esistenti non si spostano di un
carattere e chi non mappa il ruolo non vede alcuna differenza:

```
$ notion-track list --status "Da fare"
BDF-1                Hardening delle rotte                    [Da fare]  !ALTA  @Mirko Spinato
BDF-2                Cleanup S3                               [Da fare]         @Marco Arnulfo
BDF-3                Sistemare la vista mobile                [Da fare]
```

Il `!` per l'urgenza accanto alla `@` per la persona: due sigilli diversi per due cose
diverse, leggibili senza intestazione. La priorità precede l'assegnatario perché è ciò per
cui si scorre l'elenco. Entrambi i segmenti spariscono quando il valore è vuoto o il ruolo
non è mappato.

L'helper `assigneeSuffix` (`internal/cli/output.go`) ha già la forma giusta per essere
affiancato da un gemello: la ragione per cui esiste — impedire che il separatore a due
spazi divergesse fra `get` e `list` — vale identica qui.

---

## 6. Perché duplicare e non generalizzare

Con due ruoli `select` a meccanica identica, la tentazione è astrarre: una tabella di ruoli,
e `assignee`/`priority` come due righe di configurazione. Il settimo ruolo costerebbe quasi
nulla.

Non ora, per tre ragioni:

1. **Due istanze non sono un pattern.** Alla terza si vede quale forma l'astrazione debba
   avere; a due si indovina, e un'astrazione indovinata costa più del codice che sostituisce.
2. **Il contratto JSON è tipizzato.** I campi espliciti di `pageJSON` sono ciò che rende
   possibili le conversioni dirette di §4.1. Una mappa di valori li sostituirebbe con un
   contratto che nessun compilatore può più controllare.
3. **Il codice è appena stato stabilizzato.** Il ruolo assignee è passato per diciotto
   review e una review d'insieme; rifattorizzarlo il giorno dopo scambia rischio certo per
   un risparmio ipotetico su un ruolo che nessuno ha ancora chiesto.

La rivalutazione è dichiarata, non rimandata a caso: **al terzo ruolo `select`**.

---

## 7. Test

TDD, gli stessi seam del gemello: funzioni pure in `internal/tracker`, `httptest` per il
client e il service, `executeArgs` in-process per la CLI.

Oltre ai casi speculari a quelli dell'assignee, tre che sono propri di questo lavoro:

- `GuessMapping` con una board che ha `Referente` **e** `Urgenza`: entrambi i ruoli mappati,
  nessuna colonna in due ruoli (§4.2);
- il compound di **tre** clausole (status + assignee + priority), asserendo quali sono e non
  quante;
- l'output umano con priorità e assegnatario insieme, con solo uno dei due, e con nessuno
  dei due — quest'ultimo byte-per-byte identico a oggi.

**Gate CI**, tutti e cinque: `gofmt -l`, `staticcheck`, `go vet`, `go test`, build.

---

## 8. File toccati

`internal/config/config.go`, `internal/tracker/payload.go`, `internal/tracker/mapping.go`,
`internal/service/service.go`, `internal/service/plan.go`, `internal/service/doctor.go`,
`internal/cli/upsert.go`, `internal/cli/list.go`, `internal/cli/get.go`,
`internal/cli/output.go`, `internal/cli/init.go`, `internal/cli/apply.go`,
`internal/cli/mcp.go`, `internal/manifest/manifest.go`, `internal/mcp/server.go`,
`internal/tui/wizard.go`, più i rispettivi `_test.go`, `README.md`, `README.it.md` e
`skills/notion-track/SKILL.md`.

Nessun file nuovo: il ruolo non porta concetti che non esistano già.
