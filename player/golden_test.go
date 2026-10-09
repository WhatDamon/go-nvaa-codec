package player

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/internal/synth"
	"github.com/WhatDamon/go-nvaa-codec/render"
)

// Two tests guard the picture.
//
// TestSeekMatchesAStraightReplay states the property the seek path has to have,
// independently of how it is implemented: after any sequence of seeks and steps,
// the screen must be what replaying every frame from the start would have
// produced. That is the invariant a rewind, a fast path, or a cached composer is
// allowed to optimise but never to change.
//
// TestFrameDigestsMatchGolden pins the actual pixels over a long run, so a change
// that alters every frame in the same way -- a colour resolved differently, a
// wide glyph one column off -- fails with the frame number rather than passing a
// property test that only asks whether two halves of one implementation agree.

var updateGolden = flag.Bool("update-golden", false,
	"rewrite player/testdata/golden-digests.txt from the current renderer")

// ---- the invariant ----

// referenceGrid is the definition of the canvas at a frame: a composer that has
// been fed every frame from the start. It is deliberately the slow, obvious
// implementation -- the Timeline exists to avoid doing this, and the only thing
// that justifies avoiding it is agreeing with it.
func referenceGrid(tb *testing.T, anim *nvaa.Animation, target, columns, lines int) *render.Grid {
	tb.Helper()

	composer := render.NewComposer(anim)
	var frame *nvaa.Frame
	for i := range target + 1 {
		decoded, err := anim.FrameAt(i)
		if err != nil {
			tb.Fatalf("decoding frame %d: %v", i, err)
		}
		composer.Apply(decoded)
		frame = decoded
	}
	return composer.Grid(frame, columns, lines)
}

// op is one thing a viewer does.
type op struct {
	name string
	run  func(tl *Timeline) error
}

func replayOps(anim *nvaa.Animation) []op {
	last := anim.FrameCount() - 1
	keyframes := anim.Keyframes()

	ops := []op{
		{"start", func(tl *Timeline) error { return tl.Start() }},
		{"next", func(tl *Timeline) error { _, err := tl.Next(); return err }},
		{"previous", func(tl *Timeline) error { _, err := tl.Previous(); return err }},
		{"end", func(tl *Timeline) error { return tl.SeekToEnd() }},
		{"start again", func(tl *Timeline) error { return tl.Restart() }},
		{"half by fraction", func(tl *Timeline) error { return tl.SeekFraction(0.5) }},
		{"by time", func(tl *Timeline) error { return tl.SeekBy(1500) }},
	}

	// Every keyframe boundary and its neighbours: an off-by-one in a rewind shows
	// up here and almost nowhere else.
	for _, k := range keyframes {
		for _, delta := range []int{-1, 0, 1} {
			target := k + delta
			if target < 0 || target > last {
				continue
			}
			ops = append(ops, op{fmt.Sprintf("keyframe%d%+d", k, delta), func(tl *Timeline) error {
				return tl.Seek(target)
			}})
		}
	}

	// A spread of targets, which is what a viewer dragging a scrubber produces.
	for _, share := range []float64{0.1, 0.25, 0.75, 0.9} {
		ops = append(ops, op{fmt.Sprintf("at %.0f%%", share*100), func(tl *Timeline) error {
			return tl.SeekFraction(share)
		}})
	}

	return ops
}

func TestSeekMatchesAStraightReplay(t *testing.T) {
	for _, run := range goldenRuns(t) {
		t.Run(run.name, func(t *testing.T) {
			tl := NewTimeline(run.anim, run.columns, run.lines)

			// The reference for an index does not depend on how the player got
			// there, so each distinct landing place is computed once.
			references := map[int]*render.Grid{}
			reference := func(target int) *render.Grid {
				if grid, ok := references[target]; ok {
					return grid
				}
				grid := referenceGrid(t, run.anim, target, run.columns, run.lines)
				references[target] = grid
				return grid
			}

			for _, step := range replayOps(run.anim) {
				if err := step.run(tl); err != nil {
					t.Fatalf("%s: %v", step.name, err)
				}

				want := reference(tl.Index())
				got := tl.Grid()
				if got == nil {
					t.Fatalf("%s: no grid after landing on frame %d", step.name, tl.Index())
				}

				if geometry(got) != geometry(want) {
					t.Errorf("%s: frame %d shows %s, want %s",
						step.name, tl.Index(), geometry(got), geometry(want))
					continue
				}
				if gotDigest, wantDigest := got.Digest(), want.Digest(); gotDigest != wantDigest {
					t.Errorf("%s: frame %d digest %x, want %x",
						step.name, tl.Index(), gotDigest, wantDigest)
				}
			}
		})
	}
}

// geometry describes where the window sits, which the digest does not cover.
func geometry(grid *render.Grid) string {
	return fmt.Sprintf("%d,%d %dx%d", grid.X0, grid.Y0, grid.Width, grid.Height)
}

// ---- the golden ----

// goldenRun is one animation rendered at one box size.
type goldenRun struct {
	name    string
	anim    *nvaa.Animation
	columns int
	lines   int
}

