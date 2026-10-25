// Package catalog turns a parsed Apple Events master playlist into a set of
// selectable streams and builds single-rendition playlists from them.
package catalog

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/Mahir007/apple-event-streamer/internal/hls"
)

// Range is the video range with the codec baked in.
type Range int

const (
	SDR Range = iota
	SDRHEVC
	HDR10
	DolbyVision
	HLG
)

// Ranges are always offered, even when the playlist lacks them.
var Ranges = []Range{SDR, SDRHEVC, HDR10, DolbyVision}

func (r Range) String() string {
	switch r {
	case SDR:
		return "SDR"
	case SDRHEVC:
		return "SDR [HEVC]"
	case HDR10:
		return "HDR10"
	case DolbyVision:
		return "Dolby Vision"
	case HLG:
		return "HLG"
	}
	return "Unknown"
}

// Detail is a short technical description of the range.
func (r Range) Detail() string {
	switch r {
	case SDR:
		return "H.264 / AVC"
	case SDRHEVC:
		return "H.265 / HEVC"
	case HDR10:
		return "HEVC · PQ"
	case DolbyVision:
		return "HEVC · PQ + Dolby Vision metadata"
	case HLG:
		return "HEVC · HLG"
	}
	return ""
}

// ParseRange parses a --range value.
func ParseRange(s string) (Range, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "sdr", "avc", "h264":
		return SDR, nil
	case "hevc", "sdr-hevc", "sdrhevc", "h265":
		return SDRHEVC, nil
	case "hdr10", "hdr", "pq":
		return HDR10, nil
	case "dv", "dovi", "dolby-vision", "dolbyvision":
		return DolbyVision, nil
	case "hlg":
		return HLG, nil
	}
	return 0, fmt.Errorf("unknown range %q (want sdr, hevc, hdr10 or dv)", s)
}

// Audio is the audio format of a stream.
type Audio int

const (
	AAC Audio = iota
	Atmos
	EAC3
	AC3
	OtherAudio
)

func (a Audio) String() string {
	switch a {
	case AAC:
		return "AAC"
	case Atmos:
		return "Dolby Atmos"
	case EAC3:
		return "Dolby Digital Plus"
	case AC3:
		return "Dolby Digital"
	}
	return "Other"
}

// ParseAudio parses an --audio value.
func ParseAudio(s string) (Audio, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "aac", "stereo":
		return AAC, nil
	case "atmos", "joc":
		return Atmos, nil
	case "eac3", "ec3", "ddp", "dd+":
		return EAC3, nil
	case "ac3", "dd":
		return AC3, nil
	}
	return 0, fmt.Errorf("unknown audio %q (want aac or atmos)", s)
}

// Stream is one selectable video+audio combination.
type Stream struct {
	Variant    *hls.Variant
	Range      Range
	Audio      Audio
	Width      int
	Height     int
	VideoCodec string
	Backup     bool
}

func (s *Stream) Bandwidth() int64        { return s.Variant.Bandwidth() }
func (s *Stream) AverageBandwidth() int64 { return s.Variant.AverageBandwidth() }

// Catalog holds the streams usable for the chosen audio-description mode.
type Catalog struct {
	Master  *hls.Master
	AD      bool
	Streams []*Stream
	// Skipped counts variants that could not be classified.
	Skipped int

	groups map[[2]string][]*hls.Media // renditions by type and group ID
}

// New classifies m's variants. When ad is true only audio-description
// renditions are considered, otherwise they are excluded. For live
// playlists that list primary and backup endpoints, the first primary
// copy of each variant wins.
func New(m *hls.Master, ad bool) *Catalog {
	c := &Catalog{Master: m, AD: ad, groups: map[[2]string][]*hls.Media{}}
	for _, md := range m.Media {
		k := [2]string{md.Type(), md.GroupID()}
		c.groups[k] = append(c.groups[k], md)
	}
	byKey := map[string]int{}
	for _, v := range m.Variants {
		s, ok := c.classify(v)
		if !ok {
			c.Skipped++
			continue
		}
		key := fmt.Sprintf("%d|%d|%dx%d|%d|%s", s.Range, s.Audio, s.Width, s.Height, s.Bandwidth(), path.Base(v.URI))
		if i, seen := byKey[key]; seen {
			if c.Streams[i].Backup && !s.Backup {
				c.Streams[i] = s
			}
			continue
		}
		byKey[key] = len(c.Streams)
		c.Streams = append(c.Streams, s)
	}
	return c
}

