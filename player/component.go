// Package player renders and plays NeoViolet ASCII-style Animation files.
//
// It is split in two on purpose.
//
// Player is a component: it is given a box, paints the animation into it, and
// answers messages. It owns no terminal, no layout, and no program, so it can be
// embedded in someone else's Bubble Tea application -- or in anything else that
// can call View and place the string it returns.
//
// Run hosts one Player in its own program for the standalone command, and is the
// worked example of the arrangement: it owns the terminal, hands the player a
// box, routes the keys it wants for itself before the player sees them, and
// meters what actually reached the terminal.
//
// The boundary that matters is box against terminal. Everything inside the
// player's box belongs to the animation and is painted edge to edge; everything
// outside it belongs to the host.
package player

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/render"
)

// tickMsg wakes the player to check its clock. It carries nothing on purpose:
// the clock is authoritative, so a delayed or coalesced tick cannot make
// playback drift.
type tickMsg struct{}

// phase is where playback is in its own lifecycle.
type phase int

const (
	// phaseWarning is showing the photosensitivity gate. The clock has not
	// started, so a viewer is never shown content while reading a warning about
	// it. The gate is state, not a prompt: nothing here reads input directly.
	phaseWarning phase = iota

	phasePlaying
	phasePaused
	phaseFinished
	phaseError
)

// String names a phase.
func (p phase) String() string {
	switch p {
	case phaseWarning:
		return "warning"
	case phasePlaying:
		return "playing"
	case phasePaused:
		return "paused"
	case phaseFinished:
		return "finished"
	case phaseError:
		return "error"
	}
	return "unknown"
}

// KeyMap names the keys the player answers.
//
// Leaving a field empty disables that action. That is the mechanism a host uses
// to keep a key for itself: it simply does not appear here, the player reports
// the press as unhandled, and the host is free to bind it.
type KeyMap struct {
	// Acknowledge dismisses the photosensitivity gate.
	Acknowledge []string

	PlayPause []string
	Next      []string
	Previous  []string

	// NextMark and PrevMark move between keyframes, which are the seek points.
	NextMark []string
	PrevMark []string

	// The jumps by time. Stepping frame by frame is exact but slow across a long
	// animation; these travel a fixed distance on the clock, which is how a
	// viewer thinks about "go back five seconds". The vertical pair follows the
	// convention video players have used for years: up travels forward.
	Rewind     []string
	Forward    []string
	RewindFar  []string
	ForwardFar []string

	// Start and End go to the ends of the animation.
	Start []string
	End   []string

	// Jump names positions by tenth: the digits listed here go to 10%..90% of the
	// animation, so the digit's own value is the fraction. The list is what turns
	// the behaviour on, so clearing it gives the digits back to the host.
	Jump []string

	Restart []string
}

// How far one press of a time-jump key travels, in milliseconds. They are
// exported so a host can describe its own help text without repeating numbers
// that live here.
const (
	SeekStepMS = 5000
	SeekFarMS  = 60000
)

// DefaultKeys is the key map used when none is given.
//
// Note what is absent. Quits, and anything else about the program's lifecycle,
// belong to the host: a component that decides when the application exits cannot
// be embedded in one that has its own ideas.
var DefaultKeys = KeyMap{
	Acknowledge: []string{"space", "enter"},
	PlayPause:   []string{"space"},
	Next:        []string{"n", "."},
	Previous:    []string{"p", ","},
	NextMark:    []string{"]", "l"},
	PrevMark:    []string{"[", "h"},
	Rewind:      []string{"left"},
	Forward:     []string{"right"},
	RewindFar:   []string{"down"},
	ForwardFar:  []string{"up"},
	Start:       []string{"home"},
	End:         []string{"end"},
	Jump:        []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"},
	Restart:     []string{"r"},
}

