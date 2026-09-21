package utils

import "testing"

func TestNormalizeISRC(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"valid uppercase", "USUG11904206", "USUG11904206"},
		{"lowercase normalized", "usug11904206", "USUG11904206"},
		{"mixed case", "UsUg11904206", "USUG11904206"},
		{"surrounding whitespace", "  USUG11904206  ", "USUG11904206"},
		{"too short", "USUG1190420", ""},
		{"too long", "USUG119042060", ""},
		{"non-alphanumeric hyphen", "US-UG-1904206", ""},
		{"non-alphanumeric dot", "USUG1190420.", ""},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"unicode digit lookalike", "USUG1190420６", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeISRC(tt.in); got != tt.want {
				t.Errorf("NormalizeISRC(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeISRCIsIdempotent(t *testing.T) {
	once := NormalizeISRC("usug11904206")
	twice := NormalizeISRC(once)
	if once != twice {
		t.Errorf("not idempotent: once=%q twice=%q", once, twice)
	}
}