func (c *Catalog) classify(v *hls.Variant) (*Stream, bool) {
	s := &Stream{Variant: v}
	s.Width, s.Height = v.Resolution()

	var video, audio string
	for _, codec := range v.Codecs() {
		switch {
		case isVideoCodec(codec) && video == "":
			video = codec
		case !isVideoCodec(codec) && audio == "":
			audio = codec
		}
	}
	if video == "" {
		return nil, false
	}
	s.VideoCodec = video

	dv := isDolbyVision(video)
	for _, sc := range v.SupplementalCodecs() {
		dv = dv || isDolbyVision(sc)
	}
	switch vr := strings.ToUpper(v.VideoRange()); {
	case dv:
		s.Range = DolbyVision
	case vr == "PQ":
		s.Range = HDR10
	case vr == "HLG":
		s.Range = HLG
	case strings.HasPrefix(video, "avc"):
		s.Range = SDR
	case strings.HasPrefix(video, "hvc") || strings.HasPrefix(video, "hev"):
		s.Range = SDRHEVC
	default:
		return nil, false
	}

	renditions := c.AudioRenditions(v)
	if v.AudioGroup() != "" && len(renditions) == 0 {
		return nil, false
	}
	if v.AudioGroup() == "" && c.AD {
		return nil, false // muxed audio has no description track
	}
	switch {
	case strings.HasPrefix(audio, "mp4a"):
		s.Audio = AAC
	case audio == "ec-3":
		s.Audio = EAC3
		for _, r := range renditions {
			if strings.Contains(strings.ToUpper(r.Channels()), "JOC") {
				s.Audio = Atmos
			}
		}
	case audio == "ac-3":
		s.Audio = AC3
	default:
		s.Audio = OtherAudio
	}

	s.Backup = isBackup(v)
	return s, true
}

// AudioRenditions returns the audio renditions of v's group that match the
// catalog's audio-description mode.
func (c *Catalog) AudioRenditions(v *hls.Variant) []*hls.Media {
	var out []*hls.Media
	for _, md := range c.group("AUDIO", v.AudioGroup()) {
		if md.DescribesVideo() == c.AD {
			out = append(out, md)
		}
	}
	return out
}

// group returns the renditions of the given type and group ID, in playlist
// order.
func (c *Catalog) group(typ, id string) []*hls.Media { return c.groups[[2]string{typ, id}] }

func isVideoCodec(c string) bool {
	for _, p := range []string{"avc", "hvc", "hev", "dvh", "dva", "dav", "av01", "vp09"} {
		if strings.HasPrefix(c, p) {
			return true
		}
	}
	return false
}

func isDolbyVision(c string) bool {
	return strings.HasPrefix(c, "dvh") || strings.HasPrefix(c, "dva") || strings.HasPrefix(c, "dav")
}

// isBackup recognises Apple's backup endpoints: group IDs ending in "-b",
// "-b" path segments (".../live/2120471-b/...") or "-b" hosts
// ("events-live-b.apple.com").
func isBackup(v *hls.Variant) bool {
	for _, g := range []string{v.AudioGroup(), v.SubtitleGroup()} {
		if strings.HasSuffix(g, "-b") {
			return true
		}
	}
	u, err := url.Parse(v.URI)
	if err != nil {
		return false
	}
	if strings.HasSuffix(strings.Split(u.Hostname(), ".")[0], "-b") {
		return true
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if strings.HasSuffix(seg, "-b") {
			return true
		}
	}
	return false
}

// Filter narrows the stream list. Nil fields match everything.
type Filter struct {
	Range     *Range
	Audio     *Audio
	Height    *int
	MaxHeight int
}

func (f Filter) match(s *Stream) bool {
	return (f.Range == nil || s.Range == *f.Range) &&
		(f.Audio == nil || s.Audio == *f.Audio) &&
		(f.Height == nil || s.Height == *f.Height) &&
		(f.MaxHeight == 0 || s.Height <= f.MaxHeight)
}

// Select returns matching streams, highest bandwidth first.
func (c *Catalog) Select(f Filter) []*Stream {
	var out []*Stream
	for _, s := range c.Streams {
		if f.match(s) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bandwidth() > out[j].Bandwidth() })
	return out
}

// HasRange reports whether any stream uses r.
func (c *Catalog) HasRange(r Range) bool { return len(c.Select(Filter{Range: &r})) > 0 }

// AvailableRanges returns the always-offered ranges plus any extra range
// (such as HLG) present in the playlist.
func (c *Catalog) AvailableRanges() []Range {
	out := append([]Range(nil), Ranges...)
	if c.HasRange(HLG) {
		out = append(out, HLG)
	}
	return out
}

// Audios returns every audio format present in the catalog, in display order.
func (c *Catalog) Audios() []Audio {
	seen := map[Audio]bool{}
	for _, s := range c.Streams {
		seen[s.Audio] = true
	}
	var out []Audio
	for _, a := range []Audio{AAC, Atmos, EAC3, AC3, OtherAudio} {
		if seen[a] {
			out = append(out, a)
		}
	}
	return out
}

