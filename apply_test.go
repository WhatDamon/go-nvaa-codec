package nvaa

import (
	"testing"

	"github.com/WhatDamon/go-nvaa-codec/internal/synth"
)

// The applier keeps one cell buffer and writes every frame into it. Both ways
// that can go wrong are silent: a buffer that is not emptied writes the previous
// frame's cells onto the canvas as if this frame had painted them, and a buffer
// left half-written by a failed frame puts the wreckage in front of the next
// frame's cells. Neither would raise an error anywhere.

// applyOrder puts a whole-canvas keyframe next to small deltas in both
// directions, which is what makes a stale prefix visible in the length.
var applyOrder = []int{0, 1, 4, 5, 8, 2, 11, 3}

func TestApplierDoesNotCarryCellsBetweenFrames(t *testing.T) {
	anim := synthetic(t, func(o *synth.Options) {
		o.Frames, o.KeyframeEvery = 12, 4
	})

	applier := NewFrameApplier()
	canvas := NewCanvas(anim.Width, anim.Height)

	for _, index := range applyOrder {
		got, err := applier.Apply(anim, index, canvas)
		if err != nil {
			t.Fatalf("applying frame %d: %v", index, err)
		}
		want, err := anim.FrameAt(index)
		if err != nil {
			t.Fatal(err)
		}

		if len(got.Payload) != len(want.Payload) {
			t.Fatalf("frame %d has %d cells through the applier and %d through FrameAt",
				index, len(got.Payload), len(want.Payload))
		}
		for i := range got.Payload {
			if got.Payload[i] != want.Payload[i] {
				t.Fatalf("frame %d cell %d is %+v through the applier and %+v through FrameAt",
					index, i, got.Payload[i], want.Payload[i])
			}
		}
	}
}

// TestApplierSurvivesAFailedFrame checks that a frame which cannot be decoded
// leaves nothing behind for the next one to fold in.
func TestApplierSurvivesAFailedFrame(t *testing.T) {
	anim := synthetic(t, func(o *synth.Options) {
		o.Frames, o.KeyframeEvery = 6, 2
		o.Body = func(frame int, keyframe bool) []byte {
			if frame == 2 {
				// A position-delta body claiming eight cells and carrying none.
				return []byte{0x08}
			}
			return nil
		}
	})

	applier := NewFrameApplier()
	canvas := NewCanvas(anim.Width, anim.Height)

	for _, index := range []int{0, 1} {
		if _, err := applier.Apply(anim, index, canvas); err != nil {
			t.Fatalf("applying frame %d: %v", index, err)
		}
	}
	if _, err := applier.Apply(anim, 2, canvas); err == nil {
		t.Fatal("a body claiming cells it does not carry decoded without error")
	}

	got, err := applier.Apply(anim, 3, canvas)
	if err != nil {
		t.Fatalf("applying frame 3 after a failure: %v", err)
	}
	want, err := anim.FrameAt(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Payload) != len(want.Payload) {
		t.Fatalf("frame 3 has %d cells after a failure and %d from a clean decode",
			len(got.Payload), len(want.Payload))
	}
}
