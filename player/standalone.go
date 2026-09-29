package player

import (
	"fmt"
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/WhatDamon/go-nvaa-codec"
)

// HostOptions configure a standalone program around a player component.
//
// They are kept apart from Options because they answer a different question.
// Options shape the component, which any host can embed; these describe the
// terminal that this particular program has taken over, and have no meaning to
// a host that is composing the animation into a screen of its own.
type HostOptions struct {
	// ShowStats reserves the last row for a live meter, and makes the report
	// that Run returns.
	ShowStats bool

	// AlternateScreen draws on the alternate screen, so whatever the viewer was
	// reading is still there afterwards.
	AlternateScreen bool

	// Title sets the terminal's window title, where the terminal obliges.
	Title string

	// NoColor asks the renderer for no colour at all.
	//
	// It is opt-in rather than inferred because colour here is content, not
	// decoration: the format stores exact 24-bit RGB and the reference player
	// emits it unconditionally. A capability heuristic or an ambient NO_COLOR
	// would silently reduce the artwork to monochrome, and the glyphs alone do
	// not carry the picture.
	NoColor bool

	// StartAtMS begins playback partway in, for --seek.
	//
	// A pointer rather than a plain number because zero is a real position --
	// the beginning -- so the zero value has to mean "not asked for".
	StartAtMS *uint64
}

// Run plays an animation in a program of its own, and reports what playback cost
// the terminal.
//
// Read this as the worked example of hosting a Player. It owns the terminal,
// hands the component a box, keeps the keys an application needs for itself,
// and counts the bytes the terminal actually received -- a figure only the
// owner of the writer can know.
func Run(anim *nvaa.Animation, opts Options, hostOpts HostOptions, input io.Reader, output io.Writer) (string, error) {
	model := newHost(anim, opts, hostOpts)

	// Metered, but the wrapper stays a term.File. Passing a plain io.Writer
	// here makes Bubble Tea treat a real terminal as a pipe, which costs both
	// the colours and the real window size.
	wrapped, counter := meterOutput(output)

	programOptions := []tea.ProgramOption{
		tea.WithOutput(wrapped),
		tea.WithWindowSize(model.columns, model.lines),
		tea.WithColorProfile(model.colorProfile()),
	}
	if input != nil {
		programOptions = append(programOptions, tea.WithInput(input))
	}

	if _, err := tea.NewProgram(model, programOptions...).Run(); err != nil {
		return "", err
	}

	model.player.meter.Emitted = counter.Count()

	report := model.report()
	if err := model.player.Err(); err != nil {
		return report, err
	}
	return report, nil
}

// host is a player plus the decisions only a program owner can make: the
// alternate screen, the colour profile, and which keys are the application's.
type host struct {
	player *Player
	opts   HostOptions

	columns int
	lines   int
}

func newHost(anim *nvaa.Animation, opts Options, hostOpts HostOptions) *host {
	columns, lines := opts.Columns, opts.Lines
	if columns <= 0 {
		columns = 80
	}
	if lines <= 0 {
		lines = 24
	}

	h := &host{
		player:  New(anim, opts),
		opts:    hostOpts,
		columns: columns,
		lines:   lines,
	}
	h.apply()
	return h
}

// Init starts the player.
func (h *host) Init() tea.Cmd {
	cmd := h.player.Init()
	if h.opts.StartAtMS == nil {
		return cmd
	}

	// Start partway in. Init has to run first: the timeline only exists once
	// playback has begun, and seeking re-anchors the clock to wherever it landed.
	if seekCmd := h.player.SeekToTime(*h.opts.StartAtMS); seekCmd != nil {
		return seekCmd
	}
	return cmd
}

// Update gives the program's own keys first refusal, then passes the rest to the
// component and returns whatever it asked for.
func (h *host) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h.resize(msg.Width, msg.Height)
		return h, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return h, tea.Quit
		case "s":
			h.opts.ShowStats = !h.opts.ShowStats
			h.apply()
			return h, nil
		}
	}

	cmd, _ := h.player.Update(msg)
	return h, cmd
}

// View renders the player, with the meter row under it when the meter is on.
func (h *host) View() tea.View {
	content := h.player.View()
	if h.opts.ShowStats {
		content += "\n" + h.player.fillRow(h.statusText())
	}

	v := tea.NewView(content)
	v.AltScreen = h.opts.AlternateScreen
	v.WindowTitle = h.opts.Title
	return v
}

// resize adopts a terminal size. A degenerate one is ignored: terminals report
// 0x0 while resizing or tearing down, and adopting that would leave the program
// sized for nothing.
func (h *host) resize(width, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	h.columns, h.lines = width, height
	h.apply()
}

// apply gives the player whatever is left after the meter row.
func (h *host) apply() {
	lines := h.lines
	if h.opts.ShowStats && lines > 1 {
		lines--
	}
	if err := h.player.SetSize(h.columns, lines); err != nil {
		h.player.fail(err)
	}
}

// colorProfile decides what to tell the renderer it may emit.
//
// Asking the environment is what colorprofile.Detect does by default, and it
// answers "no colour" in two cases that both cost the picture: an ambient
// NO_COLOR, and any terminal whose $TERM it cannot classify. Since the format's
// colour is exact and load-bearing, the choice is made explicitly instead.
func (h *host) colorProfile() colorprofile.Profile {
	if h.opts.NoColor {
		return colorprofile.NoTTY
	}
	return colorprofile.TrueColor
}

// statusText is the single row of live playback state.
//
// It is laid out to fit 80 columns: the bar says roughly where we are, the
// timecode says exactly, and the byte counts the old line carried now belong to
// the report at exit, which has room for them. The key list lives in the usage
// text for the same reason.
func (h *host) statusText() string {
	position := h.player.Position()
	return fmt.Sprintf("  %s  %s %3.0f%%  %s  dirty %d/%d",
		position, Bar(position.Progress(), 10), position.Progress()*100,
		h.player.State(), h.player.meter.DirtyCells, h.player.meter.ScreenCells)
}

// report summarises the run.
func (h *host) report() string {
	report := h.player.meter.Report()

	// A file that does not say is not a file that is safe, so the absence is
	// reported rather than passed over in silence.
	if w := h.player.warning; w.Verdict == VerdictUnknown {
		report += "\nphotosensitivity  not assessed by the file (unknown, not safe)\n"
	} else {
		report += fmt.Sprintf("\nphotosensitivity  %s\n", w.Verdict)
	}
	return report
}
