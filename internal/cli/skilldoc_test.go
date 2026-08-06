package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/marcoarnulfo/notion-cli/internal/manifest"
	"github.com/spf13/pflag"
)

// The agent skill in skills/notion-track/SKILL.md is documentation an LLM
// executes rather than reads. A claim it makes about a flag, a command, an
// exit code or a JSON field is not a description that can quietly go stale:
// it is an instruction some agent will follow verbatim against a real board,
// where there is no undo. These tests pin the skill to the code so that
// changing one without the other fails CI instead of reaching an agent.
//
// They deliberately check only what a machine can check. Prose ("read before
// you write") is still on the author.

func skillText(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "skills", "notion-track", "SKILL.md")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the skill: %v", err)
	}
	return string(b)
}

// TestEveryFlagTheSkillMentionsExists catches the skill inventing a flag, the
// failure mode with the sharpest edge: an agent runs a command that dies on a
// usage error, and its recovery is to guess again.
func TestEveryFlagTheSkillMentionsExists(t *testing.T) {
	real := realFlagNames(t)
	for _, name := range flagsMentionedIn(skillText(t)) {
		if _, ok := real[name]; ok {
			continue
		}
		if absentByDesign[name] {
			continue // documented precisely as not existing; see the test below
		}
		t.Errorf("the skill mentions --%s, which no command defines", name)
	}
}

// TestFlagsTheSkillCallsAbsentAreAbsent is the other half. The skill tells an
// agent that --unpriority does not exist so it stops looking for one; if a
// future release adds it, that instruction becomes a lie and this fails.
func TestFlagsTheSkillCallsAbsentAreAbsent(t *testing.T) {
	real := realFlagNames(t)
	for name := range absentByDesign {
		if _, ok := real[name]; ok {
			t.Errorf("--%s now exists, but the skill still tells agents it does not", name)
		}
	}
}

// TestEveryAgentFacingFlagIsDocumented is the direction that actually caught
// drift: a flag exists, works, matters — and the skill never mentions it, so
// no agent will ever reach for it. --profile is the case in point: with
// several profiles configured, the identity behind `--assignee me` is
// per-profile, so an undocumented --profile means assigning to the wrong
// person on the wrong board.
func TestEveryAgentFacingFlagIsDocumented(t *testing.T) {
	skill := skillText(t)
	for name := range realFlagNames(t) {
		if setupOnly[name] {
			continue
		}
		if !strings.Contains(skill, "--"+name) {
			t.Errorf("--%s exists but the skill never mentions it; document it or add it to setupOnly with a reason", name)
		}
	}
}

// TestEverySubcommandIsDocumented: a command an agent does not know about is a
// capability it will work around, usually by doing something worse by hand.
func TestEverySubcommandIsDocumented(t *testing.T) {
	skill := skillText(t)
	for _, c := range newRootCmd().Commands() {
		name := c.Name()
		if name == "help" || name == "completion" {
			continue
		}
		if !strings.Contains(skill, "notion-track "+name) {
			t.Errorf("the %q command exists but the skill never shows it", name)
		}
	}
}

// TestExitCodeTableMatchesTheConstants parses the skill's exit-code table and
// checks each documented code against the constant of that name. The skill
// tells agents to branch on these rather than parse messages, so a wrong
// number here sends an agent down the wrong recovery path with confidence.
func TestExitCodeTableMatchesTheConstants(t *testing.T) {
	documented := exitCodesIn(skillText(t))
	if len(documented) == 0 {
		t.Fatal("no exit-code table found in the skill; this test has gone blind")
	}
	real := map[string]int{
		"success":   ExitOK,
		"error":     ExitError,
		"usage":     ExitUsage,
		"not found": ExitNotFound,
		"duplicate": ExitDuplicate,
		"auth":      ExitAuth,
	}
	for meaning, code := range real {
		if !documented[code] {
			t.Errorf("exit %d (%s) is a real outcome but the skill's table omits it", code, meaning)
		}
	}
	for code := range documented {
		if code < 0 || code > 5 {
			t.Errorf("the skill documents exit %d, which the CLI never returns", code)
		}
	}
}

// TestGetJSONFieldsAreDocumented pins the JSON contract the skill calls "a
// stable schema, safe to parse". An agent writes `jq -r .page_id` against it.
func TestGetJSONFieldsAreDocumented(t *testing.T) {
	skill := skillText(t)
	rt := reflect.TypeOf(pageJSON{})
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		if !strings.Contains(skill, "`"+tag+"`") {
			t.Errorf("get --json returns %q but the skill never documents that field", tag)
		}
	}
}

// TestApplyManifestRejectsAppendFile pins the skill's claim that apply cannot
// append: it only replaces a body via body_file. If a future change teaches
// the manifest format an append_file field, this starts failing, which is the
// signal to update the skill's "loop over set --append-file instead" advice
// rather than let it quietly go stale while apply grows the capability it
// currently lacks.
func TestApplyManifestRejectsAppendFile(t *testing.T) {
	_, err := manifest.Parse("manifest.json", []byte(
		`[{"op":"set","ticket":"TASK-1","append_file":"note.md"}]`))
	if err == nil {
		t.Fatal("apply's manifest format now accepts append_file; " +
			"update the skill's apply section, which tells agents to loop over " +
			"`set --append-file` because apply itself cannot append")
	}
}

// --- helpers -------------------------------------------------------------

// absentByDesign are flags the skill names in order to say they do not exist.
// Both are load-bearing: without them an agent asked to "togli la priorità"
// invents a flag instead of saying it has to be done in Notion.
var absentByDesign = map[string]bool{
	"unpriority":    true,
	"unprioritized": true,
}

