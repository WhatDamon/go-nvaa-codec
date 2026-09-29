package nvaa

import (
	"bytes"
	"runtime"
	"testing"
)

// Containers built to describe more than the bytes can hold.
//
// Every count in this format is a varint the header is free to inflate, and a
// decoder that trusts one before measuring it against the file can be made to
// reserve arbitrary memory from a few dozen bytes. These cases pin the boundary:
// a count may be large, but it may not outrun the bytes that would have to be
// there to satisfy it.

// craftOptions describes a container to assemble by hand.
type craftOptions struct {
	canvasW, canvasH uint64

	// What the header claims. Anything left at zero is claimed as one, since
	// the format requires each count to be at least one.
	claimedPalette uint64
	claimedGlyph   uint64
	claimedStyle   uint64
	claimedFrames  uint64

	// How many frame units are really written.
	frames uint64
}

// craftFile builds a minimal container: one palette entry, one glyph, one
// style, and `frames` keyframes each painting a single cell.
//
// It is deliberately independent of anything in the library that could hide a
// mistake: the bytes are laid out here from the format description, so a test
// failure means the assembler and the decoder disagree rather than that a
// shared helper is wrong.
func craftFile(o craftOptions) []byte {
	b := make([]byte, 0, 128)
	b = append(b, Signature...)
	b = append(b, Brand...)
	b = append(b, byte(Version), byte(Version>>8))
	b = append(b, 0) // no camera, index, CRC, or metadata

	b = putUvarint(b, o.canvasW)
	b = putUvarint(b, o.canvasH)

	b = append(b, 10, 0) // fps 10/1
	b = append(b, 1, 0)

	b = putUvarint(b, max(o.claimedPalette, 1))
	b = putUvarint(b, max(o.claimedGlyph, 1))
	b = putUvarint(b, max(o.claimedStyle, 1))
	b = putUvarint(b, max(o.claimedFrames, 1))

	b = append(b, 0, 0, 0) // palette: one black entry
	b = append(b, 1, ' ')  // glyphs: one space
	b = append(b, 0, 0, 0) // styles: glyph 0, fg 0, bg 0

	// One span: row 0, one span at x 0 of length 1 using style 0.
	payload := putUvarint(nil, 1) // row count
	payload = putUvarint(payload, 0)
	payload = putUvarint(payload, 1) // span count
	payload = putUvarint(payload, 0)
	payload = putUvarint(payload, 1) // run length
	payload = putUvarint(payload, 0) // style

	for range o.frames {
		b = append(b, frameKeyframe|frameSpan|frameNominal)
		b = putUvarint(b, uint64(len(payload)))
		b = append(b, payload...)
	}
	b = append(b, MarkerStreamEnd...)
	return b
}

// putUvarint appends a LEB128 encoding, the shortest form the format asks for.
func putUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v&0x7f)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// heapInUse reports the heap in use, used only to bound an allocation. It is
// read before and after a parse rather than measured precisely: the test is
// looking for an allocation proportional to a claim, which is orders of
// magnitude away from the noise here.
func heapInUse() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapInuse
}

// mustNotPanic runs fn and turns a panic into a test failure. The property under
// test is that hostile input is answered with an error, so a panic is a result
// rather than something to be caught and shrugged off.
func mustNotPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("%s panicked instead of returning an error: %v", what, p)
		}
	}()
	fn()
}

// TestCanvasAreaIsBounded covers the header that describes a grid no decoder
// could hold: two varints, a few bytes on disk, terabytes once allocated.
func TestCanvasAreaIsBounded(t *testing.T) {
	cases := []struct {
		name             string
		canvasW, canvasH uint64
	}{
		{"a million squared", 1 << 20, 1 << 20},
		{"four thousand squared", 4096, 4096},
		{"squarely past the limit", 2049, 2049},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			blob := craftFile(craftOptions{canvasW: c.canvasW, canvasH: c.canvasH, frames: 1})

			var (
				anim *Animation
				err  error
			)
			mustNotPanic(t, "Parse", func() {
				anim, err = Parse(blob)
			})
			if err == nil {
				t.Fatalf("Parse accepted a %dx%d canvas (%d cells)",
					c.canvasW, c.canvasH, anim.Width*anim.Height)
			}
		})
	}
}

// TestCanvasWithinTheLimitIsAccepted is the other half: the bound must not be so
// tight that a real animation fails it.
func TestCanvasWithinTheLimitIsAccepted(t *testing.T) {
	// The largest canvas the library uses in anger, and a grid beyond any
	// terminal, both well inside the limit.
	for _, c := range []struct{ w, h uint64 }{{128, 36}, {2048, 2048}} {
		blob := craftFile(craftOptions{canvasW: c.w, canvasH: c.h, frames: 1})
		anim, err := Parse(blob)
		if err != nil {
			t.Fatalf("Parse rejected a %dx%d canvas: %v", c.w, c.h, err)
		}
		if anim.Width != uint32(c.w) || anim.Height != uint32(c.h) {
			t.Fatalf("canvas came back as %dx%d", anim.Width, anim.Height)
		}
		// What a caller does next is allocate the canvas, so the bound has to
		// hold for that allocation too, not just for parsing.
		mustNotPanic(t, "NewCanvas", func() {
			if n := NewCanvas(anim.Width, anim.Height).CellCount(); n != c.w*c.h {
				t.Fatalf("canvas holds %d cells, want %d", n, c.w*c.h)
			}
		})
	}
}

