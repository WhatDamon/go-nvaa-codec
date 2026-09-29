package nvaa

import (
	"bytes"
	"compress/zlib"
	"hash/crc32"
	"io"
	"iter"
	"os"
	"sort"
	"unicode/utf8"
)

// Version is the container layout this package speaks.
const Version uint16 = 1

// maxInflated bounds a decompressed payload, and with it the memory a hostile
// stream can ask for: zlib will turn a few kilobytes into gigabytes given the
// chance. A frame can name at most one cell per canvas position, so the limit
// derived from the canvas area is the meaningful one for real content; this
// ceiling only starts to bind on a canvas larger than a terminal has ever been.
const maxInflated = 64 << 20

// maxCanvasCells bounds the grid a header may describe.
//
// A decoder holds one cell per position, so without a bound a few bytes of header
// would name an allocation of any size: 1048576x1048576 is a terabyte of cells
// described by two varints, and no terminal has ever been that large. The limit
// is still generous -- 2^22 cells is a 2048x2048 grid, against 128x36 for the
// largest animation this library has been pointed at.
const maxCanvasCells = 1 << 22

// maxTableHint caps the capacity pre-allocated for a table whose entry count came
// from the stream.
//
// The count is ranged-checked against the bytes available before the table is
// read, so this is not a correctness bound: it stops a truthful but enormous count
// from reserving a slice far larger than the file that would have to justify it.
// Anything past the hint is reached by append, so memory follows the entries that
// are really present.
const maxTableHint = 4096

// tableHint suggests a capacity for such a table.
func tableHint(count uint64) int {
	return int(min(count, maxTableHint))
}

