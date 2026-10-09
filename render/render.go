// Package render turns decoded frames into a grid of glyphs and colours, and
// that grid into terminal output.
//
// It has no dependency on a terminal framework, on timing, or on keys: a grid is
// a plain arrangement of cells, so a host that only wants to know what a frame
// looks like can have that without taking on a player.
//
// Rendering is split in two: a Composer folds frames into a running canvas and
// resolves the visible window into a Grid, and the Grid turns itself into text.
// Keeping resolution separate from emission is what makes the screen comparable
// between implementations and testable without a terminal.
package render

import (
	"crypto/sha256"
	"strconv"
	"strings"

	"golang.org/x/text/width"

	"github.com/WhatDamon/go-nvaa-codec"
)

// Cell is one screen position with its style already resolved.
type Cell struct {
	Glyph string
	FG    nvaa.RGB
	BG    nvaa.RGB

	// Style is the style id, kept for the stats meter and diagnostics.
	Style uint32

	// Continuation marks the right half of a wide glyph. The glyph itself
	// paints that column, so writing anything there would erase half of it and
	// drag the rest of the row one column left.
	Continuation bool
}

// Grid is one frame's visible screen.
type Grid struct {
	// X0 and Y0 locate the window in canvas coordinates.
	X0, Y0        int
	Width, Height int

	// Cells is row-major with Width cells per row.
	Cells []Cell
}

// At returns the cell at a visible position; off-grid positions are empty.
func (g *Grid) At(vx, vy int) Cell {
	if vx < 0 || vy < 0 || vx >= g.Width || vy >= g.Height {
		return Cell{}
	}
	return g.Cells[vy*g.Width+vx]
}

// Row returns one visible row, or nil when vy is off the grid.
func (g *Grid) Row(vy int) []Cell {
	if vy < 0 || vy >= g.Height {
		return nil
	}
	return g.Cells[vy*g.Width : (vy+1)*g.Width]
}

func (g *Grid) Digest() [sha256.Size]byte {
	hasher := sha256.New()

	for vy := range g.Height {
		for vx := range g.Width {
			cell := g.At(vx, vy)
			if cell.Continuation {
				continue
			}
			hasher.Write([]byte(cell.Glyph))
			hasher.Write([]byte{0})
			hasher.Write([]byte{
				cell.FG.R, cell.FG.G, cell.FG.B,
				cell.BG.R, cell.BG.G, cell.BG.B,
			})
		}
	}

	var out [sha256.Size]byte
	copy(out[:], hasher.Sum(nil))
	return out
}

// Composer folds frames into a canvas and resolves visible windows.
//
// It holds one canvas rather than a list of frames, so memory stays flat in the
// frame count -- which is the difference between working and not on an
// 8704-frame animation.
type Composer struct {
	anim   *nvaa.Animation
	canvas *nvaa.Canvas
	wide   map[uint32]bool
}

// NewComposer starts an empty composer for an animation.
func NewComposer(anim *nvaa.Animation) *Composer {
	return &Composer{
		anim:   anim,
		canvas: nvaa.NewCanvas(anim.Width, anim.Height),
		wide:   make(map[uint32]bool),
	}
}

// Reset empties the canvas, for a replay or a rewind.
func (c *Composer) Reset() { c.canvas.Reset() }

// Apply folds a frame into the canvas.
func (c *Composer) Apply(f *nvaa.Frame) { c.canvas.Apply(f) }

// Canvas exposes the running world state.
func (c *Composer) Canvas() *nvaa.Canvas { return c.canvas }

// Animation exposes the animation being composed.
func (c *Composer) Animation() *nvaa.Animation { return c.anim }

// IsWide reports whether a glyph occupies two columns. Answers are cached
// because a grid asks this once per cell per frame.
func (c *Composer) IsWide(glyphID uint32) bool {
	if wide, ok := c.wide[glyphID]; ok {
		return wide
	}

	var wide bool
	if glyphID < uint32(len(c.anim.Glyphs)) {
		wide = IsWideGlyph(c.anim.Glyphs[glyphID])
	}
	c.wide[glyphID] = wide
	return wide
}

