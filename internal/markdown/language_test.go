package markdown

import "testing"

func TestCanonicalLanguage(t *testing.T) {
	cases := map[string]string{
		"js": "javascript", "TS": "typescript", "py": "python", "sh": "shell",
		"golang": "go", "go": "go", "yaml": "yaml", "": "plain text",
		"klingon": "plain text", "  Python ": "python",
	}
	for in, want := range cases {
		if got := CanonicalLanguage(in); got != want {
			t.Errorf("CanonicalLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}
