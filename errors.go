package nvaa

import (
	"errors"
	"fmt"
)

// ErrFormat marks any input that is not a well-formed NVAA stream. Rejecting a
// file is enough to test with errors.Is(err, ErrFormat); callers that want to
// explain the failure read the message, which carries a byte offset where one
// is known.
var ErrFormat = errors.New("nvaa: malformed stream")

// ErrBadGrammarBits reports a frame whose grammar bits are both set. The pair
// has no defined meaning, so the frame cannot be decoded at all.
var ErrBadGrammarBits = errors.New("payload grammar bits span|gap are both set")

// formatError is the concrete error behind ErrFormat. Offset is -1 when the
// failure is not tied to a single position, as with a missing marker.
type formatError struct {
	Offset int
	Msg    string
}

func (e *formatError) Error() string {
	if e.Offset < 0 {
		return "nvaa: " + e.Msg
	}
	return fmt.Sprintf("nvaa: at byte %d: %s", e.Offset, e.Msg)
}

func (e *formatError) Is(target error) bool { return target == ErrFormat }

// errAt reports a structural violation at a known byte offset.
func errAt(off int, format string, args ...any) error {
	return &formatError{Offset: off, Msg: fmt.Sprintf(format, args...)}
}

// errFormat reports a violation with no single position to blame.
func errFormat(format string, args ...any) error {
	return &formatError{Offset: -1, Msg: fmt.Sprintf(format, args...)}
}

// errTruncated reports input that ended earlier than the stream claimed.
func errTruncated(off int, what string) error {
	return &formatError{Offset: off, Msg: "truncated while reading " + what}
}
