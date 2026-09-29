// Package termtest replays escape sequences into a grid, for tests.
//
// It exists because more than one package needs to ask what a terminal would
// have shown. Two interpreters that disagree would make a disagreement between
// the things under test impossible to attribute, so there is one, here, and both
// sides feed their output to it.
//
// It is a model, not an emulator: it understands the sequences this repository's
// code emits, and it is deliberately strict about the two details that produced
// confident but wrong measurements once already.
//
//   - A private-mode sequence such as ESC[?2026h is not text. The CSI grammar
//     carries parameters in 0x30-0x3f, which includes ? > < and =, and anything
//     parsed loosely leaves the '[' to be drawn into the grid.
//   - 38 and 48 swallow their own arguments. A colour component that happens to
//     be 0 or a selector that happens to be 49 must not be read as a reset, or
//     every black component invents an unpainted cell.
package termtest

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/width"
)

// Cell is what a position ends up showing.
type Cell struct {
	// Glyph is the character drawn there, empty if none was.
	Glyph string

	// BG is the background as it was set, "" if none ever was. An empty
	// background means the cell still shows whatever the terminal had there,
	// which is a different claim from "painted black".
	BG string
}

// Screen is a grid that escape sequences are replayed into.
//
// The cursor movement and repeat sequences the renderer uses are implemented
// rather than ignored, because ignoring them does not merely lose a shortcut:
// the cursor keeps drifting, every following character lands in the wrong
// column, and the result looks like corrupted output instead of a gap in the
// model.
type Screen struct {
	Width, Height int

	cells []Cell
	x, y  int
	bg    string

	// top and bottom bound the scrolling region, inclusive. A renderer that
	// sets one and then writes past its last row is relying on the scroll, and
	// a model that merely clamps leaves the previous frame in place -- which
	// reads as scrambled output rather than as a missing feature.
	top, bottom int

	// last is what a repeat sequence would repeat.
	last rune
	has  bool

	// CRLF says a newline also returns the column, which is what a tty does to
	// one in its default output mode.
	//
	// The default is a bare line feed, because that is what a terminal in raw
	// mode receives and therefore what a renderer emits. A view string is not
	// terminal output at all -- it is a block of rows for a host to place -- so
	// replaying one means asking for this.
	CRLF bool
}

// New makes an empty screen, with every position unpainted.
func New(width, height int) *Screen {
	return &Screen{
		Width:  width,
		Height: height,
		cells:  make([]Cell, width*height),
		bottom: height - 1,
	}
}

// At reports what a position shows. Out-of-range positions read as empty.
func (s *Screen) At(x, y int) Cell {
	if x < 0 || y < 0 || x >= s.Width || y >= s.Height {
		return Cell{}
	}
	return s.cells[y*s.Width+x]
}

// BG is the background of a position, "" when nothing set one.
func (s *Screen) BG(x, y int) string { return s.At(x, y).BG }

// Painted reports how many positions in a rectangle have a background.
func (s *Screen) Painted(x0, y0, x1, y1 int) int {
	count := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			if s.BG(x, y) != "" {
				count++
			}
		}
	}
	return count
}

// Row is the text of one row, with trailing blanks removed.
func (s *Screen) Row(y int) string {
	var b strings.Builder
	for x := range s.Width {
		glyph := s.At(x, y).Glyph
		if glyph == "" {
			glyph = " "
		}
		b.WriteString(glyph)
	}
	return strings.TrimRight(b.String(), " ")
}

// Feed replays bytes.
func (s *Screen) Feed(data []byte) {
	for i := 0; i < len(data); {
		switch b := data[i]; {
		case b == 0x1b:
			i += s.escape(data[i:])
		case b == '\n':
			// A bare line feed: down a row, column unchanged. A tty would turn
			// this into a carriage return too, but the writers here run it in raw
			// mode, which switches that translation off -- and the renderer
			// compensates by emitting a backspace when it wants the column back.
			// Modelling the translation would therefore double-count it and push
			// every row after the first off to the right.
			s.lineFeed()
			if s.CRLF {
				s.x = 0
			}
			i++
		case b == '\b':
			s.x = max(0, s.x-1)
			i++
		case b == '\r':
			s.x = 0
			i++
		case b < 0x20:
			i++
		default:
			r, size := utf8.DecodeRune(data[i:])
			if size == 0 {
				i++
				continue
			}
			s.draw(r)
			i += size
		}
	}
}