// Parse reads a container and validates everything except frame payloads:
// signature, brand, version, flags, tables, metadata, the frame headers, the
// index, and the checksum.
//
// Payloads are left alone deliberately. The metadata block sits ahead of the
// frame stream so a consumer can read a safety warning without decoding
// anything, and eager payload validation would defeat that. The
// payload MUSTs still hold -- they are enforced by Frames, which is why the
// conformance contract is phrased as "decoding must fail" rather than "parsing
// must fail".
func Parse(blob []byte) (*Animation, error) {
	// Signature and brand together are the smallest possible stream.
	if len(blob) < len(Signature)+len(Brand) {
		return nil, errTruncated(0, "signature and brand")
	}
	if !bytes.Equal(blob[:len(Signature)], Signature) {
		return nil, errAt(0, "bad signature")
	}
	off := len(Signature)
	if !bytes.Equal(blob[off:off+len(Brand)], Brand) {
		return nil, errAt(off, "bad brand, want %q", Brand)
	}
	off += len(Brand)

	version, next, err := readUint16(blob, off)
	if err != nil {
		return nil, err
	}
	off = next
	if version != Version {
		return nil, errAt(off-2, "unsupported version %d", version)
	}

	if off >= len(blob) {
		return nil, errTruncated(off, "header flags")
	}
	flags := blob[off]
	off++
	if flags&headerReservedMask != 0 {
		return nil, errAt(off-1, "reserved header flag bits set (0x%02x)", flags&headerReservedMask)
	}

	a := &Animation{Version: version, Flags: flags, Size: len(blob), blob: blob}
	a.HasCamera = flags&headerHasCamera != 0
	a.HasIndex = flags&headerHasIndex != 0
	a.HasCRC = flags&headerHasCRC != 0
	a.HasMeta = flags&headerHasMeta != 0

	width, next, err := readUvarint(blob, off)
	if err != nil {
		return nil, err
	}
	height, next2, err := readUvarint(blob, next)
	if err != nil {
		return nil, err
	}
	off = next2
	if width < 1 || height < 1 {
		return nil, errAt(off, "canvas %dx%d must be at least 1x1", width, height)
	}
	if width > maxCanvasCells || height > maxCanvasCells || width*height > maxCanvasCells {
		// Three comparisons rather than one product: either dimension alone can be
		// enormous, and the product of two of them would wrap.
		return nil, errAt(off,
			"canvas %dx%d is larger than the %d cells a decoder will hold",
			width, height, maxCanvasCells)
	}
	a.Width, a.Height = uint32(width), uint32(height)

	fpsNum, next, err := readUint16(blob, off)
	if err != nil {
		return nil, err
	}
	fpsDen, next2, err := readUint16(blob, next)
	if err != nil {
		return nil, err
	}
	off = next2
	if fpsNum < 1 || fpsDen < 1 {
		return nil, errAt(off, "fps %d/%d must be at least 1/1", fpsNum, fpsDen)
	}
	a.FPSNum, a.FPSDen = fpsNum, fpsDen
	a.FrameMS = nominalMS(fpsNum, fpsDen)

	paletteCount, next, err := readUvarint(blob, off)
	if err != nil {
		return nil, err
	}
	glyphCount, next2, err := readUvarint(blob, next)
	if err != nil {
		return nil, err
	}
	styleCount, next3, err := readUvarint(blob, next2)
	if err != nil {
		return nil, err
	}
	frameCount, next4, err := readUvarint(blob, next3)
	if err != nil {
		return nil, err
	}
	off = next4
	if paletteCount < 1 || glyphCount < 1 || styleCount < 1 || frameCount < 1 {
		return nil, errAt(off, "table and frame counts must each be at least 1")
	}

	if a.Palette, off, err = parsePalette(blob, off, paletteCount); err != nil {
		return nil, err
	}
	if a.Glyphs, off, err = parseGlyphs(blob, off, glyphCount); err != nil {
		return nil, err
	}
	if a.Styles, off, err = parseStyles(blob, off, styleCount, glyphCount, paletteCount); err != nil {
		return nil, err
	}
	if a.HasMeta {
		if a.Metadata, off, err = parseMetadata(blob, off); err != nil {
			return nil, err
		}
	}

	// Every frame unit spends at least a flags byte and a payload length, so a
	// count larger than half of what is left cannot be satisfied. Measuring the
	// claim against the file first keeps a few header bytes from reserving
	// bookkeeping for frames that were never written.
	if remaining := len(blob) - off; frameCount > uint64(remaining)/2 {
		return nil, errAt(off,
			"frame count %d cannot fit in the remaining %d bytes", frameCount, remaining)
	}

	// Walk the frame headers. This locates every payload and picks up the
	// per-frame scalars, but decodes nothing.
	if a.frames, off, err = parseFrames(blob, off, a, int(frameCount)); err != nil {
		return nil, err
	}
	a.buildTimeIndex()

	if a.HasIndex {
		if off, err = parseIndex(blob, off, a); err != nil {
			return nil, err
		}
	}
	if a.HasCRC {
		if off, err = parseCRC(blob, off, a); err != nil {
			return nil, err
		}
	}

	// The end marker is recommended rather than required, so its absence is
	// tolerable. Its presence must be exactly here.
	if bytes.HasPrefix(blob[off:], MarkerStreamEnd) {
		off += len(MarkerStreamEnd)
	}
	if off != len(blob) {
		return nil, errAt(off, "%d trailing bytes after the end of the stream", len(blob)-off)
	}
	return a, nil
}

// ReadFile reads and parses a .nvaa file.
func ReadFile(path string) (*Animation, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(blob)
}

// nominalMS is the nominal frame interval in milliseconds.
func nominalMS(num, den uint16) uint64 {
	return (uint64(den)*1000 + uint64(num)/2) / uint64(num)
}

// parsePalette reads palette_count RGB888 triples.
func parsePalette(b []byte, off int, count uint64) ([]RGB, int, error) {
	if count > uint64(len(b))/3 {
		return nil, off, errTruncated(off, "palette")
	}
	palette := make([]RGB, count)
	for i := range count {
		c, next, err := take(b, off, 3)
		if err != nil {
			return nil, off, err
		}
		off = next
		palette[i] = RGB{R: c[0], G: c[1], B: c[2]}
	}
	return palette, off, nil
}

