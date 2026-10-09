// Package synth builds NVAA containers for benchmarks and golden tests.
//
// It exists because the committed corpus is tiny -- the largest whole animation
// in testdata is 6 KB -- and performance work cannot be measured on 12-frame
// files. Checking a multi-megabyte animation into the repository would work, but
// the file would be opaque: a benchmark that is slow because a canvas is 200x56
// or a GOP is 60 frames long should say so in code, not in a binary blob.
//
// The output is written against the same specification the decoder reads, and
// every builder is verified by parsing what it produced (see the tests). It is
// deliberately not an encoder: it writes the shapes the format allows, in the
// proportions real footage uses, and nothing else.
//
// Nothing outside tests imports this, so it is not part of the library's API.
package synth

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math/rand"
	"sort"
)

// Container constants, mirroring the codec's own definitions.
const (
	signature = "\x89NVAA\r\n\x1a\n"
	brand     = "NVAA"
	version   = 1

	markerFrameSync = "\x00\x00NVFR"
	markerIndex     = "\x00\x00NVID"
	markerCRC       = "\x00\x00NVCC"
	markerStreamEnd = "\x00\x00NVEE"

	headerHasCamera = 0x01
	headerHasIndex  = 0x02
	headerHasCRC    = 0x04

	frameKeyframe = 0x01
	frameCamera   = 0x02
	frameDeflated = 0x08
	frameSpan     = 0x10
	frameNominal  = 0x20
	frameGap      = 0x40
)

// glyphTable is the glyph set every synthetic file is built from.
//
// The two double-width entries are not decoration: a wide glyph pair is the one
// place where resolving a grid is not a straight copy, and a corpus that never
// contains one would not exercise the continuation path it is meant to protect.
var glyphTable = []string{" ", ".", ":", "#", "@", "░", "▒", "█", "─", "世", "あ"}

// Options describe one synthetic animation.
//
// Start from Video or LongGOP and adjust. Counts left at zero take the preset's
// value, so a caller only states what it cares about.
type Options struct {
	// Width and Height are the canvas, in cells.
	Width, Height uint32

	// Frames is the total frame count.
	Frames int

	// KeyframeEvery is the GOP length. Every Nth frame is a keyframe, so
	// KeyframeEvery 1 means every frame is one.
	KeyframeEvery int

	// FPSNum and FPSDen set the nominal frame interval.
	FPSNum, FPSDen uint16

	// Palette, Glyphs and Styles set the table sizes. Glyphs is capped at the
	// size of the built-in glyph table.
	Palette, Glyphs, Styles int

	// DeltaCells is how many cells a delta frame changes. A keyframe always
	// paints the whole canvas.
	DeltaCells int

	// Deflate compresses every payload with zlib, which is what real encoders do
	// and what the decoder's hot path exists for.
	Deflate bool

	// Camera writes camera deltas on keyframes, so the visible-window path is
	// exercised rather than sitting at the origin.
	Camera bool

	// Index and CRC add the optional trailer blocks. Parse validates both against
	// what it found on its own walk, so a builder that got them wrong fails
	// loudly at the first read rather than at benchmark time.
	Index bool
	CRC   bool

	// Seed makes the deltas reproducible.
	Seed int64

	// Body, when set, replaces the pre-compression body of the frame it is asked
	// about, and returning nil for a frame leaves that frame to the builder.
	//
	// It exists so a test can put a payload the builder would never write into an
	// otherwise valid container -- one that inflates past the decoder's bound, one
	// whose checksum is wrong, one that claims cells it does not carry -- and check
	// what the decoder does with it. The grammar flags and the compression still
	// follow the options, so the body it returns is read as the grammar the frame
	// claims.
	Body func(frame int, keyframe bool) []byte
}

