package render

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhatDamon/go-nvaa-codec"
)

// The conformance fixtures double as renderer input: they are small, real, and
// already cover the cases that matter here (wide glyphs, camera movement).
const vectorRoot = "../testdata"

func loadVector(t *testing.T, name string) *nvaa.Animation {
	t.Helper()

	anim, err := nvaa.ReadFile(filepath.Join(vectorRoot, "vectors", name+".nvaa"))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return anim
}

// firstFrame composes a fixture and returns the grid for its first frame.
func firstFrame(t *testing.T, name string, columns, lines int) *Grid {
	t.Helper()

	anim := loadVector(t, name)
	composer := NewComposer(anim)

	for frame, err := range anim.Frames() {
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		composer.Apply(frame)
		return composer.Grid(frame, columns, lines)
	}
	t.Fatalf("%s: no frames", name)
	return nil
}

// ---- window rule ----

// TestViewportWindow pins the crop-never-scale rule and its clamping. The
// numbers come from the reference formula, which the conformance fixtures do
// not exercise in these corners.
func TestViewportWindow(t *testing.T) {
	anim := &nvaa.Animation{Width: 100, Height: 30}

	cases := []struct {
		name                  string
		cameraX, cameraY      int64
		viewportW, viewportH  uint32
		columns, lines        int
		wantX0, wantY0, wantW int
		wantH                 int
	}{
		{
			name: "camera at origin", cameraX: 0, cameraY: 0,
			viewportW: 50, viewportH: 18, columns: 80, lines: 24,
			wantX0: 0, wantY0: 0, wantW: 50, wantH: 18,
		},
		{
			name: "window smaller than the terminal", cameraX: 10, cameraY: 4,
			viewportW: 20, viewportH: 10, columns: 80, lines: 24,
			wantX0: 10, wantY0: 4, wantW: 20, wantH: 10,
		},
		{
			// A camera near the far edge must clamp so the window stays inside
			// the canvas instead of running past it.
			name: "clamped at the bottom right", cameraX: 95, cameraY: 28,
			viewportW: 50, viewportH: 18, columns: 80, lines: 24,
			wantX0: 50, wantY0: 12, wantW: 50, wantH: 18,
		},
		{
			// Terminal narrower than the viewport: the viewport is cropped, and
			// the window re-centres on the camera.
			name: "terminal crops the viewport", cameraX: 0, cameraY: 0,
			viewportW: 50, viewportH: 18, columns: 30, lines: 8,
			wantX0: 10, wantY0: 5, wantW: 30, wantH: 8,
		},
		{
			name: "viewport larger than the canvas", cameraX: 0, cameraY: 0,
			viewportW: 200, viewportH: 60, columns: 80, lines: 24,
			wantX0: 20, wantY0: 6, wantW: 80, wantH: 24,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame := &nvaa.Frame{
				CameraX: tc.cameraX, CameraY: tc.cameraY,
				ViewportW: tc.viewportW, ViewportH: tc.viewportH,
			}
			x0, y0, w, h := nvaa.ViewportWindow(anim, frame, tc.columns, tc.lines)

			if x0 != tc.wantX0 || y0 != tc.wantY0 || w != tc.wantW || h != tc.wantH {
				t.Errorf("got (%d,%d,%d,%d), want (%d,%d,%d,%d)",
					x0, y0, w, h, tc.wantX0, tc.wantY0, tc.wantW, tc.wantH)
			}
			if w > int(anim.Width) || h > int(anim.Height) {
				t.Errorf("window %dx%d escapes the %dx%d canvas", w, h, anim.Width, anim.Height)
			}
			if x0+w > int(anim.Width) || y0+h > int(anim.Height) {
				t.Errorf("window at (%d,%d) size %dx%d escapes the canvas", x0, y0, w, h)
			}
		})
	}
}

// ---- wide glyphs ----

// TestWideGlyphIsSkipped is the regression this renderer most needs: the right
// half of a wide glyph must never be emitted, and the cell after the pair must
// still be drawn.
func TestWideGlyphIsSkipped(t *testing.T) {
	grid := firstFrame(t, "wide-glyph", 8, 1)

	if grid.Width != 8 || grid.Height != 1 {
		t.Fatalf("grid is %dx%d, want 8x1", grid.Width, grid.Height)
	}

	// The fixture paints 日 across columns 1 and 2, then '#' at column 3.
	if got := grid.At(1, 0).Glyph; got != "日" {
		t.Errorf("cell 1 = %q, want the wide glyph", got)
	}
	if !grid.At(2, 0).Continuation {
		t.Error("cell 2 should be a continuation of the wide glyph")
	}
	// If pairing consumed the wrong number of columns, this cell would be
	// mistaken for a continuation and vanish from the screen.
	if got := grid.At(3, 0).Glyph; got != "#" {
		t.Errorf("cell 3 = %q, want %q -- the cell after a wide pair must still be drawn", got, "#")
	}
	if grid.At(3, 0).Continuation {
		t.Error("cell 3 must not be a continuation")
	}

	// The wide glyph occupies two columns but is written once.
	plain := grid.Plain()
	if n := strings.Count(plain, "日"); n != 1 {
		t.Errorf("wide glyph appears %d times in the output, want 1", n)
	}
	if !strings.HasPrefix(plain, " 日#") {
		t.Errorf("plain output starts %q, want %q", firstRunes(plain, 4), " 日#")
	}

	// And the accumulated repaint agrees.
	if n := strings.Count(grid.FullRepaint(), "日"); n != 1 {
		t.Errorf("wide glyph appears %d times in the repaint, want 1", n)
	}
}

