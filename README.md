# go-nvaa-codec

A Go decoder for **NVAA** — NeoViolet ASCII-style Animation, a container for
terminal animations in which every cell carries a glyph and its own 24-bit
foreground and background colour.

The decoder is complete and checks itself against the conformance suite in
[`testdata/`](testdata). The format is described in [`SPEC.md`](SPEC.md); version 1 of it is
final, so the decoder will keep reading files written today.

```console
$ go get github.com/WhatDamon/go-nvaa-codec
```

## Packages

| Package | What it is |
| --- | --- |
| [`nvaa`](.) | The decoder: container, tables, metadata, and the three payload grammars. **No dependencies outside the standard library** |
| [`render`](render) | A decoded frame as a grid of glyphs and colours, and a grid as terminal output |
| [`photosensitivity`](photosensitivity) | Flash analysis against the WCAG thresholds, and a reader for what a file claims about itself |
| [`player`](player) | A Bubble Tea component that plays an animation inside a box, plus the stand-alone program wrapper |
| [`example/`](example) | Five runnable programs |
| `internal/` | Shared helpers, not importable |

`render` knows nothing about terminals beyond the escape sequences it writes, and
`nvaa` knows nothing about either. If all you want is what a frame looks like, you
can depend on `nvaa` and `render` and never see a terminal framework.

## Decoding

```go
anim, err := nvaa.ReadFile("clip.nvaa")
if err != nil {
    return err
}

fmt.Println(anim.Width, anim.Height, anim.FrameCount(), anim.TotalDuration())

canvas := nvaa.NewCanvas(anim.Width, anim.Height)
for frame, err := range anim.Frames() {
    if err != nil {
        return err
    }
    canvas.Apply(frame)
}
```

Decoding happens in two stages. `Parse` validates the container and reads the
header, the tables, the metadata and every frame's offset. `Frames` decodes
payloads lazily as they are requested. The split is deliberate: a player can read
a photosensitivity warning and a title from a file **without decoding a single
frame**, which is exactly what it needs to do before it starts playing.

It also decides when a malformed file is noticed. Container faults — a wrong
signature, a reserved bit, a count too large for the file — surface from `Parse`.
Payload faults — a coordinate outside the canvas, a style that does not exist, a
payload that is not in order — surface from `Frames`.

## What a file holds

The palette, the glyph table and the style table are written once. A **style** is
one `(glyph, foreground, background)` combination, so a cell in a frame is a
single number rather than three fields.

Frames then come in two kinds. A **keyframe** clears the canvas and paints it; a
**delta frame** carries only what changed since the frame before it. A single
reference frame is supported and bidirectional prediction is not, so seeking
*backwards* means rewinding to the nearest keyframe and replaying forward — which
is why keyframe spacing is a real choice for an encoder (see section 13 of the
specification). Seeking forwards is a different question: the frame on screen is
already a decoded state, so a player steps from there, and a rewind that lands in
a group it has already replayed restores that keyframe's canvas rather than
decoding it again.

Three payload grammars are available for the changes, and an encoder picks
whichever is smallest: runs by row, a list of cells, or the distance between
successive changed positions. The last of these is usually the winner, and it is
the one that makes real footage affordable.

An optional camera moves the window over a larger canvas, stored as a per-frame
delta — the same idea as a global motion vector. An optional metadata block sits
**before** the frame stream so that safety information can be read first.

## The examples

```console
$ go build -o nvaa-info   ./example/info
$ go build -o nvaa-play   ./example/play
$ go build -o nvaa-render ./example/render
$ go build -o nvaa-pse    ./example/pse
$ go build -o nvaa-host   ./example/host

$ ./nvaa-info   testdata/demo.nvaa
$ ./nvaa-play   testdata/demo.nvaa
$ ./nvaa-play   testdata/demo.nvaa --seek 0:00.5 --stats
$ ./nvaa-render testdata/demo.nvaa -w 80 -h 24 --digest
$ ./nvaa-pse    testdata/strobe.nvaa
$ ./nvaa-host   testdata/demo.nvaa
```

`nvaa-info` prints what a file says about itself without decoding any frame.
`nvaa-play` plays it in the terminal. `nvaa-render` writes frames with no terminal
attached — plain glyph rows, base64 repaints, or a per-frame digest — which is what
makes it scriptable and comparable between implementations. `nvaa-pse` analyses the
flashing and sets it beside what the file claims. `nvaa-host` embeds the player in
an application of its own, as one pane among several.

Every binary is named `nvaa-<what it does>`, since a rule that ignores a file by
name would also swallow a directory of that name — and `render` is a package.

