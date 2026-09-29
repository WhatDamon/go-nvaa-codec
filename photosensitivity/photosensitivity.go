// Package photosensitivity checks an animation against the flash thresholds for
// photosensitive epilepsy, and reads what a file claims about itself.
//
// The thresholds are WCAG 2.2 SC 2.3.1/2.3.2. A general flash is a pair of
// opposing changes in relative luminance of at least 10% of the maximum, with
// the darker of the two below 0.80. A red flash is a pair of opposing
// transitions involving a saturated red, more than 0.2 apart in the CIE 1976 UCS
// chromaticity diagram. Neither kind may happen more than three times in any one
// second.
//
// A glyph grid is not a pixel grid: each style is blended into a single cell
// colour, and the analysis window stands in for a 10 degree visual field that
// nobody can measure from the file. What comes out is therefore a warning rather
// than a compliance claim, and an assessment records the parameters it used so
// that a reviewer can disagree with them.
//
// The package is split the way the question is. StyleSignals reduces the palette,
// glyph and style tables to one signal per style; Assess applies the rules to a
// sequence of grids; Analyze joins the two to a decoded animation. A producer
// that has frames but no container yet can use the first two directly, and a
// consumer that only wants to know what a file claims can use ReadVerdict.
package photosensitivity

import (
	"errors"
	"fmt"
	"iter"
	"math"
	"strings"

	"github.com/WhatDamon/go-nvaa-codec"
)

const (
	// Standard and Method are the provenance of an assessment: what was applied,
	// and by what procedure. A consumer can tell an approximation from a
	// certified analysis only if the producer says which one it ran.
	Standard = "WCAG 2.2 SC 2.3.1/2.3.2"
	Method   = "nvaa.photosensitivity v1"

	// FlashLimitPerSecond permits at most three flashes of either kind within
	// any one-second period.
	FlashLimitPerSecond = 3

	// DefaultLuminanceDelta is the luminance change, as a fraction of the
	// maximum, at which a transition counts.
	DefaultLuminanceDelta = 0.10

	// DefaultDarkThreshold is the luminance the darker side of a pair must fall
	// below for the change to count.
	DefaultDarkThreshold = 0.80

	// DefaultAreaThreshold is the fraction of the window that must flash before
	// a transition counts. The rule's own figure is a quarter of a 10 degree
	// visual field; mapping that onto a cell grid is not knowable from the file,
	// so whatever is used is recorded in the assessment.
	DefaultAreaThreshold = 0.25

	// DefaultInkCoverage is the fraction of a cell covered by glyph ink, used to
	// blend a style's foreground and background into the colour a viewer sees.
	DefaultInkCoverage = 0.25

	// SaturatedRedRatio is the red share of a colour, against its total, at
	// which it counts as a saturated red.
	SaturatedRedRatio = 0.8

	// RedChromaDistance is the smallest CIE 1976 UCS distance between two
	// colours for a transition between them to count as a red flash.
	RedChromaDistance = 0.2
)

// Errors an analysis can report.
var (
	// ErrNoFrames means there was nothing to analyse.
	ErrNoFrames = errors.New("no frames to analyse")

	// ErrBadViewport means the analysis window was not at least one cell.
	ErrBadViewport = errors.New("the analysis viewport must be at least 1x1")

	// ErrBadSample means a sample does not fit the analysis: a grid of the wrong
	// size, or a style id the signal table does not cover.
	ErrBadSample = errors.New("sample does not fit the analysis")
)

// ---------------------------------------------------------------------------
// colour
// ---------------------------------------------------------------------------

// Colour is a colour in linear RGB, the space the flash rules are defined in.
type Colour struct{ R, G, B float64 }

// Linear converts an 8-bit sRGB colour to linear RGB.
func Linear(c nvaa.RGB) Colour {
	return Colour{
		R: linearize(float64(c.R) / 255),
		G: linearize(float64(c.G) / 255),
		B: linearize(float64(c.B) / 255),
	}
}

// linearize undoes the sRGB transfer function for one channel in 0..1.
func linearize(channel float64) float64 {
	if channel <= 0.04045 {
		return channel / 12.92
	}
	return math.Pow((channel+0.055)/1.055, 2.4)
}

// Luminance is the relative luminance of the colour, 0..1.
func (c Colour) Luminance() float64 { return 0.2126*c.R + 0.7152*c.G + 0.0722*c.B }

