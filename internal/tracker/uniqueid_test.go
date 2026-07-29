package tracker

import (
	"errors"
	"strings"
	"testing"
)

func TestParseUniqueID(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		prefix string
		want   int64
		reason string // substring the error must contain; "" means success
	}{
		{"canonical form", "BDF-271", "BDF", 271, ""},
		{"lowercase prefix", "bdf-271", "BDF", 271, ""},
		{"bare number", "271", "BDF", 271, ""},
		{"surrounding whitespace", "  BDF-271  ", "BDF", 271, ""},
		{"bare number on a prefixless column", "271", "", 271, ""},
		{"another board's prefix", "ABC-271", "BDF", 0, `ids start with "BDF"`},
		{"prefix on a prefixless column", "BDF-271", "", 0, "have no prefix"},
		{"empty number half", "BDF-", "BDF", 0, "expected a number"},
		{"non-numeric number half", "BDF-abc", "BDF", 0, "expected a number"},
		{"empty prefix half", "-271", "BDF", 0, "expected a number"},
		{"zero", "0", "BDF", 0, "ids start at 1"},
		{"zero with prefix", "BDF-0", "BDF", 0, "ids start at 1"},
		{"empty input", "", "BDF", 0, "expected a number"},
		// Arabic-Indic digits: unicode.IsDigit accepts these, strconv does not.
		// The test exists to keep the digit check ASCII-only.
		{"non-ASCII digits", "٢٧١", "BDF", 0, "expected a number"},
		{"beyond int64", "99999999999999999999", "BDF", 0, "expected a number"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUniqueID(tt.input, tt.prefix)
			if tt.reason == "" {
				if err != nil {
					t.Fatalf("ParseUniqueID(%q, %q) = error %v, want %d", tt.input, tt.prefix, err, tt.want)
				}
				if got != tt.want {
					t.Errorf("ParseUniqueID(%q, %q) = %d, want %d", tt.input, tt.prefix, got, tt.want)
				}
				return
			}
			var invalid *InvalidIDError
			if !errors.As(err, &invalid) {
				t.Fatalf("ParseUniqueID(%q, %q) error = %v, want *InvalidIDError", tt.input, tt.prefix, err)
			}
			if !strings.Contains(invalid.Error(), tt.reason) {
				t.Errorf("error = %q, want it to mention %q", invalid.Error(), tt.reason)
			}
			// The message quotes what the user typed, not the trimmed or
			// half-parsed form: that is what they can compare against.
			if invalid.Value != tt.input {
				t.Errorf("InvalidIDError.Value = %q, want the raw input %q", invalid.Value, tt.input)
			}
		})
	}
}

func TestParseUniqueIDNamesTheBoardsPrefixInTheExample(t *testing.T) {
	_, err := ParseUniqueID("nope", "BDF")
	if err == nil {
		t.Fatal("ParseUniqueID(\"nope\", \"BDF\") = nil error, want a failure")
	}
	// The example is built from this board's prefix, so the fix is readable
	// without a trip to the docs.
	if !strings.Contains(err.Error(), `"BDF-1"`) {
		t.Errorf("error = %q, want it to show a BDF-shaped example", err.Error())
	}
}
