package player

import (
	"fmt"
	"io"
	"strings"

	"github.com/WhatDamon/go-nvaa-codec/render"
	"github.com/charmbracelet/x/term"
)

// Meter records what playback actually cost the terminal.
//
// Two of its numbers are measured and one is a baseline. Emitted counts bytes
// that really went to the terminal, captured by wrapping the writer Bubble Tea
// is given. DirtyCells and ScreenCells come from diffing successive grids.
// FullRepaint is not a measurement: it is the exact byte length of the repaint
// this frame would have needed, so the saving can be stated against something
// concrete rather than asserted.
type Meter struct {
	Frames      int
	DirtyCells  int
	ScreenCells int

	Emitted     int64
	FullRepaint int64

	previous *render.Grid
}

// NewMeter starts an empty meter.
func NewMeter() *Meter { return &Meter{} }

// Observe records one presented frame.
func (m *Meter) Observe(grid *render.Grid) {
	m.Frames++
	m.ScreenCells += grid.Width * grid.Height

	if m.previous != nil {
		m.DirtyCells += diffCells(m.previous, grid)
	} else {
		// With nothing on screen, every cell is new.
		m.DirtyCells += grid.Width * grid.Height
	}
	m.previous = grid

	m.FullRepaint += int64(len(grid.FullRepaint()))
}

// ObserveBaseline adds a frame's cost without diffing, for non-interactive
// rendering where there is no previous screen.
func (m *Meter) ObserveBaseline(grid *render.Grid) {
	m.Frames++
	m.ScreenCells += grid.Width * grid.Height
	m.DirtyCells += grid.Width * grid.Height
	m.FullRepaint += int64(len(grid.FullRepaint()))
}

// diffCells counts positions whose resolved appearance changed.
//
// Content, not style id, is compared: a style-table reshuffle that leaves the
// screen identical should not count as traffic.
func diffCells(before, after *render.Grid) int {
	if before.Width != after.Width || before.Height != after.Height {
		return after.Width * after.Height
	}

	changed := 0
	for i := range after.Cells {
		a, b := before.Cells[i], after.Cells[i]
		if a.Glyph != b.Glyph || a.FG != b.FG || a.BG != b.BG {
			changed++
		}
	}
	return changed
}

// Report summarises the run.
func (m *Meter) Report() string {
	var b strings.Builder

	pct := func(part, whole int) float64 {
		if whole == 0 {
			return 0
		}
		return 100 * float64(part) / float64(whole)
	}

	fmt.Fprintf(&b, "frames shown        %d\n", m.Frames)
	fmt.Fprintf(&b, "dirty cells         %d of %d (%.1f%%)\n",
		m.DirtyCells, m.ScreenCells, pct(m.DirtyCells, m.ScreenCells))
	fmt.Fprintf(&b, "terminal output     %d bytes (%s)\n",
		m.Emitted, bytesPerFrame(m.Emitted, m.Frames))

	if m.FullRepaint > 0 {
		saved := 100 * (1 - float64(m.Emitted)/float64(m.FullRepaint))
		fmt.Fprintf(&b, "full repaint would  %d bytes (%s)\n",
			m.FullRepaint, bytesPerFrame(m.FullRepaint, m.Frames))
		fmt.Fprintf(&b, "saving              %.1f%%\n", saved)
	}
	return b.String()
}

// bytesPerFrame formats a per-frame average.
func bytesPerFrame(total int64, frames int) string {
	if frames == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f B/frame", float64(total)/float64(frames))
}

// meterOutput wraps w so that what Bubble Tea writes can be counted, while
// staying transparent to terminal detection.
//
// The transparency is the whole point. Bubble Tea decides whether its output is
// a terminal by asserting `output.(term.File)`, so a wrapper exposing only
// Write() makes a real terminal look like a pipe. When that assertion fails the
// colour profile resolves to none -- every colour is silently dropped -- and the
// terminal size is never queried, so the view is laid out for a default size
// rather than the real one. Both symptoms come from the one missing method,
// which is why a term.File goes in and a term.File comes out.
func meterOutput(w io.Writer) (io.Writer, *countingWriter) {
	counter := &countingWriter{}

	if file, ok := w.(term.File); ok {
		counter.inner = file
		return &terminalWriter{counter: counter, file: file}, counter
	}

	// Not a terminal (a buffer, a pipe): nothing to introspect, so the plain
	// wrapper cannot mislead anyone.
	counter.inner = w
	return counter, counter
}

// countingWriter tallies what is written through it.
//
// Bubble Tea decides what to write, so the only way to report the real traffic
// rather than an estimate is to count at the writer.
type countingWriter struct {
	inner io.Writer
	count int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.inner.Write(p)
	w.count += int64(n)
	return n, err
}

// Count reports the bytes written so far.
func (w *countingWriter) Count() int64 { return w.count }

// terminalWriter is a countingWriter that still looks like the terminal it
// wraps, so terminal introspection survives being metered.
//
// Read, Close and Fd exist because term.File requires io.ReadWriteCloser.
// Bubble Tea only ever writes to its output and never reads it, so Read reports
// EOF; and Close deliberately does nothing, because closing a terminal the
// caller owns is not this type's decision to make.
type terminalWriter struct {
	counter *countingWriter
	file    term.File
}

func (w *terminalWriter) Write(p []byte) (int, error) { return w.counter.Write(p) }
func (w *terminalWriter) Read([]byte) (int, error)    { return 0, io.EOF }
func (w *terminalWriter) Close() error                { return nil }
func (w *terminalWriter) Fd() uintptr                 { return w.file.Fd() }
