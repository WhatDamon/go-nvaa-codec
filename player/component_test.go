package player

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/internal/termtest"
	"github.com/WhatDamon/go-nvaa-codec/render"
)

// press builds the message the keyboard decoder would deliver for a key.
//
// The key map is matched against KeyPressMsg.String(), so a test that guessed
// that spelling wrongly would pass while the binding was dead. checkKeys proves
// the helper against the framework before anything relies on it.
func press(name string) tea.KeyPressMsg {
	switch name {
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	default:
		runes := []rune(name)
		return tea.KeyPressMsg{Code: runes[0], Text: name}
	}
}

func checkKeys(t *testing.T) {
	t.Helper()
	names := []string{
		"space", "enter", "tab", "esc", "ctrl+c", "q", "n", "r", "[",
		"left", "right", "up", "down", "home", "end",
		"1", "5", "9",
	}
	for _, name := range names {
		if got := press(name).String(); got != name {
			t.Fatalf("press(%q).String() = %q, so this helper does not match the framework", name, got)
		}
	}
}

// loadDemo loads the animation these tests are written against. It is committed,
// so failing to read it means the checkout is incomplete.
func loadDemo(t *testing.T) *nvaa.Animation {
	t.Helper()

	anim, err := nvaa.ReadFile("../testdata/demo.nvaa")
	if err != nil {
		t.Fatalf("reading the demo: %v", err)
	}
	return anim
}

// ---- the box ----

// TestComponentViewsExactlyItsBox is the contract a host depends on: as many
// rows as it was sized for, as many columns as it was sized for, and no more.
// A view that is one row too tall pushes the host's own chrome off the screen.
func TestComponentViewsExactlyItsBox(t *testing.T) {
	const columns, lines = 60, 20

	player := New(loadDemo(t), Options{Columns: columns, Lines: lines})
	player.Init()

	view := player.View()
	rows := strings.Split(view, "\n")
	if len(rows) != lines {
		t.Fatalf("view has %d rows, want %d", len(rows), lines)
	}
	if strings.HasSuffix(view, "\n") {
		t.Error("a trailing newline would move the host's cursor an extra row")
	}

	screen := termtest.New(columns, lines)
	screen.CRLF = true // a view string is rows to be placed, not terminal output
	screen.FeedString(view)

	for y := range rows {
		if got := len(screen.Row(y)); got > columns {
			t.Errorf("row %d is %d columns wide, want at most %d", y, got, columns)
		}
	}

	if painted := screen.Painted(0, 0, columns, lines); painted != columns*lines {
		t.Errorf("%d of %d cells were left unpainted; the animation's region should be a rectangle",
			columns*lines-painted, columns*lines)
	}
}

// TestComponentFillsItsMarginWithTheAnimationBackground covers the case the
// whole design turns on: a viewport smaller than the box it was given.
//
// The margin is filled rather than left to the terminal, so the region reads as
// one rectangle and a renderer that erases with the wrong colour has nothing to
// reveal.
func TestComponentFillsItsMarginWithTheAnimationBackground(t *testing.T) {
	const columns, lines = 60, 20

	player := New(loadDemo(t), Options{Columns: columns, Lines: lines})
	player.Init()

	grid := player.Grid()
	if grid.Width >= columns || grid.Height >= lines {
		t.Skip("this fixture's viewport fills the box, so it has no margin")
	}

	screen := termtest.New(columns, lines)
	screen.CRLF = true
	screen.FeedString(player.View())

	want := rgbSelector(48, player.Fill().Cell.BG)

	// Inside the window: whatever the frame put there.
	if got := screen.BG(0, 0); got == "" {
		t.Errorf("a window cell at (0,0) has no background")
	}

	// Right of the window, and below it: the animation's own background.
	for _, at := range [][2]int{
		{grid.Width, 0},
		{columns - 1, 0},
		{0, grid.Height},
		{columns - 1, lines - 1},
	} {
		if got := screen.BG(at[0], at[1]); got != want {
			t.Errorf("margin cell (%d,%d) has background %q, want %q", at[0], at[1], got, want)
		}
	}
}