// Options configure a player component.
type Options struct {
	// Columns and Lines are the initial box. A host that lays out its own
	// screen should follow them with SetSize as its layout changes. They
	// default to 80x24.
	Columns int
	Lines   int

	// Loop keeps playing after the last frame. The zero value stops, because a
	// component that never finishes is a surprise in someone else's program.
	Loop bool

	// SkipWarning plays without stopping at a failed photosensitivity check.
	SkipWarning bool

	// TransparentMargin leaves the part of the box the animation does not cover
	// unstyled, instead of painting it with the animation's own background.
	//
	// The default is opaque, so the box reads as one rectangle and a renderer
	// that erases with the wrong colour has nothing to reveal. Transparency
	// suits a host that is composing an animation smaller than its pane and
	// wants the surrounding surface to show through.
	TransparentMargin bool

	// Keys overrides the key map. Nil means DefaultKeys.
	Keys *KeyMap
}

// Player plays one animation inside a box.
type Player struct {
	anim     *nvaa.Animation
	timeline *Timeline
	meter    *Meter
	warning  Warning
	keys     KeyMap
	fill     render.Fill

	phase phase
	err   error
	loop  bool

	columns int
	lines   int

	// start and baseMS define the animation clock: the time on screen is baseMS
	// plus however long has passed since start. Deriving the frame from the
	// clock rather than counting ticks is what keeps a 33/34ms alternation
	// intact over thousands of frames, and it means a host that delivers ticks
	// late or in bursts is corrected for rather than obeyed.
	start  time.Time
	baseMS uint64
}

// New prepares a player.
func New(anim *nvaa.Animation, opts Options) *Player {
	columns, lines := opts.Columns, opts.Lines
	if columns <= 0 {
		columns = 80
	}
	if lines <= 0 {
		lines = 24
	}

	keys := DefaultKeys
	if opts.Keys != nil {
		keys = *opts.Keys
	}

	p := &Player{
		anim:     anim,
		timeline: NewTimeline(anim, columns, lines),
		meter:    NewMeter(),
		warning:  WarningFrom(anim),
		keys:     keys,
		fill:     render.Fill{Cell: fillCell(anim), Opaque: !opts.TransparentMargin},
		loop:     opts.Loop,
		columns:  columns,
		lines:    lines,
	}

	// A warning only stops playback when the file actually declares a failure.
	// An unknown verdict does not block: refusing to play every file that lacks
	// metadata would punish the honest and be routed around by everyone else.
	if p.warning.Verdict == VerdictFail && !opts.SkipWarning {
		p.phase = phaseWarning
	} else {
		p.phase = phasePlaying
	}
	return p
}

// fillCell is the background the animation itself uses, taken from style 0.
//
// The glyph is deliberately left empty so that Box writes a space. A file whose
// empty style is defined as some other character must not have that character
// tiled across the margin.
func fillCell(anim *nvaa.Animation) render.Cell {
	style, ok := anim.StyleAt(0)
	if !ok {
		return render.Cell{}
	}

	cell := render.Cell{}
	if int(style.FG) < len(anim.Palette) {
		cell.FG = anim.Palette[style.FG]
	}
	if int(style.BG) < len(anim.Palette) {
		cell.BG = anim.Palette[style.BG]
	}
	return cell
}

// Init composes the first frame and starts the clock, unless a warning gate is
// waiting to be acknowledged. The returned command is the host's to run.
func (p *Player) Init() tea.Cmd {
	if err := p.timeline.Start(); err != nil {
		p.fail(err)
		return nil
	}
	p.observe()

	if p.phase == phaseWarning {
		return nil
	}
	p.startClock()
	return p.tick()
}

// Update handles one message.
//
// The command belongs to the host, which runs it; the bool reports whether the
// player dealt with the message. An unhandled key is one the host may treat as
// its own, which is what keeps the player from swallowing an application's
// bindings.
func (p *Player) Update(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tickMsg:
		if p.phase != phasePlaying {
			return nil, true
		}
		if err := p.advanceToClock(); err != nil {
			p.fail(err)
			return nil, true
		}
		p.observe()

		if p.phase == phaseFinished {
			if !p.loop {
				return nil, true
			}
			if err := p.timeline.Restart(); err != nil {
				p.fail(err)
				return nil, true
			}
			p.startClock()
		}
		return p.tick(), true

	case tea.KeyPressMsg:
		return p.handleKey(msg)
	}
	return nil, false
}