// Video is a preset shaped like the real footage this library has been pointed
// at: a 200x56 canvas, 30 fps, a keyframe every six frames, and deltas that touch
// a few hundred cells.
//
// The proportions come from the largest artifacts: a keyframe paints the entire
// canvas, which is where a seek spends most of its time, and the GOPs are short,
// so one seek is one keyframe plus a handful of deltas.
func Video() Options {
	return Options{
		Width:         200,
		Height:        56,
		Frames:        3000,
		KeyframeEvery: 6,
		FPSNum:        30,
		FPSDen:        1,
		Palette:       16,
		Glyphs:        11,
		Styles:        640,
		DeltaCells:    400,
		Deflate:       true,
		Camera:        true,
		Index:         true,
		CRC:           true,
		Seed:          1,
	}
}

// LongGOP is the same canvas with a keyframe every 60 frames, which is the worst
// case a seek can meet: the replay after a rewind is as long as an encoder is
// allowed to make it.
func LongGOP() Options {
	o := Video()
	o.Frames = 1200
	o.KeyframeEvery = 60
	return o
}

func (o Options) withDefaults() Options {
	preset := Video()
	if o.Width == 0 {
		o.Width = preset.Width
	}
	if o.Height == 0 {
		o.Height = preset.Height
	}
	if o.Frames <= 0 {
		o.Frames = preset.Frames
	}
	if o.KeyframeEvery <= 0 {
		o.KeyframeEvery = preset.KeyframeEvery
	}
	if o.FPSNum == 0 || o.FPSDen == 0 {
		o.FPSNum, o.FPSDen = preset.FPSNum, preset.FPSDen
	}
	if o.Palette <= 0 {
		o.Palette = preset.Palette
	}
	if o.Glyphs <= 0 {
		o.Glyphs = preset.Glyphs
	}
	if o.Styles <= 0 {
		o.Styles = preset.Styles
	}
	if o.DeltaCells < 0 {
		o.DeltaCells = 0
	}
	o.Glyphs = min(o.Glyphs, len(glyphTable))
	return o
}

