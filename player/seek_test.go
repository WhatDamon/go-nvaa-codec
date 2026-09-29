package player

import (
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/render"
)

// loadVector reads a conformance vector from testdata.
func loadVector(t *testing.T, name string) *nvaa.Animation {
	t.Helper()

	anim, err := nvaa.ReadFile("../testdata/vectors/" + name + ".nvaa")
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return anim
}

func rgbKey(colour nvaa.RGB) string {
	return itoa(uint64(colour.R)) + "," + itoa(uint64(colour.G)) + "," + itoa(uint64(colour.B))
}

// gridImage flattens a grid into something comparable: the glyph, both colours,
// and whether the position is the second half of a wide glyph, for every cell.
// Two grids are the same picture exactly when these strings match.
func gridImage(grid *render.Grid) string {
	if grid == nil {
		return ""
	}

	var b strings.Builder
	for y := range grid.Height {
		for x := range grid.Width {
			cell := grid.At(x, y)
			b.WriteString(cell.Glyph)
			b.WriteByte(' ')
			b.WriteString(rgbKey(cell.FG))
			b.WriteByte(' ')
			b.WriteString(rgbKey(cell.BG))
			if cell.Continuation {
				b.WriteByte('c')
			}
			b.WriteByte('|')
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// ---- the clock a seek lands on ----

// TestSeekPutsTheClockAtTheFrameStart is the regression for a seek that landed a
// frame late.
//
// ElapsedMS is documented as the start of the frame on screen, and the clock is
// what a jump re-anchors and a tick waits on, so an end-of-frame reading is not
// a cosmetic slip: every jump left the clock a frame ahead of the picture.
func TestSeekPutsTheClockAtTheFrameStart(t *testing.T) {
	anim := loadDemo(t)
	timeline := NewTimeline(anim, 80, 24)

	framesWithDuration := 0
	for i := range anim.FrameCount() {
		if err := timeline.Seek(i); err != nil {
			t.Fatalf("seeking to frame %d: %v", i, err)
		}
		if got, want := timeline.ElapsedMS(), anim.DurationBefore(i); got != want {
			t.Fatalf("after seeking to frame %d the clock reads %d ms, want the frame's start %d ms",
				i, got, want)
		}
		// Teeth: the two readings must differ somewhere, or this test would
		// also pass against the arithmetic it exists to catch.
		if anim.DurationThrough(i) != timeline.ElapsedMS() {
			framesWithDuration++
		}
	}

	if framesWithDuration == 0 {
		t.Fatal("no frame lasts any time, so a start cannot be told from an end here")
	}
}

// TestFrameIndexesAreStrictAndTimesAreNot pins the two doors. A frame index is a
// programmer's number and earns a refusal; a time is a viewer's request and is
// clamped to the nearest end.
func TestFrameIndexesAreStrictAndTimesAreNot(t *testing.T) {
	anim := loadDemo(t)
	timeline := NewTimeline(anim, 80, 24)
	if err := timeline.Start(); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []int{-1, anim.FrameCount(), anim.FrameCount() + 99} {
		if err := timeline.Seek(bad); !errors.Is(err, ErrNoSuchFrame) {
			t.Errorf("Seek(%d) returned %v, want ErrNoSuchFrame", bad, err)
		}
	}

	last := anim.FrameCount() - 1
	for _, ms := range []uint64{anim.TotalDuration(), anim.TotalDuration() + 60_000} {
		if err := timeline.SeekToTime(ms); err != nil {
			t.Fatalf("SeekToTime(%d): %v", ms, err)
		}
		if timeline.Index() != last {
			t.Errorf("SeekToTime(%d) landed on frame %d, want the last frame %d", ms, timeline.Index(), last)
		}
	}

	if err := timeline.SeekFraction(-0.5); err != nil {
		t.Fatal(err)
	}
	if timeline.Index() != 0 {
		t.Errorf("a negative fraction landed on frame %d, want 0", timeline.Index())
	}
	if err := timeline.SeekFraction(1.5); err != nil {
		t.Fatal(err)
	}
	if timeline.Index() != last {
		t.Errorf("a fraction above one landed on frame %d, want %d", timeline.Index(), last)
	}

	if err := timeline.SeekBy(-60_000); err != nil {
		t.Fatal(err)
	}
	if timeline.Index() != 0 {
		t.Errorf("seeking back from the start landed on frame %d, want 0", timeline.Index())
	}
}

// TestTimesLandInTheCoveringFrame checks the half-open rule against real
// content: a time inside a frame's interval lands on that frame, and the clock
// then reads that frame's start rather than the moment asked for. A seek is a
// jump to a frame, not a position between frames.
func TestTimesLandInTheCoveringFrame(t *testing.T) {
	anim := loadDemo(t)
	timeline := NewTimeline(anim, 80, 24)

	for i := range anim.FrameCount() {
		start := anim.DurationBefore(i)
		duration := anim.DurationThrough(i) - start

		moments := []uint64{start}
		if duration > 1 {
			moments = append(moments, start+1, start+duration-1)
		}
		for _, ms := range moments {
			if err := timeline.SeekToTime(ms); err != nil {
				t.Fatalf("SeekToTime(%d): %v", ms, err)
			}
			if timeline.Index() != i {
				t.Fatalf("%d ms landed on frame %d, want %d", ms, timeline.Index(), i)
			}
			if got := timeline.ElapsedMS(); got != start {
				t.Fatalf("%d ms put the clock at %d ms, want the frame's start %d ms", ms, got, start)
			}
		}
	}
}

// TestSeekByTravelsByTime is the rule behind the arrow keys.
func TestSeekByTravelsByTime(t *testing.T) {
	anim := loadDemo(t)
	timeline := NewTimeline(anim, 80, 24)
	if err := timeline.Start(); err != nil {
		t.Fatal(err)
	}

	// Every frame of the demo lasts the same 100 ms, so the arithmetic here is
	// exact and owes nothing to the timeline's own search.
	if anim.FrameMS == 100 {
		if err := timeline.SeekBy(500); err != nil {
			t.Fatal(err)
		}
		if timeline.Index() != 5 {
			t.Errorf("500 ms from the start is frame %d, want 5", timeline.Index())
		}
		if err := timeline.SeekBy(-200); err != nil {
			t.Fatal(err)
		}
		if timeline.Index() != 3 {
			t.Errorf("200 ms back from frame 5 is frame %d, want 3", timeline.Index())
		}
	}

	// And the general rule, including both clamps.
	clock := timeline.ElapsedMS()
	for _, delta := range []int64{SeekStepMS, SeekStepMS, -SeekFarMS, SeekFarMS * 10} {
		target := max(int64(clock)+delta, 0)

		if err := timeline.SeekBy(delta); err != nil {
			t.Fatalf("SeekBy(%d): %v", delta, err)
		}
		if got, want := timeline.Index(), anim.FrameAtTime(uint64(target)); got != want {
			t.Errorf("SeekBy(%d) from %d ms landed on frame %d, want %d", delta, clock, got, want)
		}
		clock = timeline.ElapsedMS()
	}
}

// ---- a seek that fails ----

// TestFailedSeekLeavesThePictureAlone is the regression for a seek that cleared
// the canvas before it could fail.
//
// The old order reset the live composer and then replayed into it, so a frame
// that would not decode left the canvas holding a partial replay while the
// playhead still named the old frame. The next delta applied to that wreckage --
// a wrong picture, with no error anywhere to explain it.
func TestFailedSeekLeavesThePictureAlone(t *testing.T) {
	const broken = 5

	// The parser records each payload as a slice of the buffer it was handed, so
	// a file that has already passed its checksum can still be damaged here.
	// That models exactly the case a two-stage decoder has to survive: a
	// container that parses and a payload that will not decode.
	blob, err := os.ReadFile("../testdata/demo.nvaa")
	if err != nil {
		t.Fatalf("reading the demo: %v", err)
	}
	anim, err := nvaa.Parse(blob)
	if err != nil {
		t.Fatalf("parsing the demo: %v", err)
	}
	header, ok := anim.HeaderAt(broken)
	if !ok {
		t.Fatalf("the demo has no frame %d", broken)
	}

	// The reference never takes the failed seek, and is built first.
	reference := NewTimeline(anim, 80, 24)

	for offset := range header.PayloadLength {
		blob[header.PayloadOffset+offset] = 0xff
	}

	if _, err := anim.FrameAt(broken); err == nil {
		t.Fatal("the damaged payload decoded anyway, so this test proves nothing")
	}

	timeline := NewTimeline(anim, 80, 24)
	if err := timeline.Seek(0); err != nil {
		t.Fatalf("seeking to the first frame: %v", err)
	}
	before := gridImage(timeline.Grid())

	if err := timeline.Seek(broken); err == nil {
		t.Fatalf("seeking to the damaged frame %d did not fail", broken)
	}
	if timeline.Index() != 0 {
		t.Errorf("a failed seek moved the playhead to frame %d", timeline.Index())
	}
	if got := gridImage(timeline.Grid()); got != before {
		t.Error("a failed seek changed the picture")
	}

	// The canvas still has to be the first frame's, so the frames after it apply
	// to the right base and give what they would have given untouched.
	if err := reference.Seek(0); err != nil {
		t.Fatal(err)
	}
	for step := 1; step < broken; step++ {
		if moved, err := timeline.Next(); err != nil || !moved {
			t.Fatalf("stepping to frame %d after the failed seek: moved=%v err=%v", step, moved, err)
		}
		if moved, err := reference.Next(); err != nil || !moved {
			t.Fatalf("the reference could not reach frame %d: moved=%v err=%v", step, moved, err)
		}
		if got, want := gridImage(timeline.Grid()), gridImage(reference.Grid()); got != want {
			t.Fatalf("frame %d differs after the failed seek: the replay was applied to a damaged canvas", step)
		}
	}

	// Teeth: the picture has to be substantial, or an emptied canvas could
	// happen to match it.
	if cells := strings.Count(gridImage(reference.Grid()), "|"); cells < 100 {
		t.Fatalf("the reference frame holds only %d cells, too few to tell a cleared canvas apart", cells)
	}
}

// ---- a jump that keeps playing ----

// TestJumpKeyframeKeepsPlaying is the regression for a jump that stopped the
// animation. Pausing inside the jump made the bracket keys a trap: the viewer
// asked to move through the animation and playback stopped instead.
func TestJumpKeyframeKeepsPlaying(t *testing.T) {
	checkKeys(t)

	// This fixture has two frames, both keyframes, so a jump has somewhere to go
	// and the frame it lands on is unambiguous.
	anim := loadVector(t, "keyframe-and-delta")
	if anim.FrameCount() < 2 {
		t.Fatalf("the fixture has %d frames, too few to jump between", anim.FrameCount())
	}

	player := New(anim, Options{Columns: 40, Lines: 12})
	player.Init()

	if got := player.State(); got != "playing" {
		t.Fatalf("state is %q, want playing", got)
	}

	player.JumpKeyframe(1)
	if got := player.Index(); got != 1 {
		t.Errorf("the jump landed on frame %d, want 1", got)
	}
	if got := player.State(); got != "playing" {
		t.Errorf("after a jump the state is %q, want playing", got)
	}
	if player.Paused() {
		t.Error("a jump paused the animation")
	}
	if got, want := player.ElapsedMS(), anim.DurationBefore(1); got != want {
		t.Errorf("after a jump the clock reads %d ms, want %d ms", got, want)
	}

	// A jump from a paused player stays paused: the state is kept, not forced
	// either way.
	player.Pause()
	player.JumpKeyframe(-1)
	if !player.Paused() {
		t.Error("a jump resumed a paused animation")
	}
	if got := player.Index(); got != 0 {
		t.Errorf("the jump back landed on frame %d, want 0", got)
	}
	if got := player.ElapsedMS(); got != 0 {
		t.Errorf("the jump back left the clock at %d ms, want 0", got)
	}
}

// TestSeekKeysTravelByTime covers the bindings and the distances behind them.
func TestSeekKeysTravelByTime(t *testing.T) {
	checkKeys(t)

	anim := loadDemo(t)
	player := New(anim, Options{Columns: 60, Lines: 20})
	player.Init()

	clock := player.ElapsedMS()
	expect := func(key string, delta int64) {
		t.Helper()

		target := max(int64(clock)+delta, 0)
		player.Update(press(key))

		if got, want := player.Index(), anim.FrameAtTime(uint64(target)); got != want {
			t.Errorf("%q from %d ms landed on frame %d, want %d", key, clock, got, want)
		}
		clock = player.ElapsedMS()
	}

	expect("right", SeekStepMS)
	expect("right", SeekStepMS)
	expect("left", -SeekStepMS)
	expect("up", SeekFarMS)
	expect("down", -SeekFarMS)

	// The ends, which are not relative to anything.
	player.Update(press("end"))
	if got, want := player.Index(), anim.FrameCount()-1; got != want {
		t.Errorf("end landed on frame %d, want %d", got, want)
	}
	player.Update(press("home"))
	if got := player.Index(); got != 0 {
		t.Errorf("home landed on frame %d, want 0", got)
	}

	// A digit names a tenth, using the same arithmetic the seek does.
	for digit, tenths := range map[string]uint64{"1": 1, "5": 5, "9": 9} {
		player.Update(press(digit))

		target := uint64(float64(tenths) / 10 * float64(anim.TotalDuration()))
		if got, want := player.Index(), anim.FrameAtTime(target); got != want {
			t.Errorf("%q landed on frame %d, want frame %d (%d ms in)", digit, got, want, target)
		}
	}

	// A host that needs the digits can have them back.
	keys := DefaultKeys
	keys.Jump = nil
	unbound := New(anim, Options{Columns: 60, Lines: 20, Keys: &keys})
	unbound.Init()
	if _, handled := unbound.Update(press("5")); handled {
		t.Error("the digits are still claimed after being unbound")
	}
}

// TestStartAtAPositionIsHonoured covers --seek: the program begins where it was
// asked to, says so in its own row, and is playing.
func TestStartAtAPositionIsHonoured(t *testing.T) {
	anim := loadDemo(t)
	want := anim.DurationBefore(anim.FrameCount() / 2)

	host := newHost(anim,
		Options{Columns: 80, Lines: 24},
		HostOptions{ShowStats: true, StartAtMS: &want})
	if cmd := host.Init(); cmd == nil {
		t.Fatal("the animation did not start")
	}

	if got, expected := host.player.Index(), anim.FrameAtTime(want); got != expected {
		t.Errorf("playback began on frame %d, want %d", got, expected)
	}
	if got := host.player.ElapsedMS(); got != want {
		t.Errorf("playback began at %d ms, want %d ms", got, want)
	}
	if got := host.player.State(); got != "playing" {
		t.Errorf("playback began in state %q, want playing", got)
	}

	// The row is ours, so its contents can be asserted exactly.
	row := host.statusText()
	if !strings.Contains(row, Timecode(want)) {
		t.Errorf("the status row %q does not name the position %s", row, Timecode(want))
	}
	if !strings.Contains(row, Bar(host.player.Progress(), 10)) {
		t.Errorf("the status row %q does not carry the progress bar", row)
	}

	// The row is written for an 80 column terminal, which is the reason the key
	// list lives in the usage text rather than here. The row is ASCII, so its
	// length in bytes is its width.
	if width := len(row); width > 80 {
		t.Errorf("the status row is %d columns wide, too many for the terminal it is written for: %q", width, row)
	}
}

// TestPositionReportsOneMoment checks that the four numbers a status line needs
// come from a single reading, rather than four calls that could disagree.
func TestPositionReportsOneMoment(t *testing.T) {
	anim := loadDemo(t)
	player := New(anim, Options{Columns: 80, Lines: 24})
	player.Init()
	if err := player.Timeline().SeekToTime(anim.TotalDuration() / 4); err != nil {
		t.Fatal(err)
	}

	position := player.Position()
	if position.Index != player.Index() || position.Count != player.Count() {
		t.Errorf("the position says %d/%d, the player says %d/%d",
			position.Index, position.Count, player.Index(), player.Count())
	}
	if position.Elapsed != player.ElapsedMS() || position.Total != player.TotalMS() {
		t.Errorf("the position says %d/%d ms, the player says %d/%d ms",
			position.Elapsed, position.Total, player.ElapsedMS(), player.TotalMS())
	}

	text := position.String()
	if !strings.Contains(text, Timecode(position.Elapsed)) {
		t.Errorf("the rendered position %q hides its own timecode", text)
	}
	if got, want := position.Progress(), float64(position.Elapsed)/float64(position.Total); got != want {
		t.Errorf("progress is %v, want %v", got, want)
	}
}

// ---- the shared formatting ----

// TestTimecodeReadsAsAClock covers the formatter every host shares.
func TestTimecodeReadsAsAClock(t *testing.T) {
	cases := []struct {
		ms   uint64
		want string
	}{
		{0, "0:00.0"},
		{1, "0:00.0"},
		{49, "0:00.0"},
		{50, "0:00.1"},
		{95, "0:00.1"},
		{1_000, "0:01.0"},
		{59_950, "1:00.0"},
		{83_000, "1:23.0"},
		{83_500, "1:23.5"},
		{290_133, "4:50.1"},
		{3_599_950, "1:00:00.0"},
		{3_600_000, "1:00:00.0"},
		{7_325_500, "2:02:05.5"},
	}
	for _, tc := range cases {
		if got := Timecode(tc.ms); got != tc.want {
			t.Errorf("Timecode(%d) = %q, want %q", tc.ms, got, tc.want)
		}
	}
}

// TestBarClampsAndFills covers the bar a host draws beside the timecode.
func TestBarClampsAndFills(t *testing.T) {
	cases := []struct {
		fraction float64
		width    int
		want     string
	}{
		{0, 4, "[----]"},
		{1, 4, "[####]"},
		{0.5, 4, "[##--]"},
		{-1, 4, "[----]"},
		{2, 4, "[####]"},
		{math.NaN(), 4, "[----]"},
		{0.25, 8, "[##------]"},
		{0.5, 0, ""},
	}
	for _, tc := range cases {
		if got := Bar(tc.fraction, tc.width); got != tc.want {
			t.Errorf("Bar(%v, %d) = %q, want %q", tc.fraction, tc.width, got, tc.want)
		}
	}
}