// IsSaturatedRed reports whether the colour meets the saturated-red test.
func (c Colour) IsSaturatedRed() bool {
	total := c.R + c.G + c.B
	return total > 0 && c.R/total >= SaturatedRedRatio
}

// ChromaDistance is the CIE 1976 UCS distance between two colours.
func (c Colour) ChromaDistance(other Colour) float64 {
	u1, v1 := c.uvPrime()
	u2, v2 := other.uvPrime()
	return math.Hypot(u1-u2, v1-v2)
}

// uvPrime projects linear RGB onto the CIE 1976 UCS chromaticity diagram.
func (c Colour) uvPrime() (float64, float64) {
	x := 0.4124564*c.R + 0.3575761*c.G + 0.1804375*c.B
	y := 0.2126729*c.R + 0.7151522*c.G + 0.0721750*c.B
	z := 0.0193339*c.R + 0.1191920*c.G + 0.9503041*c.B
	denominator := x + 15*y + 3*z
	if denominator <= 0 {
		return 0, 0
	}
	return 4 * x / denominator, 9 * y / denominator
}

// ---------------------------------------------------------------------------
// signals
// ---------------------------------------------------------------------------

// Signal is one style reduced to what the flash rules need: the colour a cell
// shows, and its luminance, which is kept alongside because the rules compare the
// luminance of every cell that changed.
type Signal struct {
	Colour    Colour
	Luminance float64
}

// StyleSignals resolves each style of a table into the colour it puts on screen.
//
// A glyph leaves most of its cell to the background, so the two are blended by
// how much ink the glyph lays down, and a blank glyph shows the background alone.
// Taking the three tables rather than an animation is what lets a producer assess
// frames it has not written yet.
func StyleSignals(styles []nvaa.Style, palette []nvaa.RGB, glyphs []string, inkCoverage float64) []Signal {
	signals := make([]Signal, len(styles))
	for i, style := range styles {
		background := Linear(paletteAt(palette, style.BG))
		colour := background
		if strings.TrimSpace(glyphAt(glyphs, style.Glyph)) != "" {
			foreground := Linear(paletteAt(palette, style.FG))
			colour = Colour{
				R: background.R + inkCoverage*(foreground.R-background.R),
				G: background.G + inkCoverage*(foreground.G-background.G),
				B: background.B + inkCoverage*(foreground.B-background.B),
			}
		}
		signals[i] = Signal{Colour: colour, Luminance: colour.Luminance()}
	}
	return signals
}

// paletteAt and glyphAt resolve a table entry, falling back to the empty value.
//
// A file's tables are validated before a style reaches an analyser, so these
// guards are for a caller assembling tables by hand: a cell that renders as
// nothing is a less surprising answer than a panic.
func paletteAt(palette []nvaa.RGB, id uint32) nvaa.RGB {
	if id < uint32(len(palette)) {
		return palette[id]
	}
	return nvaa.RGB{}
}

func glyphAt(glyphs []string, id uint32) string {
	if id < uint32(len(glyphs)) {
		return glyphs[id]
	}
	return ""
}

// ---------------------------------------------------------------------------
// the analysis
// ---------------------------------------------------------------------------

// Sample is one frame as an analysis sees it: how long it lasts, and the style id
// of every cell in the analysis window, row-major.
//
// The analyser compares each sample with the one before it, so a Grid must not be
// reused between samples. A producer that wants to avoid allocating one slice per
// frame can alternate between two buffers.
type Sample struct {
	DurationMS uint64
	Grid       []uint32
}

// Options are the parameters of an analysis.
//
// The zero value means the defaults, which is what an audit of a file should use
// unless it has a reason to differ: every value used is recorded in the
// assessment, so a reviewer can see what was assumed.
type Options struct {
	// Columns and Lines are the analysis window in cells. Analyze fills them in
	// from the viewport the file's own frames declare when both are zero.
	Columns, Lines int

	// AreaThreshold is the fraction of the window that must flash for a
	// transition to count. Zero means DefaultAreaThreshold.
	AreaThreshold float64

	// LuminanceDelta is the luminance change, as a fraction of the maximum, at
	// which a transition counts. Zero means DefaultLuminanceDelta.
	LuminanceDelta float64

	// DarkThreshold is the luminance the darker side of a pair must fall below.
	// Zero means DefaultDarkThreshold.
	DarkThreshold float64

	// InkCoverage is the fraction of a cell covered by glyph ink. It shapes the
	// signals StyleSignals builds and is recorded in the assessment; Assess
	// itself only records it. Zero means DefaultInkCoverage.
	InkCoverage float64
}

