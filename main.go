// Command apple-event-streamer picks a single rendition of the current
// Apple Event stream, writes it to a temporary playlist and plays it in
// mpv (or mpv.net on Windows).
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/Mahir007/apple-event-streamer/internal/catalog"
	"github.com/Mahir007/apple-event-streamer/internal/discover"
	"github.com/Mahir007/apple-event-streamer/internal/hls"
	"github.com/Mahir007/apple-event-streamer/internal/picker"
	"github.com/Mahir007/apple-event-streamer/internal/player"
	"github.com/Mahir007/apple-event-streamer/internal/shortid"
)

const name = "apple-event-streamer"

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = ""

type options struct {
	best, noLaunch, keep, verbose, showVersion bool
	subs, ad, asl, list                        bool
	url, mpv, mpvnet, subLang                  string
	rangeName, audioName, maxRes               string
	timeout                                    time.Duration
	playerArgs                                 []string
}

// usageError marks errors that should print a usage hint and exit 2.
type usageError struct{ error }

func main() {
	code, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintf(os.Stderr, "Run '%s --help' for usage.\n", name)
			code = 2
		}
	}
	os.Exit(code)
}

func run(args []string) (int, error) {
	o, err := parseFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		return 0, nil
	}
	if err != nil {
		return 2, err
	}
	if o.showVersion {
		fmt.Printf("%s %s (%s/%s)\n", name, buildVersion(), runtime.GOOS, runtime.GOARCH)
		return 0, nil
	}

	logf := func(format string, a ...any) {
		if o.verbose {
			fmt.Fprintf(os.Stderr, "· "+format+"\n", a...)
		}
	}

	var preset picker.Preset
	if o.rangeName != "" {
		r, err := catalog.ParseRange(o.rangeName)
		if err != nil {
			return 2, usageError{err}
		}
		preset.Range = &r
	}
	if o.audioName != "" {
		a, err := catalog.ParseAudio(o.audioName)
		if err != nil {
			return 2, usageError{err}
		}
		preset.Audio = &a
	}
	if o.maxRes != "" {
		h, err := parseHeight(o.maxRes)
		if err != nil {
			return 2, usageError{err}
		}
		preset.MaxHeight = h
	}
	if o.subs || o.subLang != "" {
		preset.Subs = &catalog.Subs{Enabled: true, Langs: splitLangs(o.subLang)}
	}

	interactive := !o.best && !o.list
	if interactive && !(term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stderr.Fd())) {
		return 2, usageError{errors.New("the interactive picker needs a terminal; use --best for non-interactive use")}
	}

	// Find the player first so a missing mpv is reported before any menus.
	var pl *player.Player
	if !o.noLaunch && !o.list {
		if pl, err = player.Find(o.mpv, o.mpvnet); err != nil {
			return 1, err
		}
		logf("player: %s (%s)", pl.Path, pl.Kind)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	src, master, err := load(ctx, o, logf)
	stop()
	if err != nil {
		if ctx.Err() != nil {
			return 130, nil
		}
		return 1, err
	}

	cat := catalog.New(master, o.ad)
	logf("%d variants, %d unique primary streams, %d unrecognised", len(master.Variants), len(cat.Streams), cat.Skipped)
	if len(cat.Streams) == 0 {
		if o.ad {
			return 1, errors.New("this playlist has no audio-description tracks")
		}
		return 1, errors.New("no playable streams found in the playlist")
	}

	if o.list {
		printList(os.Stdout, cat)
		return 0, nil
	}

	var stream *catalog.Stream
	var subs catalog.Subs
	if o.best {
		if stream, err = pickBest(cat, preset); err != nil {
			return 1, err
		}
		if preset.Subs != nil {
			subs = *preset.Subs
		}
	} else {
		if len(cat.Select(catalog.Filter{Range: preset.Range, Audio: preset.Audio, MaxHeight: preset.MaxHeight})) == 0 {
			return 1, errors.New("no stream matches --range/--audio/--max-res; run with --list to see what is available")
		}
		res, err := picker.Run(cat, picker.Header{Badge: src.badge, Live: src.live, Description: src.description}, preset)
		if errors.Is(err, picker.ErrCancelled) {
			return 130, nil
		}
		if err != nil {
			return 1, err
		}
		stream, subs = res.Stream, res.Subs
	}

	out, err := cat.Build(stream, subs)
	if err != nil {
		return 1, err
	}
	summary := catalog.Describe(stream, o.ad)
	if subs.Enabled {
		summary += " · " + subs.String()
	}
	fmt.Fprintf(os.Stderr, "▶ %s\n", summary)
	logf("source: %s", src.url)
	logf("variant: %s", stream.Variant.URI)

	path, err := writePlaylist(out, src.url, summary)
	if err != nil {
		return 1, err
	}
	logf("playlist: %s", path)

	if o.noLaunch {
		fmt.Println(path)
		return 0, nil
	}

	title := "Apple Event"
	if src.badge == "LIVE" {
		title += " (Live)"
	}
	popts := player.Options{Title: title + " · " + summary, Subtitles: subs.Enabled, Extra: o.playerArgs}
	if len(subs.Langs) > 0 {
		popts.SubLangs = subs.Langs
	}
	pargs := pl.Args(path, popts)
	logf("run: %s %s", pl.Path, strings.Join(quoteArgs(pargs), " "))

	if o.keep {
		fmt.Fprintf(os.Stderr, "Playlist kept at %s\n", path)
	}
	return pl.Run(pargs, func() {
		if !o.keep {
			os.Remove(path)
		}
	})
}

