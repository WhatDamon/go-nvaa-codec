// Package nvaa decodes NeoViolet ASCII-style Animation files.
//
// An NVAA file is a terminal animation. The picture is a canvas of cells, each
// one a glyph with a 24-bit foreground and background colour; the animation is a
// sequence of frames over that canvas. The palette, glyph and style tables are
// written once, and then each frame describes either a whole canvas or only what
// changed since the frame before it.
//
// # Decoding
//
// Parse validates a container and returns an Animation. The header, the tables,
// the metadata and every frame's offset are read at once; frame payloads are
// decoded later, as Frames yields them. A consumer that only wants what the file
// says about itself never pays for the frames:
//
//	anim, err := nvaa.ReadFile("clip.nvaa")
//	if err != nil {
//		return err
//	}
//	fmt.Println(anim.Width, anim.Height, anim.FrameCount(), anim.TotalDuration())
//
// Canvas folds a frame into the picture, which is how a player keeps one canvas
// in memory instead of every frame:
//
//	canvas := nvaa.NewCanvas(anim.Width, anim.Height)
//	for frame, err := range anim.Frames() {
//		if err != nil {
//			return err
//		}
//		canvas.Apply(frame)
//	}
//
// # Validation
//
// Malformed input is refused rather than half-decoded: a wrong signature, a
// reserved bit that is set, a count too large for the file, a coordinate outside
// the canvas, a style that does not exist, a payload that is not in order, a
// checksum that does not match. The split above decides when each is noticed --
// container faults surface from Parse, payload faults from Frames.
//
// That division is deliberate. A file may be read for its metadata alone, which
// is what lets a player show a photosensitivity warning before it decodes
// anything, so a metadata-only reader will not encounter a payload defect.
//
// # What this package does not do
//
// It does not encode, render, or play. Encoding carries decisions this package
// has no business making for a caller, and the subpackages cover the rest:
// [github.com/WhatDamon/go-nvaa-codec/render] turns a decoded frame into a grid
// of glyphs and colours, and [github.com/WhatDamon/go-nvaa-codec/player] adds
// timing, keys and a terminal.
//
// The format is described in the specification shipped with this module, and
// pinned by the conformance vectors in testdata/.
package nvaa