// parseGlyphs reads length-prefixed UTF-8 glyphs.
func parseGlyphs(b []byte, off int, count uint64) ([]string, int, error) {
	if count > uint64(len(b)) {
		return nil, off, errTruncated(off, "glyph table")
	}
	glyphs := make([]string, 0, tableHint(count))
	for i := range count {
		n, next, err := readUvarint(b, off)
		if err != nil {
			return nil, off, err
		}
		if n > uint64(len(b)) {
			return nil, next, errTruncated(next, "glyph bytes")
		}
		raw, next2, err := take(b, next, int(n))
		if err != nil {
			return nil, next, err
		}
		off = next2
		if !utf8.Valid(raw) {
			return nil, off, errAt(off, "glyph %d is not valid UTF-8", i)
		}
		glyphs = append(glyphs, string(raw))
	}
	return glyphs, off, nil
}

// parseStyles reads glyph/fg/bg triples and range-checks each index.
func parseStyles(b []byte, off int, count, glyphCount, paletteCount uint64) ([]Style, int, error) {
	if count > uint64(len(b)) {
		return nil, off, errTruncated(off, "style table")
	}
	styles := make([]Style, 0, tableHint(count))
	for i := range count {
		g, next, err := readUvarint(b, off)
		if err != nil {
			return nil, off, err
		}
		fg, next2, err := readUvarint(b, next)
		if err != nil {
			return nil, off, err
		}
		bg, next3, err := readUvarint(b, next2)
		if err != nil {
			return nil, off, err
		}
		off = next3
		if g >= glyphCount {
			return nil, off, errAt(off, "style %d glyph id %d out of range (%d)", i, g, glyphCount)
		}
		if fg >= paletteCount {
			return nil, off, errAt(off, "style %d fg id %d out of range (%d)", i, fg, paletteCount)
		}
		if bg >= paletteCount {
			return nil, off, errAt(off, "style %d bg id %d out of range (%d)", i, bg, paletteCount)
		}
		styles = append(styles, Style{Glyph: uint32(g), FG: uint32(fg), BG: uint32(bg)})
	}
	return styles, off, nil
}

// parseMetadata reads the typed key/value block.
//
// Unknown keys are skipped because that is what makes the block extensible.
// An unknown value type is refused because its payload cannot be measured, so
// there is no safe way to continue past it.
func parseMetadata(b []byte, off int) (Metadata, int, error) {
	count, next, err := readUvarint(b, off)
	if err != nil {
		return Metadata{}, off, err
	}
	off = next
	if count > uint64(len(b)) {
		return Metadata{}, off, errTruncated(off, "metadata block")
	}

	md := Metadata{
		keys: make([]string, 0, tableHint(count)),
		vals: make([]MetaValue, 0, tableHint(count)),
	}

	for i := range count {
		keyLen, next, err := readUvarint(b, off)
		if err != nil {
			return Metadata{}, off, err
		}
		if keyLen > uint64(len(b)) {
			return Metadata{}, next, errTruncated(next, "metadata key")
		}
		keyBytes, next2, err := take(b, next, int(keyLen))
		if err != nil {
			return Metadata{}, next, err
		}
		off = next2

		if len(keyBytes) == 0 {
			return Metadata{}, off, errAt(off, "metadata key %d is empty", i)
		}
		if !utf8.Valid(keyBytes) {
			return Metadata{}, off, errAt(off, "metadata key %d is not valid UTF-8", i)
		}
		key := string(keyBytes)

		// Keys are in canonical order, so ascending comparison is enough to
		// rule out both a duplicate and a mis-sorted block.
		if i > 0 && key <= md.keys[len(md.keys)-1] {
			return Metadata{}, off, errAt(off,
				"metadata keys must strictly ascend: %q follows %q",
				key, md.keys[len(md.keys)-1])
		}

		if off >= len(b) {
			return Metadata{}, off, errTruncated(off, "metadata value type")
		}
		valueType := b[off]
		off++

		value, next, err := parseMetaValue(b, off, valueType)
		if err != nil {
			return Metadata{}, off, err
		}
		off = next

		md.keys = append(md.keys, key)
		md.vals = append(md.vals, value)
	}
	return md, off, nil
}

