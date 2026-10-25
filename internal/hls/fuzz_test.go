package hls

import (
	"bytes"
	"net/url"
	"os"
	"testing"
)

// FuzzParse checks that Parse never panics and that anything it accepts is
// written back as a playlist that parses to the same text again.
func FuzzParse(f *testing.F) {
	for _, name := range []string{"live.m3u8", "vod.m3u8"} {
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte("#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nv.m3u8\n"))
	f.Add([]byte("\ufeff#EXTM3U\r\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"a\",URI=\"a b\"\r\n#EXT-X-STREAM-INF:AUDIO=\"a\",X= \"q\"\r\nv?x=1\r\n"))
	base, _ := url.Parse("https://example.com/event/master.m3u8")

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, b := range []*url.URL{nil, base} {
			m, err := Parse(bytes.NewReader(data), b)
			if err != nil {
				continue
			}
			var first bytes.Buffer
			if err := m.Encode(&first, "header\r\nline"); err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(first.Bytes(), []byte("\n## header  line\n")) {
				t.Fatalf("header line broken up:\n%s", first.Bytes())
			}
			m2, err := Parse(bytes.NewReader(first.Bytes()), nil)
			if err != nil {
				t.Fatalf("output does not parse: %v\n%s", err, first.Bytes())
			}
			if len(m2.Variants) != len(m.Variants) || len(m2.Media) != len(m.Media) {
				t.Fatalf("round trip changed counts: %d/%d variants, %d/%d media\n%s",
					len(m2.Variants), len(m.Variants), len(m2.Media), len(m.Media), first.Bytes())
			}
			var second bytes.Buffer
			m2.Encode(&second, "header\r\nline")
			if !bytes.Equal(first.Bytes(), second.Bytes()) {
				t.Fatalf("output is not stable:\n%s\n---\n%s", first.Bytes(), second.Bytes())
			}
		}
	})
}

// FuzzParseAttrs checks that attribute lists survive a round trip.
func FuzzParseAttrs(f *testing.F) {
	f.Add(`BANDWIDTH=3625094,CODECS="hvc1.2.4.H120.B0,mp4a.40.2",RESOLUTION=1280x720`)
	f.Add(`A="unterminated`)
	f.Add(`=,=,"=",A= "x" ,B`)
	f.Fuzz(func(t *testing.T, s string) {
		once := ParseAttrs(s).String()
		if twice := ParseAttrs(once).String(); twice != once {
			t.Fatalf("not stable: %q -> %q -> %q", s, once, twice)
		}
	})
}
