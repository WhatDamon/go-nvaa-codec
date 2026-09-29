package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/internal/termtest"
)

const (
	testColumns = 100
	testLines   = 30

	// Selectors as the renderer writes them, for comparison against what a
	// terminal would have been told.
	sideSelector   = "48;2;26;28;40"
	statusSelector = "48;2;44;46;62"
	canvasSelector = "48;2;0;0;0"

	// title is drawn by the animation rather than by this program, which is what
	// makes it a marker of the animation's content where the geometry allows.
	title = "NVAA DEMO"

	// animationGlyphs are marks this file paints that the sidebar's own text
	// cannot produce. The sprite is drawn in every frame, so this is usable as
	// evidence where the camera position is not: the title scrolls out of the
	// window within a frame or two, and a test leaning on it would pass or fail
	// on timing.
	animationGlyphs = "#*@\\"
)

func loadDemo(t *testing.T) *nvaa.Animation {
	t.Helper()

	anim, err := nvaa.ReadFile("../../testdata/demo.nvaa")
	if err != nil {
		t.Fatalf("reading the demo: %v", err)
	}
	return anim
}

// press builds the message the keyboard decoder would deliver for a key.
func press(name string) tea.KeyPressMsg {
	switch name {
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	default:
		runes := []rune(name)
		return tea.KeyPressMsg{Code: runes[0], Text: name}
	}
}

// runHost drives the real program, the way a terminal would.
//
// Driving the real thing matters here: the component is exercised through the
// layout, the key routing and the renderer, and a host test that called View
// directly would skip all three.
func runHost(t *testing.T, anim *nvaa.Animation) []byte {
	t.Helper()

	sink, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	program := tea.NewProgram(
		newModel(anim, testColumns, testLines),
		tea.WithInput(reader),
		tea.WithOutput(sink),
		tea.WithWindowSize(testColumns, testLines),
		// A file is not a terminal, so the profile is stated rather than
		// detected: what is under test is the picture, not the detection.
		tea.WithColorProfile(colorprofile.TrueColor),
	)

	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()

	time.Sleep(300 * time.Millisecond)
	if _, err := writer.WriteString("q"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("program: %v", err)
		}
	case <-time.After(5 * time.Second):
		program.Kill()
		t.Fatal("the host did not exit on q")
	}

	written, err := os.ReadFile(sink.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "48;2;") {
		t.Fatal("no colour was written, so this test would prove nothing")
	}
	return written
}