// parseMetaValue decodes one typed value.
func parseMetaValue(b []byte, off int, valueType uint8) (MetaValue, int, error) {
	switch valueType {
	case MetaNull:
		return MetaValue{Type: MetaNull}, off, nil

	case MetaUint:
		u, next, err := readUvarint(b, off)
		if err != nil {
			return MetaValue{}, off, err
		}
		return MetaValue{Type: MetaUint, Uint: u}, next, nil

	case MetaInt:
		v, next, err := readSvarint(b, off)
		if err != nil {
			return MetaValue{}, off, err
		}
		return MetaValue{Type: MetaInt, Int: v}, next, nil

	case MetaBool:
		if off >= len(b) {
			return MetaValue{}, off, errTruncated(off, "bool value")
		}
		c := b[off]
		if c > 1 {
			return MetaValue{}, off, errAt(off, "bool value must be 0 or 1, got %d", c)
		}
		return MetaValue{Type: MetaBool, Bool: c == 1}, off + 1, nil

	case MetaText:
		n, next, err := readUvarint(b, off)
		if err != nil {
			return MetaValue{}, off, err
		}
		if n > uint64(len(b)) {
			return MetaValue{}, next, errTruncated(next, "text value")
		}
		raw, next2, err := take(b, next, int(n))
		if err != nil {
			return MetaValue{}, next, err
		}
		if !utf8.Valid(raw) {
			return MetaValue{}, next2, errAt(next2, "text value is not valid UTF-8")
		}
		return MetaValue{Type: MetaText, Text: string(raw)}, next2, nil

	case MetaBlob:
		n, next, err := readUvarint(b, off)
		if err != nil {
			return MetaValue{}, off, err
		}
		if n > uint64(len(b)) {
			return MetaValue{}, next, errTruncated(next, "blob value")
		}
		raw, next2, err := take(b, next, int(n))
		if err != nil {
			return MetaValue{}, next, err
		}
		// Copy: the slice aliases the caller's buffer, and metadata outlives it.
		return MetaValue{Type: MetaBlob, Blob: bytes.Clone(raw)}, next2, nil

	default:
		return MetaValue{}, off, errAt(off-1, "unknown metadata value_type 0x%02x", valueType)
	}
}