func (o Options) areaThreshold() float64 {
	if o.AreaThreshold == 0 {
		return DefaultAreaThreshold
	}
	return o.AreaThreshold
}

func (o Options) luminanceDelta() float64 {
	if o.LuminanceDelta == 0 {
		return DefaultLuminanceDelta
	}
	return o.LuminanceDelta
}

func (o Options) darkThreshold() float64 {
	if o.DarkThreshold == 0 {
		return DefaultDarkThreshold
	}
	return o.DarkThreshold
}

func (o Options) inkCoverage() float64 {
	if o.InkCoverage == 0 {
		return DefaultInkCoverage
	}
	return o.InkCoverage
}

// Assessment is the outcome of an analysis together with the parameters that
// produced it. A verdict without its thresholds cannot be reviewed, and a
// reviewer who disagrees with them needs the numbers to derive their own.
type Assessment struct {
	// Verdict is pass or fail. It is never unknown here: an analysis that ran
	// reached a conclusion.
	Verdict Verdict

	// FramesAnalyzed, Columns and Lines describe what was looked at.
	FramesAnalyzed int
	Columns        int
	Lines          int

	// GeneralFlashesPerSecond and RedFlashesPerSecond are the largest counts
	// found in any one-second window.
	GeneralFlashesPerSecond int
	RedFlashesPerSecond     int

	// MaxFlashAreaPermille is the largest flashing area seen, in thousandths of
	// the window.
	MaxFlashAreaPermille int

	// MaxLuminanceDeltaPermille is the largest mean luminance step over the cells
	// that moved, whether or not it cleared the bar. Keeping it visible is what
	// stops a near miss from hiding behind a bare pass: a full-screen pure-blue
	// strobe shifts relative luminance by 0.0722, under the 0.10 threshold, and
	// would otherwise read exactly like a still frame.
	MaxLuminanceDeltaPermille int

	// AreaThresholdPermille, LuminanceDeltaPermille and InkCoveragePermille are
	// the assumptions the run was made with.
	AreaThresholdPermille  int
	LuminanceDeltaPermille int
	InkCoveragePermille    int

	// Notes is a sentence describing the outcome.
	Notes string
}

// Passed reports whether the analysis found no more flashing than the limit
// allows.
func (a Assessment) Passed() bool { return a.Verdict == VerdictPass }

// Warning is the advisory an analysis would attach to the content: what a
// content warning field should say. It is worded for a viewer, and says plainly
// that an approximation ran.
func (a Assessment) Warning() string {
	if a.Passed() {
		return "No flash sequence above the WCAG 2.3.1 thresholds was detected by an " +
			"approximate analysis. This is not a certification."
	}
	return fmt.Sprintf("This animation contains flashing that exceeds the WCAG 2.3.1 "+
		"thresholds (up to %d general and %d red flashes per second). It may trigger "+
		"seizures in people with photosensitive epilepsy.",
		a.GeneralFlashesPerSecond, a.RedFlashesPerSecond)
}

// cells names a cell count, so that a one-cell window does not read as "1 cells".
func cells(count int) string {
	if count == 1 {
		return "1 cell"
	}
	return fmt.Sprintf("%d cells", count)
}

// Report renders the assessment as the block a reviewer reads.
func (a Assessment) Report() string {
	var b strings.Builder
	line := func(label string, format string, args ...any) {
		fmt.Fprintf(&b, "%-20s%s\n", label, fmt.Sprintf(format, args...))
	}

	line("standard", "%s", Standard)
	line("method", "%s", Method)
	line("verdict", "%s", strings.ToUpper(a.Verdict.String()))
	line("frames analysed", "%d", a.FramesAnalyzed)
	line("analysis viewport", "%dx%d (%s)", a.Columns, a.Lines, cells(a.Columns*a.Lines))
	line("general flashes/s", "%d (limit %d)", a.GeneralFlashesPerSecond, FlashLimitPerSecond)
	line("red flashes/s", "%d (limit %d)", a.RedFlashesPerSecond, FlashLimitPerSecond)
	line("max flashing area", "%d per mille (threshold %d)",
		a.MaxFlashAreaPermille, a.AreaThresholdPermille)
	line("max luminance step", "%d per mille over moved cells (WCAG bar is %d)",
		a.MaxLuminanceDeltaPermille, a.LuminanceDeltaPermille)
	line("ink coverage model", "%d per mille", a.InkCoveragePermille)
	line("notes", "%s", a.Notes)
	return b.String()
}

