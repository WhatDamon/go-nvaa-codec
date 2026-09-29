package player

import (
	"fmt"
	"strings"

	"github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/photosensitivity"
)

// Verdict is the three-state reading of a file's own safety declaration.
//
// It is the analyser's type rather than a copy of it, so that what a file claims
// and what an analysis of the same file concludes are the same three values and
// can be compared directly. The rule for reading a claim lives there too.
type Verdict = photosensitivity.Verdict

const (
	// VerdictUnknown means the file does not say, or says it without an
	// assessment behind it. It is never a statement that the content is safe.
	VerdictUnknown = photosensitivity.VerdictUnknown

	// VerdictPass means the producer assessed the content and it passed.
	VerdictPass = photosensitivity.VerdictPass

	// VerdictFail means the producer assessed the content and it failed.
	VerdictFail = photosensitivity.VerdictFail
)

// Warning is the advisory a file carries about its own photosensitivity risk.
type Warning struct {
	Verdict  Verdict
	Message  string
	Method   string
	Standard string
	General  uint64
	Red      uint64
	Area     uint64
	HasArea  bool
}

// WarningFrom extracts the advisory from an animation's metadata.
//
// This is what the file claims, taken at its word. To find out whether the claim
// holds, analyse the same animation with the photosensitivity package and compare
// its verdict with this one.
func WarningFrom(anim *nvaa.Animation) Warning {
	declared := photosensitivity.Declared(anim.Metadata)
	return Warning{
		Verdict:  declared.Verdict,
		Message:  declared.Warning,
		Method:   declared.Method,
		Standard: declared.Standard,
		General:  declared.General,
		Red:      declared.Red,
		Area:     declared.Area,
		HasArea:  declared.HasArea,
	}
}

// Box renders the advisory as a bordered block for the terminal.
//
// It says what the file claims and who made the claim, and it says plainly that
// the claim is not a certification -- a producer that ran an approximation can
// only report that they ran an approximation.
func (w Warning) Box() string {
	var b strings.Builder

	title := "PHOTOSENSITIVITY WARNING"
	if w.Verdict == VerdictFail {
		title = "PHOTOSENSITIVITY: THIS FILE FAILED ITS OWN CHECK"
	}

	line := strings.Repeat("=", len(title)+4)
	fmt.Fprintf(&b, "%s\n  %s\n%s\n", line, title, line)

	if w.Message != "" {
		fmt.Fprintf(&b, "\n  %s\n", w.Message)
	}

	b.WriteString("\n  The file says:\n")
	fmt.Fprintf(&b, "    verdict            %s\n", w.Verdict)
	fmt.Fprintf(&b, "    general flashes/s  %d\n", w.General)
	fmt.Fprintf(&b, "    red flashes/s      %d\n", w.Red)
	if w.HasArea {
		fmt.Fprintf(&b, "    largest flash       %d permille of the viewport\n", w.Area)
	}
	if w.Standard != "" {
		fmt.Fprintf(&b, "    standard           %s\n", w.Standard)
	}
	if w.Method != "" {
		fmt.Fprintf(&b, "    method             %s\n", w.Method)
	}

	b.WriteString("\n  This is a claim made by whoever produced the file, not a\n")
	b.WriteString("  certification, and not this player's own judgement. It may be\n")
	b.WriteString("  wrong, and a producer who ran an approximation can only report\n")
	b.WriteString("  that they ran an approximation. Flashing imagery can trigger\n")
	b.WriteString("  seizures; if you are photosensitive, consider not watching.\n")

	return b.String()
}