// TestEachPaneOwnsItsRegion is the reason the component exists.
//
// It asserts against the view string this program composes, which is the part we
// decide. What a terminal would end up showing is a different question, and one
// this repository cannot currently answer: a renderer treats a space as blank
// regardless of its background -- ultraviolet's canClearWith looks at the glyph
// and the attributes, never the colour -- and clears runs of such cells with
// whatever pen it holds, so a background painted on a blank is not ours to keep.
// TestProgramKeepsColoursAndLeavesTheTerminalAlone covers what is claimed once
// the renderer is in the loop.
func TestEachPaneOwnsItsRegion(t *testing.T) {
	model := newModel(loadDemo(t), testColumns, testLines)
	if cmd := model.Init(); cmd == nil {
		t.Fatal("the animation did not start")
	}
	if model.sidebar == 0 {
		t.Fatal("the sidebar should be showing at this size")
	}

	screen := termtest.New(testColumns, testLines)
	screen.CRLF = true // a view string is rows to be placed, not terminal output
	screen.FeedString(model.View().Content)

	pane := model.sidebar
	statusRow := testLines - 1

	var (
		stained     int
		firstStain  string
		unpainted   int
		firstBlank  string
		canvasSeen  bool
		sidebarSeen bool
		statusSeen  bool
	)
	for y := range testLines {
		for x := range testColumns {
			selector := screen.BG(x, y)

			switch {
			case y == statusRow:
				if selector == statusSelector {
					statusSeen = true
				}
			case x < pane:
				// The canvas background is what leaked when this went wrong, so
				// finding it here is the regression this test exists for.
				if selector == canvasSelector {
					stained++
					if firstStain == "" {
						firstStain = testAt(x, y, selector)
					}
				}
				if selector == sideSelector {
					sidebarSeen = true
				}
			default:
				if selector == "" {
					unpainted++
					if firstBlank == "" {
						firstBlank = testAt(x, y, selector)
					}
				}
				if selector == canvasSelector {
					canvasSeen = true
				}
			}
		}
	}

	if stained != 0 {
		t.Errorf("%d cell(s) of the sidebar show the animation's background, first at %s", stained, firstStain)
	}
	if !sidebarSeen {
		t.Error("the sidebar never painted its own background")
	}
	if !statusSeen {
		t.Error("the status row never painted its own background")
	}
	if unpainted != 0 {
		t.Errorf("%d cell(s) of the animation pane are unpainted, first at %s", unpainted, firstBlank)
	}
	if !canvasSeen {
		t.Error("the animation's box was never filled with its own background")
	}

	// Content, as opposed to colour: no mark the animation draws may appear in
	// the sidebar, and some must appear in the pane. The sprite is drawn in every
	// frame, so this does not depend on which frame is showing.
	var spriteInPane bool
	for y := range testLines {
		if y == statusRow {
			continue
		}
		row := screen.Row(y)
		left := row[:min(pane, len(row))]
		if strings.ContainsAny(left, animationGlyphs) {
			t.Errorf("the sidebar row %d shows the animation's glyphs: %q", y, left)
		}
		if strings.ContainsAny(row[min(pane, len(row)):], animationGlyphs) {
			spriteInPane = true
		}
	}
	if !spriteInPane {
		t.Error("the animation's glyphs never reached the pane")
	}
}

// TestHostShowsWhatTheFileClaims checks that the three-state reading reaches the
// screen, on the view string this program composes.
func TestHostShowsWhatTheFileClaims(t *testing.T) {
	model := newModel(loadDemo(t), testColumns, testLines)
	model.Init()

	screen := termtest.New(testColumns, testLines)
	screen.CRLF = true
	screen.FeedString(model.View().Content)

	found := ""
	for y := range testLines {
		row := screen.Row(y)
		if strings.Contains(row, "photosensitivity") {
			found = row
			break
		}
	}
	if found == "" {
		t.Fatal("the sidebar does not mention photosensitivity at all")
	}
	if !strings.Contains(found, "declared pass") {
		t.Errorf("the sidebar reads %q, want the file's own claim", found)
	}
}

// TestProgramKeepsColoursAndLeavesTheTerminalAlone drives the real program, so
// that the renderer is in the loop rather than bypassed.
//
// It stops short of asserting what a terminal would show. Interpreting a
// renderer's output needs a terminal model, and this repository's has been
// wrong often enough (private-mode parsing, two colour-argument rules, the
// erase-in-line range, ECH and cursor moves, erases blanking their characters)
// that cell-level claims built on it are not yet worth making.
func TestProgramKeepsColoursAndLeavesTheTerminalAlone(t *testing.T) {
	written := string(runHost(t, loadDemo(t)))

	for _, selector := range []string{"38;2;", "48;2;"} {
		if !strings.Contains(written, selector) {
			t.Errorf("%s did not survive the renderer (%d bytes written)", selector, len(written))
		}
	}
	for _, osc := range []string{"\x1b]11;", "\x1b]111"} {
		if strings.Contains(written, osc) {
			t.Errorf("the program changed the terminal's background (%q)", osc)
		}
	}
	for _, text := range []string{"nvaa", "tab details"} {
		if !strings.Contains(written, text) {
			t.Errorf("the program never wrote its own text %q", text)
		}
	}
}