Player keys: `space` pause · `n` / `p` step · `[` / `]` jump keyframe ·
`←` / `→` five seconds · `↓` / `↑` a minute · `home` / `end` the ends ·
`1`..`9` a tenth of the way in · `r` restart · `s` toggle the meter · `q` quit.

## Embedding the player

`player.Player` is a component, not a program. It is given a box, paints the
animation into it, and answers messages; `player.Run` hosts one for a command
line. The boundary that matters is **box against terminal**: everything inside the
box belongs to the animation and is painted edge to edge, and everything outside
belongs to the host.

```go
component := player.New(anim, player.Options{Columns: w, Lines: h})
cmd := component.Init()

// The host decides the layout, then tells the component what it got.
component.SetSize(w, h)

// The host's own keys get first refusal; the rest fall through.
if cmd, handled := component.Update(msg); handled {
    // the animation consumed it
}

// On resize, and when the host wants the picture again:
content := component.View()
```

Four things are worth knowing before embedding it.

- **The keys it takes are playback keys.** `q`, `ctrl+c`, `tab` and `esc` are not
  among them, so a host can keep them.
- **It never ends the program.** A component that quits on its own would decide
  something that belongs to the host.
- **The photosensitivity gate is a state, not a prompt.** `AtWarning` is true
  until the gate is acknowledged, and the host decides how to show that.
- **Its view is exactly as many rows as `SetSize` was given, and as wide.** The
  part of the box the animation does not cover is filled with the animation's own
  background, so the picture does not sit on an unknown colour. If you would
  rather place your own background there, `Options.TransparentMargin` leaves those
  cells unpainted.

### What a renderer does to that box

One caveat belongs here rather than in a bug report. Bubble Tea's renderer treats
a space as erasable regardless of its background colour, and fills the rest of a
terminal line with the pen colour it happens to hold. A component can therefore
guarantee its own cells and not the margins between boxes: if a host puts a panel
that relies on the terminal's default background next to a panel that paints its
own, a frame may repaint part of the former. The cure is in the host — give every
panel a background of its own, as `example/host` does — and the reason is
worth knowing before designing the layout.

## Photosensitivity

`photosensitivity` answers two different questions about flashing, and keeps the
answers apart.

**What does the file claim?** `Declared` reads the record a producer may have left
behind: a verdict, the counts behind it, and the method that produced it. A file
with no record, or with one in a shape nothing can be concluded from, reads as
`VerdictUnknown` — never as a pass, because "nobody checked" and "checked and found
fine" are different claims.

**What do the frames do?** `Analyze` runs the WCAG thresholds over the animation
itself. It blends each style's foreground and background into one cell colour,
compares consecutive frames through the same viewport arithmetic the renderer uses,
and counts flashes as opposing pairs: a 10 fps strobe is five flashes a second,
not ten.

The result is an approximation and says so. A glyph grid is not a pixel grid, the
analysis window stands in for a ten degree visual field, and the viewing distance
is not knowable from a file — so an assessment records the thresholds it was made
with, and a pass is a statement about that analysis rather than a certification.
For broadcast, distribution, or any safety-critical use, run a certified analyser.

```console
$ go build -o nvaa-pse ./example/pse
$ ./nvaa-pse testdata/demo.nvaa
$ ./nvaa-pse testdata/strobe.nvaa
$ ./nvaa-pse testdata/demo.nvaa --viewport 20x8
```

`pse` prints the analysis, then what the file says about itself, then whether the
two agree. `testdata/strobe.nvaa` is the interesting one: twelve frames at 10 fps
that flash five times a second, carrying the failing record its own analysis
produced, so a reader has a claim to check rather than one to trust.

The player reads the same record before it starts, and gates playback on it. What
it must not do — and does not — is treat a missing record as permission.

## Verification

```console
$ go test ./...
```

The suite covers the container and payload grammars against the conformance
vectors, the four codes of the cell model (wide glyph pairs, empty-style clearing,
the last-column rule, canonical order), the time index used for seeking, the
renderer's viewport arithmetic, the analyser against the numbers the reference
implementation reports for the same frames, and the player as an embedded
component.

The expected values in `testdata/` were produced by a reference implementation, so
they pin its behaviour: a disagreement means two implementations differ, not that
either is wrong. Section 17 of the specification says this in full.

## Requirements

Go 1.26 or newer. The decoder and the analyser need nothing but the standard
library; `render` additionally uses `golang.org/x/text` for East Asian widths, and
`player` uses Bubble Tea v2.

## Licence

MIT. See [LICENSE](LICENSE).
