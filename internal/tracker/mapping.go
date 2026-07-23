package tracker

import (
	"sort"
	"strings"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

// Names init recognises without asking. Matching is case-insensitive.
var (
	ticketNames = []string{"ticket", "key", "id", "chiave", "codice"}
	statusNames = []string{"status", "stato", "state", "fase"}
	dueNames    = []string{"due", "due date", "scadenza", "deadline"}
)

// GuessMapping proposes a property mapping for init to confirm.
//
// Two rules, in order: a recognisable name wins; failing that, being the only
// column of a suitable type wins. When neither applies the guess is left
// empty — a wrong guess the user waves through is worse than a question.
func GuessMapping(schema *notion.Schema) config.Properties {
	var out config.Properties

	byType := map[string][]string{}
	for name, p := range schema.Properties {
		byType[p.Type] = append(byType[p.Type], name)
	}
	for _, names := range byType {
		sort.Strings(names) // deterministic guesses
	}

	// A data source has exactly one title property.
	if titles := byType["title"]; len(titles) == 1 {
		out.Title = titles[0]
	}

	pick := func(candidates []string, known []string) string {
		for _, name := range candidates {
			for _, k := range known {
				if strings.EqualFold(name, k) {
					return name
				}
			}
		}
		if len(candidates) == 1 {
			return candidates[0]
		}
		return ""
	}

	out.Status = pick(append(byType["status"], byType["select"]...), statusNames)
	out.Due = pick(byType["date"], dueNames)

	// The ticket key is usually rich_text, but a database may use its title.
	ticketCandidates := append([]string{}, byType["rich_text"]...)
	ticketCandidates = append(ticketCandidates, byType["title"]...)
	sort.Strings(ticketCandidates)
	out.Ticket = pick(ticketCandidates, ticketNames)

	return out
}