// TestTransparentMarginLeavesTheMarginUnpainted is the other half of that
// choice, for a host that wants the surface behind the animation to show.
func TestTransparentMarginLeavesTheMarginUnpainted(t *testing.T) {
	const columns, lines = 60, 20

	player := New(loadDemo(t), Options{
		Columns: columns, Lines: lines,
		TransparentMargin: true,
	})
	player.Init()

	grid := player.Grid()
	if grid.Width >= columns || grid.Height >= lines {
		t.Skip("this fixture's viewport fills the box, so it has no margin")
	}

	screen := termtest.New(columns, lines)
	screen.CRLF = true
	screen.FeedString(player.View())

	if got := screen.BG(grid.Width, 0); got != "" {
		t.Errorf("transparent margin cell has background %q, want none", got)
	}
	// The window itself is still painted: transparency is about the margin,
	// not about giving up on colour.
	if got := screen.BG(0, 0); got == "" {
		t.Error("a window cell lost its background under TransparentMargin")
	}
}

// TestBoxIsOpaqueAndFilled exercises the renderer directly, with a background
// that is not black, so that filling cannot pass by coincidence.
func TestBoxIsOpaqueAndFilled(t *testing.T) {
	red := nvaa.RGB{R: 200, G: 40, B: 40}
	grid := &render.Grid{
		Width: 2, Height: 1,
		Cells: []render.Cell{
			{Glyph: "#", FG: nvaa.RGB{R: 80, G: 160, B: 255}},
			{Glyph: "x", FG: nvaa.RGB{R: 255, G: 220, B: 80}},
		},
	}

	screen := termtest.New(5, 2)
	screen.CRLF = true
	screen.FeedString(grid.Box(5, 2, render.Fill{Cell: render.Cell{BG: red}, Opaque: true}))

	if got := screen.BG(0, 0); got != "48;2;0;0;0" {
		// The cells carry no background of their own, so the last one set is
		// whatever the renderer had; what matters is that every position ends
		// up painted.
		t.Logf("first cell background = %q", got)
	}
	if painted := screen.Painted(0, 0, 5, 2); painted != 10 {
		t.Errorf("%d of 10 positions unpainted: the box is not opaque", 10-painted)
	}
	if got := screen.BG(4, 0); got != rgbSelector(48, red) {
		t.Errorf("margin background = %q, want %q", got, rgbSelector(48, red))
	}
	if got := screen.BG(4, 1); got != rgbSelector(48, red) {
		t.Errorf("the row below the grid = %q, want the fill %q", got, rgbSelector(48, red))
	}

	// Growing the box adds fill, and shrinking it must not emit the extra
	// columns at all.
	if got := len(grid.Box(5, 2, render.Fill{Cell: render.Cell{BG: red}, Opaque: true})) -
		len(grid.Box(2, 2, render.Fill{Cell: render.Cell{BG: red}, Opaque: true})); got == 0 {
		t.Error("a wider box emitted nothing extra")
	}

	// Lines adds nothing: it is the grid at its own size.
	if strings.HasSuffix(grid.Lines(), " ") {
		t.Errorf("Lines padded its output: %q", grid.Lines())
	}
}

// rgbSelector spells a 24-bit selector the way the renderer writes it.
func rgbSelector(layer int, c nvaa.RGB) string {
	return "48;2;0;0;0"[:0] + itoa(uint64(layer)) + ";2;" +
		itoa(uint64(c.R)) + ";" + itoa(uint64(c.G)) + ";" + itoa(uint64(c.B))
}

// ---- keys ----