type source struct {
	url, badge, description string
	live                    bool
}

func load(ctx context.Context, o *options, logf func(string, ...any)) (*source, *hls.Master, error) {
	client := &http.Client{Timeout: o.timeout}
	src := &source{url: o.url}

	if src.url == "" {
		logf("discovering stream via %s", discover.EventsPage)
		ev, err := discover.Find(ctx, client, o.asl)
		if err != nil {
			return nil, nil, err
		}
		src.url, src.description = ev.URL, ev.Description
		src.badge = strings.ToUpper(ev.State)
		logf("page state: %q", ev.State)
	}

	var body []byte
	var base *url.URL
	if isLocal(src.url) {
		p := strings.TrimPrefix(src.url, "file://")
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		body = b
		if abs, err := filepath.Abs(p); err == nil {
			base = &url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
		}
	} else {
		u, err := url.Parse(src.url)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, nil, usageError{fmt.Errorf("--url %q is neither an http(s) URL nor an existing file", src.url)}
		}
		logf("fetching %s", src.url)
		if body, err = discover.Fetch(ctx, client, src.url); err != nil {
			return nil, nil, err
		}
		base = u
	}

	m, err := hls.Parse(bytes.NewReader(body), base)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", src.url, err)
	}

	isLiveURI := strings.Contains(m.Variants[0].URI, "/live/")
	switch {
	case src.badge == "" && isLiveURI:
		src.badge = "LIVE"
	case src.badge == "":
		src.badge = "VOD"
	}
	src.live = src.badge == "LIVE"
	return src, m, nil
}

func isLocal(s string) bool {
	if strings.HasPrefix(s, "file://") {
		return true
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return false
	}
	_, err := os.Stat(s)
	return err == nil
}

// pickBest returns the highest-bandwidth stream. Unset range and audio
// default to SDR (H.264) and AAC, falling back to the next available kind.
func pickBest(c *catalog.Catalog, p picker.Preset) (*catalog.Stream, error) {
	ranges := []catalog.Range{catalog.SDR, catalog.SDRHEVC, catalog.HDR10, catalog.DolbyVision, catalog.HLG}
	if p.Range != nil {
		ranges = []catalog.Range{*p.Range}
	}
	audios := []catalog.Audio{catalog.AAC, catalog.Atmos, catalog.EAC3, catalog.AC3, catalog.OtherAudio}
	if p.Audio != nil {
		audios = []catalog.Audio{*p.Audio}
	}
	for _, r := range ranges {
		for _, a := range audios {
			if s := c.Select(catalog.Filter{Range: &r, Audio: &a, MaxHeight: p.MaxHeight}); len(s) > 0 {
				return s[0], nil
			}
		}
	}
	return nil, errors.New("no stream matches --range/--audio/--max-res; run with --list to see what is available")
}

