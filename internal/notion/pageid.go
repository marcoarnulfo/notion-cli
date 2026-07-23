package notion

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrMalformedPageID means the input carries no page id notion-track can
// recognise, in any of the three accepted shapes. Callers must check for it
// before making any network request: a page id is either present in the
// input or it is not, and no round trip to Notion can change that answer.
var ErrMalformedPageID = errors.New("notion: malformed page id")

// hexDigits is used to validate a candidate id one rune at a time rather than
// pulling in regexp for a 32-character check.
const hexDigits = "0123456789abcdefABCDEF"

// NormalizePageID turns whatever a user pastes from Notion — the full page
// URL, a bare 32-character hex string, or a dashed UUID — into the canonical
// dashed UUID form (8-4-4-4-12) that the API documents as the id format:
// "Format this value by inserting hyphens (-) in the following pattern:
// 8-4-4-4-12" (https://developers.notion.com/docs/working-with-page-content,
// verified 2026-07-23; see docs/superpowers/notes/2026-07-23-page-id-format.md).
//
// It never makes a network call. A malformed id is a usage mistake the
// caller can catch before spending a request on it, not something worth a
// confusing 400 or 404 from Notion.
func NormalizePageID(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%w: got an empty string", ErrMalformedPageID)
	}

	// A URL carries the id as the trailing 32 characters of its last path
	// segment; anything else (a bare id, possibly dashed) is taken whole.
	// url.Parse happily "succeeds" on a bare id or UUID too (as a relative
	// URL with no host), so Host is what actually distinguishes the two.
	if u, err := url.Parse(trimmed); err == nil && u.Host != "" {
		segments := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
		last := segments[len(segments)-1]
		if hex, ok := trailingHex32(last); ok {
			return formatUUID(hex), nil
		}
		return "", malformedErr(raw)
	}

	if hex, ok := bareHex32(trimmed); ok {
		return formatUUID(hex), nil
	}
	return "", malformedErr(raw)
}

func malformedErr(raw string) error {
	return fmt.Errorf("%w: %q (accepted: the full Notion URL, a bare 32-character hex id, or a dashed UUID)",
		ErrMalformedPageID, raw)
}

// trailingHex32 extracts the id from a URL path segment, where a title slug
// may be hyphen-joined in front of it (…/Nome-task-23fb4e5c8a5f4d21b7c9d0e1f2a3b4c5).
// A prefix is only accepted when it ends cleanly at a hyphen, so a stray hex
// run in the middle of an unrelated word cannot be mistaken for the id.
func trailingHex32(segment string) (string, bool) {
	if len(segment) < 32 {
		return "", false
	}
	tail := segment[len(segment)-32:]
	if !isHex(tail) {
		return "", false
	}
	prefix := segment[:len(segment)-32]
	if prefix != "" && !strings.HasSuffix(prefix, "-") {
		return "", false
	}
	return tail, true
}

// bareHex32 accepts a plain 32-hex id or the same id already dashed as a
// UUID: stripping hyphens before checking makes both shapes one case.
func bareHex32(s string) (string, bool) {
	stripped := strings.ReplaceAll(s, "-", "")
	if len(stripped) != 32 || !isHex(stripped) {
		return "", false
	}
	return stripped, true
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune(hexDigits, r) {
			return false
		}
	}
	return true
}

// formatUUID renders 32 hex characters as the dashed 8-4-4-4-12 form Notion
// documents and returns from its API.
func formatUUID(hex32 string) string {
	hex32 = strings.ToLower(hex32)
	return hex32[0:8] + "-" + hex32[8:12] + "-" + hex32[12:16] + "-" + hex32[16:20] + "-" + hex32[20:32]
}