// View renders the animation into its box.
//
// The result is exactly as many rows as the player was sized for, each exactly
// as wide, with the area the animation does not cover filled. Nothing outside
// the box is written: outside it is the host's.
//
// Whether the surrounding application enters the alternate screen, hides the
// cursor, or sets a window title is deliberately not decided here.
func (p *Player) View() string {
	switch p.phase {
	case phaseError:
		return p.textBox("nvaa: " + p.err.Error())
	case phaseWarning:
		return p.textBox(gateText(p.warning))
	}

	grid := p.timeline.Grid()
	if grid == nil {
		return p.textBox("")
	}
	return grid.Box(p.columns, p.lines, p.fill)
}

// SetSize gives the player a new box.
//
// A degenerate size is ignored rather than adopted. Terminals report 0x0 while
// they are being resized or torn down, and taking that literally would blank the
// canvas and leave the player sized for nothing.
func (p *Player) SetSize(columns, lines int) error {
	if columns <= 0 || lines <= 0 {
		return nil
	}
	p.columns, p.lines = columns, lines
	return p.timeline.SetSize(columns, lines)
}

// ---- programmatic control ----
//
// A host need not synthesise keystrokes to drive playback; these mirror the key
// map. Each returns the command to run, if any.

// Pause freezes playback where it is.
func (p *Player) Pause() {
	if p.phase == phasePlaying {
		p.baseMS = p.clockMS()
		p.phase = phasePaused
	}
}

// Resume continues from the current frame.
func (p *Player) Resume() tea.Cmd {
	if p.phase != phasePaused {
		return nil
	}
	p.startClock()
	return p.tick()
}

// Restart returns to the first frame and plays from there.
func (p *Player) Restart() tea.Cmd {
	if err := p.timeline.Restart(); err != nil {
		p.fail(err)
		return nil
	}
	p.startClock()
	p.observe()
	return p.tick()
}

// Step pauses and moves by delta frames.
func (p *Player) Step(delta int) tea.Cmd {
	p.Pause()
	if p.phase == phaseFinished {
		p.phase = phasePaused
	}
	return p.step(delta)
}

// JumpKeyframe moves to the neighbouring seek point.
//
// Playback carries on: a jump is not a pause. Pausing here would make the
// bracket keys a trap for anyone scrubbing a running animation, and it is not
// what the reference player does.
func (p *Player) JumpKeyframe(direction int) tea.Cmd {
	moved, err := p.timeline.JumpKeyframe(direction)
	if err != nil {
		p.fail(err)
		return nil
	}
	if !moved {
		return nil
	}
	p.reseatClock()
	p.observe()
	return p.tick()
}

// SeekToTime goes to the frame covering ms into the animation, clamping at the
// ends, and keeps playing from there if playback was running.
func (p *Player) SeekToTime(ms uint64) tea.Cmd {
	return p.seek(func() error { return p.timeline.SeekToTime(ms) })
}

// SeekFraction goes to a fraction of the animation: 0 is the start, 1 the end.
func (p *Player) SeekFraction(fraction float64) tea.Cmd {
	return p.seek(func() error { return p.timeline.SeekFraction(fraction) })
}

// SeekBy moves by a signed number of milliseconds from the current frame.
func (p *Player) SeekBy(deltaMS int64) tea.Cmd {
	return p.seek(func() error { return p.timeline.SeekBy(deltaMS) })
}

// SeekToStart goes back to the first frame, leaving the playback state alone.
func (p *Player) SeekToStart() tea.Cmd {
	return p.seek(p.timeline.Restart)
}

// SeekToEnd goes to the last frame.
func (p *Player) SeekToEnd() tea.Cmd {
	return p.seek(p.timeline.SeekToEnd)
}

// seek runs a jump and puts the clock back in step with wherever it landed.
//
// The phase is left alone on purpose: a viewer scrubbing a running animation
// expects it to keep running from the new place, so the clock is re-anchored
// rather than stopped.
func (p *Player) seek(move func() error) tea.Cmd {
	if err := move(); err != nil {
		p.fail(err)
		return nil
	}
	p.reseatClock()
	p.observe()
	return p.tick()
}