// parseFrames walks the frame headers, recording where each payload lives and
// accumulating the camera. Returning the offset past the last frame lets the
// trailer blocks be found without a second pass.
func parseFrames(b []byte, off int, a *Animation, count int) ([]frameInfo, int, error) {
	frames := make([]frameInfo, 0, tableHint(uint64(count)))

	cameraX, cameraY := int64(0), int64(0)
	viewportW, viewportH := a.Width, a.Height

	for i := range count {
		start := off

		// A sync marker precedes keyframes. It may also appear elsewhere, so
		// one is consumed whenever present rather than only when expected.
		if bytes.HasPrefix(b[off:], MarkerFrameSync) {
			off += len(MarkerFrameSync)
		}

		if off >= len(b) {
			return nil, off, errTruncated(off, "frame flags")
		}
		flags := b[off]
		off++
		if flags&frameReservedMask != 0 {
			return nil, off, errAt(off-1, "reserved frame flag bit set (0x%02x)", flags&frameReservedMask)
		}
		if _, err := bodyKindFromFlags(flags); err != nil {
			return nil, off, errAt(off-1, "%v", err)
		}

		keyframe := flags&frameKeyframe != 0
		if i == 0 && !keyframe {
			return nil, off, errAt(off-1, "the first frame must be a keyframe")
		}

		info := frameInfo{offset: start, flags: flags}

		if flags&frameNominal != 0 {
			info.durationMS = a.FrameMS
		} else {
			d, next, err := readUvarint(b, off)
			if err != nil {
				return nil, off, err
			}
			off = next
			info.durationMS = d
		}

		if flags&frameCamera != 0 {
			if !a.HasCamera {
				return nil, off, errAt(off, "CAMERA_CHANGED set but the header has no HAS_CAMERA")
			}
			dx, next, err := readSvarint(b, off)
			if err != nil {
				return nil, off, err
			}
			dy, next2, err := readSvarint(b, next)
			if err != nil {
				return nil, off, err
			}
			off = next2
			cameraX += dx
			cameraY += dy
		}

		if flags&frameViewport != 0 {
			if !a.HasCamera {
				return nil, off, errAt(off, "VIEWPORT_CHANGED set but the header has no HAS_CAMERA")
			}
			w, next, err := readUvarint(b, off)
			if err != nil {
				return nil, off, err
			}
			h, next2, err := readUvarint(b, next)
			if err != nil {
				return nil, off, err
			}
			off = next2
			if w < 1 || h < 1 {
				return nil, off, errAt(off, "viewport %dx%d must be at least 1x1", w, h)
			}
			viewportW, viewportH = uint32(w), uint32(h)
		}

		payloadLen, next, err := readUvarint(b, off)
		if err != nil {
			return nil, off, err
		}
		if payloadLen > uint64(len(b)) {
			return nil, next, errTruncated(next, "frame payload")
		}
		payload, next2, err := take(b, next, int(payloadLen))
		if err != nil {
			return nil, next, err
		}
		off = next2

		info.cameraX, info.cameraY = cameraX, cameraY
		info.viewportW, info.viewportH = viewportW, viewportH
		info.payloadStart, info.payloadLen = next, int(payloadLen)
		info.payload = payload
		frames = append(frames, info)
	}
	return frames, off, nil
}

// parseIndex reads the seek table and checks it against the offsets the walk
// already found. A disagreement means one of the two is wrong, and a seek
// table that lies is worse than none.
func parseIndex(b []byte, off int, a *Animation) (int, error) {
	if !bytes.HasPrefix(b[off:], MarkerIndex) {
		return off, errAt(off, "HAS_INDEX is set but the index marker is missing")
	}
	off += len(MarkerIndex)

	count, next, err := readUvarint(b, off)
	if err != nil {
		return off, err
	}
	off = next
	if count != uint64(len(a.frames)) {
		return off, errAt(off, "index has %d entries for %d frames", count, len(a.frames))
	}
	if count > uint64(len(b))/4 {
		return off, errTruncated(off, "index entries")
	}

	a.Index = make([]uint32, count)
	for i := range count {
		entry, next, err := readUint32(b, off)
		if err != nil {
			return off, err
		}
		off = next
		if int(entry) != a.frames[i].offset {
			return off, errAt(off-4,
				"index entry %d points at %d but frame %d starts at %d",
				i, entry, i, a.frames[i].offset)
		}
		a.Index[i] = entry
	}
	return off, nil
}

// parseCRC verifies the checksum over every byte before the CRC marker.
func parseCRC(b []byte, off int, a *Animation) (int, error) {
	if !bytes.HasPrefix(b[off:], MarkerCRC) {
		return off, errAt(off, "HAS_CRC is set but the checksum marker is missing")
	}
	// The marker is not part of what it protects.
	want := crc32.ChecksumIEEE(b[:off])

	off += len(MarkerCRC)
	got, next, err := readUint32(b, off)
	if err != nil {
		return off, err
	}
	if got != want {
		return next, errAt(off, "checksum mismatch: stored 0x%08x, computed 0x%08x", got, want)
	}
	a.CRC = got
	return next, nil
}

