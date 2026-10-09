package player

import (
	"testing"
	"time"
)

// The clock is authoritative and a host is allowed to be late: a tick that
// arrives after several frame durations must land on the frame the clock names,
// not on the next one. The frames in between are stepped over, and the point of
// stepping over them is that only the last one is ever drawn -- so the grid left
// behind has to be that frame's picture, not the one before the jump.
//
// Nothing else in the suite starves the player, which is why this exists: a
// component that skipped frames without resolving a grid for the one it landed on
// would report the right index and draw the wrong frame.
func TestStarvedClockLandsOnTheFrameItNames(t *testing.T) {
	anim := loadDemo(t)
	const columns, lines = 40, 12

	player := New(anim, Options{Columns: columns, Lines: lines})
	if cmd := player.Init(); cmd == nil {
		t.Fatal("the animation did not start")
	}

	// Most of the animation passes before the host delivers a tick.
	const starved = 850 * time.Millisecond
	player.start = time.Now().Add(-starved)

	if _, handled := player.Update(tickMsg{}); !handled {
		t.Fatal("the tick was not handled")
	}

	if player.Index() <= 1 {
		t.Fatalf("the player is on frame %d, so no frames were skipped", player.Index())
	}
	if want := anim.FrameAtTime(player.ElapsedMS()); player.Index() != want {
		t.Errorf("landed on frame %d, but the clock names frame %d", player.Index(), want)
	}

	grid := player.Grid()
	if grid == nil {
		t.Fatal("no grid after the tick")
	}
	want := referenceGrid(t, anim, player.Index(), columns, lines)
	if got, expected := grid.Digest(), want.Digest(); got != expected {
		t.Errorf("frame %d is drawn as %x, want %x -- the grid is a frame the clock skipped",
			player.Index(), got, expected)
	}
}

// TestStarvedClockResolvesOnceAtTheEndCostsNothing states the other half: when
// the clock has not moved past the current frame, the tick must leave the picture
// alone rather than resolving a grid for a frame that is already on screen.
func TestAStationaryClockLeavesTheGridInPlace(t *testing.T) {
	anim := loadDemo(t)
	const columns, lines = 40, 12

	player := New(anim, Options{Columns: columns, Lines: lines})
	if cmd := player.Init(); cmd == nil {
		t.Fatal("the animation did not start")
	}

	before := player.Grid()
	if before == nil {
		t.Fatal("no grid after Init")
	}
	digest := before.Digest()
	index := player.Index()

	if _, handled := player.Update(tickMsg{}); !handled {
		t.Fatal("the tick was not handled")
	}

	if player.Index() != index {
		t.Fatalf("the clock did not move, but the player advanced from frame %d to %d",
			index, player.Index())
	}
	if got := player.Grid().Digest(); got != digest {
		t.Errorf("the picture changed without the clock moving: %x, was %x", got, digest)
	}
}