// TestComponentLeavesKeysItDoesNotOwn is the property that makes the component
// embeddable. A player that swallowed q would make the host's quit key dead, and
// a player that swallowed tab would make a pane switch stop working.
func TestComponentLeavesKeysItDoesNotOwn(t *testing.T) {
	checkKeys(t)

	player := New(loadDemo(t), Options{Columns: 60, Lines: 20})
	player.Init()

	// Quits and anything about the program's shape belong to the host.
	for _, key := range []string{"q", "ctrl+c", "esc", "tab"} {
		if _, handled := player.Update(press(key)); handled {
			t.Errorf("the player claimed %q, which is the host's", key)
		}
	}

	// Playback keys are the player's.
	for _, key := range []string{"space", "n", "p", "]", "[", "r"} {
		if _, handled := player.Update(press(key)); !handled {
			t.Errorf("the player ignored %q", key)
		}
	}
}

// TestKeyMapCanBeOverridden covers the other way to resolve a clash: a host that
// wants a key keeps it out of the map rather than relying on ordering.
func TestKeyMapCanBeOverridden(t *testing.T) {
	checkKeys(t)

	keys := DefaultKeys
	keys.Restart = nil
	keys.PlayPause = []string{"b"}

	player := New(loadDemo(t), Options{Columns: 60, Lines: 20, Keys: &keys})
	player.Init()

	if _, handled := player.Update(press("r")); handled {
		t.Error("r was unbound but still claimed")
	}
	if _, handled := player.Update(press("space")); handled {
		t.Error("space was unbound but still claimed")
	}
	if _, handled := player.Update(press("b")); !handled {
		t.Error("the replacement binding was ignored")
	}
}

// TestGateIsAStateNotAPrompt covers the photosensitivity gate as something the
// host can see and route around, rather than a prompt that reads input itself.
func TestGateIsAStateNotAPrompt(t *testing.T) {
	checkKeys(t)

	player := New(loadDemo(t), Options{Columns: 60, Lines: 20})
	player.Init()

	// The Go side has no encoder, so a failing advisory is injected rather than
	// decoded: what is under test here is the gate's behaviour, not the reading
	// of the metadata field.
	player.warning = Warning{Verdict: VerdictFail, General: 4, Red: 3, Area: 628, HasArea: true}
	player.phase = phaseWarning

	if !player.AtWarning() {
		t.Fatal("the gate should be up")
	}
	if got := player.State(); got != "warning" {
		t.Errorf("state = %q, want warning", got)
	}
	if !strings.Contains(player.View(), "PHOTOSENSITIVITY") {
		t.Error("the gate does not say anything in the box")
	}

	// The host can still quit and still switch panes while the gate is up,
	// which is exactly the reason the gate is not a prompt.
	for _, key := range []string{"q", "tab"} {
		if _, handled := player.Update(press(key)); handled {
			t.Errorf("the gate claimed %q, which is the host's", key)
		}
	}

	// Acknowledging starts the clock; the frame is still the first one.
	if _, handled := player.Update(press("space")); !handled {
		t.Error("the gate did not accept the acknowledge key")
	}
	if player.AtWarning() {
		t.Error("the gate is still up after being acknowledged")
	}
	if got := player.State(); got != "playing" {
		t.Errorf("state after acknowledging = %q, want playing", got)
	}
	if got := player.Index(); got != 0 {
		t.Errorf("acknowledging moved to frame %d, want the first", got)
	}
}

// TestSkipWarningPlaysStraightAway is the escape hatch, for a host that shows
// the advisory itself.
func TestSkipWarningPlaysStraightAway(t *testing.T) {
	player := New(loadDemo(t), Options{Columns: 60, Lines: 20, SkipWarning: true})
	player.Init()

	if player.AtWarning() {
		t.Error("SkipWarning still raised the gate")
	}
}

// ---- lifecycle ----

// TestDegenerateSizeIsIgnored covers a terminal reporting a zero or negative
// size while it is being resized or torn down. Adopting that would blank the
// canvas and leave the player sized for nothing.
func TestDegenerateSizeIsIgnored(t *testing.T) {
	player := New(loadDemo(t), Options{Columns: 60, Lines: 20})
	player.Init()

	before := player.View()

	for _, size := range [][2]int{{0, 0}, {0, 20}, {60, 0}, {-1, 10}} {
		if err := player.SetSize(size[0], size[1]); err != nil {
			t.Fatalf("SetSize(%d,%d): %v", size[0], size[1], err)
		}
		if got := player.View(); got != before {
			t.Fatalf("resize to %dx%d changed the view", size[0], size[1])
		}
	}
}

