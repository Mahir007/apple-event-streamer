// Package picker is the interactive, keyboard- and mouse-driven stream
// chooser: video range › audio › subtitles › resolution › bitrate.
package picker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Mahir007/apple-event-streamer/internal/catalog"
	"github.com/Mahir007/apple-event-streamer/internal/sanitize"
)

// ErrCancelled is returned when the user quits without choosing.
var ErrCancelled = errors.New("cancelled")

// PanicError is returned when the picker panicked. Bubble Tea has already
// restored the terminal; Value is the panic value and Trace is its report
// with the stack, for the caller to show or not.
type PanicError struct{ Value, Trace string }

func (e *PanicError) Error() string { return "picker panicked: " + e.Value }

// Header is shown above the menus.
type Header struct {
	Badge       string // e.g. "LIVE" or "VOD"
	Live        bool
	Description string
}

// Preset holds answers supplied on the command line; their steps are skipped.
type Preset struct {
	Range *catalog.Range
	Audio *catalog.Audio
	Subs  *catalog.Subs
	// AskSubs shows the subtitles step (--subs). Without it, and without a
	// Subs answer, no subtitles are included.
	AskSubs   bool
	MaxHeight int
}

// Result is the user's choice.
type Result struct {
	Stream *catalog.Stream
	Subs   catalog.Subs
}

// Run shows the picker on stderr and returns the selection.
func Run(c *catalog.Catalog, h Header, p Preset) (*Result, error) {
	m := newModel(c, h, p)
	prog := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	restore := captureStderr()
	final, err := prog.Run()
	captured := restore()
	if errors.Is(err, tea.ErrProgramPanic) {
		return nil, &PanicError{Value: panicValue(captured), Trace: captured}
	}
	os.Stderr.WriteString(captured) // nothing else is expected, but don't lose it
	if errors.Is(err, tea.ErrInterrupted) {
		return nil, ErrCancelled
	}
	if err != nil {
		return nil, err
	}
	fm := final.(*model)
	if fm.result == nil {
		return nil, ErrCancelled
	}
	return fm.result, nil
}

// captureStderr points os.Stderr at a pipe until the returned function is
// called, which restores it and returns what was written. Bubble Tea writes
// its panic report and stack there; the picker itself draws through the
// *os.File given to WithOutput, which this doesn't touch.
func captureStderr() (restore func() string) {
	r, w, err := os.Pipe()
	if err != nil {
		return func() string { return "" }
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		r.Close()
		done <- string(b)
	}()
	return func() string {
		os.Stderr = orig
		w.Close()
		return <-done
	}
}

// panicValue extracts the panic value from Bubble Tea's report:
// "Caught panic:\r\n\r\n<value>\r\n\r\nRestoring terminal...".
func panicValue(report string) string {
	_, v, ok := strings.Cut(report, "Caught panic:\r\n\r\n")
	if v, _, ok2 := strings.Cut(v, "\r\n\r\nRestoring terminal"); ok && ok2 {
		return strings.ReplaceAll(v, "\r\n", "\n")
	}
	return "unknown panic in the picker"
}

type stepKind int

const (
	stepRange stepKind = iota
	stepAudio
	stepSubs
	stepResolution
	stepBitrate
)

var stepTitles = map[stepKind]string{
	stepRange:      "Video",
	stepAudio:      "Audio",
	stepSubs:       "Subtitles",
	stepResolution: "Resolution",
	stepBitrate:    "Bitrate",
}

type item struct {
	label, detail string
	disabled      bool
	value         any
}

type step struct {
	kind   stepKind
	items  []item
	cursor int
	offset int
}

type model struct {
	cat    *catalog.Catalog
	header Header
	preset Preset

	stack []*step
	rng   *catalog.Range
	audio *catalog.Audio
	subs  *catalog.Subs
	res   *int

	result        *Result
	width, height int
}

func newModel(c *catalog.Catalog, h Header, p Preset) *model {
	h.Description = sanitize.Text(h.Description)
	m := &model{cat: c, header: h, preset: p, rng: p.Range, audio: p.Audio, subs: p.Subs}
	m.advance(-1)
	return m
}

func (m *model) filter() catalog.Filter {
	return catalog.Filter{Range: m.rng, Audio: m.audio, Height: m.res, MaxHeight: m.preset.MaxHeight}
}

func (m *model) available(f catalog.Filter) bool { return len(m.cat.Select(f)) > 0 }

// skipped reports whether kind is answered by a preset.
func (m *model) skipped(k stepKind) bool {
	switch k {
	case stepRange:
		return m.preset.Range != nil
	case stepAudio:
		return m.preset.Audio != nil
	case stepSubs:
		return m.preset.Subs != nil || !m.preset.AskSubs
	}
	return false
}

