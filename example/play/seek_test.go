package main

import "testing"

// TestParseSeekReadsWhatPeopleType covers the three shapes a position gets
// written in, and the mistakes worth naming rather than guessing at.
func TestParseSeekReadsWhatPeopleType(t *testing.T) {
	const total = 290_133 // the long animation, 4:50.1

	good := []struct {
		text string
		want uint64
	}{
		{"0", 0},
		{"83", 83_000},
		{"1:23", 83_000},
		{"1:23.5", 83_500},
		{"0:01:23", 83_000},
		{"1:02:03", 3_723_000},
		{"2:00", 120_000},
		{"  1:23 ", 83_000},
		{"0%", 0},
		{"50%", 145_066}, // half of 290133 ms, rounded down
		{"100%", total},
	}
	for _, tc := range good {
		got, err := parseSeek(tc.text, total)
		if err != nil {
			t.Errorf("parseSeek(%q) failed: %v", tc.text, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseSeek(%q) = %d ms, want %d ms", tc.text, got, tc.want)
		}
	}

	bad := []string{
		"", "abc", ":", "1:", "1:99", "1:2:3:4", "1:23:45:6",
		"%", "101%", "-5", "1.5:00", "s:83",
	}
	for _, text := range bad {
		if got, err := parseSeek(text, total); err == nil {
			t.Errorf("parseSeek(%q) = %d ms, want a refusal", text, got)
		}
	}
}