// Build renders a complete .nvaa container.
func Build(o Options) ([]byte, error) {
	o = o.withDefaults()
	rng := rand.New(rand.NewSource(o.Seed))

	var out bytes.Buffer
	out.WriteString(signature)
	out.WriteString(brand)
	putUint16(&out, version)

	flags := uint8(0)
	if o.Camera {
		flags |= headerHasCamera
	}
	if o.Index {
		flags |= headerHasIndex
	}
	if o.CRC {
		flags |= headerHasCRC
	}
	out.WriteByte(flags)

	putUvarint(&out, uint64(o.Width))
	putUvarint(&out, uint64(o.Height))
	putUint16(&out, o.FPSNum)
	putUint16(&out, o.FPSDen)

	putUvarint(&out, uint64(o.Palette))
	putUvarint(&out, uint64(o.Glyphs))
	putUvarint(&out, uint64(o.Styles))
	putUvarint(&out, uint64(o.Frames))

	writePalette(&out, o.Palette)
	writeGlyphs(&out, o.Glyphs)
	writeStyles(&out, o.Styles, o.Glyphs, o.Palette)

	nominalMS := (uint64(o.FPSDen)*1000 + uint64(o.FPSNum)/2) / uint64(o.FPSNum)

	// An encoder is allowed to shrink the viewport, but every real file seen so
	// far uses the whole canvas, so the builder does too.
	viewportW, viewportH := int(o.Width), int(o.Height)

	// rows and deltas are reused across frames: the generator is not the thing
	// under measurement, but a 3000-frame corpus built with a fresh allocation per
	// frame would still be needlessly slow to produce.
	rows := make([]byte, 0, 16<<10)
	deltas := make([]byte, 0, 4<<10)

	offsets := make([]int, 0, o.Frames)
	cameraX, cameraY := 0, 0

	for frame := range o.Frames {
		// The index entry points at the sync marker when there is one, so the
		// offset is recorded before the marker is written.
		offsets = append(offsets, out.Len())

		keyframe := frame%o.KeyframeEvery == 0
		var frameFlags uint8
		if keyframe {
			out.WriteString(markerFrameSync)
			frameFlags |= frameKeyframe
			// A keyframe repaints rows of runs; a delta names changed positions one
			// at a time. The two grammar bits select exactly that, so which one is
			// set follows from which body the frame carries.
			frameFlags |= frameSpan
		} else {
			frameFlags |= frameGap
		}

		// Most frames carry no duration and take the nominal interval; the rest
		// state one, and a couple state a longer one, so both paths are exercised
		// and a decoder that rounds cannot drift unnoticed.
		explicit := frame%7 == 0
		if !explicit {
			frameFlags |= frameNominal
		}

		cameraXNext, cameraYNext := cameraX, cameraY
		if o.Camera && keyframe {
			cameraXNext, cameraYNext = cameraFor(frame, int(o.Width)-viewportW, int(o.Height)-viewportH)
		}

		var payload []byte
		var err error
		if o.Body != nil {
			payload = o.Body(frame, keyframe)
		}
		switch {
		case payload != nil:
			// The override replaces this frame's body and nothing else.
		case keyframe:
			payload, err = keyframePayload(o, frame, rows)
		default:
			payload, err = deltaPayload(o, frame, rng, deltas)
		}
		if err != nil {
			return nil, err
		}

		if o.Deflate {
			if payload, err = deflate(payload); err != nil {
				return nil, err
			}
			frameFlags |= frameDeflated
		}

		if cameraXNext != cameraX || cameraYNext != cameraY {
			frameFlags |= frameCamera
		}

		out.WriteByte(frameFlags)
		switch {
		case !explicit:
			// Nothing: NOMINAL carries the interval.
		case frame%14 == 0:
			putUvarint(&out, nominalMS+1)
		default:
			putUvarint(&out, nominalMS)
		}

		if frameFlags&frameCamera != 0 {
			putSvarint(&out, int64(cameraXNext-cameraX))
			putSvarint(&out, int64(cameraYNext-cameraY))
			cameraX, cameraY = cameraXNext, cameraYNext
		}

		putUvarint(&out, uint64(len(payload)))
		out.Write(payload)
	}

	if o.Index {
		out.WriteString(markerIndex)
		putUvarint(&out, uint64(len(offsets)))
		for _, off := range offsets {
			putUint32(&out, uint32(off))
		}
	}
	if o.CRC {
		// The checksum covers everything before the marker, which is why it is
		// computed before the marker is written.
		sum := crc32.ChecksumIEEE(out.Bytes())
		out.WriteString(markerCRC)
		putUint32(&out, sum)
	}
	out.WriteString(markerStreamEnd)
	return out.Bytes(), nil
}

// cameraFor walks the camera slowly around the canvas so the visible window
// moves between GOPs instead of sitting at the origin.
func cameraFor(frame, spanX, spanY int) (int, int) {
	if spanX < 0 {
		spanX = 0
	}
	if spanY < 0 {
		spanY = 0
	}
	step := frame / 24
	return (step * 3) % (spanX + 1), (step * 2) % (spanY + 1)
}

