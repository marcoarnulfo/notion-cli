package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A manifest field the skill forgets is a field no agent will use; a field the
// skill invents is a run that dies on "unknown field" after writing nothing.
// Both are cheap to prevent, because the set is right here.
func TestEveryManifestFieldIsDocumented(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "skills", "notion-track", "SKILL.md"))
	if err != nil {
		t.Fatalf("reading the skill: %v", err)
	}
	skill := string(b)

	for _, f := range fieldNames {
		if !strings.Contains(skill, "`"+f+"`") {
			t.Errorf("a manifest entry accepts %q, but the skill never documents it", f)
		}
	}
}
