package picker

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Mahir007/apple-event-streamer/internal/catalog"
	"github.com/Mahir007/apple-event-streamer/internal/hls"
)

// keys are the presses the fuzzer sends, with the string the model sees.
var keys = []struct {
	msg  tea.KeyPressMsg
	want string
}{
	{tea.KeyPressMsg{Code: tea.KeyUp}, "up"},
	{tea.KeyPressMsg{Code: tea.KeyDown}, "down"},
	{tea.KeyPressMsg{Code: tea.KeyLeft}, "left"},
	{tea.KeyPressMsg{Code: tea.KeyRight}, "right"},
	{tea.KeyPressMsg{Code: tea.KeyEnter}, "enter"},
	{tea.KeyPressMsg{Code: tea.KeyEscape}, "esc"},
	{tea.KeyPressMsg{Code: tea.KeyHome}, "home"},
	{tea.KeyPressMsg{Code: tea.KeyEnd}, "end"},
	{tea.KeyPressMsg{Code: tea.KeyPgUp}, "pgup"},
	{tea.KeyPressMsg{Code: tea.KeyPgDown}, "pgdown"},
	{tea.KeyPressMsg{Code: tea.KeyTab}, "tab"},
	{tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, "shift+tab"},
	{tea.KeyPressMsg{Code: tea.KeyBackspace}, "backspace"},
	{tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, "space"},
	{tea.KeyPressMsg{Code: '1', Text: "1"}, "1"},
	{tea.KeyPressMsg{Code: '2', Text: "2"}, "2"},
	{tea.KeyPressMsg{Code: '5', Text: "5"}, "5"},
	{tea.KeyPressMsg{Code: '9', Text: "9"}, "9"},
	{tea.KeyPressMsg{Code: 'j', Text: "j"}, "j"},
	{tea.KeyPressMsg{Code: 'k', Text: "k"}, "k"},
	{tea.KeyPressMsg{Code: 'g', Text: "g"}, "g"},
	{tea.KeyPressMsg{Code: 'g', Text: "G", Mod: tea.ModShift}, "G"},
}

func TestFuzzKeysAreRecognised(t *testing.T) {
	for _, k := range keys {
		if got := k.msg.String(); got != k.want {
			t.Errorf("%+v is %q, want %q", k.msg, got, k.want)
		}
	}
}

func fuzzCatalogs(f *testing.F) []*catalog.Catalog {
	var out []*catalog.Catalog
	for _, name := range []string{"live.m3u8", "vod.m3u8"} {
		r, err := os.Open("../hls/testdata/" + name)
		if err != nil {
			f.Fatal(err)
		}
		m, err := hls.Parse(r, nil)
		r.Close()
		if err != nil {
			f.Fatal(err)
		}
		out = append(out, catalog.New(m, false), catalog.New(m, true))
	}
	return out
}

