// Command render writes frames without a terminal attached, so that the output
// can be diffed, hashed, or compared against another implementation.
//
// Three shapes are offered because three different questions get asked of the
// same animation: what do the glyphs look like (plain rows), what exactly would
// be sent to a terminal (--ansi), and did two implementations agree (--digest).
package main

import (
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"

	nvaa "github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/internal/cli"
	"github.com/WhatDamon/go-nvaa-codec/player"
)

const usage = `nvaa-render FILE.nvaa [options]

Write frames with no terminal, for scripts and comparison.

options:
  -w, -h        grid size (default 80x24)
  --from N      first frame (default 0)
  --frames N    how many frames (default all)
  --ansi        base64 of each frame's repaint, one line per frame
  --digest      one line per frame: geometry and a content digest
  (default)     plain glyph rows
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "nvaa-render: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("nvaa-render", flag.ContinueOnError)
	columns := flags.Int("w", 80, "grid width")
	lines := flags.Int("h", 24, "grid height")
	from := flags.Int("from", 0, "first frame")
	count := flags.Int("frames", 0, "how many frames, 0 for all")
	ansi := flags.Bool("ansi", false, "emit base64 repaints")
	digest := flags.Bool("digest", false, "emit per-frame digests")

	rest, operand, err := cli.SplitOperand(args, map[string]bool{
		"w": true, "h": true, "from": true, "frames": true,
	})
	if err != nil {
		return errors.New("usage: " + usage)
	}
	if err := flags.Parse(rest); err != nil {
		return err
	}
	if *ansi && *digest {
		return errors.New("--ansi and --digest are mutually exclusive")
	}

	anim, err := nvaa.ReadFile(operand)
	if err != nil {
		return err
	}
	if *from < 0 || *from >= anim.FrameCount() {
		return fmt.Errorf("--from %d is outside 0..%d", *from, anim.FrameCount()-1)
	}

	timeline := player.NewTimeline(anim, *columns, *lines)
	if err := timeline.Seek(*from); err != nil {
		return err
	}

	last := anim.FrameCount() - 1
	if *count > 0 && *from+*count-1 < last {
		last = *from + *count - 1
	}

	out := os.Stdout
	for {
		grid := timeline.Grid()

		switch {
		case *ansi:
			// One line per frame so a harness can split them without guessing
			// where a repaint ends.
			fmt.Fprintln(out, base64.StdEncoding.EncodeToString([]byte(grid.FullRepaint())))
		case *digest:
			fmt.Fprintf(out, "%d %d %d %d %d %x\n",
				timeline.Index(), grid.X0, grid.Y0, grid.Width, grid.Height, grid.Digest())
		default:
			fmt.Fprintf(out, "# frame %d  (%d,%d) %dx%d\n",
				timeline.Index(), grid.X0, grid.Y0, grid.Width, grid.Height)
			fmt.Fprint(out, grid.Plain())
		}

		if timeline.Index() >= last {
			return nil
		}
		advanced, err := timeline.Next()
		if err != nil {
			return err
		}
		if !advanced {
			return nil
		}
	}
}
