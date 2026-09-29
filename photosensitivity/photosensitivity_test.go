package photosensitivity

import (
	"errors"
	"iter"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhatDamon/go-nvaa-codec"
)

// The synthetic strobes the thresholds are checked against: eight by four cells,
// twelve frames at 10 fps. One on/off cycle is therefore a fifth of a second, so
// the flash rate is fixed by construction rather than measured by hand.
const (
	strobeColumns = 8
	strobeLines   = 4
	strobeFrames  = 12
	strobeMS      = 100
)

// strobePalette indices: 0 black, 1 white, 2 red, 3 blue, 4 mid grey.
var strobePalette = []nvaa.RGB{
	{R: 0, G: 0, B: 0},
	{R: 255, G: 255, B: 255},
	{R: 255},
	{B: 255},
	{R: 128, G: 128, B: 128},
}

// strobeSignals builds the two styles a strobe file holds: the empty style, whose
// background is black, and one style painted in the colour under test. Both use a
// space glyph, so a cell shows its background alone and the flash is the colour's.
func strobeSignals(colour uint32) []Signal {
	return StyleSignals(
		[]nvaa.Style{{Glyph: 0, FG: 1, BG: 0}, {Glyph: 0, FG: 1, BG: colour}},
		strobePalette, []string{" ", "#"}, DefaultInkCoverage,
	)
}

// strobe feeds an analysis one grid per frame, built by the caller.
func strobe(build func(frame int, grid []uint32)) iter.Seq2[Sample, error] {
	return func(yield func(Sample, error) bool) {
		for frame := range strobeFrames {
			grid := make([]uint32, strobeColumns*strobeLines)
			build(frame, grid)
			if !yield(Sample{DurationMS: strobeMS, Grid: grid}, nil) {
				return
			}
		}
	}
}

func fill(grid []uint32) {
	for i := range grid {
		grid[i] = 1
	}
}

// The lit sets: every other frame, every frame, or one cell.
func everyOther(frame int, grid []uint32) {
	if frame%2 == 0 {
		fill(grid)
	}
}

func everyFrame(frame int, grid []uint32) { fill(grid) }

func oneCell(frame int, grid []uint32) {
	if frame%2 == 0 {
		grid[0] = 1
	}
}

func analyzeStrobe(t *testing.T, colour uint32, build func(int, []uint32)) Assessment {
	t.Helper()
	got, err := Assess(strobe(build), strobeSignals(colour), Options{
		Columns: strobeColumns,
		Lines:   strobeLines,
	})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	return got
}

func loadAnimation(t *testing.T, name string) *nvaa.Animation {
	t.Helper()
	anim, err := nvaa.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return anim
}

// TestSyntheticStrobes pins every number an assessment carries.
//
// The expected values were produced by the format's reference implementation over
// these same scenarios, so this test is the field-by-field comparison between the
// two. Note that the red case fails both ways: a saturated red on black is also a
// luminance change large enough to count as a general flash.
func TestSyntheticStrobes(t *testing.T) {
	cases := []struct {
		name      string
		colour    uint32
		build     func(int, []uint32)
		verdict   Verdict
		general   int
		red       int
		area      int
		luminance int
	}{
		{"a still frame passes", 1, everyFrame, VerdictPass, 0, 0, 0, 0},
		{"a white strobe fails", 1, everyOther, VerdictFail, 5, 0, 1000, 1000},
		{"one cell stays under the area gate", 1, oneCell, VerdictPass, 0, 0, 31, 1000},
		{"a red strobe fails on both counts", 2, everyOther, VerdictFail, 5, 5, 1000, 213},
		{"a blue strobe passes", 3, everyOther, VerdictPass, 0, 0, 0, 72},
		{"a grey strobe fails", 4, everyOther, VerdictFail, 5, 0, 1000, 216},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := analyzeStrobe(t, tc.colour, tc.build)

			if got.Verdict != tc.verdict {
				t.Errorf("verdict = %s, want %s", got.Verdict, tc.verdict)
			}
			if got.GeneralFlashesPerSecond != tc.general {
				t.Errorf("general flashes/s = %d, want %d", got.GeneralFlashesPerSecond, tc.general)
			}
			if got.RedFlashesPerSecond != tc.red {
				t.Errorf("red flashes/s = %d, want %d", got.RedFlashesPerSecond, tc.red)
			}
			if got.MaxFlashAreaPermille != tc.area {
				t.Errorf("largest flashing area = %d per mille, want %d", got.MaxFlashAreaPermille, tc.area)
			}
			if got.MaxLuminanceDeltaPermille != tc.luminance {
				t.Errorf("largest luminance step = %d per mille, want %d",
					got.MaxLuminanceDeltaPermille, tc.luminance)
			}
			if got.FramesAnalyzed != strobeFrames {
				t.Errorf("frames analysed = %d, want %d", got.FramesAnalyzed, strobeFrames)
			}
			if got.AreaThresholdPermille != 250 || got.InkCoveragePermille != 250 ||
				got.LuminanceDeltaPermille != 100 {
				t.Errorf("recorded assumptions are area %d, ink %d, luminance bar %d; want 250, 250, 100",
					got.AreaThresholdPermille, got.InkCoveragePermille, got.LuminanceDeltaPermille)
			}
		})
	}
}

