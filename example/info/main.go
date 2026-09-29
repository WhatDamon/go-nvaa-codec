// Command info prints what a NeoViolet ASCII-style Animation file says about
// itself: its header, its three tables, its frame statistics, and every
// metadata item.
//
// No frame payload is decoded, so it answers "what is this file" without paying
// for the animation itself. It is also the cheapest way to read the
// photosensitivity declaration out of a file before playing it.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	nvaa "github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/internal/cli"
)

const usage = `info FILE.nvaa

Print the container header, the palette, glyph and style tables, the frame
counts, the payload grammars in use, and every metadata item.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "info: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("info", flag.ContinueOnError)

	rest, operand, err := cli.SplitOperand(args, nil)
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
	printInfo(os.Stdout, operand, anim)
	return nil
}

func printInfo(out io.Writer, path string, anim *nvaa.Animation) {
	headers := make([]nvaa.FrameHeader, 0, anim.FrameCount())
	keyframes := 0
	for header := range anim.Headers() {
		headers = append(headers, header)
		if header.Keyframe {
			keyframes++
		}
	}

	fmt.Fprintf(out, "file            %s\n", path)
	fmt.Fprintf(out, "size            %d bytes\n", anim.Size)
	fmt.Fprintf(out, "format          %s (%s)\n", nvaa.FormatName, nvaa.Alias)
	fmt.Fprintf(out, "version         %d\n", anim.Version)
	fmt.Fprintf(out, "flags           0x%02x\n", anim.Flags)
	fmt.Fprintf(out, "canvas          %dx%d\n", anim.Width, anim.Height)
	fmt.Fprintf(out, "fps             %d/%d (nominal %dms)\n", anim.FPSNum, anim.FPSDen, anim.FrameMS)
	fmt.Fprintf(out, "duration        %dms\n", anim.TotalDuration())
	fmt.Fprintf(out, "frames          %d (%d key, %d delta)\n",
		anim.FrameCount(), keyframes, anim.FrameCount()-keyframes)
	fmt.Fprintf(out, "palette         %d\n", len(anim.Palette))
	fmt.Fprintf(out, "glyphs          %d\n", len(anim.Glyphs))
	fmt.Fprintf(out, "styles          %d\n", len(anim.Styles))
	fmt.Fprintf(out, "camera          %v\n", anim.HasCamera)
	fmt.Fprintf(out, "index           %v", anim.HasIndex)
	if anim.HasIndex {
		fmt.Fprintf(out, " (%d entries)", len(anim.Index))
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "crc             %v", anim.HasCRC)
	if anim.HasCRC {
		fmt.Fprintf(out, " (0x%08x)", anim.CRC)
	}
	fmt.Fprintln(out)

	// Body grammars, so a file's encoding choices are legible at a glance.
	span, gap, list, deflated := 0, 0, 0, 0
	for header := range anim.Headers() {
		switch {
		case header.Flags&0x40 != 0:
			gap++
		case header.Flags&0x10 != 0:
			span++
		default:
			list++
		}
		if header.Flags&0x08 != 0 {
			deflated++
		}
	}
	fmt.Fprintf(out, "bodies          span %d, gap %d, cell-list %d\n", span, gap, list)
	fmt.Fprintf(out, "deflated        %d frames\n", deflated)

	if anim.Metadata.Len() > 0 {
		fmt.Fprintf(out, "metadata        %d items\n", anim.Metadata.Len())
		for i := range anim.Metadata.Len() {
			key, value := anim.Metadata.At(i)
			fmt.Fprintf(out, "  %-46s %s\n", key, metaValueText(value))
		}
	} else {
		fmt.Fprintln(out, "metadata        none")
	}
}

// metaValueText renders a metadata value for a terminal.
func metaValueText(v nvaa.MetaValue) string {
	switch v.Type {
	case nvaa.MetaNull:
		return "null"
	case nvaa.MetaUint:
		return fmt.Sprintf("%d", v.Uint)
	case nvaa.MetaInt:
		return fmt.Sprintf("%d", v.Int)
	case nvaa.MetaBool:
		return fmt.Sprintf("%v", v.Bool)
	case nvaa.MetaText:
		return fmt.Sprintf("%q", v.Text)
	case nvaa.MetaBlob:
		return fmt.Sprintf("0x%x", v.Blob)
	}
	return "?"
}