// keyframePayload paints the whole canvas in the span grammar, one entry per row.
//
// scratch is reused between calls; the returned slice aliases it, so the caller
// must consume it before the next frame is built.
func keyframePayload(o Options, frame int, scratch []byte) ([]byte, error) {
	w, h := int(o.Width), int(o.Height)
	buf := bytes.NewBuffer(scratch[:0])

	putUvarint(buf, uint64(h))
	for y := range h {
		if err := writeSpanRow(buf, uint64(y), w, o.Styles, frame, y); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// deltaPayload changes a scattered set of cells in the position-delta grammar.
//
// scratch is reused between calls; the returned slice aliases it.
func deltaPayload(o Options, frame int, rng *rand.Rand, scratch []byte) ([]byte, error) {
	w, h := int(o.Width), int(o.Height)
	count := min(o.DeltaCells, w*h)

	// Positions are chosen without replacement and sorted, which is exactly what
	// the gap grammar can express. The permutation is drawn from the reused
	// generator so the corpus is reproducible without a fresh RNG per frame.
	positions := rng.Perm(w * h)[:count]
	sort.Ints(positions)

	buf := bytes.NewBuffer(scratch[:0])
	putUvarint(buf, uint64(count))

	previous := -1
	for _, pos := range positions {
		gap := pos
		if previous >= 0 {
			gap = pos - previous - 1
		}
		previous = pos
		putUvarint(buf, uint64(gap))

		// Style 0 erases a cell, which the format makes a first-class write
		// rather than an omission, so the corpus has to contain some.
		style := (pos*7 + frame*11) % o.Styles
		if (pos+frame)%13 == 0 {
			style = 0
		}
		putUvarint(buf, uint64(style))
	}
	return buf.Bytes(), nil
}

// writeSpanRow writes one row as runs of one style.
func writeSpanRow(buf *bytes.Buffer, y uint64, width, styleCount, frame, row int) error {
	putUvarint(buf, y)

	// The run count precedes the runs, so the rows are measured before any of
	// them is written. Runs are short because the pattern changes every few
	// columns, which is what gives the span grammar's per-row structure a cost.
	type span struct{ x, width, style int }
	runs := make([]span, 0, width/2+1)

	runStart, runStyle := 0, styleAt(styleCount, 0, row, frame)
	for x := 1; x <= width; x++ {
		next := runStyle
		if x < width {
			next = styleAt(styleCount, x, row, frame)
		}
		if x == width || next != runStyle {
			runs = append(runs, span{x: runStart, width: x - runStart, style: runStyle})
			runStart, runStyle = x, next
		}
	}

	putUvarint(buf, uint64(len(runs)))
	for _, r := range runs {
		putUvarint(buf, uint64(r.x))
		putUvarint(buf, uint64(r.width))
		putUvarint(buf, uint64(r.style))
	}
	return nil
}

// styleAt is the deterministic pattern a keyframe paints. Zero is the empty
// style, so the pattern deliberately leaves holes.
func styleAt(styleCount, x, y, frame int) int {
	if styleCount <= 0 {
		return 0
	}
	if (x*y+frame)%17 == 0 {
		return 0
	}
	return (x/3 + y + frame) % styleCount
}

func writePalette(buf *bytes.Buffer, count int) {
	for i := range count {
		buf.Write([]byte{byte(i * 17), byte(i * 31), byte(i * 47)})
	}
}

func writeGlyphs(buf *bytes.Buffer, count int) {
	for i := range count {
		glyph := glyphTable[i%len(glyphTable)]
		putUvarint(buf, uint64(len(glyph)))
		buf.WriteString(glyph)
	}
}

func writeStyles(buf *bytes.Buffer, count, glyphs, palette int) {
	for i := range count {
		putUvarint(buf, uint64(i%glyphs))
		putUvarint(buf, uint64(i%palette))
		putUvarint(buf, uint64((i/3)%palette))
	}
}

// deflate compresses one payload the way an encoder does.
//
// BestSpeed rather than BestCompression: the corpus exists to be decoded
// thousands of times, and spending seconds compressing it once buys the decoder
// nothing it can feel.
func deflate(payload []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := zlib.NewWriterLevel(&buf, zlib.BestSpeed)
	if err != nil {
		return nil, fmt.Errorf("synth: opening a compressor: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return nil, fmt.Errorf("synth: compressing a payload: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("synth: closing a compressor: %w", err)
	}
	return buf.Bytes(), nil
}

func putUvarint(buf *bytes.Buffer, v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	buf.Write(tmp[:n])
}

func putSvarint(buf *bytes.Buffer, v int64) {
	putUvarint(buf, uint64(v<<1)^uint64(v>>63))
}

func putUint16(buf *bytes.Buffer, v uint16) {
	var tmp [2]byte
	binary.LittleEndian.PutUint16(tmp[:], v)
	buf.Write(tmp[:])
}

func putUint32(buf *bytes.Buffer, v uint32) {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	buf.Write(tmp[:])
}