// goldenRuns is the corpus the golden covers: the committed animations, which are
// real and small, plus trimmed synthetic ones, which are the only place a long GOP
// or a canvas larger than the box appears.
//
// The synthetic runs are trimmed relative to the benchmark corpus on purpose. The
// golden's job is coverage, not scale, and a test that allocates half a gigabyte
// to prove the same thing a shorter animation proves is a test people turn off.
func goldenRuns(tb *testing.T) []goldenRun {
	tb.Helper()

	video := synth.Video()
	video.Frames, video.KeyframeEvery = 600, 6

	longGOP := synth.LongGOP()
	longGOP.Frames, longGOP.KeyframeEvery = 300, 60

	built := func(options synth.Options) *nvaa.Animation {
		tb.Helper()
		blob, err := synth.Build(options)
		if err != nil {
			tb.Fatal(err)
		}
		anim, err := nvaa.Parse(blob)
		if err != nil {
			tb.Fatal(err)
		}
		return anim
	}

	return []goldenRun{
		{name: "demo", anim: loadDemo(tb), columns: 80, lines: 24},
		{name: "strobe", anim: loadTestAnimation(tb, "strobe.nvaa"), columns: 80, lines: 24},
		{name: "alternating", anim: loadTestAnimation(tb, "alternating.nvaa"), columns: 80, lines: 24},

		// The conformance vectors are the only place some rules appear at all: a
		// wide glyph spanning two columns, style 0 erasing a cell, the camera
		// moving, and one file per payload grammar. They are small, and they are the
		// specification's own examples.
		{name: "keyframe-and-delta", anim: loadVector(tb, "keyframe-and-delta"), columns: 80, lines: 24},
		{name: "empty-style-clears", anim: loadVector(tb, "empty-style-clears"), columns: 80, lines: 24},
		{name: "wide-glyph", anim: loadVector(tb, "wide-glyph"), columns: 80, lines: 24},
		{name: "no-camera-mode", anim: loadVector(tb, "no-camera-mode"), columns: 80, lines: 24},
		{name: "body-span", anim: loadVector(tb, "body-span"), columns: 80, lines: 24},
		{name: "body-list", anim: loadVector(tb, "body-list"), columns: 80, lines: 24},
		{name: "body-gap", anim: loadVector(tb, "body-gap"), columns: 80, lines: 24},
		{name: "deflate-index-crc", anim: loadVector(tb, "deflate-index-crc"), columns: 80, lines: 24},

		{name: "video", anim: built(video), columns: 80, lines: 24},
		{name: "video-wide", anim: built(video), columns: 220, lines: 60},
		{name: "long-gop", anim: built(longGOP), columns: 80, lines: 24},
	}
}

// loadTestAnimation reads one of the whole animations committed under testdata.
func loadTestAnimation(tb *testing.T, name string) *nvaa.Animation {
	tb.Helper()

	anim, err := nvaa.ReadFile("../testdata/" + name)
	if err != nil {
		tb.Fatalf("reading %s: %v", name, err)
	}
	return anim
}

// goldenText walks a run from its first frame to its last and describes every
// frame it sees.
//
// The case line carries a digest over every frame, so any change anywhere fails.
// The sampled lines exist so the failure can name a frame instead of a file.
func goldenText(tb *testing.T, runs []goldenRun) string {
	tb.Helper()

	var out strings.Builder
	out.WriteString("# Per-frame digests of what each animation puts on screen.\n")
	out.WriteString("# Regenerate with: go test ./player -run TestFrameDigestsMatchGolden -update-golden\n")
	out.WriteString("#\n")
	out.WriteString("# A case line's digest covers every frame in order; the frame lines that\n")
	out.WriteString("# follow are a sample, there to name a frame when a case disagrees.\n")

	for _, run := range runs {
		tl := NewTimeline(run.anim, run.columns, run.lines)
		if err := tl.Start(); err != nil {
			tb.Fatalf("%s: %v", run.name, err)
		}

		overall := sha256.New()
		var sample strings.Builder
		frames := run.anim.FrameCount()
		stride := max(1, frames/32)

		for index := 0; ; index++ {
			grid := tl.Grid()
			if grid == nil {
				tb.Fatalf("%s: no grid at frame %d", run.name, index)
			}
			digest := grid.Digest()
			overall.Write(digest[:])

			if index == 0 || index == frames-1 || index%stride == 0 {
				fmt.Fprintf(&sample, "frame %s %s %d %s %s\n",
					run.name, size(run), index, geometry(grid), hex.EncodeToString(digest[:]))
			}

			if tl.Index() >= frames-1 {
				break
			}
			advanced, err := tl.Next()
			if err != nil {
				tb.Fatalf("%s: %v", run.name, err)
			}
			if !advanced {
				break
			}
		}

		fmt.Fprintf(&out, "case %s %s frames=%d digest=%x\n", run.name, size(run), frames, overall.Sum(nil))
		out.WriteString(sample.String())
	}
	return out.String()
}

func size(run goldenRun) string { return fmt.Sprintf("%dx%d", run.columns, run.lines) }

const goldenPath = "testdata/golden-digests.txt"

func TestFrameDigestsMatchGolden(t *testing.T) {
	got := goldenText(t, goldenRuns(t))

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("creating the golden directory: %v", err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("writing the golden file: %v", err)
		}
		t.Logf("rewrote %s", goldenPath)
		return
	}

	recorded, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading the golden file (run with -update-golden to create it): %v", err)
	}
	want := string(recorded)
	if got == want {
		return
	}

	// Report the first line that differs, with the lines around it. A digest is
	// not readable, so the useful part is which frame stopped matching.
	gotLines := strings.Split(got, "\n")
	wantLines := strings.Split(want, "\n")
	for i := range max(len(gotLines), len(wantLines)) {
		var gotLine, wantLine string
		if i < len(gotLines) {
			gotLine = gotLines[i]
		}
		if i < len(wantLines) {
			wantLine = wantLines[i]
		}
		if gotLine != wantLine {
			t.Fatalf("the rendered picture changed at golden line %d:\n got: %s\nwant: %s",
				i+1, gotLine, wantLine)
		}
	}
	t.Fatal("the rendered picture changed")
}
