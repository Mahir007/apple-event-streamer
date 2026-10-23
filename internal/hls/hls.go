// Package hls parses and writes HLS multivariant ("master") playlists.
//
// Only the subset of RFC 8216 needed to pick a single rendition is
// implemented: EXT-X-MEDIA, EXT-X-STREAM-INF and a few session-level tags.
// Attribute order is preserved so re-emitted tags look like the originals.
package hls

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// Attr is a single attribute from an HLS attribute list.
type Attr struct {
	Key    string
	Value  string
	Quoted bool
}

// AttrList is an ordered HLS attribute list (KEY=VALUE,KEY="VALUE",...).
type AttrList []Attr

// ParseAttrs parses an attribute list such as the text after
// "#EXT-X-STREAM-INF:".
func ParseAttrs(s string) AttrList {
	var attrs AttrList
	for len(s) > 0 {
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			break
		}
		key := strings.TrimSpace(s[:eq])
		s = s[eq+1:]

		var a Attr
		a.Key = key
		if strings.HasPrefix(s, `"`) {
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				a.Value, s = s[1:], ""
			} else {
				a.Value, s = s[1:end+1], s[end+2:]
			}
			a.Quoted = true
		} else {
			comma := strings.IndexByte(s, ',')
			if comma < 0 {
				a.Value, s = s, ""
			} else {
				a.Value, s = s[:comma], s[comma:]
			}
			a.Value = strings.TrimSpace(a.Value)
		}
		attrs = append(attrs, a)
		s = strings.TrimPrefix(strings.TrimLeft(s, " "), ",")
	}
	return attrs
}

// Get returns the value of key, or "" if absent.
func (l AttrList) Get(key string) string {
	for _, a := range l {
		if a.Key == key {
			return a.Value
		}
	}
	return ""
}

// Has reports whether key is present.
func (l AttrList) Has(key string) bool {
	for _, a := range l {
		if a.Key == key {
			return true
		}
	}
	return false
}

// With returns a copy of l with key set to value. Existing keys keep their
// position; new keys are appended.
func (l AttrList) With(key, value string, quoted bool) AttrList {
	out := make(AttrList, len(l), len(l)+1)
	copy(out, l)
	for i := range out {
		if out[i].Key == key {
			out[i].Value, out[i].Quoted = value, quoted
			return out
		}
	}
	return append(out, Attr{Key: key, Value: value, Quoted: quoted})
}

// Without returns a copy of l with key removed.
func (l AttrList) Without(key string) AttrList {
	out := make(AttrList, 0, len(l))
	for _, a := range l {
		if a.Key != key {
			out = append(out, a)
		}
	}
	return out
}

func (l AttrList) String() string {
	var b strings.Builder
	for i, a := range l {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(a.Key)
		b.WriteByte('=')
		if a.Quoted {
			b.WriteByte('"')
			b.WriteString(a.Value)
			b.WriteByte('"')
		} else {
			b.WriteString(a.Value)
		}
	}
	return b.String()
}

// Media is an EXT-X-MEDIA rendition (audio, subtitles, ...).
type Media struct {
	Attrs AttrList
}

func (m *Media) Type() string     { return m.Attrs.Get("TYPE") }
func (m *Media) GroupID() string  { return m.Attrs.Get("GROUP-ID") }
func (m *Media) Language() string { return m.Attrs.Get("LANGUAGE") }
func (m *Media) Name() string     { return m.Attrs.Get("NAME") }
func (m *Media) URI() string      { return m.Attrs.Get("URI") }
func (m *Media) Channels() string { return m.Attrs.Get("CHANNELS") }

// DescribesVideo reports whether the rendition is an audio description track.
func (m *Media) DescribesVideo() bool {
	return strings.Contains(m.Attrs.Get("CHARACTERISTICS"), "public.accessibility.describes-video")
}

// Variant is an EXT-X-STREAM-INF entry and its URI.
type Variant struct {
	Attrs AttrList
	URI   string
}

func (v *Variant) Bandwidth() int64        { return atoi(v.Attrs.Get("BANDWIDTH")) }
func (v *Variant) AverageBandwidth() int64 { return atoi(v.Attrs.Get("AVERAGE-BANDWIDTH")) }
func (v *Variant) VideoRange() string      { return v.Attrs.Get("VIDEO-RANGE") }
func (v *Variant) FrameRate() string       { return v.Attrs.Get("FRAME-RATE") }
func (v *Variant) AudioGroup() string      { return v.Attrs.Get("AUDIO") }
func (v *Variant) SubtitleGroup() string   { return v.Attrs.Get("SUBTITLES") }

