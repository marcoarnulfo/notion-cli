// Package markdown converts Markdown into Notion blocks. It is pure domain: it
// performs no network I/O, so it is table-tested without a server.
package markdown

import "strings"

// notionLanguages is the set of code-block languages Notion's API accepts. A
// value outside it makes the append 400, so anything unknown falls back to
// "plain text". Maintained subset of Notion's enum: common languages plus what
// a task body realistically carries.
var notionLanguages = map[string]bool{
	"plain text": true, "bash": true, "c": true, "c++": true, "c#": true,
	"css": true, "diff": true, "docker": true, "go": true, "graphql": true,
	"html": true, "java": true, "javascript": true, "json": true, "kotlin": true,
	"lua": true, "makefile": true, "markdown": true, "objective-c": true,
	"perl": true, "php": true, "powershell": true, "python": true, "r": true,
	"ruby": true, "rust": true, "scala": true, "shell": true, "sql": true,
	"swift": true, "toml": true, "typescript": true, "xml": true, "yaml": true,
}

// languageAliases maps common fence tags not already canonical onto Notion's
// name. Tags already present in notionLanguages (e.g. "bash") are not aliased.
var languageAliases = map[string]string{
	"js": "javascript", "ts": "typescript", "py": "python", "rb": "ruby",
	"sh": "shell", "zsh": "shell", "golang": "go", "yml": "yaml",
	"md": "markdown", "dockerfile": "docker", "cpp": "c++", "cs": "c#",
	"objc": "objective-c", "ps1": "powershell", "text": "plain text",
	"txt": "plain text", "": "plain text",
}

// CanonicalLanguage resolves a fence tag to a Notion-accepted language, applying
// aliases and falling back to "plain text" for anything unknown, so the append
// can never 400 on an invalid language.
func CanonicalLanguage(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if canon, ok := languageAliases[s]; ok {
		s = canon
	}
	if notionLanguages[s] {
		return s
	}
	return "plain text"
}