// TestAStrobeIsHalfItsFrameRate is the correction that makes a count mean
// anything: a flash is a pair of opposing transitions, so twelve alternating
// frames at 10 fps hold five flashes, not eleven transitions and not ten.
func TestAStrobeIsHalfItsFrameRate(t *testing.T) {
	if got := analyzeStrobe(t, 1, everyOther).GeneralFlashesPerSecond; got != 5 {
		t.Errorf("a 10 fps strobe counted %d general flashes per second, want 5", got)
	}
}

// TestABlueStrobeStaysVisible checks the reason the luminance step is recorded at
// all. Pure blue is not a red flash, and its step is under the 0.10 bar, so it
// passes -- but a pass that says nothing else would be indistinguishable from a
// still frame.
func TestABlueStrobeStaysVisible(t *testing.T) {
	got := analyzeStrobe(t, 3, everyOther)
	if got.Verdict != VerdictPass || got.RedFlashesPerSecond != 0 {
		t.Fatalf("a pure blue strobe gave verdict %s with %d red flashes per second, want pass and 0",
			got.Verdict, got.RedFlashesPerSecond)
	}
	if got.MaxLuminanceDeltaPermille < 60 || got.MaxLuminanceDeltaPermille > 80 {
		t.Errorf("the blue near miss reads %d per mille, want between 60 and 80",
			got.MaxLuminanceDeltaPermille)
	}
}