// Codecs returns the CODECS attribute split on commas.
func (v *Variant) Codecs() []string { return splitList(v.Attrs.Get("CODECS")) }

// SupplementalCodecs returns the SUPPLEMENTAL-CODECS attribute split on commas.
func (v *Variant) SupplementalCodecs() []string {
	return splitList(v.Attrs.Get("SUPPLEMENTAL-CODECS"))
}

// Resolution returns the RESOLUTION attribute as width and height.
func (v *Variant) Resolution() (w, h int) {
	fmt.Sscanf(v.Attrs.Get("RESOLUTION"), "%dx%d", &w, &h)
	return w, h
}

// Master is a parsed multivariant playlist.
type Master struct {
	Version             string
	IndependentSegments bool
	Media               []*Media
	Variants            []*Variant
}

// MediaGroup returns the renditions of the given type and group ID, in
// playlist order.
func (m *Master) MediaGroup(typ, group string) []*Media {
	var out []*Media
	for _, md := range m.Media {
		if md.Type() == typ && md.GroupID() == group {
			out = append(out, md)
		}
	}
	return out
}

// ErrNotMaster is returned when the input is not a multivariant playlist.
var ErrNotMaster = errors.New("not an HLS multivariant playlist")

// Parse reads a multivariant playlist. Relative URIs are resolved against
// base when it is non-nil. Lines before #EXTM3U are ignored so captured
// output with a stray prompt line still parses.
func Parse(r io.Reader, base *url.URL) (*Master, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)

	m := &Master{}
	started := false
	var pending *Variant
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if !started {
			if line == "#EXTM3U" {
				started = true
			}
			continue
		}
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "#EXT-X-VERSION:"):
			m.Version = strings.TrimPrefix(line, "#EXT-X-VERSION:")
		case line == "#EXT-X-INDEPENDENT-SEGMENTS":
			m.IndependentSegments = true
		case strings.HasPrefix(line, "#EXT-X-MEDIA:"):
			attrs := ParseAttrs(strings.TrimPrefix(line, "#EXT-X-MEDIA:"))
			if u := attrs.Get("URI"); u != "" {
				attrs = attrs.With("URI", resolve(base, u), true)
			}
			m.Media = append(m.Media, &Media{Attrs: attrs})
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			pending = &Variant{Attrs: ParseAttrs(strings.TrimPrefix(line, "#EXT-X-STREAM-INF:"))}
		case strings.HasPrefix(line, "#EXTINF") || strings.HasPrefix(line, "#EXT-X-TARGETDURATION"):
			return nil, fmt.Errorf("%w: got a media playlist", ErrNotMaster)
		case strings.HasPrefix(line, "#"):
			// Comments, I-frame playlists and other tags are not needed.
		default:
			if pending != nil {
				pending.URI = resolve(base, line)
				m.Variants = append(m.Variants, pending)
				pending = nil
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !started {
		return nil, fmt.Errorf("%w: missing #EXTM3U header", ErrNotMaster)
	}
	if len(m.Variants) == 0 {
		return nil, fmt.Errorf("%w: no variant streams found", ErrNotMaster)
	}
	return m, nil
}

// Encode writes m as a multivariant playlist. header lines are written as
// "## " comments after the session tags.
func (m *Master) Encode(w io.Writer, header ...string) error {
	bw := bufio.NewWriter(w)
	bw.WriteString("#EXTM3U\n")
	if m.Version != "" {
		fmt.Fprintf(bw, "#EXT-X-VERSION:%s\n", m.Version)
	}
	if m.IndependentSegments {
		bw.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	}
	for _, h := range header {
		fmt.Fprintf(bw, "## %s\n", h)
	}
	if len(m.Media) > 0 {
		bw.WriteByte('\n')
	}
	for _, md := range m.Media {
		fmt.Fprintf(bw, "#EXT-X-MEDIA:%s\n", md.Attrs)
	}
	for _, v := range m.Variants {
		fmt.Fprintf(bw, "\n#EXT-X-STREAM-INF:%s\n%s\n", v.Attrs, v.URI)
	}
	return bw.Flush()
}

func resolve(base *url.URL, ref string) string {
	if base == nil {
		return ref
	}
	u, err := url.Parse(ref)
	if err != nil || u.IsAbs() {
		return ref
	}
	return base.ResolveReference(u).String()
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func atoi(s string) int64 {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
