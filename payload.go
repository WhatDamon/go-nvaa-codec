package nvaa

// The three payload grammars a frame body may use.
//
// Each decoder returns the cells the frame literally mentions, plus the offset
// it stopped at so the caller can insist the payload was consumed exactly.
//
// Canonical ordering is enforced here rather than during Parse. It is a
// property of payload content, and the format deliberately puts the metadata
// block ahead of the frame stream so a consumer can surface a safety warning
// without decoding anything. Validating payloads eagerly would throw that away.
//
// Ordering is also load-bearing beyond tidiness: since no coordinate may repeat
// within a frame, a payload can never name more cells than the canvas holds.
// That is what makes the pre-allocation hints below safe.

// cellHint bounds a pre-allocation derived from a count read out of the stream.
// Every grammar spends at least two bytes per cell, so a count larger than the
// remaining bytes is a corrupt number, not a large frame.
func cellHint(count uint64, size int) int {
	if count > uint64(size) {
		return size
	}
	return int(count)
}

// decodeSpan decodes the row-oriented run grammar. Rows ascend by y, and spans
// within a row ascend by x without overlapping.
func decodeSpan(b []byte, w, h, styleCount uint32) ([]Cell, int, error) {
	return decodeSpanInto(nil, b, w, h, styleCount)
}

// decodeSpanInto is decodeSpan writing into dst.
//
// A nil dst asks for a slice of the right size; anything else is appended to, so
// a caller that decodes frame after frame can keep one buffer and hand it back.
// On failure the cells are the caller's buffer again, not a partial answer.
func decodeSpanInto(dst []Cell, b []byte, w, h, styleCount uint32) ([]Cell, int, error) {
	rows, off, err := readUvarint(b, 0)
	if err != nil {
		return dst, off, err
	}

	cells := dst
	if cells == nil {
		cells = make([]Cell, 0, cellHint(rows, len(b)/2))
	}
	lastY := int64(-1)

	for range rows {
		y, next, err := readUvarint(b, off)
		if err != nil {
			return dst, off, err
		}
		off = next

		if y >= uint64(h) {
			return dst, off, errAt(off, "span row y=%d outside canvas height %d", y, h)
		}
		if int64(y) <= lastY {
			return dst, off, errAt(off, "span rows must ascend: y=%d follows y=%d", y, lastY)
		}
		lastY = int64(y)

		spans, next, err := readUvarint(b, off)
		if err != nil {
			return dst, off, err
		}
		off = next

		// rowLimit is the first column this row may still paint. Requiring
		// x >= rowLimit rejects both overlaps and repeats, because a run is at
		// least one cell long.
		rowLimit := uint64(0)

		for range spans {
			x, next, err := readUvarint(b, off)
			if err != nil {
				return dst, off, err
			}
			off = next

			run, next, err := readUvarint(b, off)
			if err != nil {
				return dst, off, err
			}
			off = next

			style, next, err := readUvarint(b, off)
			if err != nil {
				return dst, off, err
			}
			off = next

			if run == 0 {
				return dst, off, errAt(off, "span run_length must be at least 1")
			}
			if x >= uint64(w) {
				return dst, off, errAt(off, "span x=%d outside canvas width %d", x, w)
			}
			if run > uint64(w)-x {
				return dst, off, errAt(off,
					"span at x=%d length %d overflows canvas width %d", x, run, w)
			}
			if x < rowLimit {
				return dst, off, errAt(off,
					"spans in a row must ascend without overlap: x=%d after column %d", x, rowLimit)
			}
			if style >= uint64(styleCount) {
				return dst, off, errAt(off, "style id %d out of range (%d)", style, styleCount)
			}
			rowLimit = x + run

			for i := range run {
				cells = append(cells, Cell{X: uint32(x + i), Y: uint32(y), Style: uint32(style)})
			}
		}
	}
	return cells, off, nil
}

// decodeCellList decodes the flat list of coordinates, ascending by (y, x).
func decodeCellList(b []byte, w, h, styleCount uint32) ([]Cell, int, error) {
	return decodeCellListInto(nil, b, w, h, styleCount)
}

