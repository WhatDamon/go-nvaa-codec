package player

import (
	"testing"

	"github.com/WhatDamon/go-nvaa-codec/render"
)

// snapshot is a frame's screen copied out of the composer, so a comparison is
// against what was on screen rather than against whatever the composer's buffers
// hold now.
type snapshot struct {
	width, height int
	cells         []render.Cell
}

func takeSnapshot(grid *render.Grid) snapshot {
	return snapshot{
		width:  grid.Width,
		height: grid.Height,
		cells:  append([]render.Cell(nil), grid.Cells...),
	}
}

// changedCells is what the meter's own differencing counts: every cell when the
// geometry moved, otherwise the positions whose appearance moved.
func changedCells(before, after snapshot) int {
	if before.width != after.width || before.height != after.height {
		return after.width * after.height
	}

	changed := 0
	for i := range after.cells {
		a, b := before.cells[i], after.cells[i]
		if a.Glyph != b.Glyph || a.FG != b.FG || a.BG != b.BG {
			changed++
		}
	}
	return changed
}

// TestMeterDiffsAcrossReusedGridBuffers is the regression that makes reusing the
// composer's grids safe.
//
// The failure it guards against is quiet: a composer that wrote every frame into
// one buffer would still draw each frame correctly, and the meter would simply
// report that nothing ever changed, because it differences each frame against the
// previous one and would be comparing a buffer with itself. Nothing else in the
// test suite would notice.
func TestMeterDiffsAcrossReusedGridBuffers(t *testing.T) {
	anim := loadDemo(t)
	tl := NewTimeline(anim, 60, 18)
	if err := tl.Start(); err != nil {
		t.Fatal(err)
	}

	meter := NewMeter()
	var observed []snapshot

	for range 6 {
		grid := tl.Grid()
		if grid == nil {
			t.Fatalf("no grid after %d frames", len(observed))
		}
		observed = append(observed, takeSnapshot(grid))
		meter.Observe(grid)

		if tl.AtEnd() {
			break
		}
		if _, err := tl.Next(); err != nil {
			t.Fatal(err)
		}
	}

	if len(observed) < 3 {
		t.Fatalf("only %d frames observed; too few to tell two buffers apart", len(observed))
	}

	// The first frame has nothing before it, so every cell of it is new; the rest
	// are compared with their predecessor.
	want := observed[0].width * observed[0].height
	for i := 1; i < len(observed); i++ {
		want += changedCells(observed[i-1], observed[i])
	}

	changed := want - observed[0].width*observed[0].height
	if changed == 0 {
		t.Fatal("no cell ever changed; this fixture cannot detect a shared buffer")
	}
	if meter.DirtyCells != want {
		t.Errorf("meter counted %d changed cells, want %d -- successive grids are sharing a buffer",
			meter.DirtyCells, want)
	}
}

// TestTimelineFramePayloadMatchesAFreshDecode pins the other half of the reuse
// contract: Timeline.Frame's payload is the applier's buffer, so it must hold the
// cells of the frame the timeline is actually on -- not the ones it replayed on
// the way there, and not a prefix of them.
func TestTimelineFramePayloadMatchesAFreshDecode(t *testing.T) {
	anim := loadDemo(t)
	tl := NewTimeline(anim, 60, 18)
	if err := tl.Start(); err != nil {
		t.Fatal(err)
	}

	steps := []func() error{
		func() error { return tl.Seek(0) },
		func() error { _, err := tl.Next(); return err },
		func() error { _, err := tl.Next(); return err },
		func() error { _, err := tl.Previous(); return err },
		func() error { return tl.SeekToEnd() },
		func() error { return tl.Seek(4) },
		func() error { _, err := tl.Next(); return err },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}

		got := tl.Frame()
		if got == nil {
			t.Fatalf("step %d: no frame on screen", i)
		}
		if got.Index != tl.Index() {
			t.Fatalf("step %d: frame reports index %d, timeline is on %d", i, got.Index, tl.Index())
		}

		want, err := anim.FrameAt(got.Index)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Payload) != len(want.Payload) {
			t.Fatalf("step %d: frame %d has %d cells, want %d",
				i, got.Index, len(got.Payload), len(want.Payload))
		}
		for c := range got.Payload {
			if got.Payload[c] != want.Payload[c] {
				t.Fatalf("step %d: frame %d cell %d is %+v, want %+v",
					i, got.Index, c, got.Payload[c], want.Payload[c])
			}
		}

		// The geometry is a value rather than a buffer, so it stays true even
		// after the next step overwrites the cells.
		if got.ViewportW != want.ViewportW || got.CameraX != want.CameraX {
			t.Fatalf("step %d: frame geometry is %d/%d, want %d/%d",
				i, got.ViewportW, got.CameraX, want.ViewportW, want.CameraX)
		}
	}
}
