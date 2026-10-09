package player

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/internal/synth"
)

// Seek is what this suite exists to measure: it is the only operation whose cost
// depends on how an encoder chose to place keyframes rather than on the file's
// size alone.
//
// Every benchmark here runs against a synthetic corpus for the same reason the
// decoder's do -- the committed animations are tiny -- plus one more: the seek
// path is quadratic in GOP length across a scrub, and only a controlled GOP
// length can show that. See internal/synth.

var (
	videoOnce = sync.OnceValues(func() (*nvaa.Animation, error) {
		return parseCorpus(synth.VideoCorpus)
	})
	longGOPOnce = sync.OnceValues(func() (*nvaa.Animation, error) {
		return parseCorpus(synth.LongGOPCorpus)
	})
)

func parseCorpus(load func() (synth.Corpus, error)) (*nvaa.Animation, error) {
	built, err := load()
	if err != nil {
		return nil, err
	}
	return nvaa.Parse(built.Blob)
}

func videoAnim(tb testing.TB) *nvaa.Animation   { return animFrom(tb, videoOnce) }
func longGOPAnim(tb testing.TB) *nvaa.Animation { return animFrom(tb, longGOPOnce) }

func animFrom(tb testing.TB, load func() (*nvaa.Animation, error)) *nvaa.Animation {
	tb.Helper()

	anim, err := load()
	if err != nil {
		tb.Fatal(err)
	}
	return anim
}

// timeline starts a timeline at the first frame, which every benchmark needs and
// none of them measures.
func timeline(tb testing.TB, anim *nvaa.Animation) *Timeline {
	tb.Helper()

	tl := NewTimeline(anim, 80, 24)
	if err := tl.Start(); err != nil {
		tb.Fatal(err)
	}
	return tl
}

