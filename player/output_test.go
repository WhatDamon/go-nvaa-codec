package player

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/internal/termtest"
)

// runProgram drives a real program through Run and returns everything it wrote.
//
// The sink is a file rather than a buffer because a buffer cannot be a
// term.File, which is what Bubble Tea asks about to decide whether its output is
// a terminal.
func runProgram(t *testing.T, anim *nvaa.Animation, opts Options, hostOpts HostOptions) []byte {
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

	done := make(chan error, 1)
	go func() {
		_, err := Run(anim, opts, hostOpts, reader, sink)
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
		t.Fatal("program did not exit after q")
	}

	written, err := os.ReadFile(sink.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(written) == 0 {
		t.Fatal("the program wrote nothing to its output")
	}
	return written
}

// TestMeteredOutputIsStillATerminal is the regression test for a bug that cost
// both colour and layout.
//
// Bubble Tea asks whether its output is a terminal with `output.(term.File)`.
// Metering that output with a wrapper exposing only Write() made the answer "no"
// for a real terminal, so the colour profile collapsed to none and the terminal
// size was never queried. Nothing about the renderer was wrong; the wrapper was.
func TestMeteredOutputIsStillATerminal(t *testing.T) {
	original := os.Stdout

	wrapped, counter := meterOutput(original)
	if counter == nil {
		t.Fatal("meterOutput returned no counter")
	}

	file, ok := wrapped.(term.File)
	if !ok {
		t.Fatalf("metered output is %T, not a term.File: Bubble Tea would treat the terminal as a pipe", wrapped)
	}
	if file.Fd() != original.Fd() {
		t.Errorf("Fd() = %d, want %d (the wrapped descriptor)", file.Fd(), original.Fd())
	}

	// Reading is not part of an output stream's job; it must not block.
	if _, err := file.Read(make([]byte, 1)); err == nil {
		t.Error("Read on an output wrapper should report end of stream")
	}

	// Buffers have no terminal to hide, so the plain wrapper is right and must
	// not pretend otherwise.
	buffered := &strings.Builder{}
	plain, _ := meterOutput(buffered)
	if _, ok := plain.(term.File); ok {
		t.Error("a non-terminal output should not be reported as a terminal")
	}
	if _, err := plain.Write([]byte("xy")); err != nil {
		t.Fatal(err)
	}
	if buffered.String() != "xy" {
		t.Errorf("plain wrapper did not pass bytes through: %q", buffered.String())
	}
}

// TestProgramPreservesTrueColour drives the real program and checks that the
// escapes survive Bubble Tea's renderer, which re-encodes the view into its own
// cell buffer and could drop or downgrade them.
func TestProgramPreservesTrueColour(t *testing.T) {
	written := string(runProgram(t, loadDemo(t),
		Options{Columns: 80, Lines: 24}, HostOptions{}))

	for _, selector := range []string{"38;2;", "48;2;"} {
		if !strings.Contains(written, selector) {
			t.Errorf("%s did not survive the renderer (%d bytes written)", selector, len(written))
		}
	}
}

// TestWrapperComposesTheBoxAndTheMeter covers the standalone wrapper's version of
// a composed view: the animation's box, plus one row this program keeps.
//
// It asserts against the string the wrapper produces, which is the part we
// decide. Interpreting what a terminal would end up showing is a different
// question and one this repository cannot currently answer -- see the note in
// internal/termtest -- so no cell-level claim is made about the renderer's
// output here.
func TestWrapperComposesTheBoxAndTheMeter(t *testing.T) {
	const columns, lines = 80, 24

	host := newHost(loadDemo(t),
		Options{Columns: columns, Lines: lines},
		HostOptions{ShowStats: true})
	if cmd := host.Init(); cmd == nil {
		t.Fatal("the animation did not start")
	}

	view := host.View().Content
	if rows := strings.Split(view, "\n"); len(rows) != lines {
		t.Fatalf("view has %d rows, want %d", len(rows), lines)
	}

	screen := termtest.New(columns, lines)
	screen.CRLF = true // a view string is rows to be placed, not terminal output
	screen.FeedString(view)

	// The animation gets every row but the last, and fills them.
	if painted := screen.Painted(0, 0, columns, lines-1); painted != columns*(lines-1) {
		t.Errorf("%d of %d cells of the animation's box are unpainted",
			columns*(lines-1)-painted, columns*(lines-1))
	}

	// The last row is the program's, and the animation gave it up rather than
	// being overdrawn.
	if got := screen.Row(lines - 1); !strings.Contains(got, "frame") {
		t.Errorf("the last row is %q, want the meter", got)
	}
	if grid := host.player.Grid(); grid.Height > lines-1 {
		t.Errorf("the animation took %d rows of the %d available", grid.Height, lines)
	}
}

// TestProgramDoesNotRepaintTheTerminal checks that the picture is painted where
// it belongs rather than delegated to the terminal.
//
// Recolouring the terminal to match the canvas would hide a margin that should
// not have been coloured at all, and would make the file's appearance depend on
// the viewer's theme.
func TestProgramDoesNotRepaintTheTerminal(t *testing.T) {
	written := string(runProgram(t, loadDemo(t),
		Options{Columns: 80, Lines: 24}, HostOptions{}))

	for _, osc := range []string{"\x1b]11;", "\x1b]111"} {
		if strings.Contains(written, osc) {
			t.Errorf("the program changed the terminal's background (%q)", osc)
		}
	}
}

// TestStatusRowIsPartOfTheBox checks the arithmetic rather than the picture:
// taking a row for the meter must shrink the animation's box, not overflow the
// screen.
func TestStatusRowIsPartOfTheBox(t *testing.T) {
	const columns, lines = 80, 24

	withMeter := newHost(loadDemo(t),
		Options{Columns: columns, Lines: lines},
		HostOptions{ShowStats: true})
	withoutMeter := newHost(loadDemo(t),
		Options{Columns: columns, Lines: lines},
		HostOptions{})

	withMeter.Init()
	withoutMeter.Init()

	// The player's box, which is what a host sizes.
	if got := len(strings.Split(withMeter.player.View(), "\n")); got != lines-1 {
		t.Errorf("with the meter the animation's box is %d rows, want %d", got, lines-1)
	}
	if got := len(strings.Split(withoutMeter.player.View(), "\n")); got != lines {
		t.Errorf("without the meter the animation's box is %d rows, want %d", got, lines)
	}

	// And the program's own view, which is that box plus the meter row.
	for name, host := range map[string]*host{"with": withMeter, "without": withoutMeter} {
		if got := len(strings.Split(host.View().Content, "\n")); got != lines {
			t.Errorf("%s the meter the view is %d rows, want %d", name, got, lines)
		}
	}
}
