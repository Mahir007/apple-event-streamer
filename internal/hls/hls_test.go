package hls

import (
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestParseAttrs(t *testing.T) {
	l := ParseAttrs(`BANDWIDTH=3625094,CODECS="hvc1.2.4.H120.B0,mp4a.40.2",RESOLUTION=1280x720,FRAME-RATE=29.97,AUDIO="a-p"`)
	if got := l.Get("CODECS"); got != "hvc1.2.4.H120.B0,mp4a.40.2" {
		t.Errorf("CODECS = %q", got)
	}
	if got := l.Get("FRAME-RATE"); got != "29.97" {
		t.Errorf("FRAME-RATE = %q", got)
	}
	if got := l.String(); got != `BANDWIDTH=3625094,CODECS="hvc1.2.4.H120.B0,mp4a.40.2",RESOLUTION=1280x720,FRAME-RATE=29.97,AUDIO="a-p"` {
		t.Errorf("round trip = %q", got)
	}
	if l.Without("AUDIO").Has("AUDIO") {
		t.Error("Without kept AUDIO")
	}
}

func parseFile(t *testing.T, name string) *Master {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := Parse(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParseFixtures(t *testing.T) {
	for _, tc := range []struct {
		file             string
		variants, audios int
	}{
		{"live.m3u8", 184, 32},
		{"vod.m3u8", 68, 4},
	} {
		m := parseFile(t, tc.file)
		if len(m.Variants) != tc.variants {
			t.Errorf("%s: %d variants, want %d", tc.file, len(m.Variants), tc.variants)
		}
		n := 0
		for _, md := range m.Media {
			if md.Type() == "AUDIO" {
				n++
			}
		}
		if n != tc.audios {
			t.Errorf("%s: %d audio renditions, want %d", tc.file, n, tc.audios)
		}
		for _, v := range m.Variants {
			if !strings.HasPrefix(v.URI, "https://") {
				t.Fatalf("%s: bad variant URI %q", tc.file, v.URI)
			}
		}
	}
}

func TestParseResolvesRelativeAndSkipsJunk(t *testing.T) {
	in := "PS> curl ...\r\n#EXTM3U\r\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"a\",URI=\"audio/a.m3u8\"\r\n#EXT-X-STREAM-INF:BANDWIDTH=1,AUDIO=\"a\"\r\nvideo/v.m3u8\r\n"
	base, _ := url.Parse("https://example.com/event/master.m3u8")
	m, err := Parse(strings.NewReader(in), base)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Variants[0].URI; got != "https://example.com/event/video/v.m3u8" {
		t.Errorf("variant URI = %q", got)
	}
	if got := m.Media[0].URI(); got != "https://example.com/event/audio/a.m3u8" {
		t.Errorf("media URI = %q", got)
	}
}

func TestParseRejectsMediaPlaylist(t *testing.T) {
	_, err := Parse(strings.NewReader("#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\nseg.ts\n"), nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