// TestDeclaredFrameCountCannotOutrunTheFile pins the count that decides how much
// frame bookkeeping is reserved before a single byte of it has been read.
func TestDeclaredFrameCountCannotOutrunTheFile(t *testing.T) {
	cases := []struct {
		name   string
		claim  uint64
		frames uint64
	}{
		{"a thousand frames that are not there", 1000, 1},
		{"four billion honest frames", 1 << 32, 1},
		{"a trillion", 1 << 40, 1},
		{"the far end of a varint", 1 << 62, 1},
		{"the signed limit", 1 << 63, 1},
		{"the largest varint there is", ^uint64(0), 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			blob := craftFile(craftOptions{canvasW: 8, canvasH: 4, claimedFrames: c.claim, frames: c.frames})

			var err error
			mustNotPanic(t, "Parse", func() {
				_, err = Parse(blob)
			})
			if err == nil {
				t.Fatalf("Parse accepted a claim of %d frames in a %d-byte file", c.claim, len(blob))
			}
		})
	}
}

// TestATruthfulCountIsStillAccepted guards the other direction: the check must
// reject claims the bytes cannot satisfy, not merely large numbers.
func TestATruthfulCountIsStillAccepted(t *testing.T) {
	for _, frames := range []uint64{1, 2, 32, 300} {
		blob := craftFile(craftOptions{canvasW: 8, canvasH: 4, claimedFrames: frames, frames: frames})
		anim, err := Parse(blob)
		if err != nil {
			t.Fatalf("Parse rejected a truthful %d-frame file: %v", frames, err)
		}
		if got := uint64(anim.FrameCount()); got != frames {
			t.Fatalf("FrameCount is %d, want %d", got, frames)
		}
	}
}

// TestTableCountsCannotOutrunTheFile covers the tables, whose entries cost more
// in memory than they do on disk.
func TestTableCountsCannotOutrunTheFile(t *testing.T) {
	cases := []struct {
		name string
		o    craftOptions
	}{
		{"palette", craftOptions{canvasW: 8, canvasH: 4, claimedPalette: 1 << 40, frames: 1}},
		{"glyphs", craftOptions{canvasW: 8, canvasH: 4, claimedGlyph: 1 << 40, frames: 1}},
		{"styles", craftOptions{canvasW: 8, canvasH: 4, claimedStyle: 1 << 40, frames: 1}},
		{"everything at once", craftOptions{
			canvasW: 8, canvasH: 4,
			claimedPalette: 1 << 40, claimedGlyph: 1 << 40,
			claimedStyle: 1 << 40, claimedFrames: 1 << 40,
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			blob := craftFile(c.o)

			var err error
			mustNotPanic(t, "Parse", func() {
				_, err = Parse(blob)
			})
			if err == nil {
				t.Fatalf("Parse accepted table counts a %d-byte file cannot hold", len(blob))
			}
		})
	}
}

// TestHostileHeadersStayCheap gives the checks a budget: a hostile header must
// be answered quickly and without a large allocation. It is a coarse guard, but
// it fails loudly if a future count is read and trusted again.
func TestHostileHeadersStayCheap(t *testing.T) {
	blobs := [][]byte{
		craftFile(craftOptions{canvasW: 1 << 20, canvasH: 1 << 20, frames: 1}),
		craftFile(craftOptions{canvasW: 8, canvasH: 4, claimedFrames: 1 << 40, frames: 1}),
		craftFile(craftOptions{canvasW: 8, canvasH: 4, claimedStyle: 1 << 40, frames: 1}),
	}

	for i, blob := range blobs {
		before := heapInUse()
		anim, err := Parse(blob)
		after := heapInUse()

		if err == nil {
			t.Errorf("hostile blob %d was accepted (%d frames)", i, anim.FrameCount())
		}

		// A megabyte of slack: the point is to catch an allocation proportional
		// to the claim, not to measure the decoder.
		if grew := after - before; grew > 1<<20 {
			t.Errorf("blob %d grew the heap by %d bytes", i, grew)
		}
	}
}

// TestCraftIsWellFormed keeps the assembler honest: if it drifted from the
// format, every other test in this file would pass for the wrong reason.
func TestCraftIsWellFormed(t *testing.T) {
	blob := craftFile(craftOptions{canvasW: 8, canvasH: 4, claimedFrames: 3, frames: 3})

	anim, err := Parse(blob)
	if err != nil {
		t.Fatalf("a crafted file should parse: %v", err)
	}
	if anim.Width != 8 || anim.Height != 4 {
		t.Fatalf("canvas is %dx%d, want 8x4", anim.Width, anim.Height)
	}
	if anim.FrameCount() != 3 {
		t.Fatalf("frame count is %d, want 3", anim.FrameCount())
	}
	if !bytes.HasPrefix(blob, Signature) {
		t.Fatalf("signature is missing")
	}

	frame, err := anim.FrameAt(0)
	if err != nil {
		t.Fatalf("frame 0: %v", err)
	}
	if !frame.Keyframe || len(frame.Payload) != 1 {
		t.Fatalf("frame 0 is %+v, want one cell in a keyframe", frame)
	}
	if frame.Payload[0] != (Cell{X: 0, Y: 0, Style: 0}) {
		t.Fatalf("frame 0 payload is %+v", frame.Payload[0])
	}
}