// BenchmarkNext is one playback frame: decode the next delta, fold it into the
// canvas, and resolve the grid the host will draw.
func BenchmarkNext(b *testing.B) {
	anim := videoAnim(b)
	tl := timeline(b, anim)
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if tl.AtEnd() {
			b.StopTimer()
			if err := tl.Restart(); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
		}
		if _, err := tl.Next(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSeekRandom is seeks that land anywhere. Each one rewinds to the
// keyframe before its target and replays forward.
func BenchmarkSeekRandom(b *testing.B) {
	anim := videoAnim(b)
	tl := timeline(b, anim)
	picks := synth.Targets(anim.FrameCount(), 1024)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; b.Loop(); i++ {
		if err := tl.Seek(picks[i%len(picks)]); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSeekForwardAdjacent steps one frame forward through Seek rather than
// Next. It is what a held-down forward key or a dragged scrubber does, and it is
// the pattern that pays for a whole GOP rewind where a single delta would do.
func BenchmarkSeekForwardAdjacent(b *testing.B) {
	anim := videoAnim(b)
	tl := timeline(b, anim)
	last := anim.FrameCount() - 1
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		next := tl.Index() + 1
		if next > last {
			b.StopTimer()
			if err := tl.Restart(); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
			next = 1
		}
		if err := tl.Seek(next); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSeekBackwardAdjacent steps one frame back. A delta cannot be undone,
// so this genuinely has to rewind -- but only to the nearest keyframe, not to
// the start of the file.
func BenchmarkSeekBackwardAdjacent(b *testing.B) {
	anim := longGOPAnim(b)
	tl := timeline(b, anim)
	last := anim.FrameCount() - 1
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		previous := tl.Index() - 1
		if previous < 0 {
			b.StopTimer()
			if err := tl.Seek(last); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
			previous = last - 1
		}
		if err := tl.Seek(previous); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPrevious is the same work through the key the player offers for it,
// so a regression in the wrapper is visible too.
func BenchmarkPrevious(b *testing.B) {
	anim := longGOPAnim(b)
	tl := timeline(b, anim)
	last := anim.FrameCount() - 1
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if tl.Index() == 0 {
			b.StopTimer()
			if err := tl.Seek(last); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
		}
		if _, err := tl.Previous(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSeekByFiveSeconds is one press of the arrow key: it travels on the
// clock rather than by frame count.
func BenchmarkSeekByFiveSeconds(b *testing.B) {
	anim := videoAnim(b)
	tl := timeline(b, anim)
	total := anim.TotalDuration()
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if tl.ElapsedMS()+SeekStepMS >= total {
			b.StopTimer()
			if err := tl.Restart(); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
		}
		if err := tl.SeekBy(SeekStepMS); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkJumpKeyframe is the bracket key. The landing frames are exactly the
// ones a rewind must decode, so this is the cheapest seek the format allows.
func BenchmarkJumpKeyframe(b *testing.B) {
	anim := videoAnim(b)
	tl := timeline(b, anim)
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		moved, err := tl.JumpKeyframe(1)
		if err != nil {
			b.Fatal(err)
		}
		if !moved {
			b.StopTimer()
			if err := tl.Restart(); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
		}
	}
}

// BenchmarkSeekHeaviestGOP is the worst case the encoder can hand a decoder: it
// alternates between the two ends of the longest gap between keyframes, so every
// iteration replays as many frames as any seek ever will.
func BenchmarkSeekHeaviestGOP(b *testing.B) {
	built, err := synth.LongGOPCorpus()
	if err != nil {
		b.Fatal(err)
	}
	start, end := built.HeaviestGOP()
	anim := longGOPAnim(b)
	tl := timeline(b, anim)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; b.Loop(); i++ {
		target := start
		if i%2 == 1 {
			target = end - 1
		}
		if err := tl.Seek(target); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSeekToEnd(b *testing.B) {
	anim := videoAnim(b)
	tl := timeline(b, anim)
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		if err := tl.SeekToEnd(); err != nil {
			b.Fatal(err)
		}
		if err := tl.Restart(); err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmarks over the real artifacts, when a checkout has them. They are not
// committed, so their absence skips rather than fails.
func BenchmarkSeekRandomReal(b *testing.B) {
	paths := synth.Artifacts("../artifacts", "../../artifacts")
	if len(paths) == 0 {
		b.Skip("no artifacts/ directory in this checkout")
	}

	for _, path := range paths {
		b.Run(filepath.Base(path), func(b *testing.B) {
			anim := readAnim(b, path)
			tl := timeline(b, anim)
			picks := synth.Targets(anim.FrameCount(), 1024)
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; b.Loop(); i++ {
				if err := tl.Seek(picks[i%len(picks)]); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkNextReal(b *testing.B) {
	paths := synth.Artifacts("../artifacts", "../../artifacts")
	if len(paths) == 0 {
		b.Skip("no artifacts/ directory in this checkout")
	}

	for _, path := range paths {
		b.Run(filepath.Base(path), func(b *testing.B) {
			anim := readAnim(b, path)
			tl := timeline(b, anim)
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				if tl.AtEnd() {
					b.StopTimer()
					if err := tl.Restart(); err != nil {
						b.Fatal(err)
					}
					b.StartTimer()
				}
				if _, err := tl.Next(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func readAnim(tb testing.TB, path string) *nvaa.Animation {
	tb.Helper()

	anim, err := nvaa.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	return anim
}

// requireRealAllocator steps aside when the race detector is on, which tracks
// every allocation: a budget is a statement about a build somebody ships, and
// that build has no detector in it.
func requireRealAllocator(t *testing.T) {
	t.Helper()

	if synth.RaceEnabled {
		t.Skip("allocation counts are not meaningful under the race detector")
	}
}

// Allocation budgets. A step decodes one frame and a seek replays a group from
// its keyframe, so each is measured per operation.
//
// Each ceiling is the baseline as measured here plus room for a different
// allocator, not a target. A step costs 45 allocations and 200 KB; a seek costs
// 110 and 785 KB, because it decodes every frame between the keyframe and the
// target and each of those allocates its own cell buffer.
const (
	seekBudgetBytes  = 1 << 20
	seekBudgetAllocs = 140
	nextBudgetBytes  = 256 << 10
	nextBudgetAllocs = 56
)

func TestSeekAllocationBudget(t *testing.T) {
	requireRealAllocator(t)

	anim := videoAnim(t)
	tl := timeline(t, anim)
	picks := synth.Targets(anim.FrameCount(), 200)

	allocs, bytes := synth.Measure(3, func() {
		for _, target := range picks {
			if err := tl.Seek(target); err != nil {
				t.Fatal(err)
			}
		}
	})
	perSeek := float64(len(picks))
	t.Logf("Seek: %.0f allocs/seek, %.1f KB/seek", allocs/perSeek, bytes/perSeek/1024)

	if got := allocs / perSeek; got > seekBudgetAllocs {
		t.Errorf("Seek allocates %.0f times, over the %d budget", got, seekBudgetAllocs)
	}
	if got := bytes / perSeek; got > seekBudgetBytes {
		t.Errorf("Seek allocates %.1f KB, over the %.1f KB budget", got/1024, float64(seekBudgetBytes)/1024)
	}
}

func TestNextAllocationBudget(t *testing.T) {
	requireRealAllocator(t)

	anim := videoAnim(t)
	tl := timeline(t, anim)

	const frames = 200
	allocs, bytes := synth.Measure(3, func() {
		if err := tl.Restart(); err != nil {
			t.Fatal(err)
		}
		for range frames {
			if _, err := tl.Next(); err != nil {
				t.Fatal(err)
			}
		}
	})
	perFrame := float64(frames)
	t.Logf("Next: %.0f allocs/frame, %.1f KB/frame", allocs/perFrame, bytes/perFrame/1024)

	if got := allocs / perFrame; got > nextBudgetAllocs {
		t.Errorf("Next allocates %.0f times per frame, over the %d budget", got, nextBudgetAllocs)
	}
	if got := bytes / perFrame; got > nextBudgetBytes {
		t.Errorf("Next allocates %.1f KB per frame, over the %.1f KB budget", got/1024, float64(nextBudgetBytes)/1024)
	}
}
