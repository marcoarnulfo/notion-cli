package notion

import (
	"errors"
	"testing"
)

func TestNormalizePageID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "full Notion URL",
			input: "https://www.notion.so/Nome-task-23fb4e5c8a5f4d21b7c9d0e1f2a3b4c5?pvs=4",
			want:  "23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5",
		},
		{
			name:  "Notion URL with workspace segment and no title",
			input: "https://www.notion.so/myworkspace/23fb4e5c8a5f4d21b7c9d0e1f2a3b4c5",
			want:  "23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5",
		},
		{
			name:  "bare 32-hex id",
			input: "23fb4e5c8a5f4d21b7c9d0e1f2a3b4c5",
			want:  "23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5",
		},
		{
			name:  "uppercase bare 32-hex id",
			input: "23FB4E5C8A5F4D21B7C9D0E1F2A3B4C5",
			want:  "23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5",
		},
		{
			name:  "dashed UUID",
			input: "23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5",
			want:  "23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5",
		},
		{
			name:  "dashed UUID with surrounding whitespace",
			input: "  23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5\n",
			want:  "23fb4e5c-8a5f-4d21-b7c9-d0e1f2a3b4c5",
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "garbage",
			input:   "not-a-page-id-at-all",
			wantErr: true,
		},
		{
			name:    "too short",
			input:   "23fb4e5c8a5f4d21b7c9d0e1f2a3b4", // 30 hex chars
			wantErr: true,
		},
		{
			name:    "URL with no recognisable id in the last segment",
			input:   "https://www.notion.so/Just-A-Title",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizePageID(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizePageID(%q) = %q, want an error", tt.input, got)
				}
				if !errors.Is(err, ErrMalformedPageID) {
					t.Fatalf("NormalizePageID(%q) error = %v, want ErrMalformedPageID", tt.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizePageID(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizePageID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