// Heights returns the distinct heights of matching streams, highest first.
func (c *Catalog) Heights(f Filter) []int {
	seen := map[int]bool{}
	var out []int
	for _, s := range c.Select(f) {
		if !seen[s.Height] {
			seen[s.Height] = true
			out = append(out, s.Height)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out
}

// Subtitles returns the subtitle renditions offered alongside streams
// matching f, one per language.
func (c *Catalog) Subtitles(f Filter) []*hls.Media {
	for _, s := range c.Select(f) {
		if g := s.Variant.SubtitleGroup(); g != "" {
			return uniqueLangs(c.group("SUBTITLES", g))
		}
	}
	return nil
}

func uniqueLangs(in []*hls.Media) []*hls.Media {
	seen := map[string]bool{}
	var out []*hls.Media
	for _, m := range in {
		k := strings.ToLower(m.Language() + "|" + m.Name())
		if !seen[k] {
			seen[k] = true
			out = append(out, m)
		}
	}
	return out
}

// Subs selects subtitle tracks for the output playlist.
type Subs struct {
	Enabled bool
	// Langs restricts the tracks to these BCP-47 tags; empty means all.
	// "en" matches "en" and "en-GB"; "zh" matches every Chinese variant.
	Langs []string
}

// Matches reports whether rendition m is selected.
func (s Subs) Matches(m *hls.Media) bool {
	if !s.Enabled {
		return false
	}
	if len(s.Langs) == 0 {
		return true
	}
	lang := strings.ToLower(m.Language())
	for _, want := range s.Langs {
		want = strings.ToLower(want)
		if lang == want || strings.HasPrefix(lang, want+"-") || strings.EqualFold(m.Name(), want) {
			return true
		}
	}
	return false
}

func (s Subs) String() string {
	switch {
	case !s.Enabled:
		return "no subtitles"
	case len(s.Langs) == 0:
		return "all subtitles"
	}
	return "subtitles: " + strings.Join(s.Langs, ", ")
}

// Build returns a playlist containing only s, its audio rendition(s) and
// the requested subtitles. I-frame playlists and backups are dropped.
func (c *Catalog) Build(s *Stream, subs Subs) (*hls.Master, error) {
	out := &hls.Master{Version: c.Master.Version, IndependentSegments: c.Master.IndependentSegments}
	attrs := s.Variant.Attrs.Without("CLOSED-CAPTIONS")

	if s.Variant.AudioGroup() != "" {
		for i, md := range c.AudioRenditions(s.Variant) {
			def := "NO"
			if i == 0 {
				def = "YES"
			}
			out.Media = append(out.Media, &hls.Media{Attrs: md.Attrs.With("DEFAULT", def, false).With("AUTOSELECT", "YES", false)})
		}
	}

	var picked []*hls.Media
	if g := s.Variant.SubtitleGroup(); g != "" {
		for _, md := range c.group("SUBTITLES", g) {
			if subs.Matches(md) {
				picked = append(picked, md)
			}
		}
	}
	if subs.Enabled && len(picked) == 0 {
		if len(subs.Langs) == 0 {
			return nil, errors.New("this stream has no subtitles")
		}
		return nil, fmt.Errorf("no subtitles match %s", strings.Join(subs.Langs, ", "))
	}
	if len(picked) == 0 {
		attrs = attrs.Without("SUBTITLES")
	}
	for i, md := range picked {
		a := md.Attrs
		if len(subs.Langs) > 0 {
			def := "NO"
			if i == 0 {
				def = "YES"
			}
			a = a.With("DEFAULT", def, false).With("AUTOSELECT", "YES", false)
		}
		out.Media = append(out.Media, &hls.Media{Attrs: a})
	}

	out.Variants = []*hls.Variant{{Attrs: attrs, URI: s.Variant.URI}}
	return out, nil
}

// Describe summarises s for humans, e.g. "Dolby Vision · Dolby Atmos · 2160p · 24.5 Mbps".
func Describe(s *Stream, ad bool) string {
	audio := s.Audio.String()
	if ad {
		audio += " (Audio Description)"
	}
	return fmt.Sprintf("%s · %s · %s · %s", s.Range, audio, ResolutionLabel(s.Height), Mbps(s.Bandwidth()))
}

// ResolutionLabel returns e.g. "1080p" or "2160p (4K)".
func ResolutionLabel(h int) string {
	switch h {
	case 0:
		return "unknown resolution"
	case 2160:
		return "2160p (4K)"
	case 1440:
		return "1440p (QHD)"
	}
	return fmt.Sprintf("%dp", h)
}

// Mbps formats a bit rate in megabits per second.
func Mbps(bps int64) string { return fmt.Sprintf("%.1f Mbps", float64(bps)/1e6) }
