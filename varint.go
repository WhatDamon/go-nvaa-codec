package nvaa

// LEB128 primitives: the variable-length integers every other field is built
// from, in unsigned and zigzag-signed forms.

// maxVarintBytes bounds a 64-bit LEB128: ceil(64/7) = 10.
const maxVarintBytes = 10

// readUvarint decodes an unsigned LEB128 value at b[off:], returning the
// value and the offset just past it.
//
// The spec requires encoders to use the shortest form, but a decoder is not
// told to reject padding, so a redundant 0x80 lead byte is tolerated. What is
// rejected is a value that cannot fit in 64 bits, since that has no defined
// meaning.
func readUvarint(b []byte, off int) (uint64, int, error) {
	var value uint64
	var shift uint

	for i := 0; i < maxVarintBytes; i++ {
		if off+i >= len(b) {
			return 0, off, errTruncated(off+i, "uvarint")
		}
		c := b[off+i]

		// The tenth byte lands on bit 63, so only one bit of it can be used.
		// Anything larger also covers the illegal continuation bit.
		if i == maxVarintBytes-1 && c > 1 {
			return 0, off, errAt(off+i, "uvarint overflows 64 bits")
		}
		value |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return value, off + i + 1, nil
		}
		shift += 7
	}
	return 0, off, errAt(off, "uvarint exceeds 10 bytes")
}

// readSvarint decodes a zigzag-mapped signed LEB128 value at b[off:].
func readSvarint(b []byte, off int) (int64, int, error) {
	u, next, err := readUvarint(b, off)
	if err != nil {
		return 0, off, err
	}
	return unzigzag(u), next, nil
}

// unzigzag maps an unsigned code back to its signed value.
func unzigzag(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}

// zigzag maps a signed value onto the unsigned code space. Present because the
// encoder is absent but tests and tooling still need the inverse.
func zigzag(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// readUint16 reads a little-endian unsigned 16-bit integer at b[off:].
func readUint16(b []byte, off int) (uint16, int, error) {
	if off+2 > len(b) {
		return 0, off, errTruncated(off, "uint16")
	}
	return uint16(b[off]) | uint16(b[off+1])<<8, off + 2, nil
}

// readUint32 reads a little-endian unsigned 32-bit integer at b[off:].
func readUint32(b []byte, off int) (uint32, int, error) {
	if off+4 > len(b) {
		return 0, off, errTruncated(off, "uint32")
	}
	return uint32(b[off]) | uint32(b[off+1])<<8 |
		uint32(b[off+2])<<16 | uint32(b[off+3])<<24, off + 4, nil
}

// take slices n bytes at b[off:], rejecting a slice that runs past the end.
func take(b []byte, off, n int) ([]byte, int, error) {
	if n < 0 {
		return nil, off, errAt(off, "negative length")
	}
	if off+n > len(b) {
		return nil, off, errTruncated(off, "byte block")
	}
	return b[off : off+n], off + n, nil
}