// TestTheDemoPasses analyses a real file. The demo is motion rather than
// flashing: a large luminance step over a small area. Its numbers, and the
// viewport they were taken at, are the reference implementation's.
func TestTheDemoPasses(t *testing.T) {
	got, err := Analyze(loadAnimation(t, "demo.nvaa"), Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if got.Verdict != VerdictPass {
		t.Errorf("verdict = %s, want pass", got.Verdict)
	}
	if got.FramesAnalyzed != 12 || got.Columns != 50 || got.Lines != 18 {
		t.Errorf("analysed %d frames at %dx%d, want 12 at 50x18 (the file's own viewport)",
			got.FramesAnalyzed, got.Columns, got.Lines)
	}
	if got.GeneralFlashesPerSecond != 0 || got.RedFlashesPerSecond != 0 {
		t.Errorf("flashes/s = %d general, %d red; want 0, 0",
			got.GeneralFlashesPerSecond, got.RedFlashesPerSecond)
	}
	if got.MaxFlashAreaPermille != 76 {
		t.Errorf("largest flashing area = %d per mille, want 76", got.MaxFlashAreaPermille)
	}
	if got.MaxLuminanceDeltaPermille != 211 {
		t.Errorf("largest luminance step = %d per mille, want 211", got.MaxLuminanceDeltaPermille)
	}
}

// TestAnExplicitWindowIsHonoured checks that an analysis can be asked about a
// different area than the file declares: the same twelve frames read differently
// through a smaller window, because the area fractions are taken over it.
func TestAnExplicitWindowIsHonoured(t *testing.T) {
	got, err := Analyze(loadAnimation(t, "demo.nvaa"), Options{Columns: 20, Lines: 8})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if got.Columns != 20 || got.Lines != 8 {
		t.Errorf("recorded viewport = %dx%d, want 20x8", got.Columns, got.Lines)
	}
	if got.MaxFlashAreaPermille != 75 || got.MaxLuminanceDeltaPermille != 168 {
		t.Errorf("area %d per mille and step %d per mille, want 75 and 168",
			got.MaxFlashAreaPermille, got.MaxLuminanceDeltaPermille)
	}
	if got.Verdict != VerdictPass {
		t.Errorf("verdict = %s, want pass", got.Verdict)
	}
}

// TestOneCellEveryFrameStepsBelowTheBar uses the fixture that changes one cell
// on every frame at 30 fps over six hundred frames. Its luminance step lands
// under the bar, so it passes with no flashes counted -- while the recorded step
// still shows the frames are not identical.
func TestOneCellEveryFrameStepsBelowTheBar(t *testing.T) {
	got, err := Analyze(loadAnimation(t, "alternating.nvaa"), Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if got.Verdict != VerdictPass || got.GeneralFlashesPerSecond != 0 {
		t.Errorf("verdict = %s with %d general flashes/s, want pass and 0",
			got.Verdict, got.GeneralFlashesPerSecond)
	}
	if got.FramesAnalyzed != 600 || got.Columns != 1 || got.Lines != 1 {
		t.Errorf("analysed %d frames at %dx%d, want 600 at 1x1",
			got.FramesAnalyzed, got.Columns, got.Lines)
	}
	if got.MaxFlashAreaPermille != 0 || got.MaxLuminanceDeltaPermille != 85 {
		t.Errorf("area %d per mille and step %d per mille, want 0 and 85",
			got.MaxFlashAreaPermille, got.MaxLuminanceDeltaPermille)
	}
}

// TestLongAnimationsAreAnalysedInConstantMemory is not a memory measurement: it
// checks the property that makes one possible, that only the previous grid is
// held. A sequence that reused one buffer would hand the analyser a grid it had
// already overwritten.
func TestLongAnimationsAreAnalysedInConstantMemory(t *testing.T) {
	var first, second Sample
	seen := 0
	for sample, err := range windowFrames(loadAnimation(t, "alternating.nvaa"), 1, 1) {
		if err != nil {
			t.Fatalf("windowFrames: %v", err)
		}
		switch seen {
		case 0:
			first = sample
		case 1:
			second = sample
		}
		seen++
		if seen == 2 {
			break
		}
	}

	if seen != 2 {
		t.Fatalf("pulled %d samples, want 2", seen)
	}
	if &first.Grid[0] == &second.Grid[0] {
		t.Error("two samples share a grid; each must be its own slice, because the analyser keeps the previous one")
	}
}

func TestStyleSignalsBlendInk(t *testing.T) {
	signals := StyleSignals(
		[]nvaa.Style{{Glyph: 0, FG: 1, BG: 0}, {Glyph: 1, FG: 1, BG: 0}},
		strobePalette, []string{" ", "#"}, DefaultInkCoverage,
	)

	if signals[0].Luminance != 0 {
		t.Errorf("a space should show its background alone, got luminance %v", signals[0].Luminance)
	}
	// A glyph covers a quarter of its cell in this model, so a white glyph on
	// black reads as a quarter of white rather than as white.
	if math.Abs(signals[1].Luminance-0.25) > 1e-12 {
		t.Errorf("a white glyph on black should read as 0.25, got %v", signals[1].Luminance)
	}
}

func TestColourMaths(t *testing.T) {
	if got := Linear(nvaa.RGB{R: 255, G: 255, B: 255}).Luminance(); math.Abs(got-1) > 1e-12 {
		t.Errorf("white has luminance %v, want 1", got)
	}
	if got := Linear(nvaa.RGB{}).Luminance(); got != 0 {
		t.Errorf("black has luminance %v, want 0", got)
	}
	if got := Linear(nvaa.RGB{B: 255}).Luminance(); math.Abs(got-0.0722) > 0.0005 {
		t.Errorf("pure blue has luminance %v, want about 0.0722", got)
	}
	if !Linear(nvaa.RGB{R: 255}).IsSaturatedRed() {
		t.Error("pure red is a saturated red")
	}
	if Linear(nvaa.RGB{B: 255}).IsSaturatedRed() {
		t.Error("pure blue is not a saturated red")
	}
	if d := Linear(nvaa.RGB{R: 255}).ChromaDistance(Linear(nvaa.RGB{})); d <= RedChromaDistance {
		t.Errorf("red against black is %v apart, want more than %v", d, RedChromaDistance)
	}
}

func TestAnalyzeRefusesWhatItCannotAnalyse(t *testing.T) {
	if _, err := Analyze(&nvaa.Animation{}, Options{}); !errors.Is(err, ErrNoFrames) {
		t.Errorf("an animation with no frames: err = %v, want ErrNoFrames", err)
	}
	// Both dimensions are needed, so a half-specified window is a mistake rather
	// than a request to derive the other half.
	if _, err := Analyze(loadAnimation(t, "demo.nvaa"), Options{Lines: 5}); !errors.Is(err, ErrBadViewport) {
		t.Errorf("a window of 0x5: err = %v, want ErrBadViewport", err)
	}
	_, err := Assess(strobe(everyFrame), strobeSignals(1), Options{})
	if !errors.Is(err, ErrBadViewport) {
		t.Errorf("an analysis with no window: err = %v, want ErrBadViewport", err)
	}
}

func TestAssessRefusesASampleThatDoesNotFit(t *testing.T) {
	short := iter.Seq2[Sample, error](func(yield func(Sample, error) bool) {
		yield(Sample{DurationMS: 100, Grid: make([]uint32, 5)}, nil)
	})
	if _, err := Assess(short, strobeSignals(1), Options{Columns: 2, Lines: 2}); !errors.Is(err, ErrBadSample) {
		t.Errorf("a grid of 5 cells in a 2x2 window: err = %v, want ErrBadSample", err)
	}

	// A file cannot name a style outside its own table, but a caller can.
	stranger := iter.Seq2[Sample, error](func(yield func(Sample, error) bool) {
		blank := make([]uint32, strobeColumns*strobeLines)
		if !yield(Sample{DurationMS: strobeMS, Grid: blank}, nil) {
			return
		}
		painted := make([]uint32, strobeColumns*strobeLines)
		painted[0] = 7
		yield(Sample{DurationMS: strobeMS, Grid: painted}, nil)
	})
	_, err := Assess(stranger, strobeSignals(1), Options{Columns: strobeColumns, Lines: strobeLines})
	if !errors.Is(err, ErrBadSample) {
		t.Errorf("a style id with no signal: err = %v, want ErrBadSample", err)
	}
}

// TestVerdictRule walks the whole decision table, because getting it wrong in the
// lenient direction is the failure mode that matters: a malformed claim must not
// read as a clearance.
func TestVerdictRule(t *testing.T) {
	boolean := func(v bool) nvaa.MetaValue {
		return nvaa.MetaValue{Type: nvaa.MetaBool, Bool: v}
	}
	text := func(s string) nvaa.MetaValue {
		return nvaa.MetaValue{Type: nvaa.MetaText, Text: s}
	}
	absent := nvaa.MetaValue{}

	cases := []struct {
		name        string
		assessed    nvaa.MetaValue
		hasAssessed bool
		verdict     nvaa.MetaValue
		hasVerdict  bool
		want        Verdict
	}{
		{"nothing at all", absent, false, absent, false, VerdictUnknown},
		{"assessed, with no verdict", boolean(true), true, absent, false, VerdictUnknown},
		{"assessed as the string true", text("true"), true, text("pass"), true, VerdictUnknown},
		{"assessed as false", boolean(false), true, text("pass"), true, VerdictUnknown},
		{"a verdict stored as a number", boolean(true), true, nvaa.MetaValue{Type: nvaa.MetaUint, Uint: 1}, true, VerdictUnknown},
		{"a verdict nothing recognises", boolean(true), true, text("maybe"), true, VerdictUnknown},
		{"assessed and passing", boolean(true), true, text("pass"), true, VerdictPass},
		{"assessed and failing", boolean(true), true, text("fail"), true, VerdictFail},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideVerdict(tc.assessed, tc.hasAssessed, tc.verdict, tc.hasVerdict)
			if got != tc.want {
				t.Errorf("verdict = %s, want %s", got, tc.want)
			}
		})
	}

	if got := ReadVerdict(nvaa.Metadata{}); got != VerdictUnknown {
		t.Errorf("an empty metadata block reads as %s, want unknown", got)
	}
}

