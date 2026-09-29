package nvaa

import (
	"math"
	"testing"
)

// loadVector reads a conformance vector.
func loadVector(t *testing.T, name string) *Animation {
	t.Helper()

	anim, err := ReadFile("testdata/vectors/" + name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return anim
}

// loadAnimation reads one of the whole animations the tests walk through.
//
// Both are committed, so a missing one is a failure: it means the checkout is
// incomplete, not that a test was skipped.
func loadAnimation(t *testing.T, name string) *Animation {
	t.Helper()

	anim, err := ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading animation %s: %v", name, err)
	}
	return anim
}

func headerList(anim *Animation) []FrameHeader {
	headers := make([]FrameHeader, 0, anim.FrameCount())
	for header := range anim.Headers() {
		headers = append(headers, header)
	}
	return headers
}

// TestTimeIndexAgreesWithAWalk checks the prefix sum against the arithmetic it
// replaces. A cached answer is only worth having if it is the same answer, and
// this is the test that would catch an index built from the wrong end.
func TestTimeIndexAgreesWithAWalk(t *testing.T) {
	for _, name := range []string{
		"minimal.nvaa",
		"keyframe-and-delta.nvaa",
		"body-gap.nvaa",
		"deflate-index-crc.nvaa",
	} {
		checkTimeIndex(t, name, loadVector(t, name))
	}
}

// TestTimeIndexOnWholeAnimations runs the same comparison over whole animations,
// where a running total has room to drift.
func TestTimeIndexOnWholeAnimations(t *testing.T) {
	for _, name := range []string{"demo.nvaa", "alternating.nvaa"} {
		checkTimeIndex(t, name, loadAnimation(t, name))
	}
}

func checkTimeIndex(t *testing.T, name string, anim *Animation) {
	t.Helper()

	headers := headerList(anim)

	var (
		total uint64
		walk  []uint64
	)
	for _, header := range headers {
		walk = append(walk, total)
		total += header.DurationMS
	}

	if got := anim.TotalDuration(); got != total {
		t.Errorf("%s: the total is %d ms, the walk says %d ms", name, got, total)
	}

	for i, header := range headers {
		if got := anim.DurationBefore(i); got != walk[i] {
			t.Fatalf("%s: frame %d starts at %d ms, the walk says %d ms", name, i, got, walk[i])
		}
		if got, want := anim.DurationThrough(i), walk[i]+header.DurationMS; got != want {
			t.Fatalf("%s: frame %d ends at %d ms, want %d ms", name, i, got, want)
		}
		// A frame owns the half-open interval that starts at it, so its own
		// start time belongs to it and not to its predecessor.
		if got := anim.FrameAtTime(walk[i]); got != i {
			t.Fatalf("%s: %d ms is frame %d, want %d", name, walk[i], got, i)
		}
	}

	// The ends, which are what a seek relies on when it is asked for a moment
	// outside the animation.
	last := anim.FrameCount() - 1
	for _, ms := range []uint64{total, total + 1, total + 3_600_000} {
		if got := anim.FrameAtTime(ms); got != last {
			t.Errorf("%s: %d ms is past the end but gave frame %d, want %d", name, ms, got, last)
		}
	}
	if got := anim.FrameAtTime(0); got != 0 {
		t.Errorf("%s: time zero is frame %d, want 0", name, got)
	}

	if got := anim.DurationBefore(-1); got != 0 {
		t.Errorf("%s: before the first frame gives %d ms, want 0", name, got)
	}
	if got := anim.DurationBefore(anim.FrameCount() + 5); got != total {
		t.Errorf("%s: past the last frame gives %d ms, want the total %d ms", name, got, total)
	}
	if got := anim.DurationThrough(-1); got != 0 {
		t.Errorf("%s: through -1 gives %d ms, want 0", name, got)
	}
	if got := anim.DurationThrough(anim.FrameCount() + 5); got != total {
		t.Errorf("%s: through the end gives %d ms, want %d ms", name, got, total)
	}
}

// TestFrameAtTimeFindsEveryFrame checks the covering rule exhaustively: the
// answer changes at frame boundaries and nowhere else.
func TestFrameAtTimeFindsEveryFrame(t *testing.T) {
	anim := loadVector(t, "keyframe-and-delta.nvaa")
	headers := headerList(anim)

	for i, header := range headers {
		start := anim.DurationBefore(i)

		if header.DurationMS > 1 {
			if got := anim.FrameAtTime(start + 1); got != i {
				t.Errorf("one millisecond into frame %d is frame %d", i, got)
			}
			if got := anim.FrameAtTime(start + header.DurationMS - 1); got != i {
				t.Errorf("the last millisecond of frame %d is frame %d", i, got)
			}
		}
		if i+1 < len(headers) {
			if got := anim.FrameAtTime(start + header.DurationMS); got != i+1 {
				t.Errorf("the millisecond frame %d ends on is frame %d, want %d", i, got, i+1)
			}
		}
	}
}

// TestAlternatingFrameDurationsDoNotDrift covers why the format stores a
// duration per frame: 30 fps is 33.33 ms, and rounding every frame the same way
// would put the picture seconds away from the music by the end.
func TestAlternatingFrameDurationsDoNotDrift(t *testing.T) {
	anim := loadAnimation(t, "alternating.nvaa")
	headers := headerList(anim)

	for i, header := range headers {
		if header.DurationMS != 33 && header.DurationMS != 34 {
			t.Fatalf("frame %d lasts %d ms, want 33 or 34", i, header.DurationMS)
		}
	}

	exact := float64(len(headers)) * 1000 / 30
	if drift := math.Abs(float64(anim.TotalDuration()) - exact); drift > 1 {
		t.Errorf("the animation runs %.0f ms against %.2f ms, drifting %.1f ms",
			float64(anim.TotalDuration()), exact, drift)
	}

	// The far end is where an off-by-one in the index would show up largest.
	last := headers[len(headers)-1]
	if got, want := anim.DurationBefore(len(headers)-1), anim.TotalDuration()-last.DurationMS; got != want {
		t.Errorf("the last frame starts at %d ms, want %d ms", got, want)
	}
}