// ---- input ----

func (p *Player) handleKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	key := msg.String()

	if p.phase == phaseWarning {
		if contains(p.keys.Acknowledge, key) {
			p.startClock()
			return p.tick(), true
		}
		// Anything else is the host's, including quitting.
		return nil, false
	}

	switch {
	case contains(p.keys.PlayPause, key):
		if p.phase == phasePlaying {
			p.Pause()
		} else if p.phase == phasePaused {
			return p.Resume(), true
		}
		return nil, true

	case contains(p.keys.Next, key):
		return p.Step(1), true

	case contains(p.keys.Previous, key):
		return p.Step(-1), true

	case contains(p.keys.NextMark, key):
		return p.JumpKeyframe(1), true

	case contains(p.keys.PrevMark, key):
		return p.JumpKeyframe(-1), true

	case contains(p.keys.Restart, key):
		return p.Restart(), true

	case contains(p.keys.Rewind, key):
		return p.SeekBy(-SeekStepMS), true

	case contains(p.keys.Forward, key):
		return p.SeekBy(SeekStepMS), true

	case contains(p.keys.RewindFar, key):
		return p.SeekBy(-SeekFarMS), true

	case contains(p.keys.ForwardFar, key):
		return p.SeekBy(SeekFarMS), true

	case contains(p.keys.Start, key):
		return p.SeekToStart(), true

	case contains(p.keys.End, key):
		return p.SeekToEnd(), true
	}

	// A digit names a tenth of the animation, so "5" is the middle of it.
	if tenths := digitTenths(key); tenths > 0 && contains(p.keys.Jump, key) {
		return p.SeekFraction(float64(tenths) / 10), true
	}

	return nil, false
}

// digitTenths reads a digit key as tenths, or 0 when the key is not one. Only
// 1..9 qualify: the tenth they name is their own value, and there is no key for
// "0 tenths" that would not collide with something else.
func digitTenths(key string) int {
	if len(key) != 1 || key[0] < '1' || key[0] > '9' {
		return 0
	}
	return int(key[0] - '0')
}

// contains reports whether key is in the list. An empty list matches nothing,
// which is how an action gets disabled.
func contains(keys []string, key string) bool {
	for _, candidate := range keys {
		if candidate == key {
			return true
		}
	}
	return false
}

// step moves by delta frames without touching the clock's phase beyond pausing.
func (p *Player) step(delta int) tea.Cmd {
	var (
		moved bool
		err   error
	)
	if delta > 0 {
		moved, err = p.timeline.Next()
	} else {
		moved, err = p.timeline.Previous()
	}
	if err != nil {
		p.fail(err)
		return nil
	}

	// Keep the clock with the frame, so resuming continues from here.
	p.baseMS = p.timeline.ElapsedMS()
	if moved {
		p.observe()
	}
	return nil
}

// ---- playback clock ----

// startClock anchors the animation clock at the current frame and plays.
func (p *Player) startClock() {
	p.start = time.Now()
	p.baseMS = p.timeline.ElapsedMS()
	p.phase = phasePlaying
}

// reseatClock anchors the clock at the current frame without changing whether
// playback is running, which is what a seek needs: the position moved, the
// intent did not.
func (p *Player) reseatClock() {
	p.baseMS = p.timeline.ElapsedMS()

	switch p.phase {
	case phasePlaying:
		p.start = time.Now()
	case phaseFinished:
		// Leaving the end leaves the player paused rather than finished.
		// Finished is inert -- neither the clock nor PlayPause applies to it -- so
		// keeping it would strand a viewer who jumped back from the last frame.
		p.phase = phasePaused
	}
}

// clockMS is the animation time, derived from wall time rather than counted
// ticks so error cannot accumulate.
func (p *Player) clockMS() uint64 {
	if p.phase != phasePlaying {
		return p.baseMS
	}
	return p.baseMS + uint64(time.Since(p.start).Milliseconds())
}