func TestDeclaredReadsAWholeRecord(t *testing.T) {
	declared := Declared(loadAnimation(t, "demo.nvaa").Metadata)

	if declared.Verdict != VerdictPass {
		t.Errorf("verdict = %s, want pass", declared.Verdict)
	}
	if declared.Method != Method || declared.Standard != Standard {
		t.Errorf("provenance = %q / %q, want %q / %q",
			declared.Method, declared.Standard, Method, Standard)
	}
	if declared.General != 0 || declared.Red != 0 {
		t.Errorf("flashes/s = %d general, %d red; want 0, 0", declared.General, declared.Red)
	}
	if !declared.HasArea || declared.Area != 76 {
		t.Errorf("largest flashing area = %d per mille (recorded: %v), want 76 and true",
			declared.Area, declared.HasArea)
	}
	if declared.Warning != "" {
		t.Errorf("a passing file should carry no content warning, got %q", declared.Warning)
	}

	silent := Declared(loadAnimation(t, "alternating.nvaa").Metadata)
	if silent.Verdict != VerdictUnknown || silent.HasArea || silent.Method != "" {
		t.Errorf("a file that declares nothing reads as %s with area %v and method %q, want unknown, false, empty",
			silent.Verdict, silent.HasArea, silent.Method)
	}
}

