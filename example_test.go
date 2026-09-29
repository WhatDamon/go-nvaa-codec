package nvaa_test

import (
	"fmt"

	nvaa "github.com/WhatDamon/go-nvaa-codec"
)

// ExampleReadFile shows what a container says about itself before any frame is
// decoded: everything below comes from the header and the tables.
func ExampleReadFile() {
	anim, err := nvaa.ReadFile("testdata/demo.nvaa")
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Printf("%s (%s) v%d\n", nvaa.FormatName, nvaa.Alias, anim.Version)
	fmt.Printf("canvas %dx%d, %d frames, %d ms\n",
		anim.Width, anim.Height, anim.FrameCount(), anim.TotalDuration())
	fmt.Printf("tables: %d colours, %d glyphs, %d styles\n",
		len(anim.Palette), len(anim.Glyphs), len(anim.Styles))

	// Output:
	// NeoViolet ASCII-style Animation (NVAA) v1
	// canvas 100x30, 12 frames, 1200 ms
	// tables: 6 colours, 29 glyphs, 33 styles
}

// ExampleAnimation_Frames walks the frames and folds them into one canvas. The
// canvas is what a player holds: its size does not grow with the animation.
func ExampleAnimation_Frames() {
	anim, err := nvaa.ReadFile("testdata/demo.nvaa")
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	canvas := nvaa.NewCanvas(anim.Width, anim.Height)
	keyframes := 0

	for frame, err := range anim.Frames() {
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		if frame.Keyframe {
			keyframes++
		}
		canvas.Apply(frame)
	}

	fmt.Printf("%d of %d frames start a new canvas\n", keyframes, anim.FrameCount())

	// Style 0 is the reserved empty style, so a cell holding it is bare canvas.
	painted := 0
	for y := range canvas.H {
		for x := range canvas.W {
			if canvas.At(x, y) != 0 {
				painted++
			}
		}
	}
	fmt.Printf("after all of them the canvas holds %d painted cells\n", painted)

	// Output:
	// 1 of 12 frames start a new canvas
	// after all of them the canvas holds 268 painted cells
}
