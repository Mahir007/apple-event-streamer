package picker

import (
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Mahir007/apple-event-streamer/internal/catalog"
	"github.com/Mahir007/apple-event-streamer/internal/hls"
)

func liveCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	f, err := os.Open("../hls/testdata/live.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := hls.Parse(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	return catalog.New(m, false)
}

func key(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func TestKeyboardSkipsUnavailableRange(t *testing.T) {
	m := newModel(liveCatalog(t), Header{}, Preset{})
	s := m.top()
	if s.items[2].label != "HDR10" || !s.items[2].disabled {
		t.Fatalf("HDR10 should be listed but disabled: %+v", s.items[2])
	}
	m.Update(key(tea.KeyDown))
	m.Update(key(tea.KeyDown))
	if got := s.items[s.cursor].label; got != "Dolby Vision" {
		t.Fatalf("cursor on %q, want Dolby Vision", got)
	}
}

func TestFullFlowAndBack(t *testing.T) {
	m := newModel(liveCatalog(t), Header{}, Preset{})
	m.Update(key(tea.KeyDown)) // SDR [HEVC]
	m.Update(key(tea.KeyEnter))
	if m.top().kind != stepAudio {
		t.Fatalf("step = %v, want audio", m.top().kind)
	}
	m.Update(key(tea.KeyLeft))
	if m.top().kind != stepRange || m.rng != nil {
		t.Fatal("back did not return to the range step")
	}
	m.Update(key(tea.KeyEnter)) // range
	m.Update(key(tea.KeyEnter)) // audio: AAC
	if m.top().kind != stepResolution {
		t.Fatalf("without --subs resolution should follow audio, got %v", m.top().kind)
	}
	m.Update(key(tea.KeyEnter)) // resolution: 2160p
	_, cmd := m.Update(key(tea.KeyEnter))
	if cmd == nil || m.result == nil {
		t.Fatal("no result after the bitrate step")
	}
	if s := m.result.Stream; s.Range != catalog.SDRHEVC || s.Height != 2160 || m.result.Subs.Enabled {
		t.Fatalf("unexpected result %+v", s)
	}
}

func TestAskSubs(t *testing.T) {
	m := newModel(liveCatalog(t), Header{}, Preset{AskSubs: true})
	if got := len(m.steps()); got != 5 {
		t.Fatalf("%d steps, want 5 with --subs", got)
	}
	m.Update(key(tea.KeyEnter)) // range: SDR
	m.Update(key(tea.KeyEnter)) // audio: AAC
	s := m.top()
	if s.kind != stepSubs {
		t.Fatalf("subtitles should follow audio, got %v", s.kind)
	}
	if s.items[0].label != "All languages" || s.items[1].label == "None" {
		t.Fatalf("want All languages then languages, got %q, %q", s.items[0].label, s.items[1].label)
	}
	m.Update(key(tea.KeyDown))
	m.Update(key(tea.KeyDown))
	m.Update(key(tea.KeyDown)) // ar, de, en
	m.Update(key(tea.KeyEnter))
	m.Update(key(tea.KeyEnter)) // resolution
	m.Update(key(tea.KeyEnter)) // bitrate
	if m.result == nil || len(m.result.Subs.Langs) != 1 || m.result.Subs.Langs[0] != "en" {
		t.Fatalf("result subs = %+v", m.result)
	}
}

func TestNoSubsStepByDefault(t *testing.T) {
	m := newModel(liveCatalog(t), Header{}, Preset{})
	for _, k := range m.steps() {
		if k == stepSubs {
			t.Fatal("subtitles step shown without --subs")
		}
	}
}

func TestPresetsSkipSteps(t *testing.T) {
	r, a := catalog.DolbyVision, catalog.Atmos
	m := newModel(liveCatalog(t), Header{}, Preset{Range: &r, Audio: &a, Subs: &catalog.Subs{}})
	if m.top().kind != stepResolution {
		t.Fatalf("first step = %v, want resolution", m.top().kind)
	}
}
