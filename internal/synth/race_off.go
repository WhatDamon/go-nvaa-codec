//go:build !race

package synth

// RaceEnabled reports whether this test binary was built with the race detector.
// See the race-tagged file beside this one for why it matters.
const RaceEnabled = false