func writePlaylist(m *hls.Master, src, summary string) (string, error) {
	for attempt := 0; ; attempt++ {
		path := filepath.Join(os.TempDir(), "apple-event-"+shortid.New()+".m3u8")
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) && attempt < 3 {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("creating playlist: %w", err)
		}
		err = m.Encode(f,
			fmt.Sprintf("Generated by %s %s", name, buildVersion()),
			"Source: "+src,
			"Selection: "+summary,
		)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(path)
			return "", fmt.Errorf("writing playlist: %w", err)
		}
		return path, nil
	}
}

func printList(w io.Writer, c *catalog.Catalog) {
	streams := append([]*catalog.Stream(nil), c.Streams...)
	sort.SliceStable(streams, func(i, j int) bool {
		a, b := streams[i], streams[j]
		if a.Range != b.Range {
			return a.Range < b.Range
		}
		if a.Audio != b.Audio {
			return a.Audio < b.Audio
		}
		if a.Height != b.Height {
			return a.Height > b.Height
		}
		return a.Bandwidth() > b.Bandwidth()
	})
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RANGE\tAUDIO\tRESOLUTION\tPEAK\tAVERAGE\tVIDEO CODEC")
	for _, s := range streams {
		audio := s.Audio.String()
		if c.AD {
			audio += " (AD)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%dx%d\t%s\t%s\t%s\n", s.Range, audio, s.Width, s.Height,
			catalog.Mbps(s.Bandwidth()), catalog.Mbps(s.AverageBandwidth()), s.VideoCodec)
	}
	tw.Flush()

	for _, r := range catalog.Ranges {
		if !c.HasRange(r) {
			fmt.Fprintf(w, "\n%s: not available in this playlist", r)
		}
	}
	if subs := c.Subtitles(catalog.Filter{}); len(subs) > 0 {
		var langs []string
		for _, s := range subs {
			langs = append(langs, s.Language())
		}
		fmt.Fprintf(w, "\nSubtitles (--sub-lang): %s", strings.Join(langs, ", "))
	}
	fmt.Fprintln(w)
}

// parseHeight accepts "1080", "1080p", "4k" and similar.
func parseHeight(s string) (int, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	switch t {
	case "4k", "uhd":
		return 2160, nil
	case "2k", "qhd":
		return 1440, nil
	case "hd", "fhd":
		return 1080, nil
	}
	h, err := strconv.Atoi(strings.TrimSuffix(t, "p"))
	if err != nil || h <= 0 {
		return 0, fmt.Errorf("invalid --max-res %q (want e.g. 1080, 1440p or 4k)", s)
	}
	return h, nil
}

func splitLangs(s string) []string {
	var out []string
	for _, l := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		out = append(out, l)
	}
	return out
}

func quoteArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \"'") {
			a = strconv.Quote(a)
		}
		out[i] = a
	}
	return out
}

func buildVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

// secondsFlag accepts plain seconds ("30", "2.5") or Go durations ("1m30s").
type secondsFlag struct{ d *time.Duration }

func (f secondsFlag) String() string {
	if f.d == nil {
		return ""
	}
	return strconv.FormatFloat(f.d.Seconds(), 'f', -1, 64)
}

func (f secondsFlag) Set(s string) error {
	if sec, err := strconv.ParseFloat(s, 64); err == nil {
		*f.d = time.Duration(sec * float64(time.Second))
	} else if d, err := time.ParseDuration(s); err == nil {
		*f.d = d
	} else {
		return fmt.Errorf("want seconds, e.g. 20 or 1m30s")
	}
	if *f.d <= 0 {
		return errors.New("must be positive")
	}
	return nil
}