// FeedString replays a string.
func (s *Screen) FeedString(data string) { s.Feed([]byte(data)) }

// draw puts one rune at the cursor and advances by its display width.
func (s *Screen) draw(r rune) {
	s.last, s.has = r, true

	glyph := string(r)
	s.put(s.x, s.y, Cell{Glyph: glyph, BG: s.bg})

	span := 1
	if wide(glyph) {
		span = 2
	}
	// A wide glyph owns the column after it too, so that position is marked as
	// covered rather than left looking like untouched terminal.
	if span == 2 {
		s.put(s.x+1, s.y, Cell{Glyph: "", BG: s.bg})
	}
	s.x += span
}

func (s *Screen) put(x, y int, cell Cell) {
	if x < 0 || y < 0 || x >= s.Width || y >= s.Height {
		return
	}
	s.cells[y*s.Width+x] = cell
}

// lineFeed moves down one row, scrolling when it is already at the bottom of
// the scrolling region.
func (s *Screen) lineFeed() {
	if s.y == s.bottom {
		s.scrollUp(1)
		return
	}
	s.y = min(s.y+1, s.Height-1)
}

// scrollUp shifts the scrolling region up by n rows, filling what it exposes
// with the current background, which is how a terminal erases on scroll.
func (s *Screen) scrollUp(n int) {
	n = min(n, s.bottom-s.top+1)

	for y := s.top; y <= s.bottom-n; y++ {
		copy(
			s.cells[y*s.Width:(y+1)*s.Width],
			s.cells[(y+n)*s.Width:(y+n+1)*s.Width],
		)
	}
	for y := s.bottom - n + 1; y <= s.bottom; y++ {
		for x := range s.Width {
			s.cells[y*s.Width+x] = Cell{BG: s.bg}
		}
	}
}

// escape consumes one escape sequence and returns its length.
func (s *Screen) escape(data []byte) int {
	if len(data) < 2 {
		return 1
	}

	switch data[1] {
	case '[':
		j := 2
		for j < len(data) && data[j] >= 0x30 && data[j] <= 0x3f {
			j++
		}
		params := string(data[2:j])
		for j < len(data) && data[j] >= 0x20 && data[j] <= 0x2f {
			j++
		}
		if j >= len(data) {
			return len(data)
		}
		s.csi(parse(params), data[j])
		return j + 1

	case ']':
		// Operating system command, ended by BEL or by string terminator.
		for j := 2; j < len(data); j++ {
			if data[j] == 0x07 {
				return j + 1
			}
			if data[j] == 0x1b && j+1 < len(data) && data[j+1] == '\\' {
				return j + 2
			}
		}
		return len(data)

	case '(':
		// Character set selection: two bytes.
		return 3

	default:
		return 2
	}
}

