package synth

import (
	"os"
	"path/filepath"
	"runtime"
)

// artifactNames are the real animations benchmarks want when they are present.
//
// They are produced by a separate project into ../../artifacts and are not
// committed -- the largest is 14 MB -- so a benchmark that needs one has to be
// able to say "not here" rather than fail. The names are listed rather than
// globbed so that a stray file dropped in the directory cannot silently change
// what a benchmark measures.
var artifactNames = []string{"pdoom.nvaa", "miku.nvaa", "world-execute.nvaa", "credits.nvaa"}

// Artifacts returns the real animations found under any of the given roots.
//
// Callers pass the directories their package's tests run from and one level up,
// since the artifact folder sits beside the module rather than inside it. An
// empty result is normal and not an error: it means the checkout has no
// generated footage, and the caller should skip.
func Artifacts(roots ...string) []string {
	var found []string
	seen := make(map[string]bool)
	for _, root := range roots {
		for _, name := range artifactNames {
			path := filepath.Clean(filepath.Join(root, name))
			if seen[path] {
				continue
			}
			if _, err := os.Stat(path); err == nil {
				seen[path] = true
				found = append(found, path)
			}
		}
	}
	return found
}

// Measure reports what a call costs: allocations per call by count and by bytes.
//
// testing.B.ReportAllocs covers a benchmark end to end, but the budgets that
// matter here are per operation on a path that is called thousands of times per
// second, and a number that can be asserted is worth more than one that can only
// be read. The first run is discarded so that lazily built tables and pools are
// not charged to it.
func Measure(runs int, fn func()) (allocsPerCall, bytesPerCall float64) {
	if runs < 1 {
		runs = 1
	}
	fn()
	runtime.GC()

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		fn()
	}
	runtime.ReadMemStats(&after)

	calls := float64(runs)
	return float64(after.Mallocs-before.Mallocs) / calls,
		float64(after.TotalAlloc-before.TotalAlloc) / calls
}

// The sinks are package-level destinations for values a benchmark computes but
// does not use. Without somewhere for them to go, an optimising compiler is
// entitled to delete the call that produced them, and the benchmark would measure
// an empty loop.
//
// There is one per shape rather than a single any, because assigning an int, a
// string or a slice to an interface boxes it: a benchmark that sinks through
// interface{} measures the boxing on every iteration, and a budget derived from it
// is a budget for the box.
var (
	Sink     any
	SinkInt  int
	SinkText string
	SinkInts []int
)

// Targets returns a reproducible sequence of frame indices spread across a file.
//
// A benchmark that samples frames as i%count only ever sees the opening frames
// when its iteration count is small, and the opening of an animation is not like
// the rest of it: the first keyframe is usually the largest thing in the file.
// Spreading the sample over the whole count makes the number mean the same thing
// whatever -benchtime asks for.
func Targets(count, length int) []int {
	out := make([]int, length)
	state := uint64(0x2545F4914F6CDD1D)
	for i := range out {
		state = state*6364136223846793005 + 1442695040888963407
		out[i] = int((state >> 33) % uint64(count))
	}
	return out
}
