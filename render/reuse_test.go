package render

import (
	"path/filepath"
	"testing"

	"github.com/WhatDamon/go-nvaa-codec"
)

// loadWhole reads one of the whole animations committed under testdata, which
// have enough frames and a large enough canvas to tell two grid buffers apart.
func loadWhole(t *testing.T, name string) *nvaa.Animation {
	t.Helper()

	anim, err := nvaa.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return anim
}

// The composer reuses its grid buffers, which is what removed the per-frame
// allocation. These tests pin the lifetime that makes the reuse safe, because
// the failure mode is quiet: a single buffer would still render every frame
// correctly and only break whoever compares one frame with the last.
//
// The contract is two buffers written alternately, so a grid stays valid until
// the call after next.

func TestGridBuffersAlternate(t *testing.T) {
	anim := loadWhole(t, "demo.nvaa")
	frames := collectFrames(t, anim)
	if len(frames) < 3 {
		t.Fatalf("fixture has %d frames, want at least 3", len(frames))
	}

	composer := NewComposer(anim)
	first := composer.Grid(frames[0], 40, 12)
	second := composer.Grid(frames[1], 40, 12)

	if first == second {
		t.Fatal("two successive grids share a buffer: differencing a frame against its predecessor would compare a buffer with itself")
	}

	// The third call returns to the first buffer, which is the documented
	// lifetime: a caller may hold a grid until the call after next.
	third := composer.Grid(frames[2], 40, 12)
	if third != first {
		t.Error("the third grid did not reuse the first buffer; the reuse rule and the documentation disagree")
	}

	// The grid built second must still be intact while the third is written.
	if second.Width != 40 || second.Height != 12 {
		t.Errorf("the previous grid was disturbed: %dx%d", second.Width, second.Height)
	}
}

// TestGridBufferFollowsTheWindowSize checks that a box change gets a grid of the
// new size rather than resizing a buffer a caller might still be holding.
func TestGridBufferFollowsTheWindowSize(t *testing.T) {
	anim := loadWhole(t, "demo.nvaa")
	frame := collectFrames(t, anim)[0]

	composer := NewComposer(anim)
	small := composer.Grid(frame, 40, 12)
	wide := composer.Grid(frame, 30, 9)

	if wide == small {
		t.Fatal("a different window size reused the same grid object")
	}
	if small.Width != 40 || small.Height != 12 {
		t.Errorf("the held grid was resized to %dx%d under its holder", small.Width, small.Height)
	}
	if wide.Width != 30 || wide.Height != 9 {
		t.Errorf("new grid is %dx%d, want 30x9", wide.Width, wide.Height)
	}
	if len(wide.Cells) != wide.Width*wide.Height {
		t.Errorf("grid has %d cells for %dx%d", len(wide.Cells), wide.Width, wide.Height)
	}
}

// collectFrames decodes a fixture's frames in order.
func collectFrames(t *testing.T, anim *nvaa.Animation) []*nvaa.Frame {
	t.Helper()

	frames := make([]*nvaa.Frame, 0, anim.FrameCount())
	for frame, err := range anim.Frames() {
		if err != nil {
			t.Fatalf("frame %d: %v", len(frames), err)
		}
		frames = append(frames, frame)
	}
	return frames
}
