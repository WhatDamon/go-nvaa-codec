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
	return &Timeline{
		anim:      anim,
		composer:  render.NewComposer(anim),
		keyframes: anim.Keyframes(),
		columns:   columns,
		lines:     lines,
	}
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
// rewinds to the nearest keyframe at or before the target and replays forward.
func (t *Timeline) Seek(target int) error {
	if target < 0 || target >= t.Count() {
		return fmt.Errorf("%w: %d is outside 0..%d", ErrNoSuchFrame, target, t.Count()-1)
	}

	// Replay into a scratch composer and swap it in only after every frame has
	// decoded. Resetting the live canvas first would leave it holding a partial
	// replay if a payload turned out to be corrupt, and the next delta applied
	// to that wreckage would produce a wrong picture with no error anywhere.
	scratch := render.NewComposer(t.anim)

	var frame *nvaa.Frame
	for i := t.keyframeAtOrBefore(target); i <= target; i++ {
		f, err := t.anim.FrameAt(i)
		if err != nil {
			return err
		}
		scratch.Apply(f)
		frame = f
	}

	t.composer = scratch
	t.index = target
	t.frame = frame

	// The clock names the start of the frame, so it is the sum of everything
	// before it -- not DurationThrough, which includes the frame itself.
	t.elapsedMS = t.anim.DurationBefore(target)
	t.loaded = true

	return t.recompose()
}

// Next advances one frame in place, which is cheap because the canvas is
// already at the previous frame. It reports false at the end.
func (t *Timeline) Next() (bool, error) {
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
	frame, err := t.anim.FrameAt(next)
	if err != nil {
		return false, err
	}
	t.composer.Apply(frame)

	t.elapsedMS += t.frame.DurationMS
	t.index = next
	t.frame = frame

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