// advanceToClock moves playback to wherever the clock says it should be,
// skipping frames if the host was starved.
//
// The frames it skips are stepped over without resolving a grid: only the frame
// the clock lands on is ever drawn, and resolving a grid is on a small canvas the
// most expensive part of a playback frame. The grid is resolved once, at the end,
// for the frame that will actually be shown.
func (p *Player) advanceToClock() error {
	now := p.clockMS()

	skipped := false
	for !p.timeline.AtEnd() {
		deadline := p.timeline.ElapsedMS() + p.timeline.Frame().DurationMS
		if now < deadline {
			break
		}
		next, err := p.timeline.advance()
		if err != nil {
			return err
		}
		if !next {
			break
		}
		skipped = true
	}

	// Finish only once the last frame has been on screen for its own duration.
	deadline := p.timeline.ElapsedMS() + p.timeline.Frame().DurationMS
	if now >= deadline {
		p.phase = phaseFinished
	}

	if skipped {
		return p.timeline.recompose()
	}
	return nil
}

// tick schedules the next wake-up, for exactly as long as the current frame has
// left. Sleeping the nominal interval instead would round each frame and let the
// error accumulate.
func (p *Player) tick() tea.Cmd {
	if p.phase != phasePlaying || p.timeline.Frame() == nil {
		return nil
	}

	deadline := p.timeline.ElapsedMS() + p.timeline.Frame().DurationMS
	now := p.clockMS()

	wait := time.Millisecond
	if deadline > now {
		wait = time.Duration(deadline-now) * time.Millisecond
	}

	return tea.Tick(wait, func(time.Time) tea.Msg { return tickMsg{} })
}

// ---- rendering helpers ----

// textBox paints text into the box, one row per line of it.
func (p *Player) textBox(text string) string {
	rows := strings.Split(text, "\n")

	box := make([]string, 0, p.lines)
	for vy := range p.lines {
		line := ""
		if vy < len(rows) {
			line = rows[vy]
		}
		box = append(box, p.fillRow(line))
	}
	return strings.Join(box, "\n")
}