// Frames yields each frame in order, inflating and decoding its payload as it
// goes. Iteration stops at the first malformed payload.
//
// Yielding an error alongside each value keeps the two-phase contract intact:
// callers that only want metadata never call this, and callers that do get the
// payload-level MUSTs enforced without Parse having to touch payloads.
func (a *Animation) Frames() iter.Seq2[*Frame, error] {
	return func(yield func(*Frame, error) bool) {
		for i := range a.frames {
			frame, err := a.frameAt(i)
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(frame, nil) {
				return
			}
		}
	}
}

// FrameAt decodes a single frame by index, useful for seeking.
func (a *Animation) FrameAt(i int) (*Frame, error) { return a.frameAt(i) }

// frameAt inflates and decodes the payload of one frame.
func (a *Animation) frameAt(i int) (*Frame, error) {
	if i < 0 || i >= len(a.frames) {
		return nil, errFormat("frame index %d out of range (%d)", i, len(a.frames))
	}
	info := a.frames[i]

	kind, err := bodyKindFromFlags(info.flags)
	if err != nil {
		// Parse already screened the frame flags, so reaching this means the
		// record came from somewhere other than Parse.
		return nil, errFormat("%v", err)
	}

	payload := info.payload
	if info.flags&frameDeflated != 0 {
		if payload, err = inflate(payload, a.area()); err != nil {
			return nil, err
		}
	}

	cells, err := decodePayload(payload, kind, a.Width, a.Height, uint32(len(a.Styles)))
	if err != nil {
		return nil, err
	}

	return &Frame{
		Index:      i,
		Flags:      info.flags,
		DurationMS: info.durationMS,
		Keyframe:   info.flags&frameKeyframe != 0,
		CameraX:    info.cameraX,
		CameraY:    info.cameraY,
		ViewportW:  info.viewportW,
		ViewportH:  info.viewportH,
		Payload:    cells,
	}, nil
}

// TotalDuration is the sum of every frame's duration.
func (a *Animation) TotalDuration() uint64 {
	a.ensureTimeIndex()
	return a.prefix[len(a.prefix)-1]
}

// buildTimeIndex fills prefix, the start time of every frame plus the total.
func (a *Animation) buildTimeIndex() {
	a.prefix = make([]uint64, len(a.frames)+1)

	var total uint64
	for i := range a.frames {
		a.prefix[i] = total
		total += a.frames[i].durationMS
	}
	a.prefix[len(a.frames)] = total
}

// ensureTimeIndex covers animations that did not come from Parse. Parse builds
// the index itself; this is for anything assembled another way, so that callers
// never have to know which kind they hold.
func (a *Animation) ensureTimeIndex() {
	if a.prefix == nil {
		a.buildTimeIndex()
	}
}

// FrameHeader is the per-frame information Parse reads without decoding a
// payload: enough to advance time, seek, or list keyframes. A player needs
// this because it cannot afford to decode every frame just to find out where
// the next keyframe is.
type FrameHeader struct {
	Index      int
	Flags      uint8
	DurationMS uint64
	Keyframe   bool
	CameraX    int64
	CameraY    int64
	ViewportW  uint32
	ViewportH  uint32

	// Offset is where the frame unit begins in the file: at its sync marker
	// when it has one, otherwise at its flags byte.
	Offset int

	// PayloadOffset and PayloadLength locate the payload exactly as stored:
	// compressed if PAYLOAD_DEFLATED is set, otherwise the grammar's own bytes.
	// Together with Offset they are enough to copy, replace, or compare one
	// frame's payload without decoding it.
	PayloadOffset int
	PayloadLength int
}

// HeaderAt returns one frame's header.
func (a *Animation) HeaderAt(i int) (FrameHeader, bool) {
	if i < 0 || i >= len(a.frames) {
		return FrameHeader{}, false
	}
	return a.headerOf(i), true
}

