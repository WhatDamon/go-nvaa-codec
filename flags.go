package nvaa

// Container constants: flags, markers, and the identity signature.

// File signature. Nine bytes rather than PNG's eight because the
// brand is four letters, not three; the mnemonic is worth more than the
// alignment. The leading 0x89 cannot occur in 7-bit ASCII, and the trailing
// CR LF SUB LF trio is PNG's defence against line-ending translation and
// `type file.nvaa` spewing binary.
var Signature = []byte{0x89, 'N', 'V', 'A', 'A', 0x0D, 0x0A, 0x1A, 0x0A}

// Brand follows the signature so a payload lifted out of its container can
// still identify itself without relying on file offset zero.
var Brand = []byte("NVAA")

// Structural markers. Each is a 00 00 guard plus four ASCII
// letters. The guard imitates MPEG start codes: compressed payloads contain
// arbitrary byte pairs, and two zero bytes make a false hit unlikely enough to
// not need escaping.
var (
	MarkerFrameSync = []byte{0x00, 0x00, 'N', 'V', 'F', 'R'}
	MarkerIndex     = []byte{0x00, 0x00, 'N', 'V', 'I', 'D'}
	MarkerCRC       = []byte{0x00, 0x00, 'N', 'V', 'C', 'C'}
	MarkerStreamEnd = []byte{0x00, 0x00, 'N', 'V', 'E', 'E'}
)

// Header flag bits.
const (
	headerHasCamera uint8 = 0x01
	headerHasIndex  uint8 = 0x02
	headerHasCRC    uint8 = 0x04
	headerHasMeta   uint8 = 0x08

	headerReservedMask = 0xF0
)

// Frame flag bits.
const (
	frameKeyframe uint8 = 0x01
	frameCamera   uint8 = 0x02
	frameViewport uint8 = 0x04
	frameDeflated uint8 = 0x08
	frameSpan     uint8 = 0x10
	frameNominal  uint8 = 0x20
	frameGap      uint8 = 0x40

	frameReservedMask = 0x80
)

// bodyKind names a payload grammar. The two flag bits encode three grammars
// because their combination is forbidden rather than a fourth case.
type bodyKind int

const (
	bodySpan bodyKind = iota
	bodyList
	bodyGap
)

// String renders the grammar for diagnostics.
func (k bodyKind) String() string {
	switch k {
	case bodySpan:
		return "span"
	case bodyList:
		return "cell-list"
	case bodyGap:
		return "gap"
	}
	return "unknown"
}

// bodyKindFromFlags selects a grammar. Both grammar bits set is an error: the
// encoding has no defined meaning, and picking one would hide a corrupt file.
func bodyKindFromFlags(flags uint8) (bodyKind, error) {
	span := flags&frameSpan != 0
	gap := flags&frameGap != 0

	switch {
	case span && gap:
		return 0, ErrBadGrammarBits
	case gap:
		return bodyGap, nil
	case span:
		return bodySpan, nil
	default:
		return bodyList, nil
	}
}
