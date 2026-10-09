package player

import (
	"errors"
	"fmt"
	"sort"

	"github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/render"
)

// ErrNoSuchFrame reports a frame index outside the animation. It is exported so
// a caller can tell a bad seek from a corrupt file.
var ErrNoSuchFrame = errors.New("no such frame")

// How much a keyframe snapshot is worth keeping.
//
// The expensive frame in a group is the keyframe that begins it, because it paints
// every cell. A snapshot is that frame's result rather than its payload, so a
// second rewind into the same group costs one canvas copy instead of one full
// decode. The per-snapshot bound keeps a very large canvas from making the cache
// the largest thing in the process, and there are only ever a few: a viewer steps
// around one part of an animation, not all of it.
const (
	maxSnapshots           = 4
	maxSnapshotCanvasBytes = 1 << 20

	// snapshotHistory is how many of the last seeks are remembered for deciding
	// whether a group is being replayed. Two is enough to catch the shape a viewer
	// actually makes -- jump to the next keyframe and back again -- without keeping
	// enough history to call a scattered walk a pattern.
	snapshotHistory = 2
)

// keyframeSnapshot is the canvas a keyframe produced, kept so that coming back to
// the same group does not decode that keyframe again.
type keyframeSnapshot struct {
	index  int
	canvas *nvaa.Canvas
}

// Timeline drives playback. It owns the current frame index, the running
// canvas, and the visible grid.
//
// It has no terminal or Bubble Tea dependency on purpose: the interactive
// player, the render subcommand, and the tests all drive playback through this
// one type, so seek behaviour is exercised by whichever is cheapest to run.
type Timeline struct {
	anim      *nvaa.Animation
	composer  *render.Composer
	keyframes []int

	// scratch is the second composer a seek replays into, and applier folds frames
	// into whichever composer is live without allocating a frame's cells for each.
	// Both are held rather than created per seek: a seek is a keystroke, and the
	// buffers it needs are the same size every time.
	scratch *render.Composer
	applier *nvaa.FrameApplier

	// snapshots are keyframe results, most recently used first, and recent are the
	// groups the last few seeks replayed. Together they decide what a snapshot is
	// worth; see rememberSnapshot.
	snapshots  []keyframeSnapshot
	recent     [snapshotHistory]int
	snapshotOK bool

	index     int
	frame     *nvaa.Frame
	grid      *render.Grid
	columns   int
	lines     int
	loaded    bool
	elapsedMS uint64
}

// NewTimeline prepares playback at the first frame. Nothing is decoded until
// Start or Seek is called.
func NewTimeline(anim *nvaa.Animation, columns, lines int) *Timeline {
	// A canvas is one uint32 per cell, and there are four snapshots to hold.
	canvasBytes := uint64(anim.Width) * uint64(anim.Height) * 4

	t := &Timeline{
		anim:       anim,
		composer:   render.NewComposer(anim),
		scratch:    render.NewComposer(anim),
		applier:    nvaa.NewFrameApplier(),
		keyframes:  anim.Keyframes(),
		columns:    columns,
		lines:      lines,
		snapshotOK: canvasBytes*maxSnapshots <= uint64(maxSnapshotCanvasBytes),
	}
	for i := range t.recent {
		t.recent[i] = -1
	}
	return t
}

// Columns reports the output width.
func (t *Timeline) Columns() int { return t.columns }

// Lines reports the output height.
func (t *Timeline) Lines() int { return t.lines }

// Index reports the current frame index.
func (t *Timeline) Index() int { return t.index }

// Count reports how many frames the animation has.
func (t *Timeline) Count() int { return t.anim.FrameCount() }

// Animation exposes the underlying animation.
func (t *Timeline) Animation() *nvaa.Animation { return t.anim }

// Frame returns the current frame, or nil before Start.
//
// The frame's Payload is the applier's buffer: it stays valid until the next
// step, the same window Grid's cells have and for the same reason. Every other
// field is a value, so a caller reading the geometry or the duration of a frame
// it stepped past is reading something that is still true. A caller that wants to
// keep the cells should read them from the animation with FrameAt instead.
func (t *Timeline) Frame() *nvaa.Frame { return t.frame }

// Grid returns the current visible screen, or nil before Start.
func (t *Timeline) Grid() *render.Grid { return t.grid }

// ElapsedMS is the animation time at the start of the current frame.
func (t *Timeline) ElapsedMS() uint64 { return t.elapsedMS }

// AtEnd reports whether the last frame has been reached.
func (t *Timeline) AtEnd() bool { return t.index >= t.Count()-1 }

// SetSize changes the output grid, recomposing the current frame because the
// visible window depends on how much room there is.
func (t *Timeline) SetSize(columns, lines int) error {
	if columns == t.columns && lines == t.lines {
		return nil
	}
	t.columns, t.lines = columns, lines
	if !t.loaded {
		return nil
	}
	return t.recompose()
}