// fillRow paints one row of the box across its full width, on the animation's
// own background so a message stands where the picture will be.
//
// Rows are filled rather than left short so that the box is a rectangle: a
// renderer is free to erase with whatever colour its pen holds, and an erase
// runs to the end of the terminal line rather than to the end of this box.
func (p *Player) fillRow(text string) string {
	line := clampText(text, p.columns)

	var b strings.Builder
	if p.fill.Opaque {
		b.Write(render.AppendSGR(nil, 38, p.fill.Cell.FG))
		b.Write(render.AppendSGR(nil, 48, p.fill.Cell.BG))
	}
	b.WriteString(line)
	for pad := p.columns - displayWidth(line); pad > 0; pad-- {
		b.WriteByte(' ')
	}
	if p.fill.Opaque {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// gateText is the compact advisory shown in the box while the gate is up. The
// framed version, with its candid note about what a claim is worth, belongs to
// whoever owns the scrollback: see Warning.Box.
func gateText(w Warning) string {
	lines := []string{
		"PHOTOSENSITIVITY",
		"",
		"this file failed its own check",
		"",
		"general flashes/s  " + itoa(w.General),
		"red flashes/s      " + itoa(w.Red),
	}
	if w.HasArea {
		lines = append(lines, "largest flash      "+itoa(w.Area)+" permille of the viewport")
	}
	if w.Message != "" {
		lines = append(lines, "", w.Message)
	}
	lines = append(lines,
		"",
		"The file's claim, not a certification, and not this player's own",
		"judgement. Flashing imagery can trigger seizures.",
		"",
		"space or enter to play",
	)
	return strings.Join(lines, "\n")
}

// itoa formats an unsigned count without pulling in fmt for it.
func itoa(value uint64) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}

// clampText truncates s to at most width columns, never splitting a wide glyph
// in half.
func clampText(s string, width int) string {
	if width <= 0 {
		return ""
	}

	used := 0
	for index, r := range s {
		span := 1
		if render.IsWideGlyph(string(r)) {
			span = 2
		}
		if used+span > width {
			return s[:index]
		}
		used += span
	}
	return s
}

// displayWidth counts the columns s occupies.
func displayWidth(s string) int {
	used := 0
	for _, r := range s {
		if render.IsWideGlyph(string(r)) {
			used += 2
			continue
		}
		used++
	}
	return used
}

// ---- accessors ----

// observe records the frame now on screen.
func (p *Player) observe() {
	if grid := p.timeline.Grid(); grid != nil {
		p.meter.Observe(grid)
	}
}

func (p *Player) fail(err error) {
	p.err = err
	p.phase = phaseError
}

// Animation is the file being played.
func (p *Player) Animation() *nvaa.Animation { return p.anim }

// Grid is the frame currently on screen, as the player resolved it.
func (p *Player) Grid() *render.Grid { return p.timeline.Grid() }

// Timeline exposes playback position and seeking.
func (p *Player) Timeline() *Timeline { return p.timeline }

// Meter exposes the running cost of playback. Its Emitted field is the host's
// to fill: only whoever owns the terminal can say how many bytes it received.
func (p *Player) Meter() *Meter { return p.meter }

// Warning is the advisory the file carries about itself.
func (p *Player) Warning() Warning { return p.warning }

// Verdict is the three-state reading of that advisory.
func (p *Player) Verdict() Verdict { return p.warning.Verdict }

// Fill is the background the player paints its box with, for a host that wants
// the surrounding surface to match.
func (p *Player) Fill() render.Fill { return p.fill }

// Index is the frame on screen.
func (p *Player) Index() int { return p.timeline.Index() }

// Count is how many frames the file holds.
func (p *Player) Count() int { return p.timeline.Count() }

// ElapsedMS is how far into the animation playback has reached.
func (p *Player) ElapsedMS() uint64 { return p.timeline.ElapsedMS() }

// TotalMS is the animation's length.
func (p *Player) TotalMS() uint64 { return p.anim.TotalDuration() }

// Timecode is the position, formatted for display.
func (p *Player) Timecode() string { return p.timeline.Timecode() }

// Progress is how far into the animation the current frame begins, from 0 to 1.
func (p *Player) Progress() float64 { return p.timeline.Progress() }

// Position is where playback is, in the terms a status line shows it. A host
// draws the line, so the numbers come out together rather than as four calls
// that could disagree about the moment they describe.
func (p *Player) Position() Position {
	return Position{
		Index:   p.timeline.Index(),
		Count:   p.timeline.Count(),
		Elapsed: p.timeline.ElapsedMS(),
		Total:   p.timeline.TotalMS(),
	}
}

// Position is a playback location.
//
// The player gives hosts this instead of drawing a progress bar itself: the
// component owns its box and nothing else, and where a position readout goes is
// a question about the surrounding application.
type Position struct {
	Index   int    // frame on screen
	Count   int    // frames in the file
	Elapsed uint64 // milliseconds into the animation
	Total   uint64 // length in milliseconds
}

// Progress is how far through the animation the frame begins, from 0 to 1.
func (pos Position) Progress() float64 {
	if pos.Total == 0 {
		return 0
	}
	return float64(pos.Elapsed) / float64(pos.Total)
}

// String renders the position as "frame 12/8704  0:04.1/4:50.1", leaving the
// rest of a status line -- a bar, a state, whatever else -- to the host.
func (pos Position) String() string {
	return "frame " + itoa(uint64(pos.Index)) + "/" + itoa(uint64(pos.Count)) +
		"  " + Timecode(pos.Elapsed) + "/" + Timecode(pos.Total)
}

// State names where playback is, for a host's own status line.
func (p *Player) State() string { return p.phase.String() }

// Paused reports whether playback is held.
func (p *Player) Paused() bool { return p.phase == phasePaused }

// Done reports whether the animation has run out. A looping player is never
// done, since it starts over instead.
func (p *Player) Done() bool { return p.phase == phaseFinished }

// AtWarning reports whether the photosensitivity gate is still up.
func (p *Player) AtWarning() bool { return p.phase == phaseWarning }

// Err is the failure that stopped playback, if any.
//
// A failed player is inert rather than fatal: it holds the error, reports it in
// its box, and leaves the decision to quit to the host.
func (p *Player) Err() error { return p.err }
