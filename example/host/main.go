// Command host is an example of embedding the NVAA player in an application of
// your own.
//
// It exists because a component can only be judged by a host that does not think
// the way it does. The standalone player happens to give the animation the whole
// screen; here it is one pane among several, sized by a layout it knows nothing
// about, sharing a key map with controls that are not its own.
//
// Three things are worth copying from this file:
//
//   - The player is given a box and answers messages. It never touches the
//     terminal, never decides when to quit, and never asks how big it is: it is
//     told.
//   - Keys are offered to the host first. tab belongs to this program and never
//     reaches the animation; everything else falls through to the player and is
//     reported back as either handled or not.
//   - Every pane paints its own background, including the space between its
//     content and its edge. A renderer may erase with whatever colour its pen
//     holds, so a pane that leaves its edges to the terminal can be stained by
//     whichever pane was drawn last.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/WhatDamon/go-nvaa-codec"
	"github.com/WhatDamon/go-nvaa-codec/player"
	"github.com/WhatDamon/go-nvaa-codec/render"
)

const (
	usage = `host - an example application that embeds the NVAA player

usage:
  host [-w N] [-h N] FILE.nvaa

keys:
  tab           show or hide the file's details
  space         play or pause (and acknowledge a photosensitivity gate)
  n/. p/,       step one frame forwards or back
  ]/l [/h       jump to the next or previous keyframe
  r             restart
  q, ctrl+c     quit

The animation is drawn inside its own pane. The panes around it belong to this
program, which is the point of the example.`

	sidebarWidth = 32
)

// pane colours. Each pane carries a background of its own rather than leaning on
// whatever the terminal happens to be set to.
var (
	sideFG    = nvaa.RGB{R: 150, G: 155, B: 180}
	sideBG    = nvaa.RGB{R: 26, G: 28, B: 40}
	statusFG  = nvaa.RGB{R: 200, G: 200, B: 210}
	statusBG  = nvaa.RGB{R: 44, G: 46, B: 62}
	accentFG  = nvaa.RGB{R: 255, G: 220, B: 80}
	playerErr = errors.New("could not open the animation")
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "host: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("host", flag.ContinueOnError)
	columns := flags.Int("w", 100, "terminal width")
	lines := flags.Int("h", 30, "terminal height")
	flags.Usage = func() { fmt.Fprint(os.Stderr, usage) }

	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: host [-w N] [-h N] FILE.nvaa")
	}

	anim, err := nvaa.ReadFile(flags.Arg(0))
	if err != nil {
		return fmt.Errorf("%w: %w", playerErr, err)
	}

	model := newModel(anim, *columns, *lines)

	// WithWindowSize pins the layout so the panes can be reasoned about; on a
	// real terminal the program still resizes with the window.
	program := tea.NewProgram(model, tea.WithWindowSize(*columns, *lines))
	if _, err := program.Run(); err != nil {
		return err
	}
	return nil
}

// model is this application. It owns the screen and hands the animation one
// rectangle of it.
type model struct {
	anim   *nvaa.Animation
	player *player.Player

	columns int
	lines   int

	showSidebar bool
	sidebar     int
}

func newModel(anim *nvaa.Animation, columns, lines int) *model {
	m := &model{
		anim:        anim,
		columns:     columns,
		lines:       lines,
		showSidebar: true,
	}

	// The component is created with an arbitrary box and immediately corrected
	// by the layout. A host that sizes panes from a resize message does the same
	// thing one message later.
	m.player = player.New(anim, player.Options{
		Columns: columns,
		Lines:   lines,
		Loop:    false,
		Keys:    &player.DefaultKeys,
	})
	m.layout()
	return m
}

// Init starts the animation.
func (m *model) Init() tea.Cmd { return m.player.Init() }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width <= 0 || msg.Height <= 0 {
			// Terminals report 0x0 while resizing or tearing down.
			return m, nil
		}
		m.columns, m.lines = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "tab":
			// This key is the application's, so the player never sees it. It
			// works even while the photosensitivity gate is up, because the
			// player only claims the keys it acknowledges the gate with.
			m.showSidebar = !m.showSidebar
			m.layout()
			return m, nil
		}
	}

	// Everything else is offered to the animation, which reports back whether it
	// wanted it. The command belongs to this program to run.
	cmd, _ := m.player.Update(msg)
	return m, cmd
}

func (m *model) View() tea.View {
	// Reserve the last row for this program's own status line.
	rows := m.lines - 1
	if rows < 1 {
		rows = 1
	}

	animation := strings.Split(m.player.View(), "\n")
	details := m.sidebarRows(rows)

	var b strings.Builder
	for row := range rows {
		if m.sidebar > 0 {
			b.WriteString(details[row])
		}
		if row < len(animation) {
			b.WriteString(animation[row])
		}
		b.WriteByte('\n')
	}
	b.WriteString(m.statusRow())

	v := tea.NewView(b.String())
	v.AltScreen = true
	v.WindowTitle = "nvaa host demo"
	return v
}