// FuzzPicker drives the model with arbitrary keys, clicks, wheel turns,
// motion and resizes, checking after every event that the cursor is valid,
// the view fits the terminal, clicks land on the row drawn there, and any
// choice is a real stream matching the answers given.
func FuzzPicker(f *testing.F) {
	cats := fuzzCatalogs(f)
	f.Add([]byte{0, 0, 1, 80, 24, 0, 4, 0, 4, 0, 4, 0, 4})
	f.Add([]byte{3, 7, 1, 20, 3, 0, 1, 0, 9, 2, 3, 9, 0, 4})
	f.Add([]byte{1, 0, 1, 1, 1, 0, 8, 0, 9, 3, 0, 5, 5, 0, 4})

	f.Fuzz(func(t *testing.T, data []byte) {
		next := func() int {
			if len(data) == 0 {
				return 0
			}
			b := data[0]
			data = data[1:]
			return int(b)
		}
		c := cats[next()%len(cats)]
		var p Preset
		flags := next()
		if flags&1 != 0 {
			r := catalog.Ranges[next()%len(catalog.Ranges)]
			p.Range = &r
		}
		if flags&2 != 0 {
			a := c.Audios()[next()%len(c.Audios())]
			p.Audio = &a
		}
		if flags&4 != 0 {
			p.AskSubs = true
		}
		if flags&8 != 0 {
			p.MaxHeight = []int{360, 720, 1080, 2160}[next()%4]
		}
		if len(c.Select(catalog.Filter{Range: p.Range, Audio: p.Audio, MaxHeight: p.MaxHeight})) == 0 {
			return // main rejects these before showing the picker
		}

		m := newModel(c, Header{Badge: "VOD", Description: "x\x1b[2Jy"}, p)
		for len(data) > 0 {
			var cmd tea.Cmd
			switch next() % 6 {
			case 0:
				_, cmd = m.Update(keys[next()%len(keys)].msg)
			case 1:
				m.Update(tea.WindowSizeMsg{Width: next() % 160, Height: next() % 60})
			case 2:
				_, cmd = m.Update(tea.MouseClickMsg{X: next() % 100, Y: next() % 60, Button: []tea.MouseButton{tea.MouseLeft, tea.MouseRight}[next()%2]})
			case 3:
				m.Update(tea.MouseWheelMsg{Button: []tea.MouseButton{tea.MouseWheelUp, tea.MouseWheelDown}[next()%2]})
			case 4:
				m.Update(tea.MouseMotionMsg{X: next() % 100, Y: next() % 60})
			case 5:
				checkClickTargets(t, m)
			}
			checkModel(t, m, p)
			if cmd != nil {
				return // tea.Quit: the program ends here
			}
		}
	})
}

func checkModel(t *testing.T, m *model, p Preset) {
	t.Helper()
	if len(m.stack) == 0 {
		t.Fatal("empty step stack")
	}
	s := m.top()
	enabled := false
	for _, it := range s.items {
		enabled = enabled || !it.disabled
	}
	switch {
	case !enabled && s.cursor != -1:
		t.Fatalf("cursor %d with no enabled items", s.cursor)
	case enabled && (s.cursor < 0 || s.cursor >= len(s.items) || s.items[s.cursor].disabled):
		t.Fatalf("cursor %d is not on an enabled item of %d", s.cursor, len(s.items))
	}

	lines := strings.Split(m.View().Content, "\n")
	if m.height >= 2 && len(lines) > m.height {
		t.Fatalf("%d lines in a %d-line terminal", len(lines), m.height)
	}
	for _, l := range lines {
		if m.width > 0 && ansi.StringWidth(l) > m.width {
			t.Fatalf("line %q is wider than %d", ansi.Strip(l), m.width)
		}
		if strings.ContainsRune(ansi.Strip(l), '\x1b') {
			t.Fatalf("raw escape in %q", l)
		}
	}

	if r := m.result; r != nil {
		st := r.Stream
		found := false
		for _, cs := range m.cat.Streams {
			found = found || cs == st
		}
		switch {
		case !found:
			t.Fatal("chosen stream is not in the catalog")
		case p.Range != nil && st.Range != *p.Range, m.rng != nil && st.Range != *m.rng:
			t.Fatalf("chose %v, range answer was %v", st.Range, m.rng)
		case p.Audio != nil && st.Audio != *p.Audio, m.audio != nil && st.Audio != *m.audio:
			t.Fatalf("chose %v, audio answer was %v", st.Audio, m.audio)
		case m.res != nil && st.Height != *m.res:
			t.Fatalf("chose %dp, resolution answer was %d", st.Height, *m.res)
		case p.MaxHeight > 0 && st.Height > p.MaxHeight:
			t.Fatalf("chose %dp above --max-res %d", st.Height, p.MaxHeight)
		}
	}
}

// checkClickTargets verifies every item hitbox sits on the row showing that
// item, so a click selects what the user sees under the pointer.
func checkClickTargets(t *testing.T, m *model) {
	t.Helper()
	lines, hits := m.layout()
	s := m.top()
	for _, h := range hits {
		if h.item < 0 {
			continue
		}
		if h.y >= len(lines) || !strings.Contains(ansi.Strip(lines[h.y]), s.items[h.item].label) {
			t.Fatalf("hitbox for %q is on row %d, which shows something else", s.items[h.item].label, h.y)
		}
	}
}