// Headers yields every frame header in order, decoding no payloads.
func (a *Animation) Headers() iter.Seq[FrameHeader] {
	return func(yield func(FrameHeader) bool) {
		for i := range a.frames {
			if !yield(a.headerOf(i)) {
				return
			}
		}
	}
}

// headerOf projects the internal record into the exported view.
func (a *Animation) headerOf(i int) FrameHeader {
	info := a.frames[i]
	return FrameHeader{
		Index:      i,
		Flags:      info.flags,
		DurationMS: info.durationMS,
		Keyframe:   info.flags&frameKeyframe != 0,
		CameraX:    info.cameraX,
		CameraY:    info.cameraY,
		ViewportW:  info.viewportW,
		ViewportH:  info.viewportH,
		Offset:     info.offset,

		PayloadOffset: info.payloadStart,
		PayloadLength: info.payloadLen,
	}
}

// Keyframes lists the indices that begin a GOP.
//
// Seeking goes to one of these and replays forward. A delta frame names only
// what changed since its predecessor, so there is no other place a decoder can
// resume from.
func (a *Animation) Keyframes() []int {
	out := make([]int, 0, len(a.frames)/12+1)
	for i := range a.frames {
		if a.frames[i].flags&frameKeyframe != 0 {
			out = append(out, i)
		}
	}
	return out
}

// DurationBefore is the animation time at which frame i begins, which is also
// the instant its predecessor ends. Out-of-range indices clamp, since callers
// reaching for "just before the first" or "just after the last" mean the ends
// rather than an error.
func (a *Animation) DurationBefore(i int) uint64 {
	a.ensureTimeIndex()

	switch {
	case i <= 0:
		return 0
	case i >= len(a.prefix):
		return a.prefix[len(a.prefix)-1]
	default:
		return a.prefix[i]
	}
}

// DurationThrough sums the durations of frames 0..end inclusive.
func (a *Animation) DurationThrough(end int) uint64 {
	a.ensureTimeIndex()

	if end < 0 {
		return 0
	}
	if end >= len(a.frames) {
		return a.prefix[len(a.prefix)-1]
	}
	return a.prefix[end+1]
}

// FrameAtTime reports which frame is on screen `ms` into the animation.
//
// Frame i occupies [DurationBefore(i), DurationBefore(i)+duration), so a time
// landing exactly on a boundary belongs to the frame that starts there. A time
// at or past the end gives the last frame; there is no empty answer, since a
// container with no frames is rejected at parse time.
func (a *Animation) FrameAtTime(ms uint64) int {
	a.ensureTimeIndex()

	n := len(a.frames)
	if n == 0 {
		return 0
	}

	// The first entry past ms is the frame after the one that contains it.
	after := sort.Search(len(a.prefix), func(i int) bool { return a.prefix[i] > ms })
	return min(max(after-1, 0), n-1)
}

// area is the canvas cell count, in 64 bits so that a large grid cannot wrap.
func (a *Animation) area() uint64 { return uint64(a.Width) * uint64(a.Height) }

// inflate decompresses a per-frame zlib stream, bounded by maxInflated.
func inflate(data []byte, area uint64) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, errFormat("payload is not a valid zlib stream: %v", err)
	}
	defer func() { _ = zr.Close() }()

	// At most one cell per canvas position, and a cell costs a handful of bytes
	// even with its varints padded to full length, so 40 per cell is generous.
	limit := area*40 + 4096
	if limit > maxInflated {
		limit = maxInflated
	}

	out, err := io.ReadAll(io.LimitReader(zr, int64(limit)))
	if err != nil {
		return nil, errFormat("inflating payload: %v", err)
	}
	return out, nil
}

// BodyKindOf reports which grammar a frame used, for tooling and tests.
func (f *Frame) BodyKind() bodyKind {
	kind, _ := bodyKindFromFlags(f.Flags)
	return kind
}

// KindName renders a frame's grammar as text.
func (f *Frame) KindName() string { return f.BodyKind().String() }
