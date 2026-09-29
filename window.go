package nvaa

// ViewportWindow maps a frame's camera and viewport onto a columns x lines
// output grid. It returns the origin of the visible window in canvas
// coordinates plus its size, cropping rather than scaling.
//
// This is the single definition of "what is on screen". The renderer and the
// photosensitivity analyser both go through it, so an analysis always describes
// what the viewer actually saw rather than each deriving its own window.
func ViewportWindow(a *Animation, f *Frame, columns, lines int) (x0, y0, width, height int) {
	width = min(int(f.ViewportW), columns)
	height = min(int(f.ViewportH), lines)

	// The camera names the window's top-left corner, but centring is what
	// keeps the subject stable as the viewport changes size.
	centreX := f.CameraX + int64(f.ViewportW)/2
	centreY := f.CameraY + int64(f.ViewportH)/2

	x0 = clampInt(int(centreX)-width/2, 0, int(a.Width)-width)
	y0 = clampInt(int(centreY)-height/2, 0, int(a.Height)-height)

	// A viewport wider or taller than the canvas is not something a conformant
	// encoder emits, but a decoder that passed it on would hand its callers a grid
	// larger than the canvas -- and the canvas is the bound every allocation in a
	// renderer is sized against. Cropping is the identity for a conformant file,
	// where x0 is already clamped so that x0+width fits inside the canvas.
	width = min(width, int(a.Width)-x0)
	height = min(height, int(a.Height)-y0)
	return x0, y0, width, height
}

// clampInt pins v into [lo, hi]. An inverted range collapses to lo, which is
// what a viewport wider than the canvas produces.
func clampInt(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
