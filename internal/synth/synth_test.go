package synth_test

import (
	"testing"

	"github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/internal/synth"
)

// TestBuildIsParseable is the self-check that keeps the corpus honest.
//
// Parse validates the signature, the tables, every frame header, the index
// against its own walk, and the checksum, so a builder that got any of those
// wrong fails here rather than producing a benchmark that measures a broken file.
// Decoding every payload goes one step further: it is the only way to know the
// two grammars the builder writes are the two the decoder expects.
func TestBuildIsParseable(t *testing.T) {
	cases := map[string]synth.Options{
		"video":    synth.Video(),
		"long-gop": synth.LongGOP(),
		"stored": func() synth.Options {
			o := synth.Video()
			o.Deflate = false
			o.Frames, o.KeyframeEvery = 40, 4
			return o
		}(),
		"every-frame-a-keyframe": func() synth.Options {
			o := synth.Video()
			o.Frames, o.KeyframeEvery = 24, 1
			return o
		}(),
		"no-camera-no-index-no-crc": func() synth.Options {
			o := synth.Video()
			o.Camera, o.Index, o.CRC = false, false, false
			o.Frames, o.KeyframeEvery = 40, 5
			return o
		}(),
		"tiny-canvas": func() synth.Options {
			o := synth.Video()
			o.Width, o.Height = 1, 1
			o.Frames, o.KeyframeEvery, o.DeltaCells = 20, 3, 1
			return o
		}(),
		"one-frame": func() synth.Options {
			o := synth.Video()
			o.Frames, o.KeyframeEvery = 1, 1
			return o
		}(),
	}

	for name, options := range cases {
		t.Run(name, func(t *testing.T) {
			blob, err := synth.Build(options)
			if err != nil {
				t.Fatalf("building: %v", err)
			}

			anim, err := nvaa.Parse(blob)
			if err != nil {
				t.Fatalf("parsing a synthetic container: %v", err)
			}

			if got, want := anim.FrameCount(), options.Frames; got != want {
				t.Errorf("frame count = %d, want %d", got, want)
			}
			if got, want := int(anim.Width), int(options.Width); got != want {
				t.Errorf("canvas width = %d, want %d", got, want)
			}
			if got, want := len(anim.Keyframes()), keyframeCount(options); got != want {
				t.Errorf("keyframe count = %d, want %d", got, want)
			}

			// Every payload must decode, and the time index must agree with the
			// headers it was built from.
			decoded := 0
			for frame, err := range anim.Frames() {
				if err != nil {
					t.Fatalf("decoding frame %d: %v", decoded, err)
				}
				if frame == nil {
					t.Fatal("nil frame with no error")
				}
				decoded++
			}
			if decoded != anim.FrameCount() {
				t.Errorf("decoded %d frames, want %d", decoded, anim.FrameCount())
			}
			if anim.TotalDuration() == 0 {
				t.Error("total duration is zero")
			}
		})
	}
}

// keyframeCount is how many keyframes the builder is expected to place: frame 0
// plus one every KeyframeEvery, counted the way the builder counts them.
func keyframeCount(options synth.Options) int {
	return (options.Frames + options.KeyframeEvery - 1) / options.KeyframeEvery
}

// TestBuildIsDeterministic keeps the golden digests meaningful: two builds from
// the same options must be byte-identical, or a recorded digest would be a
// statement about the machine that produced it.
func TestBuildIsDeterministic(t *testing.T) {
	options := synth.Video()
	options.Frames, options.KeyframeEvery = 60, 6

	first, err := synth.Build(options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := synth.Build(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("lengths differ: %d and %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("byte %d differs", i)
		}
	}
}
