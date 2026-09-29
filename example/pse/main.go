// Command pse analyses an animation for flashing that may trigger photosensitive
// epilepsy, and prints what the file claims about itself beside what the analysis
// found.
//
// The analysis approximates the WCAG flash thresholds over a glyph grid, so it is
// a warning rather than a compliance claim: for broadcast, distribution or any
// safety-critical use, run a certified analyser.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	nvaa "github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/internal/cli"
	"github.com/WhatDamon/go-nvaa-codec/photosensitivity"
)

const usage = `pse FILE.nvaa [options]

Analyse an animation against the WCAG flash thresholds, and report the result
beside the photosensitivity record the file carries about itself.

A file that declares nothing has not been assessed, which is not the same as
being safe; that shows up here as "unknown" rather than as a pass.

options:
  --viewport WxH       analysis window (default: the file's own viewport)
  --area-threshold F   fraction of the window that must flash (default 0.25)
  --ink-coverage F     per-cell glyph ink coverage (default 0.25)
`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "pse: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("pse", flag.ContinueOnError)
	viewport := flags.String("viewport", "", "analysis window, WxH")
	areaThreshold := flags.Float64("area-threshold", photosensitivity.DefaultAreaThreshold,
		"flashing area fraction that counts")
	inkCoverage := flags.Float64("ink-coverage", photosensitivity.DefaultInkCoverage,
		"per-cell glyph ink coverage")

	rest, operand, err := cli.SplitOperand(args, map[string]bool{
		"viewport": true, "area-threshold": true, "ink-coverage": true,
	})
	if err != nil {
		return errors.New("usage: " + usage)
	}
	if err := flags.Parse(rest); err != nil {
		return err
	}

	options := photosensitivity.Options{
		AreaThreshold: *areaThreshold,
		InkCoverage:   *inkCoverage,
	}
	if *viewport != "" {
		options.Columns, options.Lines, err = parseViewport(*viewport)
		if err != nil {
			return err
		}
	}

	anim, err := nvaa.ReadFile(operand)
	if err != nil {
		return err
	}
	assessment, err := photosensitivity.Analyze(anim, options)
	if err != nil {
		return err
	}
	declared := photosensitivity.Declared(anim.Metadata)

	field(out, "file", operand)
	fmt.Fprint(out, assessment.Report())
	field(out, "declared", describeDeclaration(declared))
	if declared.Warning != "" {
		field(out, "warning", declared.Warning)
	}
	field(out, "agreement", agreement(declared.Verdict, assessment.Verdict))
	return nil
}

// field writes one labelled line, aligned with the lines of the report.
func field(out io.Writer, label, value string) {
	fmt.Fprintf(out, "%-20s%s\n", label, value)
}

// parseViewport reads the WxH form, in either case.
func parseViewport(text string) (columns, lines int, err error) {
	width, height, ok := strings.Cut(strings.ToLower(strings.TrimSpace(text)), "x")
	if !ok {
		return 0, 0, errors.New("--viewport wants WxH, for example 50x18")
	}

	columns, columnsErr := strconv.Atoi(strings.TrimSpace(width))
	lines, linesErr := strconv.Atoi(strings.TrimSpace(height))
	if columnsErr != nil || linesErr != nil {
		return 0, 0, fmt.Errorf("--viewport wants two numbers, for example 50x18, got %q", text)
	}
	if columns < 1 || lines < 1 {
		return 0, 0, fmt.Errorf("--viewport must be at least 1x1, got %q", text)
	}
	return columns, lines, nil
}

// describeDeclaration renders what the file claims. A file that carries no
// record has made no claim, which is worth saying out loud: it is not a claim of
// safety, and a reader who needs one has to get it somewhere else.
func describeDeclaration(d photosensitivity.Declaration) string {
	switch d.Verdict {
	case photosensitivity.VerdictUnknown:
		return "nothing (unknown: not assessed, so not safe to assume)"

	default:
		parts := []string{fmt.Sprintf("method %q", d.Method)}
		if d.Standard != "" {
			parts = append(parts, "standard "+d.Standard)
		}
		parts = append(parts,
			fmt.Sprintf("general %d/s", d.General),
			fmt.Sprintf("red %d/s", d.Red),
		)
		if d.HasArea {
			parts = append(parts, fmt.Sprintf("largest flash %d per mille", d.Area))
		}
		return fmt.Sprintf("%s (%s)", d.Verdict, strings.Join(parts, ", "))
	}
}

// agreement compares a claim with an analysis of the same frames.
func agreement(declared, analysed photosensitivity.Verdict) string {
	switch {
	case declared == photosensitivity.VerdictUnknown:
		return "the file makes no claim to compare against"
	case declared == analysed:
		return "the analysis agrees with the file"
	default:
		return fmt.Sprintf("the file claims %s, the analysis says %s", declared, analysed)
	}
}
