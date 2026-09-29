package main

import (
	"strings"
	"testing"

	"github.com/WhatDamon/go-nvaa-codec/photosensitivity"
)

// report runs the command and returns what it printed.
func report(t *testing.T, args ...string) string {
	t.Helper()
	var out strings.Builder
	if err := run(args, &out); err != nil {
		t.Fatalf("pse %v: %v", args, err)
	}
	return out.String()
}

// value returns the value of a labelled line, so that a test does not depend on
// how wide the label column happens to be. It is not called field, because main.go
// already has a function by that name.
func value(t *testing.T, out, label string) string {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if rest, ok := strings.CutPrefix(line, label); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatalf("no %q line in:\n%s", label, out)
	return ""
}

func TestItReportsAPassingFile(t *testing.T) {
	out := report(t, "../../testdata/demo.nvaa")

	if got := value(t, out, "verdict"); got != "PASS" {
		t.Errorf("verdict = %q, want PASS", got)
	}
	if got := value(t, out, "analysis viewport"); !strings.HasPrefix(got, "50x18") {
		t.Errorf("analysis viewport = %q, want the file's own 50x18", got)
	}
	if got := value(t, out, "declared"); !strings.HasPrefix(got, "pass") {
		t.Errorf("declared = %q, want the pass the file records", got)
	}
	if got := value(t, out, "agreement"); !strings.Contains(got, "agrees") {
		t.Errorf("agreement = %q, want agreement", got)
	}
}

// TestItReportsAFlashingFile is the audit end to end: a file that says it failed
// its own check, and an analysis that reaches the same conclusion from the frames.
func TestItReportsAFlashingFile(t *testing.T) {
	out := report(t, "../../testdata/strobe.nvaa")

	if got := value(t, out, "verdict"); got != "FAIL" {
		t.Errorf("verdict = %q, want FAIL", got)
	}
	if got := value(t, out, "general flashes/s"); !strings.HasPrefix(got, "5 ") {
		t.Errorf("general flashes/s = %q, want 5", got)
	}
	if got := value(t, out, "declared"); !strings.HasPrefix(got, "fail") {
		t.Errorf("declared = %q, want the fail the file records", got)
	}
	if got := value(t, out, "warning"); !strings.Contains(got, "photosensitive epilepsy") {
		t.Errorf("warning = %q, want the content warning the file carries", got)
	}
	if got := value(t, out, "agreement"); !strings.Contains(got, "agrees") {
		t.Errorf("agreement = %q, want agreement", got)
	}
}

func TestItSaysWhenAFileClaimsNothing(t *testing.T) {
	out := report(t, "../../testdata/alternating.nvaa")

	if got := value(t, out, "declared"); !strings.Contains(got, "unknown") {
		t.Errorf("declared = %q, want unknown", got)
	}
	if got := value(t, out, "agreement"); !strings.Contains(got, "no claim") {
		t.Errorf("agreement = %q, want a note that nothing was claimed", got)
	}
}

func TestAnExplicitViewportIsUsed(t *testing.T) {
	out := report(t, "../../testdata/demo.nvaa", "--viewport", "20x8")

	if got := value(t, out, "analysis viewport"); !strings.HasPrefix(got, "20x8") {
		t.Errorf("analysis viewport = %q, want 20x8", got)
	}
}

func TestInkCoverageIsRecorded(t *testing.T) {
	out := report(t, "../../testdata/demo.nvaa", "--ink-coverage", "0.5")

	if got := value(t, out, "ink coverage model"); got != "500 per mille" {
		t.Errorf("ink coverage model = %q, want 500 per mille", got)
	}
}

// TestTheOperandMayComeLast covers the argument handling every program here
// shares: the file may be named before the flags or after them.
func TestTheOperandMayComeLast(t *testing.T) {
	before := report(t, "--viewport", "20x8", "../../testdata/demo.nvaa")
	after := report(t, "../../testdata/demo.nvaa", "--viewport", "20x8")

	if before != after {
		t.Errorf("moving the operand changed the report:\n%s\n---\n%s", before, after)
	}
}

func TestItRefusesWhatItCannotRead(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no file at all", nil},
		{"two files", []string{"../../testdata/demo.nvaa", "../../testdata/strobe.nvaa"}},
		{"a viewport that is not WxH", []string{"../../testdata/demo.nvaa", "--viewport", "50"}},
		{"a viewport of zero", []string{"../../testdata/demo.nvaa", "--viewport", "0x8"}},
		{"a viewport that is not numbers", []string{"../../testdata/demo.nvaa", "--viewport", "wxh"}},
		{"a file that is not there", []string{"../../testdata/absent.nvaa"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			if err := run(tc.args, &out); err == nil {
				t.Errorf("pse %v was accepted", tc.args)
			}
		})
	}
}

func TestParseViewport(t *testing.T) {
	good := map[string][2]int{
		"50x18":   {50, 18},
		"1x1":     {1, 1},
		"50X18":   {50, 18},
		" 50x18 ": {50, 18},
		"50 x 18": {50, 18},
	}
	for text, want := range good {
		columns, lines, err := parseViewport(text)
		if err != nil {
			t.Errorf("parseViewport(%q): %v", text, err)
			continue
		}
		if columns != want[0] || lines != want[1] {
			t.Errorf("parseViewport(%q) = %d, %d; want %d, %d",
				text, columns, lines, want[0], want[1])
		}
	}

	for _, text := range []string{"", "50", "x18", "50x", "0x8", "50x0", "-1x8", "50x18x2", "a x b"} {
		if _, _, err := parseViewport(text); err == nil {
			t.Errorf("parseViewport(%q) was accepted", text)
		}
	}
}

// TestAgreement covers the finding an audit exists for: a file whose claim and
// whose frames disagree.
func TestAgreement(t *testing.T) {
	pass, fail := photosensitivity.VerdictPass, photosensitivity.VerdictFail
	unknown := photosensitivity.VerdictUnknown

	cases := []struct {
		name     string
		declared photosensitivity.Verdict
		analysed photosensitivity.Verdict
		want     string
	}{
		{"nothing claimed", unknown, fail, "no claim"},
		{"both pass", pass, pass, "agrees"},
		{"both fail", fail, fail, "agrees"},
		{"a pass that is not", pass, fail, "the file claims pass, the analysis says fail"},
		{"a fail that is not", fail, pass, "the file claims fail, the analysis says pass"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := agreement(tc.declared, tc.analysed); !strings.Contains(got, tc.want) {
				t.Errorf("agreement = %q, want it to mention %q", got, tc.want)
			}
		})
	}
}
