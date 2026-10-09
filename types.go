package nvaa

import "sync"

// The decoded data model: the tables, the canvas, the camera, and one frame
// worth of changes.

// RGB is one palette entry, 24-bit true colour.
type RGB struct{ R, G, B uint8 }

// Style is a row of the style table: a glyph plus foreground and background
// palette indices. This triple is a cell's entire appearance, which is why a
// cell in a payload costs one varint instead of three fields.
type Style struct {
	Glyph uint32
	FG    uint32
	BG    uint32
}

// Cell is one payload entry, in canvas coordinates with the origin top-left.
type Cell struct {
	X, Y  uint32
	Style uint32
}

// Metadata value types. Each value is typed rather than free-form, so a decoder
// can read one it was not written for.
const (
	MetaNull uint8 = 0x00
	MetaUint uint8 = 0x01
	MetaInt  uint8 = 0x02
	MetaBool uint8 = 0x03
	MetaText uint8 = 0x04
	MetaBlob uint8 = 0x05
)

// MetaValue holds one typed value; Type selects which field is meaningful.
//
// There is deliberately no float type. A producer wanting a ratio writes a
// scaled integer instead, so no rounding behaviour has to be agreed on.
type MetaValue struct {
	Type uint8
	Uint uint64
	Int  int64
	Bool bool
	Text string
	Blob []byte
}

// Metadata is the descriptive block that precedes the frame stream.
//
// Entries are kept in file order, which the spec constrains to be strictly
// ascending by key. That makes lookup a linear scan over a handful of entries
// rather than a map, and it preserves the ordering for anyone re-encoding.
type Metadata struct {
	keys []string
	vals []MetaValue
}

// Len reports how many entries the block holds.
func (m Metadata) Len() int { return len(m.keys) }

// Key returns the key at position i.
func (m Metadata) Key(i int) string { return m.keys[i] }

// At returns the key and value at position i.
func (m Metadata) At(i int) (string, MetaValue) { return m.keys[i], m.vals[i] }

// Lookup finds a key. A missing key is not an error: unknown keys must be
// ignored, and an absent safety field means "not assessed" rather than "safe".
func (m Metadata) Lookup(key string) (MetaValue, bool) {
	for i, k := range m.keys {
		if k == key {
			return m.vals[i], true
		}
	}
	return MetaValue{}, false
}

// Canvas is the world state: a flat row-major grid of style ids.
//
// Style 0 is the empty style, so the zero value of the backing slice is already
// "nothing painted here". Using a dense slice rather than a map of painted
// cells keeps a 128x36 canvas at 18 KB and removes hashing from the delta path.
type Canvas struct {
	W, H uint32
	cell []uint32
}

// NewCanvas allocates an empty canvas.
//
// The dimensions of a parsed file are already bounded, so the usual call is safe
// by construction. A caller assembling a canvas by hand owns the same bound: the
// allocation is one uint32 per cell, and nothing here checks it.
func NewCanvas(w, h uint32) *Canvas {
	return &Canvas{W: w, H: h, cell: make([]uint32, uint64(w)*uint64(h))}
}

// At reads the style at (x, y). Out-of-range coordinates read as empty.
func (c *Canvas) At(x, y uint32) uint32 {
	if x >= c.W || y >= c.H {
		return 0
	}
	return c.cell[uint64(y)*uint64(c.W)+uint64(x)]
}

// Set writes a style at (x, y), ignoring coordinates off the canvas.
func (c *Canvas) Set(x, y, style uint32) {
	if x >= c.W || y >= c.H {
		return
	}
	c.cell[uint64(y)*uint64(c.W)+uint64(x)] = style
}

// Reset empties every cell, which is what a keyframe starts from.
func (c *Canvas) Reset() {
	clear(c.cell)
}

// Clone returns an independent copy of the canvas.
//
// A player keeps one of these per keyframe it has recently replayed: rewinding
// into a GOP means restoring the state its keyframe produced, and copying a canvas
// is cheaper than decoding the frame that painted it -- a keyframe paints every
// cell, which makes it the most expensive frame in a group.
func (c *Canvas) Clone() *Canvas {
	return &Canvas{W: c.W, H: c.H, cell: append([]uint32(nil), c.cell...)}
}