// Grid resolves the visible window of the current canvas.
func (c *Composer) Grid(f *nvaa.Frame, columns, lines int) *Grid {
	x0, y0, width, height := nvaa.ViewportWindow(c.anim, f, columns, lines)

	g := &Grid{
		X0:     x0,
		Y0:     y0,
		Width:  width,
		Height: height,
		Cells:  make([]Cell, width*height),
	}

	styles := make([]uint32, width)

	for vy := range height {
		for vx := range width {
			styles[vx] = c.canvas.At(uint32(x0+vx), uint32(y0+vy))
		}

		// Pair wide glyphs left to right, consuming both columns. Asking "is my
		// left neighbour wide?" instead would also condemn the cell *after* a
		// pair, because a continuation cell carries the same wide glyph as its
		// owner -- so a real glyph would be skipped.
		continuation := make([]bool, width)
		for vx := 0; vx < width-1; {
			if c.IsWide(c.styleGlyph(styles[vx])) {
				continuation[vx+1] = true
				vx += 2
			} else {
				vx++
			}
		}

		base := vy * width
		for vx := range width {
			cell := Cell{Style: styles[vx], Continuation: continuation[vx]}
			if style, ok := c.anim.StyleAt(styles[vx]); ok {
				if glyph, ok := c.anim.GlyphAt(style.Glyph); ok {
					cell.Glyph = glyph
				}
				if int(style.FG) < len(c.anim.Palette) {
					cell.FG = c.anim.Palette[style.FG]
				}
				if int(style.BG) < len(c.anim.Palette) {
					cell.BG = c.anim.Palette[style.BG]
				}
			}
			g.Cells[base+vx] = cell
		}
	}
	return g
}

// styleGlyph resolves a style id to its glyph id, or 0 when unknown.
func (c *Composer) styleGlyph(styleID uint32) uint32 {
	if style, ok := c.anim.StyleAt(styleID); ok {
		return style.Glyph
	}
	return 0
}

// FullRepaint renders every visible row behind an absolute cursor move.
//
// The interactive player does not use this: Bubble Tea differences successive
// frames itself, and duplicating that would be a second, worse implementation.
// It exists for the `render` subcommand and for parity checks, where a
// self-contained deterministic frame is exactly what is wanted.
func (g *Grid) FullRepaint() string {
	// Built by hand rather than with fmt: this runs once per frame over every
	// cell, and a formatting call per cell is measurable.
	buf := make([]byte, 0, g.Width*g.Height*6+64)

	for vy := range g.Height {
		buf = append(buf, "\x1b["...)
		buf = strconv.AppendInt(buf, int64(vy+1), 10)
		buf = append(buf, ";1H"...)

		var curFG, curBG nvaa.RGB
		haveFG, haveBG := false, false

		for vx := range g.Width {
			cell := g.At(vx, vy)
			if cell.Continuation {
				continue
			}
			if !haveFG || cell.FG != curFG {
				buf = AppendSGR(buf, 38, cell.FG)
				curFG, haveFG = cell.FG, true
			}
			if !haveBG || cell.BG != curBG {
				buf = AppendSGR(buf, 48, cell.BG)
				curBG, haveBG = cell.BG, true
			}
			buf = append(buf, cell.Glyph...)
		}
	}
	buf = append(buf, "\x1b[0m"...)
	return string(buf)
}

// AppendSGR writes a 24-bit colour sequence: ESC [ 38|48 ; 2 ; r ; g ; b m
//
// It is exported because a host that writes rows of its own -- padding a box,
// painting a sidebar -- needs the same sequence and should not invent a second
// spelling of it.
func AppendSGR(buf []byte, layer int, c nvaa.RGB) []byte {
	buf = append(buf, "\x1b["...)
	buf = strconv.AppendInt(buf, int64(layer), 10)
	buf = append(buf, ";2;"...)
	buf = strconv.AppendUint(buf, uint64(c.R), 10)
	buf = append(buf, ';')
	buf = strconv.AppendUint(buf, uint64(c.G), 10)
	buf = append(buf, ';')
	buf = strconv.AppendUint(buf, uint64(c.B), 10)
	return append(buf, 'm')
}