// steps lists the steps the user will actually see, for the "n/total" hint.
func (m *model) steps() []stepKind {
	var out []stepKind
	for k := stepRange; k <= stepBitrate; k++ {
		if !m.skipped(k) {
			out = append(out, k)
		}
	}
	return out
}

// advance pushes the first unanswered step after from.
func (m *model) advance(from stepKind) {
	for k := from + 1; k <= stepBitrate; k++ {
		if m.skipped(k) {
			continue
		}
		s := m.build(k)
		if s == nil {
			continue // nothing to ask (e.g. no subtitles in this playlist)
		}
		m.stack = append(m.stack, s)
		return
	}
}

func (m *model) build(k stepKind) *step {
	s := &step{kind: k}
	f := m.filter()
	switch k {
	case stepRange:
		for _, r := range m.cat.AvailableRanges() {
			f.Range = &r
			s.items = append(s.items, item{label: r.String(), detail: r.Detail(), disabled: !m.available(f), value: r})
		}
	case stepAudio:
		for _, a := range m.cat.Audios() {
			f.Audio = &a
			label := a.String()
			if m.cat.AD {
				label += " · Audio Description"
			}
			s.items = append(s.items, item{label: label, detail: m.audioDetail(a), disabled: !m.available(f), value: a})
		}
	case stepSubs:
		subs := m.cat.Subtitles(f)
		if len(subs) == 0 {
			off := catalog.Subs{}
			m.subs = &off
			return nil
		}
		s.items = append(s.items, item{label: "All languages", detail: fmt.Sprintf("%d tracks", len(subs)), value: catalog.Subs{Enabled: true}})
		for _, md := range subs {
			label, lang := md.Name(), md.Language()
			if label == "" {
				label = "Unnamed track"
			}
			if lang == "" {
				lang = md.Name() // Subs.Matches also matches by name
			}
			s.items = append(s.items, item{label: label, detail: md.Language(), value: catalog.Subs{Enabled: true, Langs: []string{lang}}})
		}
	case stepResolution:
		for _, h := range m.cat.Heights(f) {
			f.Height = &h
			detail := ""
			if st := m.cat.Select(f); len(st) > 0 {
				detail = fmt.Sprintf("%d×%d", st[0].Width, st[0].Height)
			}
			s.items = append(s.items, item{label: catalog.ResolutionLabel(h), detail: detail, value: h})
		}
	case stepBitrate:
		for _, st := range m.cat.Select(f) {
			detail := fmt.Sprintf("avg %s · %s", catalog.Mbps(st.AverageBandwidth()), st.VideoCodec)
			if fr := st.Variant.FrameRate(); fr != "" {
				detail += " · " + strings.TrimRight(strings.TrimRight(fr, "0"), ".") + " fps"
			}
			s.items = append(s.items, item{label: catalog.Mbps(st.Bandwidth()), detail: detail, value: st})
		}
	}
	for i := range s.items {
		s.items[i].label = sanitize.Text(s.items[i].label)
		s.items[i].detail = sanitize.Text(s.items[i].detail)
	}
	s.cursor = -1
	s.move(1)
	return s
}

func (m *model) audioDetail(a catalog.Audio) string {
	for _, st := range m.cat.Streams {
		if st.Audio != a {
			continue
		}
		for _, r := range m.cat.AudioRenditions(st.Variant) {
			switch ch := r.Channels(); {
			case strings.Contains(ch, "JOC"):
				return "spatial · " + ch
			case ch == "2":
				return "stereo"
			case ch != "":
				return ch + " ch"
			}
		}
	}
	return ""
}

// move moves the cursor by delta over enabled items, wrapping around.
func (s *step) move(delta int) {
	n := len(s.items)
	for i, c := 0, s.cursor; i < n; i++ {
		c = ((c+delta)%n + n) % n
		if !s.items[c].disabled {
			s.cursor = c
			return
		}
	}
}

// jump moves to the first enabled item at or after/before index i.
func (s *step) jump(i, dir int) {
	for ; i >= 0 && i < len(s.items); i += dir {
		if !s.items[i].disabled {
			s.cursor = i
			return
		}
	}
}

func (m *model) top() *step { return m.stack[len(m.stack)-1] }

func (m *model) choose() tea.Cmd {
	s := m.top()
	if s.cursor < 0 || s.items[s.cursor].disabled {
		return nil
	}
	switch v := s.items[s.cursor].value.(type) {
	case catalog.Range:
		m.rng = &v
	case catalog.Audio:
		m.audio = &v
	case catalog.Subs:
		m.subs = &v
	case int:
		m.res = &v
	case *catalog.Stream:
		subs := catalog.Subs{}
		if m.subs != nil {
			subs = *m.subs
		}
		m.result = &Result{Stream: v, Subs: subs}
		return tea.Quit
	}
	m.advance(s.kind)
	return nil
}