// Start loads the first frame.
func (t *Timeline) Start() error { return t.Seek(0) }

// Seek jumps to a frame. Because a delta frame describes only what changed
// since its predecessor, there is nothing to decode in isolation: seeking
// backwards rewinds to the nearest keyframe at or before the target and replays
// forward.
//
// Forwards is a different question. The canvas is already the state at the frame
// on screen, so if no keyframe lies between that frame and the target, the frames
// in between are the only work left -- and stepping through those costs less than
// replaying the whole group from its start. The price is one pass over the canvas,
// which is also what keeps a corrupt payload from reaching the live picture: the
// replay happens in the scratch composer either way, and it is swapped in only
// once every frame has decoded.
func (t *Timeline) Seek(target int) error {
	if target < 0 || target >= t.Count() {
		return fmt.Errorf("%w: %d is outside 0..%d", ErrNoSuchFrame, target, t.Count()-1)
	}

	// Already showing the frame asked for: there is nothing to replay, and the
	// canvas and the grid are what the answer already is.
	if t.loaded && t.index == target {
		t.elapsedMS = t.anim.DurationBefore(target)
		return t.recompose()
	}

	start := t.keyframeAtOrBefore(target)
	forward := t.loaded && start < t.index && t.index <= target

	// Whether this group is worth a snapshot has to be decided before this seek
	// becomes part of the history it is asking about.
	keep := t.snapshotWorthKeeping(start)
	copy(t.recent[1:], t.recent[:snapshotHistory-1])
	t.recent[0] = start

	scratch := t.scratch
	scratch.Reset()

	first := start
	switch {
	case forward:
		scratch.Canvas().CopyFrom(t.composer.Canvas())
		first = t.index + 1
	case start < target:
		// A snapshot holds the canvas a keyframe produced but not the frame it is,
		// so it can only stand in for a replay that still has deltas left to apply.
		// Landing exactly on the keyframe has to decode it, which is what produces
		// the frame the host will be told about.
		if snapshot := t.lookupSnapshot(start); snapshot != nil {
			scratch.Canvas().CopyFrom(snapshot)
			first = start + 1
		}
	}

	// Every path above leaves at least one frame to apply, which is what makes the
	// landing frame's geometry -- its camera and viewport -- correct rather than
	// whatever the previous frame happened to carry.
	var frame *nvaa.Frame
	for i := first; i <= target; i++ {
		decoded, err := t.applier.Apply(t.anim, i, scratch.Canvas())
		if err != nil {
			return err
		}
		frame = decoded

		if i == start {
			// Applying the keyframe left the canvas in exactly the state a later
			// rewind into this group wants, and it is the frame that cost the most
			// to get there.
			t.rememberSnapshot(start, scratch.Canvas(), keep)
		}
	}

	t.composer, t.scratch = scratch, t.composer
	t.index = target
	t.frame = frame

	// The clock names the start of the frame, so it is the sum of everything
	// before it -- not DurationThrough, which includes the frame itself.
	t.elapsedMS = t.anim.DurationBefore(target)
	t.loaded = true

	return t.recompose()
}

// lookupSnapshot returns the canvas a keyframe produced, if one is held.
func (t *Timeline) lookupSnapshot(index int) *nvaa.Canvas {
	for i, snapshot := range t.snapshots {
		if snapshot.index != index {
			continue
		}
		// Most recently used first, so what gets evicted is the group nobody is
		// stepping around in.
		copy(t.snapshots[1:i+1], t.snapshots[:i])
		t.snapshots[0] = snapshot
		return snapshot.canvas
	}
	return nil
}

// snapshotWorthKeeping reports whether a snapshot of this group would be read.
//
// It would not, usually: a viewer jumping around a file replays a different group
// every time, and a canvas copied for each of those is memory written on every
// seek that nothing ever reads. A viewer scrubbing one part of an animation
// replays the same group over and over, and for them the copy replaces a full
// keyframe decode. So a group is only kept once it has been replayed, and the
// cache filling up is what tells the difference.
func (t *Timeline) snapshotWorthKeeping(start int) bool {
	if len(t.snapshots) < maxSnapshots {
		return true
	}
	for _, recent := range t.recent {
		if recent == start {
			return true
		}
	}
	return false
}

// rememberSnapshot keeps the canvas a keyframe produced, most recently used
// first.
func (t *Timeline) rememberSnapshot(index int, canvas *nvaa.Canvas, worthKeeping bool) {
	if !t.snapshotOK || !worthKeeping {
		return
	}

	kept := keyframeSnapshot{index: index, canvas: canvas.Clone()}
	if len(t.snapshots) < maxSnapshots {
		t.snapshots = append(t.snapshots, keyframeSnapshot{})
	} else {
		// Make room by dropping the least recently used, which is the last one.
		copy(t.snapshots[1:], t.snapshots[:maxSnapshots-1])
	}
	t.snapshots[0] = kept
}