// layout decides the split, then tells the player what it got. The component is
// never asked how big it would like to be.
func (m *model) layout() {
	m.sidebar = 0
	// Keep the animation usable: a sidebar that leaves no room for it is not a
	// layout worth honouring.
	if m.showSidebar && m.columns-sidebarWidth >= 40 {
		m.sidebar = sidebarWidth
	}

	rows := m.lines - 1
	if rows < 1 {
		rows = 1
	}
	if err := m.player.SetSize(m.columns-m.sidebar, rows); err != nil {
		// A failure here is a decode problem that the player now reports in its
		// own box, so this program carries on drawing its chrome around it.
		return
	}
}

// sidebarRows builds the detail pane, one painted row per line of the layout.
func (m *model) sidebarRows(count int) []string {
	fields := []struct{ name, value string }{
		{"canvas", fmt.Sprintf("%dx%d", m.anim.Width, m.anim.Height)},
		{"frames", strconv.Itoa(m.anim.FrameCount())},
		{"fps", fmt.Sprintf("%d/%d", m.anim.FPSNum, m.anim.FPSDen)},
		{"duration", fmt.Sprintf("%.1fs", float64(m.anim.TotalDuration())/1000)},
		{"palette", strconv.Itoa(len(m.anim.Palette))},
		{"glyphs", strconv.Itoa(len(m.anim.Glyphs))},
		{"styles", strconv.Itoa(len(m.anim.Styles))},
		{"size", fmt.Sprintf("%d kB", m.anim.Size/1024)},
		{"photosensitivity", verdictText(m.player.Verdict())},
	}

	lines := []string{"  nvaa", ""}
	for _, field := range fields {
		lines = append(lines, "  "+pad(field.name, 16)+field.value)
	}
	lines = append(lines, "", "  frame "+strconv.Itoa(m.player.Index()+1)+"/"+strconv.Itoa(m.player.Count()))
	lines = append(lines, "  "+m.player.Timecode()+" / "+player.Timecode(m.player.TotalMS()))
	lines = append(lines, "  "+player.Bar(m.player.Progress(), 26))
	lines = append(lines, "  "+m.player.State())

	rows := make([]string, 0, count)
	for row := range count {
		text := ""
		if row < len(lines) {
			text = lines[row]
		}
		fg := sideFG
		if row < 6 {
			fg = accentFG
		}
		rows = append(rows, paint(text, m.sidebar, fg, sideBG))
	}
	return rows
}

// verdictText names the file's own claim.
//
// The three states stay three: a file that did not assess itself is reported as
// unassessed rather than as safe, because this program cannot tell the two apart
// and neither should a viewer assume it can.
func verdictText(v player.Verdict) string {
	switch v {
	case player.VerdictPass:
		return "declared pass"
	case player.VerdictFail:
		return "declared FAIL"
	}
	return "not assessed"
}

// statusRow is this program's own chrome, across the full width.
func (m *model) statusRow() string {
	hint := "tab details   space play   n p step   [ ] keyframe   r restart   q quit"
	if m.player.AtWarning() {
		hint = "space acknowledges the warning   q quit"
	}
	return paint("  "+hint, m.columns, statusFG, statusBG)
}

// paint renders one row of a pane at its full width, so the pane is a rectangle
// that leaves the terminal nothing to fill in.
func paint(text string, columns int, fg, bg nvaa.RGB) string {
	text = clamp(text, columns)

	var b strings.Builder
	b.WriteString(selector(38, fg))
	b.WriteString(selector(48, bg))
	b.WriteString(text)
	for pad := columns - cells(text); pad > 0; pad-- {
		b.WriteByte(' ')
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// selector writes one 24-bit colour selection.
func selector(layer int, c nvaa.RGB) string {
	return "\x1b[" + strconv.Itoa(layer) + ";2;" +
		strconv.Itoa(int(c.R)) + ";" + strconv.Itoa(int(c.G)) + ";" + strconv.Itoa(int(c.B)) + "m"
}

// cells counts the columns a string occupies.
func cells(s string) int {
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

// clamp truncates to at most limit columns without splitting a wide glyph.
func clamp(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	used := 0
	for index, r := range s {
		span := 1
		if render.IsWideGlyph(string(r)) {
			span = 2
		}
		if used+span > limit {
			return s[:index]
		}
		used += span
	}
	return s
}

// pad right-pads a short string so a column of values lines up.
func pad(s string, to int) string {
	if len(s) >= to {
		return s
	}
	return s + strings.Repeat(" ", to-len(s))
}