// decodeCellListInto is decodeCellList writing into dst; see decodeSpanInto.
func decodeCellListInto(dst []Cell, b []byte, w, h, styleCount uint32) ([]Cell, int, error) {
	count, off, err := readUvarint(b, 0)
	if err != nil {
		return dst, off, err
	}

	cells := dst
	if cells == nil {
		cells = make([]Cell, 0, cellHint(count, len(b)/2))
	}
	lastPos := int64(-1)

	for range count {
		x, next, err := readUvarint(b, off)
		if err != nil {
			return dst, off, err
		}
		off = next

		y, next, err := readUvarint(b, off)
		if err != nil {
			return dst, off, err
		}
		off = next

		style, next, err := readUvarint(b, off)
		if err != nil {
			return dst, off, err
		}
		off = next

		if x >= uint64(w) {
			return dst, off, errAt(off, "cell x=%d outside canvas width %d", x, w)
		}
		if y >= uint64(h) {
			return dst, off, errAt(off, "cell y=%d outside canvas height %d", y, h)
		}
		if style >= uint64(styleCount) {
			return dst, off, errAt(off, "style id %d out of range (%d)", style, styleCount)
		}

		pos := int64(y)*int64(w) + int64(x)
		if pos <= lastPos {
			return dst, off, errAt(off,
				"cell changes must be in strictly ascending row-major order")
		}
		lastPos = pos

		cells = append(cells, Cell{X: uint32(x), Y: uint32(y), Style: uint32(style)})
	}
	return cells, off, nil
}

// decodeGap decodes position deltas. Each entry skips `gap` cells and then
// paints one, so positions rise strictly by construction -- which is why this
// grammar cannot express a duplicate coordinate even in principle.
func decodeGap(b []byte, w, h, styleCount uint32) ([]Cell, int, error) {
	return decodeGapInto(nil, b, w, h, styleCount)
}

// decodeGapInto is decodeGap writing into dst; see decodeSpanInto.
func decodeGapInto(dst []Cell, b []byte, w, h, styleCount uint32) ([]Cell, int, error) {
	count, off, err := readUvarint(b, 0)
	if err != nil {
		return dst, off, err
	}

	area := uint64(w) * uint64(h)
	cells := dst
	if cells == nil {
		cells = make([]Cell, 0, cellHint(count, len(b)/2))
	}

	var pos uint64

	for i := range count {
		gap, next, err := readUvarint(b, off)
		if err != nil {
			return dst, off, err
		}
		off = next

		style, next, err := readUvarint(b, off)
		if err != nil {
			return dst, off, err
		}
		off = next

		if i == 0 {
			// The first entry has no predecessor, so its gap is the position.
			if gap >= area {
				return dst, off, errAt(off, "gap body position %d outside canvas area %d", gap, area)
			}
			pos = gap
		} else {
			// The landing cell must stay inside the canvas. Both tests avoid
			// overflow rather than checking the sum afterwards.
			if gap >= area {
				return dst, off, errAt(off, "gap body skip %d exceeds canvas area %d", gap, area)
			}
			step := gap + 1
			if step >= area || pos >= area-step {
				return dst, off, errAt(off, "gap body runs past canvas area %d", area)
			}
			pos += step
		}

		if style >= uint64(styleCount) {
			return dst, off, errAt(off, "style id %d out of range (%d)", style, styleCount)
		}

		cells = append(cells, Cell{
			X:     uint32(pos % uint64(w)),
			Y:     uint32(pos / uint64(w)),
			Style: uint32(style),
		})
	}
	return cells, off, nil
}

// decodePayload dispatches on the grammar bits and insists the whole payload
// was consumed. Leftover bytes mean the frame body and its length disagree,
// which is a corruption worth reporting rather than ignoring.
func decodePayload(payload []byte, kind bodyKind, w, h, styleCount uint32) ([]Cell, error) {
	return decodePayloadInto(nil, payload, kind, w, h, styleCount)
}

// decodePayloadInto is decodePayload writing into dst.
//
// A player decodes frame after frame from one keyframe to the next, and only the
// frame it lands on is ever read. Decoding into a buffer it keeps turns a cell
// slice per frame into a cell slice per rewind.
func decodePayloadInto(dst []Cell, payload []byte, kind bodyKind, w, h, styleCount uint32) ([]Cell, error) {
	var (
		cells []Cell
		off   int
		err   error
	)

	switch kind {
	case bodySpan:
		cells, off, err = decodeSpanInto(dst, payload, w, h, styleCount)
	case bodyList:
		cells, off, err = decodeCellListInto(dst, payload, w, h, styleCount)
	case bodyGap:
		cells, off, err = decodeGapInto(dst, payload, w, h, styleCount)
	default:
		return dst, errFormat("unknown payload grammar %d", int(kind))
	}
	if err != nil {
		return dst, err
	}
	if off != len(payload) {
		return dst, errAt(off, "%s payload has %d trailing bytes",
			kind, len(payload)-off)
	}
	return cells, nil
}