// transition is a change of direction, stamped with the time it was observed.
type transition struct {
	atMS      uint64
	direction int
}

// flashesPerSecond returns the largest number of flashes in any one-second
// window.
//
// Two corrections matter here. Consecutive same-direction entries collapse into a
// single transition, because a monotonic run is one change between a peak and a
// valley rather than one per frame. And a flash is two opposing transitions, so
// flashes are taken as non-overlapping pairs: pairing every neighbouring couple
// would count a plain alternation twice and report a 10 fps strobe as ten flashes
// a second instead of five.
func flashesPerSecond(transitions []transition) int {
	const windowMS = 1000

	turning := make([]transition, 0, len(transitions))
	for _, t := range transitions {
		if n := len(turning); n > 0 && turning[n-1].direction == t.direction {
			turning[n-1] = t
			continue
		}
		turning = append(turning, t)
	}

	// Flash i spans transitions 2i and 2i+1, so it is stamped at the odd index.
	flashes := make([]uint64, 0, len(turning)/2+1)
	for i := 1; i < len(turning); i += 2 {
		flashes = append(flashes, turning[i].atMS)
	}

	best := 0
	for i, start := range flashes {
		count := 0
		for _, at := range flashes[i:] {
			// Half-open window: exactly a second apart is not "within one second".
			if at-start >= windowMS {
				break
			}
			count++
		}
		best = max(best, count)
	}
	return best
}

// sign reports which way a summed luminance change went.
func sign(total float64) int {
	if total > 0 {
		return 1
	}
	return -1
}