// Lines renders the grid as newline-joined rows, each carrying its own SGR
// escapes.
//
// Unlike FullRepaint it emits no cursor positioning, because the view library
// places the block itself and differences successive blocks. Absolute moves
// inside a View would fight that differencing instead of helping it.
// Fill is what a box puts in the part of itself the animation does not cover.
//
// It is a struct rather than a bare Cell because the two cases are different
// decisions, not different colours: an opaque fill copies the animation's own
// background (so the region reads as one rectangle), while a transparent one
// leaves the area to whatever is outside. Inferring the choice from a zero Cell
// would conflate "no fill" with "a fill of black", which are the same struct.
type Fill struct {
	Cell   Cell
	Opaque bool
}

// Box renders the grid into a box of columns x lines.
//
// The box is the animation's own region: whatever the grid does not cover is
// filled, so the region is painted edge to edge rather than left to show
// through. Nothing outside the box is written, which is what lets a host place
// the animation anywhere and own everything around it.
//
// An opaque fill always writes a space, never the fill's own glyph: a file that
// defines its empty style as some other character must not have that character
// tiled across the margin.
//
// Filling matters because a renderer is entitled to erase with whatever colour
// its pen holds, and an erase runs to the end of the terminal line rather than
// to the end of this box. A box painted in full leaves such an erase nothing to
// reveal.
func (g *Grid) Box(columns, lines int, fill Fill) string {
	var b strings.Builder

	for vy := range lines {
		var curFG, curBG nvaa.RGB
		haveFG, haveBG := false, false

		write := func(glyph string, fg, bg nvaa.RGB) {
			if glyph == "" {
				glyph = " "
			}
			if !haveFG || fg != curFG {
				b.Write(AppendSGR(nil, 38, fg))
				curFG, haveFG = fg, true
			}
			if !haveBG || bg != curBG {
				b.Write(AppendSGR(nil, 48, bg))
				curBG, haveBG = bg, true
			}
			b.WriteString(glyph)
		}

		column := 0
		if vy < g.Height {
			for vx := range g.Width {
				cell := g.At(vx, vy)
				if cell.Continuation {
					// Already drawn by the wide glyph that owns this column.
					continue
				}
				span := 1
				if IsWideGlyph(cell.Glyph) {
					span = 2
				}
				if column+span > columns {
					// A wide glyph with one column left has nowhere to go, and
					// writing it would push the row past its own box.
					write(" ", cell.FG, cell.BG)
					column++
					break
				}
				write(cell.Glyph, cell.FG, cell.BG)
				column += span
			}
		}

		if !fill.Opaque {
			// Reset first: an unstyled space is the terminal's background,
			// which is the opposite of continuing the last cell's colours.
			if haveFG || haveBG {
				b.WriteString("\x1b[0m")
				haveFG, haveBG = false, false
			}
			for ; column < columns; column++ {
				b.WriteByte(' ')
			}
		} else {
			for ; column < columns; column++ {
				write(" ", fill.Cell.FG, fill.Cell.BG)
			}
		}

		if haveFG || haveBG {
			b.WriteString("\x1b[0m")
		}
		if vy != lines-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Lines renders the grid at its own size, with nothing added.
func (g *Grid) Lines() string {
	return g.Box(g.Width, g.Height, Fill{})
}

// Plain renders glyphs only, one line per visible row. Continuation columns
// contribute nothing, because the wide glyph already covers them.
func (g *Grid) Plain() string {
	var b strings.Builder
	for vy := range g.Height {
		for vx := range g.Width {
			cell := g.At(vx, vy)
			if cell.Continuation {
				continue
			}
			b.WriteString(cell.Glyph)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// IsWideGlyph reports whether a glyph occupies two terminal columns.
//
// It mirrors the reference implementation: only East Asian Wide and Fullwidth
// count, and nothing below U+1100 is ever wide. The second clause is not
// redundant -- it keeps box-drawing and block characters, which some tables
// mark ambiguous, at a single column.
//
// The width property comes from x/text/width rather than a hand-rolled range
// table, so it tracks the same Unicode data as the reference's unicodedata.
func IsWideGlyph(s string) bool {
	for _, r := range s {
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