func (m *model) back() tea.Cmd {
	if len(m.stack) == 1 {
		return nil
	}
	m.stack = m.stack[:len(m.stack)-1]
	// Forget the answer given on the step we are returning to, and any
	// auto-answer made after it.
	switch m.top().kind {
	case stepRange:
		m.rng, m.audio, m.res = m.preset.Range, m.preset.Audio, nil
		m.subs = m.preset.Subs
	case stepAudio:
		m.audio, m.res, m.subs = nil, nil, m.preset.Subs
	case stepSubs:
		m.subs, m.res = nil, nil
	case stepResolution:
		m.res = nil
	}
	return nil
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	s := m.top()
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		switch key := msg.String(); key {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up", "k", "shift+tab":
			s.move(-1)
		case "down", "j", "tab":
			s.move(1)
		case "home", "g":
			s.jump(0, 1)
		case "end", "G", "shift+g":
			s.jump(len(s.items)-1, -1)
		case "pgup":
			_, vis := m.fit()
			s.jump(max(s.cursor-vis, 0), 1)
		case "pgdown":
			_, vis := m.fit()
			s.jump(min(s.cursor+vis, len(s.items)-1), -1)
		case "enter", "space", "right", "l":
			return m, m.choose()
		case "esc":
			if len(m.stack) == 1 {
				return m, tea.Quit
			}
			return m, m.back()
		case "left", "h", "backspace":
			return m, m.back()
		default:
			if len(key) == 1 && key >= "1" && key <= "9" {
				if i := int(key[0] - '1'); i < len(s.items) && !s.items[i].disabled {
					s.cursor = i
					return m, m.choose()
				}
			}
		}
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			s.move(-1)
		case tea.MouseWheelDown:
			s.move(1)
		}
	case tea.MouseMotionMsg:
		if t, ok := m.hitTest(msg.X, msg.Y); ok && t.item >= 0 && !s.items[t.item].disabled {
			s.cursor = t.item
		}
	case tea.MouseClickMsg:
		switch msg.Button {
		case tea.MouseRight:
			return m, m.back()
		case tea.MouseLeft:
			t, ok := m.hitTest(msg.X, msg.Y)
			switch {
			case !ok:
			case t.item >= 0 && !s.items[t.item].disabled:
				s.cursor = t.item
				return m, m.choose()
			case t.button == "back":
				return m, m.back()
			case t.button == "quit":
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

// ---- rendering ----

var (
	accent      = lipgloss.Color("#2997FF")
	titleStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	offStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("239"))
	cursorStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	crumbStyle  = lipgloss.NewStyle().Foreground(accent)
	stepStyle   = lipgloss.NewStyle().Bold(true).Underline(true)
	buttonStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("236")).Padding(0, 1)
	liveBadge   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#E30000")).Padding(0, 1)
	vodBadge    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(lipgloss.Color("#0071E3")).Padding(0, 1)
	cursorGlyph = "❯"
	separator   = " › "
	backLabel   = "‹ Back"
	quitLabel   = "Quit"
)

func init() {
	// The legacy Windows console's default fonts lack these glyphs.
	if runtime.GOOS == "windows" && os.Getenv("WT_SESSION") == "" {
		cursorGlyph, separator, backLabel = ">", " > ", "< Back"
	}
}

type target struct {
	item   int
	button string
}

type hitbox struct {
	y, x0, x1 int
	target
}

// Optional lines, dropped in this order when the terminal is too short to
// show them and a few items.
const (
	dropFooterGap   = iota + 1 // blank line above the help
	dropHelp                   // key and mouse help
	dropDescription            // event description and the blank after it
	dropCrumbGap               // blank line after the breadcrumbs
	dropButtons                // Back and Quit buttons
	dropTitle                  // app title and badge
	dropCrumbs                 // breadcrumbs
	dropHints                  // "more" scroll hints
)

// fit returns how many optional lines to drop and how many items to show
// so the view fits the terminal height.
func (m *model) fit() (drop, vis int) {
	n := len(m.top().items)
	if m.height <= 0 {
		return 0, n
	}
	for want := min(n, 3); drop < dropHints && chrome(drop)+want > m.height; {
		drop++
	}
	return drop, max(min(n, m.height-chrome(drop)), 1)
}

// chrome counts the lines other than items left after dropping drop parts.
func chrome(drop int) int {
	n := 1 // step title
	for _, part := range []struct{ level, lines int }{
		{dropTitle, 1}, {dropDescription, 2}, {dropCrumbs, 1}, {dropCrumbGap, 1},
		{dropHints, 2}, {dropFooterGap, 1}, {dropHelp, 1}, {dropButtons, 1},
	} {
		if drop < part.level {
			n += part.lines
		}
	}
	return n
}

