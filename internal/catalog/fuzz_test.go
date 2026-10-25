package catalog

import (
	"bytes"
	"os"
	"testing"

	"github.com/Mahir007/apple-event-streamer/internal/hls"
)

// FuzzCatalog feeds arbitrary playlists through classification and
// building. Every playlist the app writes must be one it reads back as the
// same single stream.
func FuzzCatalog(f *testing.F) {
	for _, name := range []string{"live.m3u8", "vod.m3u8"} {
		b, err := os.ReadFile("../hls/testdata/" + name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b, false)
		f.Add(b, true)
	}
	f.Add([]byte(`#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="a",NAME="English",LANGUAGE="en",CHANNELS="16/JOC",URI="a.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="a",NAME="AD",LANGUAGE="en",CHARACTERISTICS="public.accessibility.describes-video",URI="ad.m3u8"
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="s",NAME="",LANGUAGE="",URI="s.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=1,CODECS="dvh1.08.07,ec-3",RESOLUTION=-1x-1,AUDIO="a",SUBTITLES="s",VIDEO-RANGE=PQ
v.m3u8
`), false)

	f.Fuzz(func(t *testing.T, data []byte, ad bool) {
		m, err := hls.Parse(bytes.NewReader(data), nil)
		if err != nil {
			return
		}
		c := New(m, ad)
		if len(c.Streams)+c.Skipped > len(m.Variants) {
			t.Fatalf("%d streams + %d skipped > %d variants", len(c.Streams), c.Skipped, len(m.Variants))
		}
		c.AvailableRanges()
		c.Audios()
		c.Heights(Filter{})
		subs := c.Subtitles(Filter{})
		choices := []Subs{{}, {Enabled: true}}
		for _, md := range subs {
			choices = append(choices, Subs{Enabled: true, Langs: []string{md.Language()}})
		}
		for _, s := range c.Streams {
			Describe(s, ad)
			for _, sub := range choices {
				out, err := c.Build(s, sub)
				if err != nil {
					continue
				}
				var buf bytes.Buffer
				if err := out.Encode(&buf, "x"); err != nil {
					t.Fatal(err)
				}
				back, err := hls.Parse(&buf, nil)
				if err != nil {
					t.Fatalf("built playlist does not parse: %v\n%s", err, buf.Bytes())
				}
				again := New(back, ad)
				if len(again.Streams) != 1 || again.Streams[0].Variant.URI != s.Variant.URI {
					t.Fatalf("built playlist is not the chosen stream (%d streams)\n%s", len(again.Streams), buf.Bytes())
				}
			}
		}
	})
}
