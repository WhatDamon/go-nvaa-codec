// Command play plays a NeoViolet ASCII-style Animation in the terminal.
//
// It is the smallest useful host for the player component: a box the size of
// the terminal, the keys the component offers, and the photosensitivity gate
// that the library refuses to skip on its own.
//
// Colour is on by default and is not inferred from the environment. The format
// stores exact 24-bit RGB, so a capability guess or an ambient NO_COLOR would
// silently reduce the animation to monochrome; --no-color is the explicit way to
// ask for that.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	nvaa "github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/internal/cli"
	"github.com/WhatDamon/go-nvaa-codec/player"
)

const usage = `nvaa-play FILE.nvaa [options]

Play the animation in the terminal.

options:
  -w, -h        grid size (default 80x24, resized live)
  --stats       show a live meter in the last row and report on exit
  --once        stop at the end instead of looping
  --seek TIME   start at TIME: 83, 1:23, 1:23.5, 0:01:23, 50%
  --no-warning  do not stop at a failed photosensitivity check
  --no-color    emit no colour

keys:
  space         pause and resume
  n, p          step one frame forward or back
  [ ]           jump to the previous or next keyframe
  left right    travel five seconds
  up down       travel a minute
  home end      go to the ends
  1-9           jump a tenth of the way in
  r             restart
  s             toggle the meter
  q             quit
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "nvaa-play: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("nvaa-play", flag.ContinueOnError)
	columns := flags.Int("w", 80, "grid width")
	lines := flags.Int("h", 24, "grid height")
	showStats := flags.Bool("stats", false, "show a live meter")
	once := flags.Bool("once", false, "stop at the end")
	seek := flags.String("seek", "", "start at a position, e.g. 1:23")
	skipWarning := flags.Bool("no-warning", false, "do not stop at a failed check")
	noColor := flags.Bool("no-color", false, "disable colour")

	rest, operand, err := cli.SplitOperand(args, map[string]bool{"w": true, "h": true, "seek": true})
	if err != nil {
		return errors.New("usage: " + usage)
	}
	if err := flags.Parse(rest); err != nil {
		return err
	}

	anim, err := nvaa.ReadFile(operand)
	if err != nil {
		return err
	}

	var startAt *uint64
	if *seek != "" {
		position, err := parseSeek(*seek, anim.TotalDuration())
		if err != nil {
			return err
		}
		if position > anim.TotalDuration() {
			return fmt.Errorf("--seek %s is past the end (%s)",
				*seek, player.Timecode(anim.TotalDuration()))
		}
		startAt = &position
	}

	// Only a declared failure earns a banner. Announcing a warning for a file
	// that passed would train viewers to dismiss the one that matters, and the
	// alternate screen is about to cover this anyway. It goes to stderr before
	// the alternate screen starts, so it survives in the scrollback.
	if warning := player.WarningFrom(anim); warning.Verdict == player.VerdictFail {
		fmt.Fprintln(os.Stderr, warning.Box())
		if !*skipWarning {
			fmt.Fprintln(os.Stderr, "  The gate is up: the animation starts once you acknowledge it.")
		}
	}

	report, err := player.Run(
		anim,
		player.Options{
			Columns:     *columns,
			Lines:       *lines,
			Loop:        !*once,
			SkipWarning: *skipWarning,
		},
		player.HostOptions{
			ShowStats:       *showStats,
			AlternateScreen: true,
			NoColor:         *noColor,
			StartAtMS:       startAt,
		},
		os.Stdin, os.Stdout,
	)

	// The meter goes to stderr, so that a run whose output is being captured
	// does not have a report spliced into the animation.
	if *showStats && report != "" {
		fmt.Fprintln(os.Stderr)
		fmt.Fprint(os.Stderr, report)
	}
	return err
}

// parseSeek reads a starting position. Three shapes are accepted, because all
// three are things a person says out loud: a plain number of seconds ("83"), a
// clock ("1:23", "0:01:23.5"), and a share of the whole ("50%").
//
// totalMS is needed only for the percentage form. The result is not clamped: a
// caller that names a position past the end has made a mistake worth reporting,
// and silently landing at the end would hide it.
func parseSeek(text string, totalMS uint64) (uint64, error) {
	input := strings.TrimSpace(text)
	if input == "" {
		return 0, errors.New("--seek needs a position, such as 83, 1:23 or 50%")
	}

	if percent, ok := strings.CutSuffix(input, "%"); ok {
		share, err := strconv.ParseFloat(percent, 64)
		if err != nil {
			return 0, fmt.Errorf("--seek %q: %q is not a percentage", text, percent)
		}
		if share < 0 || share > 100 {
			return 0, fmt.Errorf("--seek %q: a percentage must be between 0 and 100", text)
		}
		return uint64(share / 100 * float64(totalMS)), nil
	}

	parts := strings.Split(input, ":")
	if len(parts) > 3 {
		return 0, fmt.Errorf("--seek %q: too many colons", text)
	}

	// Each part is read once. In a clock, all but the last are whole numbers of
	// minutes or hours and the last is a number of seconds; out of range is
	// refused rather than carried, because "1:75" is a typo for something and
	// guessing which would be worse than saying so.
	values := make([]float64, 0, len(parts))
	for index, part := range parts {
		if index == len(parts)-1 {
			seconds, err := strconv.ParseFloat(part, 64)
			if err != nil || seconds < 0 {
				return 0, fmt.Errorf("--seek %q: %q is not a number of seconds", text, part)
			}
			if len(parts) > 1 && seconds >= 60 {
				return 0, fmt.Errorf("--seek %q: %q is not a number of seconds below 60", text, part)
			}
			values = append(values, seconds)
			continue
		}

		whole, err := strconv.Atoi(part)
		if err != nil || whole < 0 || whole > 59 {
			return 0, fmt.Errorf("--seek %q: %q is not a whole number below 60", text, part)
		}
		values = append(values, float64(whole))
	}

	// Horner from the left: 1:23 is 1*60+23, and 1:02:03 is (1*60+2)*60+3.
	total := float64(0)
	for _, value := range values {
		total = total*60 + value
	}

	return uint64(total * 1000), nil
}