func (m *model) layout() ([]string, []hitbox) {
	s := m.top()
	drop, vis := m.fit()
	var lines []string
	var hits []hitbox

	if drop < dropTitle {
		badge := vodBadge.Render(m.header.Badge)
		if m.header.Live {
			badge = liveBadge.Render(m.header.Badge)
		}
		lines = append(lines, " "+titleStyle.Render("Apple Event Streamer")+"  "+badge)
	}
	if drop < dropDescription {
		desc := m.header.Description
		if m.width > 4 {
			desc = ansi.Truncate(desc, m.width-2, "…")
		}
		lines = append(lines, " "+dimStyle.Render(desc), "")
	}

	var crumbs []string
	if m.rng != nil {
		crumbs = append(crumbs, m.rng.String())
	}
	if m.audio != nil {
		crumbs = append(crumbs, m.audio.String())
	}
	if m.subs != nil && s.kind > stepSubs {
		crumbs = append(crumbs, m.subs.String())
	}
	if m.res != nil {
		crumbs = append(crumbs, catalog.ResolutionLabel(*m.res))
	}
	switch {
	case drop >= dropCrumbs:
	case len(crumbs) == 0:
		lines = append(lines, " "+dimStyle.Render("Choose a stream"))
	default:
		lines = append(lines, " "+crumbStyle.Render(strings.Join(crumbs, separator)))
	}
	if drop < dropCrumbGap {
		lines = append(lines, "")
	}

	steps := m.steps()
	pos := 0
	for i, k := range steps {
		if k == s.kind {
			pos = i + 1
		}
	}
	lines = append(lines, " "+stepStyle.Render(stepTitles[s.kind])+dimStyle.Render(fmt.Sprintf("  %d/%d", pos, len(steps))))

	// Keep the cursor inside the scroll window.
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if s.cursor >= s.offset+vis {
		s.offset = s.cursor - vis + 1
	}
	s.offset = max(min(s.offset, len(s.items)-vis), 0)

	labelW := 0
	for _, it := range s.items {
		labelW = max(labelW, lipgloss.Width(it.label))
	}

	if drop < dropHints {
		lines = append(lines, scrollHint(s.offset > 0, "▲"))
	}
	for i := s.offset; i < s.offset+vis && i < len(s.items); i++ {
		it := s.items[i]
		pad := strings.Repeat(" ", labelW-lipgloss.Width(it.label)+3)
		var row string
		switch {
		case it.disabled:
			detail := "not available"
			row = "   " + offStyle.Render(it.label+pad+detail)
		case i == s.cursor:
			row = " " + cursorStyle.Render(cursorGlyph+" "+it.label) + pad + dimStyle.Render(it.detail)
		default:
			row = "   " + it.label + pad + dimStyle.Render(it.detail)
		}
		hits = append(hits, hitbox{y: len(lines), x0: 0, x1: 1 << 30, target: target{item: i}})
		lines = append(lines, row)
	}
	if drop < dropHints {
		lines = append(lines, scrollHint(s.offset+vis < len(s.items), "▼"))
	}
	if drop < dropFooterGap {
		lines = append(lines, "")
	}
	if drop < dropHelp {
		lines = append(lines, " "+dimStyle.Render("↑/↓ move · enter select · ← back · q quit · mouse: click, wheel, right-click back"))
	}
	if drop >= dropButtons {
		return lines, hits
	}
	back, quit := buttonStyle.Render(backLabel), buttonStyle.Render(quitLabel)
	if len(m.stack) == 1 {
		back = offStyle.Padding(0, 1).Render(backLabel)
	}
	y := len(lines)
	bx := 1
	hits = append(hits, hitbox{y: y, x0: bx, x1: bx + lipgloss.Width(back), target: target{item: -1, button: "back"}})
	qx := bx + lipgloss.Width(back) + 2
	hits = append(hits, hitbox{y: y, x0: qx, x1: qx + lipgloss.Width(quit), target: target{item: -1, button: "quit"}})
	lines = append(lines, " "+back+"  "+quit)
	return lines, hits
}

func scrollHint(show bool, glyph string) string {
	if !show {
		return ""
	}
	return "   " + dimStyle.Render(glyph+" more")
}

func (m *model) hitTest(x, y int) (target, bool) {
	_, hits := m.layout()
	for _, h := range hits {
		if h.y == y && x >= h.x0 && x < h.x1 {
			return h.target, true
		}
	}
	return target{}, false
}

func (m *model) View() tea.View {
	lines, _ := m.layout()
	if m.width > 0 {
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, m.width, "")
		}
	}
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	v.WindowTitle = "Apple Event Streamer"
	return v
}
