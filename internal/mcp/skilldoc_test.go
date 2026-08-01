package mcp

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The MCP surface is the half of the skill nobody exercises by hand: an agent
// speaking MCP never runs a shell command, so a wrong claim here is invisible
// until it produces a null. These tests pin the two things the skill promises
// about that surface — the envelope keys and the argument set — to the structs
// that actually define them.

func skillText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "skills", "notion-track", "SKILL.md"))
	if err != nil {
		t.Fatalf("reading the skill: %v", err)
	}
	return string(b)
}

// TestWriteEnvelopeIsDocumented guards the difference that cost the most: the
// CLI wraps a write in "page", MCP wraps it in "row". An agent that carries
// the CLI's `.page.page_id` over to MCP reads null and reports success.
func TestWriteEnvelopeIsDocumented(t *testing.T) {
	skill := skillText(t)
	for _, tag := range jsonTags(writeResult{}) {
		if !strings.Contains(skill, "`"+tag+"`") {
			t.Errorf("an MCP write returns %q, but the skill never names that key — an agent will reuse the CLI's envelope", tag)
		}
	}
	for _, tag := range jsonTags(listResult{}) {
		if !strings.Contains(skill, "`"+tag+"`") {
			t.Errorf("list_tasks returns %q, but the skill never names that key", tag)
		}
	}
}

// TestMCPWriteArgumentsAreWhatTheSkillDescribes fails when the tool surface
// grows. The skill tells agents that MCP has no dry run, no body writing and
// no way to address a row other than by ticket; each of those is a statement
// about this struct. Add a field here and the skill needs a sentence.
func TestMCPWriteArgumentsAreWhatTheSkillDescribes(t *testing.T) {
	want := []string{"ticket", "title", "status", "due", "assignee", "unassign", "priority"}
	got := jsonTags(upsertArgs{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MCP write arguments = %v, want %v.\nThe skill documents this exact set: no dry_run, no body_file, and ticket as the only way to address a row. Update the skill's MCP section, then this list.", got, want)
	}
}

// TestGetAddressesRowsOnlyByTicket pins the one asymmetry the skill calls out
// explicitly: --id and --page-id are CLI-only.
func TestGetAddressesRowsOnlyByTicket(t *testing.T) {
	if got := jsonTags(getArgs{}); !reflect.DeepEqual(got, []string{"ticket"}) {
		t.Errorf("get_task arguments = %v, want [ticket]; the skill tells agents ticket is the only way to address a row over MCP", got)
	}
}

func jsonTags(v any) []string {
	rt := reflect.TypeOf(v)
	var out []string
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if tag != "" && tag != "-" {
			out = append(out, tag)
		}
	}
	return out
}