func parseFlags(args []string) (*options, error) {
	o := &options{timeout: 20 * time.Second}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.BoolVar(&o.best, "best", false, "")
	fs.StringVar(&o.url, "url", "", "")
	fs.BoolVar(&o.noLaunch, "no-launch", false, "")
	fs.BoolVar(&o.keep, "keep-playlist", false, "")
	fs.StringVar(&o.mpv, "mpv", "", "")
	fs.StringVar(&o.mpvnet, "mpvnet", "", "")
	fs.Var(secondsFlag{&o.timeout}, "timeout", "")
	fs.BoolVar(&o.verbose, "verbose", false, "")
	fs.BoolVar(&o.verbose, "v", false, "")
	fs.BoolVar(&o.showVersion, "version", false, "")
	fs.BoolVar(&o.subs, "subs", false, "")
	fs.StringVar(&o.subLang, "sub-lang", "", "")
	fs.BoolVar(&o.ad, "ad", false, "")
	fs.BoolVar(&o.asl, "asl", false, "")
	fs.BoolVar(&o.list, "list", false, "")
	fs.StringVar(&o.rangeName, "range", "", "")
	fs.StringVar(&o.audioName, "audio", "", "")
	fs.StringVar(&o.maxRes, "max-res", "", "")
	help := fs.Bool("help", false, "")
	fs.BoolVar(help, "h", false, "")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(os.Stdout)
			return nil, err
		}
		return nil, usageError{err}
	}
	if *help {
		printUsage(os.Stdout)
		return nil, flag.ErrHelp
	}

	// Everything after "--" goes to the player; stray words are a mistake.
	if rest := fs.Args(); len(rest) > 0 {
		if i := len(args) - len(rest) - 1; i < 0 || args[i] != "--" {
			return nil, usageError{fmt.Errorf("unexpected argument %q (pass player options after --)", rest[0])}
		}
		o.playerArgs = rest
	}
	if o.url != "" && o.asl {
		return nil, usageError{errors.New("--asl cannot be combined with --url")}
	}
	return o, nil
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `%[1]s %[2]s — play the current Apple Event in mpv

Usage:
  %[1]s [options] [-- player options]

With no options an interactive picker is shown:
  video (SDR, SDR [HEVC], HDR10, Dolby Vision) › audio › subtitles › resolution › bitrate
Use the arrow keys (or j/k), Enter to choose, ←/Esc to go back, q to quit; the
mouse works too (click to choose, wheel to scroll, right-click to go back).

Selection:
  --best              pick the highest-bitrate stream without asking
                      (SDR + AAC unless --range/--audio say otherwise)
  --range RANGE       sdr | hevc | hdr10 | dv           (skips that menu)
  --audio AUDIO       aac | atmos                       (skips that menu)
  --max-res RES       highest resolution to offer, e.g. 1080, 1440p, 4k
  --subs              include subtitles (all languages unless --sub-lang)
  --sub-lang LANGS    subtitle languages, comma-separated, e.g. en or en,fr
                      (implies --subs; "en" also matches en-GB etc.)
  --ad                use audio-description tracks instead of regular audio
  --list              print the available streams and exit

Source:
  --url URL           use this master playlist (URL or local file) instead
                      of discovering it from apple.com/apple-events
  --asl               use the American Sign Language version of the event
  --timeout SEC       network timeout in seconds or as a duration (default 20)

Playback:
  --no-launch         write the playlist and print its path; don't play
  --keep-playlist     don't delete the playlist after the player exits
  --mpv PATH          use this mpv executable
  --mpvnet PATH       use this mpv.net executable
                      (default: mpvnet then mpv on Windows, mpv elsewhere)
  -- ARGS             pass the remaining arguments to the player

Other:
  -v, --verbose       log discovery, selection and the player command line
  --version           print the version and exit
  -h, --help          show this help

Examples:
  %[1]s
  %[1]s --best
  %[1]s --best --range dv --audio atmos
  %[1]s --best --subs --sub-lang en --ad
  %[1]s --url https://example.com/master.m3u8 --no-launch
  %[1]s --best -- --fs --volume=70
`, name, buildVersion())
}