func (s *Screen) csi(p []int, final byte) {
	switch final {
	case 'H', 'f':
		s.y, s.x = param(p, 0, 1)-1, param(p, 1, 1)-1

	case 'A':
		s.y = max(0, s.y-param(p, 0, 1))

	case 'B':
		s.y = min(s.Height-1, s.y+param(p, 0, 1))

	case 'C':
		s.x = min(s.Width-1, s.x+param(p, 0, 1))

	case 'D':
		s.x = max(0, s.x-param(p, 0, 1))

	case 'G', '`':
		// Column absolute: the renderer uses it to skip runs of cells it can
		// leave alone.
		s.x = min(s.Width-1, param(p, 0, 1)-1)

	case 'd':
		// Line absolute, the vertical counterpart of the one above.
		s.y = min(s.Height-1, param(p, 0, 1)-1)

	case 'r':
		// Set the scrolling region. With no parameters it is the whole screen.
		top, bottom := param(p, 0, 1)-1, param(p, 1, s.Height)-1
		if top < 0 || bottom >= s.Height || top >= bottom {
			top, bottom = 0, s.Height-1
		}
		s.top, s.bottom = top, bottom
		// Setting the region homes the cursor to its first line, as a terminal
		// does, so a stream that relies on that lands where it expects to.
		s.x, s.y = 0, top

	case 'b':
		// Repeat the last character. The renderer uses it for runs of one
		// glyph, which this picture is full of.
		if s.has {
			for range param(p, 0, 1) {
				s.draw(s.last)
			}
		}

	case 'K':
		// Erase in line: 0 from the cursor to the end, 1 from the start to the
		// cursor, 2 the whole line. Getting the second one wrong erases the
		// content after the cursor as well, which does not read as a gap in the
		// model -- it reads as scrambled output.
		from, to := s.x, s.Width-1
		switch param(p, 0, 0) {
		case 1:
			from, to = 0, s.x
		case 2:
			from, to = 0, s.Width-1
		}
		for x := from; x <= to; x++ {
			s.fill(x, s.y)
		}

	case 'X':
		// Erase character. The cursor stays where it is: the renderer emits an
		// explicit move when it needs one, which is how we know. Advancing here
		// instead drifts a little further out of position on every run of erased
		// cells, and the drift shows up as content in the wrong pane rather than
		// as an obviously broken picture.
		n := param(p, 0, 1)
		for i := range n {
			s.fill(s.x+i, s.y)
		}

	case 'J':
		// Erase in display: 0 from the cursor down, 1 from the top down to the
		// cursor, 2 everything. Like an erase in line, the colour is the pen's.
		switch param(p, 0, 0) {
		case 0:
			for y := s.y; y < s.Height; y++ {
				start := 0
				if y == s.y {
					start = s.x
				}
				for x := start; x < s.Width; x++ {
					s.fill(x, y)
				}
			}
		case 1:
			for y := 0; y <= s.y; y++ {
				end := s.Width
				if y == s.y {
					end = s.x + 1
				}
				for x := 0; x < end; x++ {
					s.fill(x, y)
				}
			}
		case 2:
			for y := range s.Height {
				for x := range s.Width {
					s.fill(x, y)
				}
			}
		}

	case 'm':
		s.sgr(p)
	}
}

// fill erases a position: the character becomes a blank and the colour becomes
// the pen's.
// Both halves matter. An erase in a terminal with background colour erase leaves
// a coloured space, so keeping the old character would leave the previous
// frame's text showing through every frame that follows it -- and the result
// reads as a renderer drawing the wrong thing rather than as a gap in the model.
func (s *Screen) fill(x, y int) {
	s.put(x, y, Cell{BG: s.bg})
}

// sgr applies a graphic rendition.
//
// The loop steps over each selector's arguments rather than inspecting them one
// by one, which is what stops a colour component from being read as a reset.
func (s *Screen) sgr(p []int) {
	if len(p) == 0 {
		s.bg = ""
		return
	}

	for i := 0; i < len(p); i++ {
		switch v := p[i]; {
		case v == 0 || v == 49:
			s.bg = ""
		case v == 48:
			switch {
			case i+1 < len(p) && p[i+1] == 5:
				s.bg = "48;5;" + strconv.Itoa(arg(p, i+2))
				i += 2
			case i+1 < len(p) && p[i+1] == 2:
				s.bg = "48;2;" + strconv.Itoa(arg(p, i+2)) + ";" +
					strconv.Itoa(arg(p, i+3)) + ";" + strconv.Itoa(arg(p, i+4))
				i += 4
			}
		case v == 38:
			switch {
			case i+1 < len(p) && p[i+1] == 5:
				i += 2
			case i+1 < len(p) && p[i+1] == 2:
				i += 4
			}
		}
	}
}

// parse reads CSI parameters, keeping the private prefixes out of the numbers.
func parse(params string) []int {
	if params == "" {
		return nil
	}
	fields := strings.Split(params, ";")
	out := make([]int, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimLeft(field, "?><!=\"")
		if field == "" {
			out = append(out, 0)
			continue
		}
		value, err := strconv.Atoi(field)
		if err != nil {
			out = append(out, 0)
			continue
		}
		out = append(out, value)
	}
	return out
}

func param(p []int, i, fallback int) int {
	if i >= len(p) || p[i] == 0 {
		return fallback
	}
	return p[i]
}

func arg(p []int, i int) int {
	if i < len(p) {
		return p[i]
	}
	return 0
}

// wide reports whether a glyph takes two columns.
//
// It mirrors the rule the renderer uses, and is repeated rather than imported
// because this package is fed by tests that live inside the package it would
// otherwise depend on.
func wide(glyph string) bool {
	for _, r := range glyph {
		if r < 0x1100 {
			return false
		}
		switch width.LookupRune(r).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			return true
		}
		return false
	}
	return false
}