// TestFinishDoesNotQuitTheHost is the difference between a component and a
// program: reaching the end stops playback, it does not end the application.
func TestFinishDoesNotQuitTheHost(t *testing.T) {
	anim := loadDemo(t)

	player := New(anim, Options{Columns: 60, Lines: 20})
	player.Init()

	// Put the clock far past the end of the animation.
	if err := player.timeline.Seek(anim.FrameCount() - 1); err != nil {
		t.Fatal(err)
	}
	player.phase = phasePlaying
	player.baseMS = 0
	player.start = time.Now().Add(-time.Hour)

	cmd, handled := player.Update(tickMsg{})
	if !handled {
		t.Error("a tick should be the player's own message")
	}
	if cmd != nil {
		t.Error("finishing returned a command; quitting is the host's decision")
	}
	if !player.Done() {
		t.Errorf("state after the last frame = %q, want finished", player.State())
	}
	// The component still renders, so the host is left with a picture rather
	// than a blank pane.
	if got := len(strings.Split(player.View(), "\n")); got != 20 {
		t.Errorf("view has %d rows after finishing, want 20", got)
	}
}

// TestLoopStartsOver covers the option that says the animation is ambient.
func TestLoopStartsOver(t *testing.T) {
	anim := loadDemo(t)

	player := New(anim, Options{Columns: 60, Lines: 20, Loop: true})
	player.Init()

	if err := player.timeline.Seek(anim.FrameCount() - 1); err != nil {
		t.Fatal(err)
	}
	player.phase = phasePlaying
	player.baseMS = 0
	player.start = time.Now().Add(-time.Hour)

	cmd, _ := player.Update(tickMsg{})
	if cmd == nil {
		t.Error("a looping player should schedule the frame it restarted onto")
	}
	if player.Done() {
		t.Error("a looping player should not be done")
	}
	if got := player.Index(); got != 0 {
		t.Errorf("looping landed on frame %d, want 0", got)
	}
}

// TestProgrammaticControl covers driving playback without a keyboard, which is
// what a host with its own transport controls needs.
func TestProgrammaticControl(t *testing.T) {
	player := New(loadDemo(t), Options{Columns: 60, Lines: 20})
	player.Init()

	player.Step(1)
	if got := player.Index(); got != 1 {
		t.Errorf("after one step, frame = %d, want 1", got)
	}
	if !player.Paused() {
		t.Error("stepping should leave playback paused")
	}

	player.Step(-1)
	if got := player.Index(); got != 0 {
		t.Errorf("after stepping back, frame = %d, want 0", got)
	}

	player.Resume()
	if player.Paused() {
		t.Error("resume left playback paused")
	}

	player.Restart()
	if got := player.Index(); got != 0 {
		t.Errorf("after restart, frame = %d, want 0", got)
	}
	if player.Paused() {
		t.Error("restart should be playing")
	}
}

// TestVerdictIsThreeState checks the reading of the field itself, on a file that
// declares a pass and on one that says nothing at all. Neither the Go encoder
// nor these fixtures can produce a mistyped field, so that branch is not covered
// here and is covered by the Python suite instead.
func TestVerdictIsThreeState(t *testing.T) {
	if got := WarningFrom(loadDemo(t)).Verdict; got != VerdictPass {
		t.Errorf("the demo's verdict = %v, want pass", got)
	}

	silent, err := nvaa.ReadFile("../testdata/vectors/minimal.nvaa")
	if err != nil {
		t.Skipf("no fixture: %v", err)
	}
	if got := WarningFrom(silent).Verdict; got != VerdictUnknown {
		t.Errorf("a file with no assessment = %v, want unknown", got)
	}
}