// TestTheStrobeFixtureAgreesWithItself is the whole audit in one file: the record
// says the content failed, and analysing the frames reaches the same conclusion.
// The record was written by the reference implementation, so reading it back also
// checks the two implementations' vocabularies against each other.
func TestTheStrobeFixtureAgreesWithItself(t *testing.T) {
	anim := loadAnimation(t, "strobe.nvaa")

	declared := Declared(anim.Metadata)
	if declared.Verdict != VerdictFail {
		t.Errorf("declared verdict = %s, want fail", declared.Verdict)
	}
	if declared.General != 5 || declared.Red != 0 {
		t.Errorf("declared flashes/s = %d general, %d red; want 5, 0", declared.General, declared.Red)
	}
	if !declared.HasArea || declared.Area != 1000 {
		t.Errorf("declared area = %d per mille (recorded: %v), want 1000 and true",
			declared.Area, declared.HasArea)
	}
	if !strings.Contains(declared.Warning, "photosensitive epilepsy") {
		t.Errorf("declared warning = %q, want the content warning the file carries", declared.Warning)
	}

	analysed, err := Analyze(anim, Options{})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if analysed.Verdict != VerdictFail || analysed.GeneralFlashesPerSecond != 5 {
		t.Errorf("analysis verdict = %s with %d general flashes/s, want fail and 5",
			analysed.Verdict, analysed.GeneralFlashesPerSecond)
	}
	if analysed.MaxFlashAreaPermille != 1000 || analysed.MaxLuminanceDeltaPermille != 1000 {
		t.Errorf("analysis area %d per mille and step %d per mille, want 1000 and 1000",
			analysed.MaxFlashAreaPermille, analysed.MaxLuminanceDeltaPermille)
	}
	if analysed.Verdict != declared.Verdict {
		t.Errorf("the file claims %s, the analysis found %s", declared.Verdict, analysed.Verdict)
	}
}

func TestTheWarningSaysWhatItIs(t *testing.T) {
	passing := analyzeStrobe(t, 1, everyFrame)
	if !strings.Contains(passing.Warning(), "not a certification") {
		t.Errorf("a passing warning should say it is not a certification, got %q", passing.Warning())
	}

	failing := analyzeStrobe(t, 1, everyOther)
	if !strings.Contains(failing.Warning(), "5 general and 0 red") {
		t.Errorf("a failing warning should name the counts, got %q", failing.Warning())
	}
	report := failing.Report()
	for _, want := range []string{Standard, Method, "FAIL", "8x4 (32 cells)"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}
}
