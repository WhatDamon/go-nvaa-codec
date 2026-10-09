package synth

import "sync"

// Corpus is one synthetic animation together with the options that produced it.
//
// It deliberately holds no decoded form. This package is imported by tests
// inside the codec's own packages, so importing the codec here would be an
// import cycle; each caller parses the bytes once and caches the result, which
// costs a few milliseconds and keeps this package free of the thing it feeds.
type Corpus struct {
	Blob    []byte
	Options Options
}

var (
	videoCorpusOnce   = sync.OnceValues(func() (Corpus, error) { return buildCorpus(Video()) })
	longGOPCorpusOnce = sync.OnceValues(func() (Corpus, error) { return buildCorpus(LongGOP()) })
)

// VideoCorpus is the preset shaped like the real footage this library has been
// pointed at.
func VideoCorpus() (Corpus, error) { return videoCorpusOnce() }

// LongGOPCorpus is the same canvas with the longest GOP an encoder is likely to
// emit, which is the worst case a seek can meet.
func LongGOPCorpus() (Corpus, error) { return longGOPCorpusOnce() }

func buildCorpus(options Options) (Corpus, error) {
	blob, err := Build(options)
	if err != nil {
		return Corpus{}, err
	}
	return Corpus{Blob: blob, Options: options.withDefaults()}, nil
}

// HeaviestGOP is the span a seek replays when it rewinds as far as the encoder
// allows: the longest run of frames between one keyframe and the next. The end
// is exclusive.
//
// It is arithmetic rather than a scan because the builder places keyframes on a
// fixed interval, so the answer is known from the options without decoding
// anything.
func (c Corpus) HeaviestGOP() (start, end int) {
	frames, every := c.Options.Frames, c.Options.KeyframeEvery
	if frames <= 0 || every <= 0 {
		return 0, frames
	}
	start = ((frames - 1) / every) * every
	return start, frames
}