// Assess applies the flash rules to a sequence of samples.
//
// Samples are consumed one at a time and only the previous grid is held on to, so
// memory follows the analysis window rather than the length of the animation. A
// sequence that reports an error ends the analysis with it.
func Assess(frames iter.Seq2[Sample, error], signals []Signal, opts Options) (Assessment, error) {
	if opts.Columns < 1 || opts.Lines < 1 {
		return Assessment{}, ErrBadViewport
	}
	window := opts.Columns * opts.Lines

	var (
		luminance        []transition
		red              []transition
		previous         []uint32
		maxFlashArea     int
		maxLuminanceStep float64
		previousRedRatio float64
		elapsedMS        uint64
		analysed         int
	)

	for sample, err := range frames {
		if err != nil {
			return Assessment{}, err
		}
		if len(sample.Grid) != window {
			return Assessment{}, fmt.Errorf("%w: a grid of %d cells does not fit %dx%d",
				ErrBadSample, len(sample.Grid), opts.Columns, opts.Lines)
		}
		analysed++

		// The first sample has no predecessor to compare against; it only
		// establishes the baseline.
		if previous != nil {
			var changed, flashing, redChanged int
			var changedTotal, signedTotal float64

			for cell, styleID := range sample.Grid {
				oldStyle := previous[cell]
				if oldStyle == styleID {
					continue
				}
				if styleID >= uint32(len(signals)) || oldStyle >= uint32(len(signals)) {
					return Assessment{}, fmt.Errorf("%w: style id %d has no signal",
						ErrBadSample, max(styleID, oldStyle))
				}
				was, now := signals[oldStyle], signals[styleID]

				delta := now.Luminance - was.Luminance
				if delta != 0 {
					changed++
					changedTotal += math.Abs(delta)
				}

				// General flash: a luminance change of at least the bar, with the
				// darker side well below maximum.
				if math.Abs(delta) >= opts.luminanceDelta() &&
					min(was.Luminance, now.Luminance) < opts.darkThreshold() {
					flashing++
					signedTotal += delta
				}

				// Red flash: a saturated red on either side, far enough from the
				// other colour in the chromaticity diagram.
				if (was.Colour.IsSaturatedRed() || now.Colour.IsSaturatedRed()) &&
					was.Colour.ChromaDistance(now.Colour) > RedChromaDistance {
					redChanged++
				}
			}

			flashArea := float64(flashing) / float64(window)
			maxFlashArea = max(maxFlashArea, int(math.RoundToEven(flashArea*1000)))
			if changed > 0 {
				// The mean step over every cell that moved at all, whether or not
				// it cleared the bar.
				maxLuminanceStep = max(maxLuminanceStep, changedTotal/float64(changed))
			}
			if flashArea >= opts.areaThreshold() && signedTotal != 0 {
				luminance = append(luminance, transition{atMS: elapsedMS, direction: sign(signedTotal)})
			}

			// How much of the frame is saturated red, which is the direction a red
			// transition is read from.
			redCells := 0
			for _, styleID := range sample.Grid {
				if styleID >= uint32(len(signals)) {
					return Assessment{}, fmt.Errorf("%w: style id %d has no signal",
						ErrBadSample, styleID)
				}
				if signals[styleID].Colour.IsSaturatedRed() {
					redCells++
				}
			}
			redRatio := float64(redCells) / float64(window)
			if float64(redChanged)/float64(window) >= opts.areaThreshold() {
				direction := -1
				if redRatio > previousRedRatio {
					direction = 1
				}
				red = append(red, transition{atMS: elapsedMS, direction: direction})
			}
			previousRedRatio = redRatio
		}

		previous = sample.Grid
		elapsedMS += sample.DurationMS
	}

	general := flashesPerSecond(luminance)
	redFlashes := flashesPerSecond(red)

	assessment := Assessment{
		FramesAnalyzed:            analysed,
		Columns:                   opts.Columns,
		Lines:                     opts.Lines,
		GeneralFlashesPerSecond:   general,
		RedFlashesPerSecond:       redFlashes,
		MaxFlashAreaPermille:      maxFlashArea,
		MaxLuminanceDeltaPermille: int(math.RoundToEven(maxLuminanceStep * 1000)),
		AreaThresholdPermille:     int(math.RoundToEven(opts.areaThreshold() * 1000)),
		LuminanceDeltaPermille:    int(math.RoundToEven(opts.luminanceDelta() * 1000)),
		InkCoveragePermille:       int(math.RoundToEven(opts.inkCoverage() * 1000)),
	}
	if general > FlashLimitPerSecond || redFlashes > FlashLimitPerSecond {
		assessment.Verdict = VerdictFail
		assessment.Notes = fmt.Sprintf("%d general and %d red flashes per second exceed the limit of %d",
			general, redFlashes, FlashLimitPerSecond)
	} else {
		assessment.Verdict = VerdictPass
		assessment.Notes = fmt.Sprintf("at most %d general and %d red flashes per second (limit %d); approximate analysis, not a certification",
			general, redFlashes, FlashLimitPerSecond)
	}
	return assessment, nil
}

// Analyze runs the flash rules over a decoded animation.
//
// The default analysis window is the viewport the file's own first frame
// declares, which is the area its frames were meant to be seen in; set Columns
// and Lines to ask about a different one.
//
// Frames are decoded, folded and analysed one at a time, so cost follows the
// canvas rather than the frame count.
func Analyze(anim *nvaa.Animation, opts Options) (Assessment, error) {
	if anim.FrameCount() == 0 {
		return Assessment{}, ErrNoFrames
	}
	if opts.Columns == 0 && opts.Lines == 0 {
		header, ok := anim.HeaderAt(0)
		if !ok {
			return Assessment{}, ErrNoFrames
		}
		opts.Columns, opts.Lines = int(header.ViewportW), int(header.ViewportH)
	}
	if opts.Columns < 1 || opts.Lines < 1 {
		return Assessment{}, ErrBadViewport
	}
	signals := StyleSignals(anim.Styles, anim.Palette, anim.Glyphs, opts.inkCoverage())
	return Assess(windowFrames(anim, opts.Columns, opts.Lines), signals, opts)
}

