package player

import (
	"fmt"
	"strings"

	"github.com/WhatDamon/go-nvaa-codec"
)

// Photosensitivity metadata keys, as a producer declares them. A consumer that
// finds none treats the animation as unassessed, never as safe.
const (
	keyPSEAssessed    = "epilepsy.assessed"
	keyPSEVerdict     = "epilepsy.verdict"
	keyPSEMethod      = "epilepsy.method"
	keyPSEStandard    = "epilepsy.standard"
	keyPSEGeneral     = "epilepsy.general_flashes_per_second"
	keyPSERed         = "epilepsy.red_flashes_per_second"
	keyPSEArea        = "epilepsy.max_flash_area_permille"
	keyContentWarning = "content.warning"
)

// Verdict is the three-state reading of a file's own safety declaration.
type Verdict int

const (
	// VerdictUnknown means the file does not say, or says it without an
	// assessment behind it. It is never a statement that the content is safe.
	VerdictUnknown Verdict = iota

	// VerdictPass means the producer assessed the content and it passed.
	VerdictPass

	// VerdictFail means the producer assessed the content and it failed.
	VerdictFail
)

// String renders a verdict for display.
func (v Verdict) String() string {
	switch v {
	case VerdictPass:
		return "pass"
	case VerdictFail:
		return "fail"
	}
	return "unknown"
}

// ReadVerdict interprets the photosensitivity metadata.
//
// The asymmetry is the whole point: a missing, mistyped, or absent field yields
// unknown rather than pass, because "nobody checked" and "checked and found
// fine" are different claims. A string "true" for epilepsy.assessed is not an
// assessment, and treating it as one would let a malformed file silence a
// warning that a well-formed one would raise.
func ReadVerdict(md nvaa.Metadata) Verdict {
	assessed, ok := md.Lookup(keyPSEAssessed)
	if !ok || assessed.Type != nvaa.MetaBool || !assessed.Bool {
		return VerdictUnknown
	}

	verdict, ok := md.Lookup(keyPSEVerdict)
	if !ok || verdict.Type != nvaa.MetaText {
		return VerdictUnknown
	}

	switch verdict.Text {
	case "pass":
		return VerdictPass
	case "fail":
		return VerdictFail
	}
	return VerdictUnknown
}

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
func WarningFrom(anim *nvaa.Animation) Warning {
	md := anim.Metadata

	w := Warning{
		Verdict:  ReadVerdict(md),
		Method:   textOf(md, keyPSEMethod),
		Standard: textOf(md, keyPSEStandard),
		Message:  textOf(md, keyContentWarning),
		General:  uintOf(md, keyPSEGeneral),
		Red:      uintOf(md, keyPSERed),
	}

	if area, ok := md.Lookup(keyPSEArea); ok && area.Type == nvaa.MetaUint {
		w.Area, w.HasArea = area.Uint, true
	}
	return w
}

// textOf reads a text field, ignoring one stored under the wrong type.
func textOf(md nvaa.Metadata, key string) string {
	if v, ok := md.Lookup(key); ok && v.Type == nvaa.MetaText {
		return v.Text
	}
	return ""
}

// uintOf reads an unsigned field, ignoring one stored under the wrong type.
func uintOf(md nvaa.Metadata, key string) uint64 {
	if v, ok := md.Lookup(key); ok && v.Type == nvaa.MetaUint {
		return v.Uint
	}
	return 0
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
