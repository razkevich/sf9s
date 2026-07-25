package ui

import (
	"strings"
	"testing"
)

func TestSanitizeCellStripsEscapeSequences(t *testing.T) {
	// OSC 52 would rewrite the operator's clipboard if it reached the tty.
	hostile := "\x1b]52;c;bWFsaWNpb3Vz\x07pwned\x1b[31mRED\x1b[0m\x1b]0;title\x07"
	got := sanitizeCell(hostile)
	if strings.ContainsRune(got, '\x1b') || strings.Contains(got, "52;c") || strings.Contains(got, "[31m") {
		t.Fatalf("escape sequences survived sanitization: %q", got)
	}
	if !strings.Contains(got, "pwned") || !strings.Contains(got, "RED") {
		t.Fatalf("visible text should survive: %q", got)
	}
}

func TestSanitizeTextKeepsLinesDropsEscapes(t *testing.T) {
	got := sanitizeText("line1\n\x1b]52;c;YWJj\x07line2\r\nline3\x07")
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x07') || strings.ContainsRune(got, '\r') {
		t.Fatalf("control bytes survived: %q", got)
	}
	if lines := strings.Split(got, "\n"); len(lines) != 3 {
		t.Fatalf("line structure should be preserved, got %d lines: %q", len(lines), got)
	}
}

func TestCSVSafeNeutralizesFormulas(t *testing.T) {
	for _, in := range []string{`=cmd|' /c calc'!A1`, `+1+1`, `-2+3`, `@SUM(A1)`} {
		if got := csvSafe(in); !strings.HasPrefix(got, "'") {
			t.Errorf("formula %q not neutralized: %q", in, got)
		}
	}
	for _, in := range []string{"Acme Rockets", "001A", ""} {
		if got := csvSafe(in); got != in {
			t.Errorf("benign value %q was altered: %q", in, got)
		}
	}
}