// windowFrames decodes an animation into samples for a fixed window.
//
// It folds each frame into one running canvas and copies the visible window out
// of it, so the sequence costs a canvas and a window rather than the animation: a
// file with thousands of frames is analysed in constant memory. Cells outside a
// frame's own viewport stay empty, which is what keeps consecutive samples
// comparable when a frame declares a smaller window than the analysis uses.
//
// Each sample gets its own grid, because the analyser keeps the previous one.
func windowFrames(anim *nvaa.Animation, columns, lines int) iter.Seq2[Sample, error] {
	return func(yield func(Sample, error) bool) {
		canvas := nvaa.NewCanvas(anim.Width, anim.Height)
		for frame, err := range anim.Frames() {
			if err != nil {
				yield(Sample{}, err)
				return
			}
			canvas.Apply(frame)

			x0, y0, width, height := nvaa.ViewportWindow(anim, frame, columns, lines)
			grid := make([]uint32, columns*lines)
			for vy := range height {
				row := vy * columns
				for vx := range width {
					grid[row+vx] = canvas.At(uint32(x0+vx), uint32(y0+vy))
				}
			}
			if !yield(Sample{DurationMS: frame.DurationMS, Grid: grid}, nil) {
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// what a file claims
// ---------------------------------------------------------------------------

// The keys of the photosensitivity record a producer may attach to a file.
const (
	keyAssessed = "epilepsy.assessed"
	keyVerdict  = "epilepsy.verdict"
	keyMethod   = "epilepsy.method"
	keyStandard = "epilepsy.standard"
	keyGeneral  = "epilepsy.general_flashes_per_second"
	keyRed      = "epilepsy.red_flashes_per_second"
	keyArea     = "epilepsy.max_flash_area_permille"
	keyWarning  = "content.warning"
)

// Verdict is the three-state reading of a photosensitivity claim, and the
// conclusion of an analysis.
//
// Unknown is not a quiet pass. A file that says nothing, or says it in a shape
// nothing can be concluded from, has not been assessed, and a consumer has to
// treat "nobody checked" differently from "checked, and found acceptable".
type Verdict int

const (
	// VerdictUnknown means no assessment can be read out of the file.
	VerdictUnknown Verdict = iota

	// VerdictPass means the content was assessed and came within the thresholds.
	VerdictPass

	// VerdictFail means the content was assessed and exceeded them.
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

// Declaration is what a file claims about its own photosensitivity risk.
type Declaration struct {
	// Verdict is pass, fail, or unknown when the file does not say.
	Verdict Verdict

	// Warning is the content warning text, when the file carries one.
	Warning string

	// Method and Standard name the procedure the producer says it ran. They are
	// what tells an approximation apart from a certified analysis.
	Method   string
	Standard string

	// General and Red are the flash counts the producer recorded, per second.
	General uint64
	Red     uint64

	// Area is the largest flashing area the producer recorded, in thousandths of
	// the viewport; HasArea is false when they did not record one.
	Area    uint64
	HasArea bool
}

// Declared reads the photosensitivity record out of a metadata block.
//
// Fields the file does not carry, or carries under a type that does not fit, are
// left at their zero values rather than guessed at.
func Declared(md nvaa.Metadata) Declaration {
	var d Declaration
	d.Verdict = ReadVerdict(md)
	d.Method = textOf(md, keyMethod)
	d.Standard = textOf(md, keyStandard)
	d.Warning = textOf(md, keyWarning)
	d.General = uintOf(md, keyGeneral)
	d.Red = uintOf(md, keyRed)
	if area, ok := md.Lookup(keyArea); ok && area.Type == nvaa.MetaUint {
		d.Area, d.HasArea = area.Uint, true
	}
	return d
}

// ReadVerdict reads a file's own verdict, as pass, fail, or unknown.
//
// The asymmetry is the whole point: a missing, mistyped or absent field yields
// unknown rather than pass, because "nobody checked" and "checked and found
// fine" are different claims. A string "true" for the assessed flag is not an
// assessment, and reading it as one would let a malformed file silence a warning
// that a well-formed one would raise.
func ReadVerdict(md nvaa.Metadata) Verdict {
	assessed, hasAssessed := md.Lookup(keyAssessed)
	verdict, hasVerdict := md.Lookup(keyVerdict)
	return decideVerdict(assessed, hasAssessed, verdict, hasVerdict)
}

// decideVerdict is the rule itself, separated from the lookup so that every
// combination of what a file might carry can be exercised directly.
func decideVerdict(assessed nvaa.MetaValue, hasAssessed bool, verdict nvaa.MetaValue, hasVerdict bool) Verdict {
	if !hasAssessed || assessed.Type != nvaa.MetaBool || !assessed.Bool {
		return VerdictUnknown
	}
	if !hasVerdict || verdict.Type != nvaa.MetaText {
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