// tab belongs to this program: the animation never sees it, so it neither pauses
// nor changes frame. space belongs to the animation, so the host must not
// swallow it.
func TestKeysAreOfferedToTheHostFirst(t *testing.T) {
	model := newModel(loadDemo(t), testColumns, testLines)
	if cmd := model.Init(); cmd == nil {
		t.Fatal("the animation did not start")
	}

	before := model.player.Index()

	if _, cmd := model.Update(press("tab")); cmd != nil {
		t.Error("tab asked for a command; it is a layout key")
	}
	if model.sidebar != 0 {
		t.Errorf("tab left the sidebar %d columns wide, want it hidden", model.sidebar)
	}
	if model.player.Paused() {
		t.Error("tab reached the animation, which paused on it")
	}
	if model.player.Index() != before {
		t.Error("tab moved the animation")
	}

	model.Update(press("tab"))
	if model.sidebar == 0 {
		t.Error("tab did not bring the sidebar back")
	}

	// The animation's own key, which this program deliberately does not claim.
	model.Update(press("space"))
	if !model.player.Paused() {
		t.Error("space did not reach the animation")
	}
}

// TestQuitBelongsToTheHost checks that quitting is this program's decision: the
// animation stops advancing rather than the program ending on its own.
func TestQuitBelongsToTheHost(t *testing.T) {
	model := newModel(loadDemo(t), testColumns, testLines)
	model.Init()

	_, cmd := model.Update(press("q"))
	if cmd == nil {
		t.Fatal("q produced no command; the host is supposed to quit on it")
	}
	if model.player.Paused() || model.player.Done() {
		t.Errorf("q changed the animation's state to %q", model.player.State())
	}
}

// TestLayoutGivesTheAnimationWhatIsLeft checks the arithmetic a host does for a
// component: panes are decided first, and the animation is told the remainder.
//
// It also pins what happens when that remainder grows. The component never
// scales: the file's viewport is the limit, so a wider box buys a wider margin,
// not a bigger picture.
func TestLayoutGivesTheAnimationWhatIsLeft(t *testing.T) {
	anim := loadDemo(t)

	model := newModel(anim, testColumns, testLines)
	if cmd := model.Init(); cmd == nil {
		t.Fatal("the animation did not start")
	}

	frame := model.player.Timeline().Frame()
	if frame == nil {
		t.Fatal("the animation has no frame")
	}

	// The pane is the box the layout handed over, clipped by the file's own
	// viewport.
	wantWidth := min(int(frame.ViewportW), testColumns-model.sidebar)
	wantHeight := min(int(frame.ViewportH), testLines-1)

	grid := model.player.Grid()
	if grid.Width != wantWidth || grid.Height != wantHeight {
		t.Errorf("grid is %dx%d, want %dx%d", grid.Width, grid.Height, wantWidth, wantHeight)
	}

	before := canvasCells(t, model)

	// Hiding the sidebar widens the box. The picture stays its own size and the
	// extra room is filled instead.
	model.Update(press("tab"))
	if model.sidebar != 0 {
		t.Fatal("the sidebar did not hide")
	}
	if got := model.player.Grid().Width; got != grid.Width {
		t.Errorf("the grid changed to %d wide; the viewport, not the pane, is its limit", got)
	}
	if after := canvasCells(t, model); after <= before {
		t.Errorf("a wider box produced %d filled cells, no more than the %d before", after, before)
	}
}

// canvasCells counts the positions of the animation's box that were filled with
// the animation's own background rather than painted by a frame.
func canvasCells(t *testing.T, model *model) int {
	t.Helper()

	screen := termtest.New(testColumns, testLines)
	screen.FeedString(model.View().Content)

	count := 0
	for y := range testLines - 1 {
		for x := range testColumns {
			if screen.BG(x, y) == canvasSelector {
				count++
			}
		}
	}
	return count
}

// testAt formats a position for a failure message.
func testAt(x, y int, selector string) string {
	return fmt.Sprintf("(%d,%d) = %q", x, y, selector)
}
