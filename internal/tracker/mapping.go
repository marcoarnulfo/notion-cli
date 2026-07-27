package tracker

import (
	"sort"
	"strings"

	"github.com/marcoarnulfo/notion-cli/internal/config"
	"github.com/marcoarnulfo/notion-cli/internal/notion"
)

// Names init recognises without asking. Matching is case-insensitive.
var (
	ticketNames   = []string{"ticket", "key", "id", "chiave", "codice"}
	statusNames   = []string{"status", "stato", "state", "fase"}
	dueNames      = []string{"due", "due date", "scadenza", "deadline"}
	assigneeNames = []string{
		"assignee", "owner", "referente", "persona", "responsabile",
		"assegnatario", "incaricato",
	}
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

	// Copy into a fresh slice before concatenating, and re-sort: appending
	// straight onto byType["status"] would write into its backing array, and
	// the merged list has to stay alphabetical for the guess to be
	// reproducible across runs.
	statusCandidates := append([]string{}, byType["status"]...)
	statusCandidates = append(statusCandidates, byType["select"]...)
	sort.Strings(statusCandidates)
	out.Status = pick(statusCandidates, statusNames)
	out.Due = pick(byType["date"], dueNames)

	// The ticket key is usually rich_text, but a database may use its title.
	ticketCandidates := append([]string{}, byType["rich_text"]...)
	ticketCandidates = append(ticketCandidates, byType["title"]...)
	sort.Strings(ticketCandidates)
	out.Ticket = pick(ticketCandidates, ticketNames)

	// Assignee is guessed by name only: pick's "the only candidate wins" rule
	// is right for a required role, where one plausible column *is* the answer,
	// but wrong for an optional one — guessing "Urgenza" as the assignee is
	// worse than guessing nothing, and nothing is a perfectly good outcome
	// here. The status column is excluded because both roles draw from
	// selects, and a board with a single select must not end up with the same
	// column in two roles.
	for _, name := range byType["select"] {
		if name == out.Status {
			continue
		}
		for _, known := range assigneeNames {
			if strings.EqualFold(name, known) {
				out.Assignee = name
				break
			}
		}
		if out.Assignee != "" {
			break
		}
	}

	return out
}
