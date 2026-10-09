package render

import (
	"sync"
	"testing"

	"github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/internal/synth"
)

// The renderer is measured against the synthetic corpus rather than the
// committed vectors because resolving a grid is a per-cell cost: a 100x30
// fixture pays it 3000 times, and a real animation pays it 11200 times per
// keyframe. See internal/synth.

var videoOnce = sync.OnceValues(func() (*nvaa.Animation, error) {
	built, err := synth.VideoCorpus()
	if err != nil {
		return nil, err
	}
	return nvaa.Parse(built.Blob)
})

func videoAnim(tb testing.TB) *nvaa.Animation {
	tb.Helper()

	anim, err := videoOnce()
	if err != nil {
		tb.Fatal(err)
	}
	return anim
}

// heaviestKeyframe is the frame a grid is most expensive to resolve: a keyframe
// that paints the whole canvas, at the start of the longest GOP.
func heaviestKeyframe(tb testing.TB, anim *nvaa.Animation) *nvaa.Frame {
	tb.Helper()

	keyframes := anim.Keyframes()
	if len(keyframes) == 0 {
		tb.Fatal("the synthetic corpus has no keyframes")
	}
	frame, err := anim.FrameAt(keyframes[len(keyframes)/2])
	if err != nil {
		tb.Fatal(err)
	}
	return frame
}

func BenchmarkGrid(b *testing.B) {
	anim := videoAnim(b)
	composer := NewComposer(anim)
	frame := heaviestKeyframe(b, anim)
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		synth.Sink = composer.Grid(frame, 80, 24)
	}
}

// BenchmarkGridFullWindow resolves the whole canvas instead of the 80x24 box the
// player usually has, which is what a host composing into a large pane asks for.
func BenchmarkGridFullWindow(b *testing.B) {
	anim := videoAnim(b)
	composer := NewComposer(anim)
	frame := heaviestKeyframe(b, anim)
	columns, lines := int(anim.Width), int(anim.Height)
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		synth.Sink = composer.Grid(frame, columns, lines)
	}
}

// BenchmarkApply is the fold half of a replay: the payloads are decoded outside
// the timer so the measurement is the canvas write and nothing else.
func BenchmarkApply(b *testing.B) {
	anim := videoAnim(b)
	keyframes := anim.Keyframes()
	start := keyframes[len(keyframes)/2]
	end := min(start+12, anim.FrameCount())

	frames := make([]*nvaa.Frame, 0, end-start)
	for i := start; i < end; i++ {
		frame, err := anim.FrameAt(i)
		if err != nil {
			b.Fatal(err)
		}
		frames = append(frames, frame)
	}

	composer := NewComposer(anim)
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		for _, frame := range frames {
			composer.Apply(frame)
		}
	}
}

func BenchmarkFullRepaint(b *testing.B) {
	anim := videoAnim(b)
	grid := NewComposer(anim).Grid(heaviestKeyframe(b, anim), 80, 24)
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		synth.SinkText = grid.FullRepaint()
	}
}

func BenchmarkBox(b *testing.B) {
	anim := videoAnim(b)
	grid := NewComposer(anim).Grid(heaviestKeyframe(b, anim), 80, 24)
	fill := Fill{Opaque: true}
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		synth.SinkText = grid.Box(80, 24, fill)
	}
}

func BenchmarkDigest(b *testing.B) {
	anim := videoAnim(b)
	grid := NewComposer(anim).Grid(heaviestKeyframe(b, anim), 80, 24)
	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		digest := grid.Digest()
		// Read two ends of it so the compiler cannot decide the digest is unused;
		// assigning the array to an interface would box 32 bytes per iteration.
		synth.SinkInt = int(digest[0]) ^ int(digest[len(digest)-1])
	}
}

// Allocation budgets. Resolving a grid is on the per-frame path of a player that
// wants to hold 60 frames a second, so what it allocates matters as much as how
// long it takes.
//
// Grid resolution now reuses the composer's buffers: measured on an M4 with
// go1.27 it allocates nothing per call and takes 7.3µs, down from 27 allocations,
// 66 KB and 12.3µs. The ceiling allows for one allocation, which is what the
// first call of a new window size costs, and no more.
const (
	gridBudgetBytes  = 4 << 10
	gridBudgetAllocs = 2
)

func TestGridAllocationBudget(t *testing.T) {
	if synth.RaceEnabled {
		t.Skip("allocation counts are not meaningful under the race detector")
	}

	anim := videoAnim(t)
	composer := NewComposer(anim)
	frame := heaviestKeyframe(t, anim)

	// The first call allocates the two reused grids; the budget is about every
	// call after it, which is what the measurement's discarded warm-up run covers.
	allocs, bytes := synth.Measure(200, func() {
		synth.Sink = composer.Grid(frame, 80, 24)
	})
	t.Logf("Grid 80x24: %.1f allocs/op, %.1f KB/op", allocs, bytes/1024)

	if allocs > gridBudgetAllocs {
		t.Errorf("Grid allocates %.0f times, over the %d budget", allocs, gridBudgetAllocs)
	}
	if bytes > gridBudgetBytes {
		t.Errorf("Grid allocates %.1f KB, over the %.1f KB budget", bytes/1024, float64(gridBudgetBytes)/1024)
	}
}