// advance moves one frame forward without resolving a grid for it.
//
// A host that was starved steps through every frame it missed, and only the last
// of them is ever drawn: resolving a grid per skipped frame is work thrown away,
// and on a small canvas a grid is the most expensive part of a playback frame.
func (t *Timeline) advance() (bool, error) {
	if !t.loaded {
		if err := t.Start(); err != nil {
			return false, err
		}
		return true, nil
	}
	if t.AtEnd() {
		return false, nil
	}

	next := t.index + 1
	frame, err := t.applier.Apply(t.anim, next, t.composer.Canvas())
	if err != nil {
		return false, err
	}

	t.elapsedMS += t.frame.DurationMS
	t.index = next
	t.frame = frame
	return true, nil
}

// Next advances one frame in place, which is cheap because the canvas is
// already at the previous frame. It reports false at the end.
func (t *Timeline) Next() (bool, error) {
	moved, err := t.advance()
	if err != nil || !moved {
		return moved, err
	}
	return true, t.recompose()
}

// Previous steps back one frame. It has to seek, since a delta cannot be
// undone; it reports false at the start.
func (t *Timeline) Previous() (bool, error) {
	if !t.loaded {
		if err := t.Start(); err != nil {
			return false, err
		}
		return true, nil
	}
	if t.index == 0 {
		return false, nil
	}
	return true, t.Seek(t.index - 1)
}

// Restart returns to the first frame.
func (t *Timeline) Restart() error { return t.Seek(0) }

// SeekToTime goes to the frame covering ms milliseconds into the animation.
//
// Times outside the animation clamp to its ends instead of failing. This is the
// entry point for "--seek 1:23", where naming a moment is a request rather than
// a programming error; Seek, which takes a frame index, still refuses a bad one.
func (t *Timeline) SeekToTime(ms uint64) error {
	return t.Seek(t.anim.FrameAtTime(ms))
}

// SeekFraction goes to a fraction of the animation: 0 is the start, 1 the end.
// Values outside that range clamp.
func (t *Timeline) SeekFraction(fraction float64) error {
	total := float64(t.anim.TotalDuration())
	return t.SeekToTime(uint64(clampFraction(fraction) * total))
}

// SeekBy moves by a signed number of milliseconds from the start of the current
// frame, clamping at either end. It is the nudge behind the arrow keys.
func (t *Timeline) SeekBy(deltaMS int64) error {
	target := int64(t.elapsedMS) + deltaMS
	if target < 0 {
		target = 0
	}
	return t.SeekToTime(uint64(target))
}

// SeekToEnd goes to the last frame.
func (t *Timeline) SeekToEnd() error { return t.Seek(t.Count() - 1) }

// TotalMS is the animation's whole length.
func (t *Timeline) TotalMS() uint64 { return t.anim.TotalDuration() }

// Progress is how far into the animation the current frame begins, from 0 to 1.
func (t *Timeline) Progress() float64 {
	total := t.TotalMS()
	if total == 0 {
		return 0
	}
	return float64(t.elapsedMS) / float64(total)
}

// Timecode is the current position, formatted for display.
func (t *Timeline) Timecode() string { return Timecode(t.elapsedMS) }

// JumpKeyframe moves to the previous or next keyframe, which is the unit a
// viewer can navigate in without decoding everything between.
func (t *Timeline) JumpKeyframe(direction int) (bool, error) {
	if len(t.keyframes) == 0 {
		return false, nil
	}

	if direction >= 0 {
		for _, k := range t.keyframes {
			if k > t.index {
				return true, t.Seek(k)
			}
		}
		return false, nil
	}

	for i := len(t.keyframes) - 1; i >= 0; i-- {
		if t.keyframes[i] < t.index {
			return true, t.Seek(t.keyframes[i])
		}
	}
	return false, nil
}

// KeyframeCount reports how many seek points the animation has.
func (t *Timeline) KeyframeCount() int { return len(t.keyframes) }

// recompose rebuilds the visible grid for the current frame.
func (t *Timeline) recompose() error {
	if t.frame == nil {
		return nil
	}
	t.grid = t.composer.Grid(t.frame, t.columns, t.lines)
	return nil
}

// keyframeAtOrBefore finds the latest keyframe at or before target.
//
// Keyframe lists are ascending, so this is a binary search rather than the
// linear scan a small animation would tolerate.
func (t *Timeline) keyframeAtOrBefore(target int) int {
	idx := sort.SearchInts(t.keyframes, target+1) - 1
	if idx < 0 {
		return 0
	}
	return t.keyframes[idx]
}