// CopyFrom replaces c's contents with other's.
//
// The two are expected to be the same size, which is what two canvases for one
// animation are. A mismatch is honoured rather than ignored: silently keeping the
// old grid would produce a picture that is wrong in a way nothing reports.
func (c *Canvas) CopyFrom(other *Canvas) {
	if other == nil {
		c.Reset()
		return
	}
	if c.W != other.W || c.H != other.H {
		c.W, c.H = other.W, other.H
		c.cell = make([]uint32, uint64(other.W)*uint64(other.H))
	}
	copy(c.cell, other.cell)
}

// Apply folds a frame into the canvas: a keyframe clears first, then the
// payload is written over the top. Writing style 0 therefore erases a cell
// rather than leaving it alone.
func (c *Canvas) Apply(f *Frame) {
	if f.Keyframe {
		c.Reset()
	}
	for _, cell := range f.Payload {
		c.Set(cell.X, cell.Y, cell.Style)
	}
}

// CellCount reports the grid area.
func (c *Canvas) CellCount() uint64 { return uint64(c.W) * uint64(c.H) }

// Row returns a view of one row, or nil when y is off the canvas.
func (c *Canvas) Row(y uint32) []uint32 {
	if y >= c.H {
		return nil
	}
	start := uint64(y) * uint64(c.W)
	return c.cell[start : start+uint64(c.W)]
}

// Frame is one frame unit after its header has been decoded. Payload lists only
// the cells this frame mentions; merge it into a Canvas for a picture.
type Frame struct {
	Index      int
	Flags      uint8
	DurationMS uint64
	Keyframe   bool
	CameraX    int64
	CameraY    int64
	ViewportW  uint32
	ViewportH  uint32
	Payload    []Cell
}

// frameInfo is what walking the frame headers produces. The payload bytes stay
// in the original buffer, so only decoding is deferred -- not the work of
// finding out where each frame begins.
type frameInfo struct {
	offset       int
	payloadStart int
	payloadLen   int
	flags        uint8
	durationMS   uint64
	cameraX      int64
	cameraY      int64
	viewportW    uint32
	viewportH    uint32
	payload      []byte
}

// Animation is a parsed container. Construction validates everything except
// frame payloads; Frames does that as it yields (see the two-phase note in
// Parse).
type Animation struct {
	Version uint16
	Flags   uint8

	Width  uint32
	Height uint32

	FPSNum uint16
	FPSDen uint16
	// FrameMS is the nominal frame interval, round(FPSDen*1000/FPSNum).
	FrameMS uint64

	Palette []RGB
	Glyphs  []string
	Styles  []Style

	Metadata Metadata

	HasCamera bool
	HasIndex  bool
	HasCRC    bool
	HasMeta   bool
	Index     []uint32
	CRC       uint32

	// Size is the file length the container was parsed from, kept so callers
	// can report payload share without re-measuring.
	Size int

	frames []frameInfo
	blob   []byte

	// prefix holds the start time of every frame, with the total in the last
	// slot: prefix[i] is when frame i begins and prefix[n] is the end. Built once
	// by Parse so that locating a frame by time is a binary search rather than a
	// sum over every frame -- seeking should not get slower as the animation
	// grows.
	prefix []uint64

	// keyframes holds the indices that begin a GOP, and keyframeOnce guards
	// building it. It is also read-only after that; the once is for animations
	// assembled by hand rather than by Parse, where the first caller to ask may be
	// any caller.
	keyframeOnce sync.Once
	keyframes    []int
}

// FrameCount reports the frame count from the header.
func (a *Animation) FrameCount() int { return len(a.frames) }

// StyleAt resolves a style id to its cell appearance.
func (a *Animation) StyleAt(id uint32) (Style, bool) {
	if id >= uint32(len(a.Styles)) {
		return Style{}, false
	}
	return a.Styles[id], true
}

// GlyphAt resolves a glyph id to its text.
func (a *Animation) GlyphAt(id uint32) (string, bool) {
	if id >= uint32(len(a.Glyphs)) {
		return "", false
	}
	return a.Glyphs[id], true
}
