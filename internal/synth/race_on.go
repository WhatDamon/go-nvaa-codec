//go:build race

package synth

// RaceEnabled reports whether this test binary was built with the race detector.
//
// It exists for the allocation budgets. The detector tracks every allocation and
// keeps sync.Pool from retaining anything, so pooled buffers are reallocated on
// every use and the counts it produces describe a build nobody ships. A budget is
// a statement about the shipped build, so it steps aside when this is true rather
// than being loosened until it can no longer fail under either.
const RaceEnabled = true