// TestIsWideGlyph pins the width rule, including the two cases a naive
// East-Asian-Width check gets wrong.
func TestIsWideGlyph(t *testing.T) {
	cases := map[string]bool{
		"a":  false,
		" ":  false,
		"#":  false,
		"":   false,
		"日":  true,  // East Asian Wide
		"ト":  true,  // Katakana, Wide
		"Ａ":  true,  // Fullwidth Latin
		"─":  false, // Box drawing: not ASCII, but single width
		"·":  false, // U+00B7 is below the U+1100 floor
		"\t": false,
	}

	for glyph, want := range cases {
		if got := IsWideGlyph(glyph); got != want {
			t.Errorf("IsWideGlyph(%q) = %v, want %v", glyph, got, want)
		}
	}
}

func firstRunes(s string, n int) string {
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// ---- grids over real fixtures ----

func TestGridOverFixtures(t *testing.T) {
	for _, name := range []string{
		"minimal", "body-span", "body-gap", "body-list",
		"keyframe-and-delta", "deflate-index-crc", "empty-style-clears",
		"no-camera-mode",
	} {
		t.Run(name, func(t *testing.T) {
			grid := firstFrame(t, name, 80, 24)

			if grid.Width <= 0 || grid.Height <= 0 {
				t.Fatalf("grid is %dx%d", grid.Width, grid.Height)
			}
			if len(grid.Cells) != grid.Width*grid.Height {
				t.Fatalf("grid holds %d cells for a %dx%d area",
					len(grid.Cells), grid.Width, grid.Height)
			}
			if grid.X0+grid.Width > 128 || grid.Y0+grid.Height > 128 {
				t.Errorf("window at (%d,%d) size %dx%d looks unclamped",
					grid.X0, grid.Y0, grid.Width, grid.Height)
			}

			// Every emitted row must be exactly as wide as the grid, counting a
			// wide glyph as two columns.
			for vy := range grid.Height {
				width := 0
				for vx := range grid.Width {
					cell := grid.At(vx, vy)
					if cell.Continuation {
						continue
					}
					if IsWideGlyph(cell.Glyph) {
						width += 2
					} else {
						width++
					}
				}
				if width != grid.Width {
					t.Errorf("row %d renders %d columns, want %d", vy, width, grid.Width)
				}
			}
		})
	}
}

// TestComposerFoldsDeltas checks that a delta frame actually changes the canvas
// rather than only being parsed.
func TestComposerFoldsDeltas(t *testing.T) {
	anim := loadVector(t, "keyframe-and-delta")
	composer := NewComposer(anim)

	frames := 0
	sawPainted := false

	for frame, err := range anim.Frames() {
		if err != nil {
			t.Fatalf("frame %d: %v", frames, err)
		}
		composer.Apply(frame)
		frames++

		for y := range anim.Height {
			for _, style := range composer.Canvas().Row(y) {
				if style != 0 {
					sawPainted = true
				}
			}
		}
	}

	if frames < 2 {
		t.Fatalf("fixture has %d frames, want at least 2", frames)
	}
	if !sawPainted {
		t.Error("canvas is empty after folding every frame")
	}
}

// TestTheDemoIsRenderable runs the whole renderer over the committed demo: every
// frame composed and turned into terminal output, which is the closest a test
// comes to playing it.
func TestTheDemoIsRenderable(t *testing.T) {
	path := filepath.Join("..", "testdata", "demo.nvaa")

	anim, err := nvaa.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	composer := NewComposer(anim)
	frames := 0

	for frame, err := range anim.Frames() {
		if err != nil {
			t.Fatalf("frame %d: %v", frames, err)
		}
		composer.Apply(frame)
		grid := composer.Grid(frame, 80, 24)

		if grid.Width*grid.Height == 0 {
			t.Fatalf("frame %d produced an empty grid", frames)
		}
		if repaint := grid.FullRepaint(); !strings.HasSuffix(repaint, "\x1b[0m") {
			t.Fatalf("frame %d: repaint does not reset attributes", frames)
		}
		frames++
	}

	if frames == 0 {
		t.Fatal("no frames rendered")
	}
	t.Logf("rendered %d frames from demo.nvaa", frames)
}
