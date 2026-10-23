package catalog

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/Mahir007/apple-event-streamer/internal/hls"
)

func load(t *testing.T, name string, ad bool) *Catalog {
	t.Helper()
	f, err := os.Open("../hls/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := hls.Parse(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	return New(m, ad)
}

func ptr[T any](v T) *T { return &v }

func TestLivePrefersPrimaryEndpoint(t *testing.T) {
	c := load(t, "live.m3u8", false)
	if len(c.Streams) != 46 {
		t.Fatalf("%d streams, want 46 (184 variants / 4 endpoints)", len(c.Streams))
	}
	for _, s := range c.Streams {
		if s.Backup {
			t.Errorf("backup stream kept: %s", s.Variant.URI)
		}
		// The first primary encoder (elive11) is listed before enc-101.
		if !strings.Contains(s.Variant.URI, "kiwi-fa-e.apple.com") || !strings.HasSuffix(s.Variant.AudioGroup(), "-fa-p") {
			t.Errorf("not the first primary endpoint: %s (%s)", s.Variant.URI, s.Variant.AudioGroup())
		}
	}
}

func TestRanges(t *testing.T) {
	live := load(t, "live.m3u8", false)
	vod := load(t, "vod.m3u8", false)
	for _, r := range Ranges {
		if !vod.HasRange(r) {
			t.Errorf("vod lacks %s", r)
		}
	}
	if live.HasRange(HDR10) {
		t.Error("live dovi_uhd (hvc1 + SUPPLEMENTAL-CODECS dvh1) must be Dolby Vision, not HDR10")
	}
	if !live.HasRange(DolbyVision) || !live.HasRange(SDR) || !live.HasRange(SDRHEVC) {
		t.Error("live is missing SDR, SDR [HEVC] or Dolby Vision")
	}
	if got := len(live.AvailableRanges()); got != 4 {
		t.Errorf("AvailableRanges = %d, want all 4 offered", got)
	}
}

func TestBestDefaults(t *testing.T) {
	for file, want := range map[string]string{
		"live.m3u8": "avc_hd_1080p_7500.m3u8",
		"vod.m3u8":  "sdr_avc_1080p_8500/prog_index.m3u8",
	} {
		c := load(t, file, false)
		s := c.Select(Filter{Range: ptr(SDR), Audio: ptr(AAC)})
		if len(s) == 0 || !strings.HasSuffix(s[0].Variant.URI, want) {
			t.Errorf("%s: best SDR+AAC = %v, want …%s", file, s[0].Variant.URI, want)
		}
	}
}

func TestAudioDescription(t *testing.T) {
	c := load(t, "vod.m3u8", true)
	s := c.Select(Filter{Range: ptr(DolbyVision), Audio: ptr(Atmos)})[0]
	out, err := c.Build(s, Subs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Media) != 1 || !out.Media[0].DescribesVideo() {
		t.Fatalf("want exactly the AD rendition, got %d media", len(out.Media))
	}
	if !strings.Contains(out.Media[0].URI(), "audio_ad_en_16ch_atmos_640") {
		t.Errorf("wrong AD track: %s", out.Media[0].URI())
	}
}

func TestBuildExcludesSubsByDefault(t *testing.T) {
	c := load(t, "live.m3u8", false)
	s := c.Select(Filter{Range: ptr(SDRHEVC), Audio: ptr(Atmos)})[0]
	out, err := c.Build(s, Subs{})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	out.Encode(&buf)
	text := buf.String()
	for _, bad := range []string{"SUBTITLES", "I-FRAME", "-ak-b", "events-live-b", "Audio Descriptions"} {
		if strings.Contains(text, bad) {
			t.Errorf("playlist contains %q:\n%s", bad, text)
		}
	}
	if strings.Count(text, "#EXT-X-STREAM-INF") != 1 || strings.Count(text, "TYPE=AUDIO") != 1 {
		t.Errorf("want one variant and one audio rendition:\n%s", text)
	}
}

func TestBuildSubtitleLanguages(t *testing.T) {
	c := load(t, "live.m3u8", false)
	s := c.Select(Filter{Range: ptr(DolbyVision)})[0]

	out, err := c.Build(s, Subs{Enabled: true, Langs: []string{"en", "zh"}})
	if err != nil {
		t.Fatal(err)
	}
	var langs []string
	for _, m := range out.Media {
		if m.Type() == "SUBTITLES" {
			langs = append(langs, m.Language())
		}
	}
	if got := strings.Join(langs, ","); got != "en,zh-CN,zh-HK,zh-TW" {
		t.Errorf("langs = %s", got)
	}
	if !out.Variants[0].Attrs.Has("SUBTITLES") {
		t.Error("variant lost its SUBTITLES group")
	}

	all, _ := c.Build(s, Subs{Enabled: true})
	if n := len(all.Media) - 1; n != 16 {
		t.Errorf("all subs = %d, want 16", n)
	}

	if _, err := c.Build(s, Subs{Enabled: true, Langs: []string{"xx"}}); err == nil {
		t.Error("unknown language should fail")
	}
}

func TestParseRange(t *testing.T) {
	for in, want := range map[string]Range{"sdr": SDR, "HEVC": SDRHEVC, "hdr10": HDR10, "dv": DolbyVision, "dolby-vision": DolbyVision} {
		if got, err := ParseRange(in); err != nil || got != want {
			t.Errorf("ParseRange(%q) = %v, %v", in, got, err)
		}
	}
}
