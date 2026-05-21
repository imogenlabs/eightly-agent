package websocket

import "testing"

func TestValidDockerID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{"valid 12-char hex", "abcdef123456", true},
		{"valid 64-char hex", "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", true},
		{"valid 12-char mixed case", "aBcDeF123456", true},
		{"empty string", "", false},
		{"too short 11 chars", "abcdef12345", false},
		{"too long 65 chars", "abcdef1234567890abcdef1234567890abcdef1234567890abcdef12345678901", false},
		{"contains CRLF", "abcdef1234\r\n", false},
		{"contains newline", "abcdef12345\n", false},
		{"contains path separator", "abcdef/12345", false},
		{"contains backslash", "abcdef\\12345", false},
		{"contains spaces", "abcdef 12345", false},
		{"contains dots", "abcdef.12345", false},
		{"contains dashes", "abcdef-12345", false},
		{"contains underscore", "abcdef_12345", false},
		{"exactly 12 chars", "aB3cD4eF5678", true},
		{"exactly 64 chars all digits", "1234567890123456789012345678901234567890123456789012345678901234", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validDockerID(tt.id)
			if got != tt.want {
				t.Errorf("validDockerID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}