// setupOnly are flags that configure the tool rather than drive it. They
// belong to `init`, which a human runs once; an agent never needs them, and
// documenting them would add noise to the part of the skill that has to stay
// scannable.
var setupOnly = map[string]bool{
	"help": true, "version": true,
	"assignee-prop": true, "data-source-id": true, "database-id": true,
	"due-prop": true, "id-prop": true, "list": true, "priority-prop": true,
	"status-prop": true, "ticket-prop": true, "title-prop": true,
}

var flagPattern = regexp.MustCompile(`--([a-z][a-z0-9-]*)`)

func flagsMentionedIn(skill string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range flagPattern.FindAllStringSubmatch(skill, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

func realFlagNames(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	root := newRootCmd()
	// cobra registers --help and --version on the way into Execute, not at
	// construction. Without these two calls the skill's documented
	// `notion-track --version` looks like a flag nobody defines.
	root.InitDefaultHelpFlag()
	root.InitDefaultVersionFlag()
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) { out[f.Name] = true })
	root.Flags().VisitAll(func(f *pflag.Flag) { out[f.Name] = true })
	for _, c := range root.Commands() {
		c.Flags().VisitAll(func(f *pflag.Flag) { out[f.Name] = true })
	}
	return out
}

// exitCodesIn reads the leading "| N |" cell of each table row.
var exitRow = regexp.MustCompile(`(?m)^\|\s*(\d+)\s*\|`)

func exitCodesIn(skill string) map[int]bool {
	out := map[int]bool{}
	for _, m := range exitRow.FindAllStringSubmatch(skill, -1) {
		n, err := strconv.Atoi(m[1])
		if err == nil {
			out[n] = true
		}
	}
	return out
}

// The 500KB payload limit is a number an agent acts on: it decides whether to
// split a note into two runs. Prose alone would let the constant and the docs
// drift apart silently, so every place that states it is pinned to the code.
//
// Deliberately checks the LIMIT, not the implementation: how notion-track
// measures the payload is free to change, what it tells an agent is not.
func TestThePayloadLimitTheDocsStateMatchesTheConstant(t *testing.T) {
	// "500KB" as the docs render it, from the constant, so the two cannot drift.
	want := strconv.Itoa(maxAppendPayloadBytes/1000) + "KB"

	if !strings.Contains(skillText(t), want) {
		t.Errorf("SKILL.md must state the %s payload limit an agent has to respect", want)
	}
	for _, name := range []string{"README.md", "README.it.md"} {
		b, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		// The READMEs write it with a space, as prose does.
		spaced := strconv.Itoa(maxAppendPayloadBytes/1000) + " KB"
		if !strings.Contains(string(b), spaced) && !strings.Contains(string(b), want) {
			t.Errorf("%s must state the %s payload limit", name, spaced)
		}
	}
}

// The docs tell an agent the request is larger than the file. If that ever
// stopped being true the advice would be noise, so it is pinned to the code
// that makes it true.
func TestTheRequestIsReallyLargerThanTheFile(t *testing.T) {
	const content = "line one\nline two\n"
	if n := appendPayloadBytes(content); n <= len(content) {
		t.Fatalf("payload (%d) must exceed the content (%d), or the docs are wrong",
			n, len(content))
	}
	// And newlines specifically must cost more than one byte, which is the
	// mechanism the docs name.
	withNewlines := appendPayloadBytes("a\nb\nc\n")
	withoutNewlines := appendPayloadBytes("a b c ")
	if withNewlines <= withoutNewlines {
		t.Errorf("escaping newlines must inflate the payload (%d vs %d): the docs say it does",
			withNewlines, withoutNewlines)
	}
}

// The body JSON keys are outside TestGetJSONFieldsAreDocumented, which walks
// pageJSON only. They have functional tests, but those get edited in the same
// change as a rename — nothing then forces SKILL.md to follow, which is exactly
// the drift these skilldoc tests exist to catch.
//
// bodyJSON's fields are reflected like pageJSON's; the emitWrite keys are
// literals in a map, so they are listed here and pinned by name.
func TestBodyJSONFieldsAreDocumented(t *testing.T) {
	skill := skillText(t)

	// Matched as a quoted JSON key too, not only in backticks: the skill shows
	// these inside a literal response object, which is the clearer way to
	// document a nested shape and is what a reader actually pattern-matches.
	rt := reflect.TypeOf(bodyJSON{})
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		if !strings.Contains(skill, "`"+tag+"`") && !strings.Contains(skill, `"`+tag+`"`) {
			t.Errorf("get --body --json returns %q but the skill never documents that field", tag)
		}
	}

	// Written by emitWrite (body.go) rather than by a struct, so they cannot be
	// reflected. An agent branches on both: `appended` for the outcome,
	// `ambiguous` to tell "refused, safe to re-run" from "unknown, do not".
	for _, key := range []string{"appended", "ambiguous"} {
		if !strings.Contains(skill, "`"+key+"`") {
			t.Errorf("the append JSON reports %q but the skill never documents it", key)
		}
	}
}

// The skill states --body-file's limit as well as the append one. The 500KB
// figure is already pinned to its constant; this pins the other half, so the
// pair cannot drift apart in the document an agent executes.
func TestTheBodyFileLimitTheSkillStatesMatchesTheConstant(t *testing.T) {
	if want := strconv.Itoa(maxBodyFileBytes>>20) + " MiB"; !strings.Contains(skillText(t), want) {
		t.Errorf("SKILL.md must state --body-file's %s limit", want)
	}
}
