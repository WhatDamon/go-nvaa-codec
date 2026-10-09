package player

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/WhatDamon/go-nvaa-codec"
)

// Phase 2 gave Seek two ways to reach a frame without replaying a group from its
// keyframe: stepping forward from the frame already on screen, and restoring a
// keyframe snapshot. Both are invisible in the result -- they produce the same
// picture as a full replay, which is the point -- so proving they are used needs
// a probe rather than a comparison.
//
// The probe is a damaged keyframe. Its payload aliases the buffer the animation
// was parsed from, so it can be corrupted after parsing, which is exactly the
// situation a two-stage decoder has to survive: a container that parses and a
// payload that will not decode. A seek that no longer needs that keyframe
// succeeds; a seek that still replays it fails. That difference is the evidence,
// and the control in each test is a fresh timeline which must fail.

// loadDamageable reads a fixture and keeps the bytes its payloads alias, so a
// frame can be corrupted in place after parsing.
func loadDamageable(t *testing.T, name string) (*nvaa.Animation, []byte) {
	t.Helper()

	blob, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	anim, err := nvaa.Parse(blob)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	return anim, blob
}

// damage overwrites one frame's payload, and fails if that left it decodable.
func damage(t *testing.T, blob []byte, anim *nvaa.Animation, index int) {
	t.Helper()

	header, ok := anim.HeaderAt(index)
	if !ok {
		t.Fatalf("no frame %d to damage", index)
	}
	for offset := range header.PayloadLength {
		blob[header.PayloadOffset+offset] = 0xff
	}

	if _, err := anim.FrameAt(index); err == nil {
		t.Fatalf("damaging frame %d left it decodable, so the probe proves nothing", index)
	}
}

// TestForwardSeekDoesNotReplayTheFramesBehindIt checks that seeking forward from a
// frame inside a group steps from that frame rather than replaying the group from
// its keyframe.
//
// demo.nvaa holds a single keyframe, so the whole file is one group: once frame 3
// is on screen, frame 6 is three deltas away. The probe damages the first delta
// rather than the keyframe, which is what makes it specific to this path: a
// rewind from the keyframe would decode that delta, and so would a replay from a
// snapshot of it, so only stepping from the frame on screen can reach frame 6.
func TestForwardSeekDoesNotReplayTheFramesBehindIt(t *testing.T) {
	anim, blob := loadDamageable(t, "demo.nvaa")

	// What frame 6 looks like, taken before anything is damaged.
	reference := NewTimeline(anim, 80, 24)
	if err := reference.Seek(6); err != nil {
		t.Fatalf("seeking the reference to frame 6: %v", err)
	}
	want := gridImage(reference.Grid())

	timeline := NewTimeline(anim, 80, 24)
	if err := timeline.Seek(3); err != nil {
		t.Fatalf("seeking to frame 3: %v", err)
	}

	const behind = 1
	damage(t, blob, anim, behind)

	// The control: with no canvas to step from, frame 6 is unreachable now, both
	// by replaying the group and by replaying from a snapshot of its keyframe.
	if err := NewTimeline(anim, 80, 24).Seek(6); err == nil {
		t.Fatal("a fresh timeline reached frame 6 through a damaged frame, so this test proves nothing")
	}

	if err := timeline.Seek(6); err != nil {
		t.Fatalf("seeking forward past frames it had already applied: %v", err)
	}
	if timeline.Index() != 6 {
		t.Errorf("landed on frame %d, want 6", timeline.Index())
	}
	if got := gridImage(timeline.Grid()); got != want {
		t.Error("the picture after a forward seek is not the one frame 6 describes")
	}
}

// TestSnapshotSeekDoesNotDecodeTheKeyframeAgain checks that coming back into a
// group restores the canvas its keyframe produced instead of decoding it again.
//
// Same probe, the other way: the timeline walks past the keyframe while the file
// is intact, so a snapshot of it is kept, and damaging the keyframe afterwards
// leaves every seek into that group still working.
func TestSnapshotSeekDoesNotDecodeTheKeyframeAgain(t *testing.T) {
	anim, blob := loadDamageable(t, "demo.nvaa")

	reference := NewTimeline(anim, 80, 24)
	if err := reference.Seek(2); err != nil {
		t.Fatalf("seeking the reference to frame 2: %v", err)
	}
	want := gridImage(reference.Grid())

	timeline := NewTimeline(anim, 80, 24)
	if err := timeline.Seek(8); err != nil {
		t.Fatalf("seeking to frame 8: %v", err)
	}

	damage(t, blob, anim, 0)

	if err := NewTimeline(anim, 80, 24).Seek(2); err == nil {
		t.Fatal("a fresh timeline reached frame 2 through a damaged keyframe, so this test proves nothing")
	}

	if err := timeline.Seek(2); err != nil {
		t.Fatalf("seeking back into a group whose keyframe is held: %v", err)
	}
	if timeline.Index() != 2 {
		t.Errorf("landed on frame %d, want 2", timeline.Index())
	}
	if got := gridImage(timeline.Grid()); got != want {
		t.Error("the picture restored from a snapshot is not the one frame 2 describes")
	}
}

// TestFailedForwardSeekLeavesThePictureAlone is the failure case for the forward
// path, which is the one place a seek copies the live canvas before it knows the
// replay will succeed. A payload that will not decode must leave that canvas as it
// was, and leave it usable.
func TestFailedForwardSeekLeavesThePictureAlone(t *testing.T) {
	const broken = 5

	anim, blob := loadDamageable(t, "demo.nvaa")

	reference := NewTimeline(anim, 80, 24)
	if err := reference.Seek(2); err != nil {
		t.Fatalf("seeking the reference to frame 2: %v", err)
	}

	timeline := NewTimeline(anim, 80, 24)
	if err := timeline.Seek(2); err != nil {
		t.Fatalf("seeking to frame 2: %v", err)
	}
	before := gridImage(timeline.Grid())

	damage(t, blob, anim, broken)

	// Frame 2 to frame 6 crosses the damaged frame, and is a forward seek: the
	// canvas on screen is the state at frame 2, which is the scratch composer's
	// starting point.
	if err := timeline.Seek(6); err == nil {
		t.Fatalf("seeking forward across the damaged frame %d did not fail", broken)
	}
	if timeline.Index() != 2 {
		t.Errorf("a failed forward seek moved the playhead to frame %d", timeline.Index())
	}
	if got := gridImage(timeline.Grid()); got != before {
		t.Error("a failed forward seek changed the picture")
	}

	// The canvas still has to be frame 2's, so the frames after it apply to the
	// right base. The reference can only walk up to the damaged frame.
	for step := 3; step < broken; step++ {
		if moved, err := timeline.Next(); err != nil || !moved {
			t.Fatalf("stepping to frame %d after the failed seek: moved=%v err=%v", step, moved, err)
		}
		if moved, err := reference.Next(); err != nil || !moved {
			t.Fatalf("the reference could not reach frame %d: moved=%v err=%v", step, moved, err)
		}
		if got, want := gridImage(timeline.Grid()), gridImage(reference.Grid()); got != want {
			t.Fatalf("frame %d differs after the failed seek: the copy was left half-applied", step)
		}
	}
}
