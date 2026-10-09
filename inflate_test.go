package nvaa

import (
	"strings"
	"testing"

	"github.com/WhatDamon/go-nvaa-codec/internal/synth"
)

// The decoder pools the zlib reader and the buffer a payload inflates into,
// because a player asks for one of each per frame. Pooling is the one change in
// this code that can go wrong without a symptom: a reader that keeps state from
// the frame before it, or a buffer that is not emptied, produces a payload that
// decodes without complaint into the wrong cells.
//
// These tests are about that, and about the bound the pooling must not have
// loosened.

// order is a frame index list to decode in.
func order(from, to, step int) []int {
	if step == 0 {
		step = 1
	}
	var out []int
	if from <= to {
		for i := from; i <= to; i += step {
			out = append(out, i)
		}
		return out
	}
	for i := from; i >= to; i += step {
		out = append(out, i)
	}
	return out
}

// decodeIn decodes the given frames and copies out what each one painted.
func decodeIn(t *testing.T, anim *Animation, indices []int) [][]Cell {
	t.Helper()

	out := make([][]Cell, len(indices))
	for i, index := range indices {
		frame, err := anim.FrameAt(index)
		if err != nil {
			t.Fatalf("decoding frame %d: %v", index, err)
		}
		out[i] = append([]Cell(nil), frame.Payload...)
	}
	return out
}

func sameCells(a, b []Cell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDecodedPayloadsDoNotDependOnOrder is the regression for every way a pooled
// reader or buffer can leak state: the same frames, decoded backwards and
// interleaved with a different animation, must paint exactly what a forward pass
// painted.
func TestDecodedPayloadsDoNotDependOnOrder(t *testing.T) {
	first := synthetic(t, func(o *synth.Options) {
		o.Frames, o.KeyframeEvery, o.Seed = 60, 5, 7
	})
	second := synthetic(t, func(o *synth.Options) {
		// A different canvas and glyph pattern, so a stale buffer would show as a
		// mismatch rather than as a coincidentally equal payload.
		o.Frames, o.KeyframeEvery, o.Seed = 40, 3, 99
		o.Width, o.Height = 64, 20
		o.Palette, o.Styles = 8, 96
	})

	forward := decodeIn(t, first, order(0, 59, 1))
	backward := decodeIn(t, first, order(59, 0, -1))
	for i := range forward {
		if !sameCells(forward[i], backward[len(backward)-1-i]) {
			t.Fatalf("frame %d painted differently when decoded backwards", i)
		}
	}

	// Interleave the two animations: every decode in between comes from the other
	// file, so the pool has to hand back a reader in the state the new payload
	// needs rather than the one the previous frame left.
	interleaved := make([][2][]Cell, 40)
	for i := range interleaved {
		interleaved[i][0] = decodeIn(t, first, []int{i})[0]
		interleaved[i][1] = decodeIn(t, second, []int{i})[0]
	}
	for i, pair := range interleaved {
		if !sameCells(pair[0], forward[i]) {
			t.Fatalf("frame %d of the first animation changed when interleaved", i)
		}
	}
}

// TestDecompressionBombIsBounded checks the bound that keeps a few kilobytes of
// payload from asking for gigabytes: the payload must be refused, and the memory
// it asked for must not have been handed out on the way to finding that out.
func TestDecompressionBombIsBounded(t *testing.T) {
	o := synth.Video()
	o.Width, o.Height = 1, 1
	o.Frames, o.KeyframeEvery = 1, 1
	o.Body = func(int, bool) []byte {
		// Two megabytes of zeros with no cell in them: a valid stream, far past
		// the 4136 bytes a 1x1 canvas can justify.
		return make([]byte, 2<<20)
	}

	blob, err := synth.Build(o)
	if err != nil {
		t.Fatal(err)
	}
	anim, err := Parse(blob)
	if err != nil {
		t.Fatalf("parsing a container with a bomb in it: %v", err)
	}

	allocs, bytes := synth.Measure(1, func() {
		if _, err := anim.FrameAt(0); err == nil {
			t.Error("a payload that inflates past the bound decoded without error")
		}
	})
	_ = allocs

	// The bound is area*40 + 4096, so anything near the payload's real size means
	// the inflation ran to completion before the payload was rejected.
	if bytes > 1<<20 {
		t.Errorf("refusing the bomb allocated %.1f KB; the inflation was not bounded", bytes/1024)
	}
}

// TestChecksummedPayloadIsVerified pins that an inflated payload is checked.
//
// compress/zlib verifies its checksum when the stream reaches its end and says so
// in its documentation, which is why the reader is read to EOF and why Close is
// not called for it. The failure is silent otherwise: the cells would decode from
// corrupted bytes and nothing would say so.
func TestChecksummedPayloadIsVerified(t *testing.T) {
	o := synth.Video()
	o.Frames, o.KeyframeEvery = 2, 1
	o.CRC, o.Index, o.Camera = false, false, false

	blob, err := synth.Build(o)
	if err != nil {
		t.Fatal(err)
	}

	anim, err := Parse(blob)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := anim.FrameAt(0); err != nil {
		t.Fatalf("an intact payload was rejected: %v", err)
	}

	header, ok := anim.HeaderAt(0)
	if !ok || header.PayloadLength <= 4 {
		t.Fatalf("frame 0 has no deflated payload to corrupt (length %d)", header.PayloadLength)
	}

	// The last four bytes of a zlib stream are its Adler-32, so flipping one
	// leaves the compressed data intact and only the checksum wrong.
	corrupted := append([]byte(nil), blob...)
	corrupted[header.PayloadOffset+header.PayloadLength-1] ^= 0xff

	damaged, err := Parse(corrupted)
	if err != nil {
		t.Fatalf("the container no longer parses once a payload byte changed: %v", err)
	}
	_, err = damaged.FrameAt(0)
	if err == nil {
		t.Fatal("a payload with a corrupt checksum decoded without error")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("error is %q, which does not name the checksum that failed", err)
	}
}

// synthetic builds a small animation from the shared presets with overrides
// applied, which is what these tests need: containers that differ from each other
// so that a pooled buffer cannot pass by coincidence.
func synthetic(t *testing.T, override func(*synth.Options)) *Animation {
	t.Helper()

	o := synth.Video()
	override(&o)

	blob, err := synth.Build(o)
	if err != nil {
		t.Fatal(err)
	}
	anim, err := Parse(blob)
	if err != nil {
		t.Fatal(err)
	}
	return anim
}
