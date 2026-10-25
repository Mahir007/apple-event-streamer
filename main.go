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
	"math"
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
	"github.com/Mahir007/apple-event-streamer/internal/sanitize"
	"github.com/Mahir007/apple-event-streamer/internal/shortid"
	"github.com/Mahir007/apple-event-streamer/internal/userdirs"
)

const name = "apple-event-streamer"

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = ""

type options struct {
	best, noLaunch, keep, verbose, showVersion bool
	subs, allSubs, ad, asl, list               bool
	url, mpv, mpvnet, subLang, keepDir         string
	rangeName, audioName, maxRes               string
	timeout                                    time.Duration
	playerArgs                                 []string
}

// usageError marks errors that should print a usage hint and exit 2.
type usageError struct{ error }

// showTrace is --verbose, for crash reports: the stack trace is only
// printed when it was given.
var showTrace bool

// reportCrash says what crashed and where to report it. Without --verbose
// it asks for a rerun with it rather than printing the stack trace.
func reportCrash(value any) {
	fmt.Fprintf(os.Stderr, "\n%s %s (%s/%s) crashed: %s\n", name, buildVersion(), runtime.GOOS, runtime.GOARCH, sanitize.Text(fmt.Sprint(value)))
	const issues = "https://github.com/Mahir007/apple-event-streamer/issues"
	if showTrace {
		fmt.Fprintf(os.Stderr, "Please report this, with the output below, at\n%s\n\n", issues)
	} else {
		fmt.Fprintf(os.Stderr, "Please report this at %s,\nwith the output of the same command run again with --verbose.\n", issues)
	}
}

func main() {
	// Deferred cleanups (the playlist) have run by now. With --verbose the
	// panic carries on to print its stack trace; either way the exit code is 2.
	defer func() {
		if r := recover(); r != nil {
			reportCrash(r)
			if showTrace {
				panic(r)
			}
			os.Exit(2)
		}
	}()
	code, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s\n", name, sanitize.Text(err.Error()))
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
	showTrace = o.verbose
	if o.showVersion {
		fmt.Printf("%s %s (%s/%s)\n", name, buildVersion(), runtime.GOOS, runtime.GOARCH)
		return 0, nil
	}

	logf := func(format string, a ...any) {
		if o.verbose {
			fmt.Fprintf(os.Stderr, "· %s\n", sanitize.Text(fmt.Sprintf(format, a...)))
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
	switch {
	case o.subLang != "":
		preset.Subs = &catalog.Subs{Enabled: true, Langs: splitLangs(o.subLang)}
	case o.allSubs:
		preset.Subs = &catalog.Subs{Enabled: true}
	case o.subs:
		preset.AskSubs = true
	}

	interactive := !o.best && !o.list
	if interactive && !(term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stderr.Fd())) {
		return 2, usageError{errors.New("the interactive picker needs a terminal; use --best for non-interactive use")}
	}

	// Find the player and the --keep-playlist folder first, so problems
	// with either are reported before any downloads or menus.
	var pl *player.Player
	if !o.noLaunch && !o.list {
		if pl, err = player.Find(o.mpv, o.mpvnet); err != nil {
			return 1, err
		}
		logf("player: %s (%s)", pl.Path, pl.Kind)
	}
	dir := os.TempDir()
	if o.keep && !o.list {
		if dir, err = keepFolder(o.keepDir); err != nil {
			return 1, err
		}
		logf("keeping the playlist in %s", dir)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	src, master, err := load(ctx, o, logf)
	interrupted := ctx.Err() != nil // stop cancels ctx, so check first
	stop()
	if err != nil {
		if interrupted {
			return 130, nil
		}
		var fe *discover.Error
		if errors.As(err, &fe) {
			logf("cause: %v", fe.Err)
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

	if preset.AskSubs || preset.Subs != nil {
		available := cat.Subtitles(catalog.Filter{})
		if len(available) == 0 {
			return 1, errors.New("this playlist has no subtitles")
		}
		if err := checkSubLangs(preset.Subs, available); err != nil {
			return 1, err
		}
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
		var pe *picker.PanicError
		if errors.As(err, &pe) {
			reportCrash(pe.Value)
			if showTrace {
				fmt.Fprint(os.Stderr, strings.ReplaceAll(pe.Trace, "\r\n", "\n"))
			}
			return 2, nil
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
	summary = sanitize.Text(summary)
	fmt.Fprintf(os.Stderr, "▶ %s\n", summary)
	logf("source: %s", src.url)
	logf("variant: %s", stream.Variant.URI)

	sweepPlaylists(logf)
	path, err := writePlaylist(out, dir, src.url, summary)
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
	f := &discover.Fetcher{Client: &http.Client{Timeout: o.timeout}, Logf: logf}
	src := &source{url: o.url}

	if src.url == "" {
		logf("discovering stream via %s", discover.EventsPage)
		ev, err := f.Find(ctx, o.asl)
		if err != nil {
			return nil, nil, err
		}
		src.url, src.description = ev.URL, ev.Description
		src.badge = strings.ToUpper(ev.State)
		logf("page state: %q", ev.State)
	}

	var body []byte
	var base *url.URL
	p, local, err := localPath(o.url)
	if err != nil {
		return nil, nil, err
	}
	if local {
		if body, err = readLocal(p); err != nil {
			return nil, nil, err
		}
		if abs, err := filepath.Abs(p); err == nil {
			abs = filepath.ToSlash(abs)
			if !strings.HasPrefix(abs, "/") {
				abs = "/" + abs // C:/x becomes file:///C:/x
			}
			base = &url.URL{Scheme: "file", Path: abs}
		}
	} else {
		u, err := url.Parse(src.url)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, nil, usageError{fmt.Errorf("--url %q is neither an http(s) URL nor an existing file", src.url)}
		}
		logf("fetching %s", src.url)
		if body, err = f.Fetch(ctx, "the stream's playlist", src.url); err != nil {
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

// localPath returns the file a --url value names: a file:// URL or an
// existing path. local is false for anything else, including no --url.
func localPath(s string) (path string, local bool, err error) {
	if s == "" {
		return "", false, nil
	}
	if strings.HasPrefix(strings.ToLower(s), "file:") {
		u, err := url.Parse(s)
		if err != nil {
			return "", true, usageError{fmt.Errorf("--url: %w", err)}
		}
		path = u.Path
		remote := u.Host != "" && u.Host != "localhost"
		switch {
		case path == "":
			return "", true, usageError{fmt.Errorf("--url %q: want file:///absolute/path", s)}
		case runtime.GOOS == "windows" && remote:
			path = "//" + u.Host + path // UNC share
		case runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':':
			path = path[1:] // /C:/x
		case remote:
			return "", true, usageError{fmt.Errorf("--url %q: file URLs on other hosts are not supported", s)}
		}
		return filepath.FromSlash(path), true, nil
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return "", false, nil
	}
	_, err = os.Stat(s)
	return s, err == nil, nil
}

// readLocal reads a playlist file, refusing anything but a regular file:
// a FIFO would block forever and a device like /dev/zero never ends.
func readLocal(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := discover.ReadLimited(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return b, nil
}

// checkSubLangs fails early when --sub-lang names no available track, so
// the picker isn't shown for a selection that can't be built.
func checkSubLangs(subs *catalog.Subs, available []*hls.Media) error {
	if subs == nil || len(subs.Langs) == 0 {
		return nil
	}
	var langs []string
	for _, md := range available {
		if subs.Matches(md) {
			return nil
		}
		langs = append(langs, md.Language())
	}
	return fmt.Errorf("no subtitles match %s (available: %s)", strings.Join(subs.Langs, ", "), strings.Join(langs, ", "))
}

// pickBest returns the highest-bandwidth stream of the preset's range and
// audio, SDR (H.264) and AAC when they are not set.
func pickBest(c *catalog.Catalog, p picker.Preset) (*catalog.Stream, error) {
	r, a := catalog.SDR, catalog.AAC
	if p.Range != nil {
		r = *p.Range
	}
	if p.Audio != nil {
		a = *p.Audio
	}
	if s := c.Select(catalog.Filter{Range: &r, Audio: &a, MaxHeight: p.MaxHeight}); len(s) > 0 {
		return s[0], nil
	}
	limit := ""
	if p.MaxHeight > 0 {
		limit = fmt.Sprintf(" up to %dp", p.MaxHeight)
	}
	return nil, fmt.Errorf("no %s + %s stream%s in this playlist; run with --list to see what is available", r, a, limit)
}

// Temporary playlists are named playlistPrefix + a shortid + ".m3u8".
const playlistPrefix = "apple-event-"

// writePlaylist writes m to a new, uniquely named file in dir.
func writePlaylist(m *hls.Master, dir, src, summary string) (string, error) {
	for attempt := 0; ; attempt++ {
		path := filepath.Join(dir, playlistPrefix+shortid.New()+".m3u8")
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

// sweepPlaylists deletes temporary playlists from runs that never got to
// clean up (killed, crashed, power lost). Kept playlists go to Downloads or
// the --keep-playlist folder, so a temp file with exactly our name pattern
// is disposable once it is a day old, far longer than any event.
func sweepPlaylists(logf func(string, ...any)) {
	dir := os.TempDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		id, ok := strings.CutPrefix(e.Name(), playlistPrefix)
		id, ok2 := strings.CutSuffix(id, ".m3u8")
		if !ok || !ok2 || !shortid.Valid(id) || !e.Type().IsRegular() {
			continue
		}
		if info, err := e.Info(); err != nil || time.Since(info.ModTime()) < 24*time.Hour {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if err := os.Remove(p); err == nil {
			logf("removed stale playlist %s", p)
		}
	}
}

// keepFolder returns the folder for --keep-playlist, creating it if
// needed: dir, with a leading ~ meaning the home folder (shells don't
// expand it after = or on Windows, and a folder literally named ~ is a
// trap), or the user's Downloads folder when dir is empty.
func keepFolder(dir string) (string, error) {
	if dir == "" {
		d, err := userdirs.Downloads()
		if err != nil {
			return "", fmt.Errorf("--keep-playlist: %w; name a folder instead", err)
		}
		dir = d
	} else if rest, ok := strings.CutPrefix(dir, "~"); ok && (rest == "" || os.IsPathSeparator(rest[0])) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("--keep-playlist %s: %w", dir, err)
		}
		dir = filepath.Join(home, rest)
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs // so "Playlist kept at" shows where it really is
	}
	if st, err := os.Stat(dir); err == nil && !st.IsDir() {
		return "", fmt.Errorf("--keep-playlist: %s is a file, not a folder", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return "", fmt.Errorf("--keep-playlist: couldn't create %s: %w", dir, err)
	}
	return dir, nil
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
			catalog.Mbps(s.Bandwidth()), catalog.Mbps(s.AverageBandwidth()), sanitize.Text(s.VideoCodec))
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
		fmt.Fprintf(w, "\nSubtitles (--sub-lang): %s", sanitize.Text(strings.Join(langs, ", ")))
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
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
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
		switch {
		case math.IsNaN(sec) || sec <= 0:
			return errors.New("must be positive")
		case sec > math.MaxInt64/float64(time.Second):
			return errors.New("too large")
		}
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

// keepFlag is --keep-playlist [FOLDER]. On its own (Set("true")) it keeps
// the playlist in Downloads; --keep-playlist=FOLDER keeps it in FOLDER.
type keepFlag struct{ o *options }

func (keepFlag) IsBoolFlag() bool { return true }
func (keepFlag) String() string   { return "" }

func (k keepFlag) Set(s string) error {
	switch s {
	case "true":
		k.o.keep = true
	case "false":
		k.o.keep, k.o.keepDir = false, ""
	default:
		k.o.keep, k.o.keepDir = true, s
	}
	return nil
}

// joinKeepFolder lets --keep-playlist take its folder as the next argument.
// The flag package has no optional values, so "--keep-playlist F" becomes
// "--keep-playlist=F" when F is not empty and doesn't start with "-" (this
// app takes no other bare arguments). Nothing after "--" is touched: that
// belongs to the player.
func joinKeepFolder(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return append(out, args[i:]...)
		}
		if (a == "--keep-playlist" || a == "-keep-playlist") && i+1 < len(args) &&
			args[i+1] != "" && !strings.HasPrefix(args[i+1], "-") {
			i++
			a += "=" + args[i]
		}
		out = append(out, a)
	}
	return out
}

func parseFlags(args []string) (*options, error) {
	o := &options{timeout: 20 * time.Second}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.BoolVar(&o.best, "best", false, "")
	fs.StringVar(&o.url, "url", "", "")
	fs.BoolVar(&o.noLaunch, "no-launch", false, "")
	fs.Var(keepFlag{o}, "keep-playlist", "")
	fs.StringVar(&o.mpv, "mpv", "", "")
	fs.StringVar(&o.mpvnet, "mpvnet", "", "")
	fs.Var(secondsFlag{&o.timeout}, "timeout", "")
	fs.BoolVar(&o.verbose, "verbose", false, "")
	fs.BoolVar(&o.verbose, "v", false, "")
	fs.BoolVar(&o.showVersion, "version", false, "")
	fs.BoolVar(&o.subs, "subs", false, "")
	fs.BoolVar(&o.allSubs, "all-subs", false, "")
	fs.StringVar(&o.subLang, "sub-lang", "", "")
	fs.BoolVar(&o.ad, "ad", false, "")
	fs.BoolVar(&o.asl, "asl", false, "")
	fs.BoolVar(&o.list, "list", false, "")
	fs.StringVar(&o.rangeName, "range", "", "")
	fs.StringVar(&o.audioName, "audio", "", "")
	fs.StringVar(&o.maxRes, "max-res", "", "")
	help := fs.Bool("help", false, "")
	fs.BoolVar(help, "h", false, "")

	args = joinKeepFolder(args)
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
	if o.subLang != "" && len(splitLangs(o.subLang)) == 0 {
		return nil, usageError{fmt.Errorf("--sub-lang %q names no language", o.subLang)}
	}
	if o.subLang != "" && o.allSubs {
		return nil, usageError{errors.New("--sub-lang and --all-subs cannot be combined")}
	}
	if o.subs && o.best && o.subLang == "" && !o.allSubs {
		return nil, usageError{errors.New("--subs shows the subtitles menu, which --best skips; use --all-subs or --sub-lang with --best")}
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
  video (SDR, SDR [HEVC], HDR10, Dolby Vision) › audio › resolution › bitrate
  (plus a subtitles step after audio with --subs)
Use the arrow keys (or j/k), Enter to choose, ←/Esc to go back, q to quit; the
mouse works too (click to choose, wheel to scroll, right-click to go back).

Selection:
  --best              pick the highest-bitrate stream without asking
                      (SDR + AAC unless --range/--audio say otherwise)
  --range RANGE       sdr | hevc | hdr10 | dv           (skips that menu)
  --audio AUDIO       aac | atmos                       (skips that menu)
  --max-res RES       highest resolution to offer, e.g. 1080, 1440p, 4k
  --subs              ask which subtitles to include (picker only)
  --sub-lang LANGS    include these subtitle languages, comma-separated,
                      e.g. en or en,fr ("en" also matches en-GB etc.)
  --all-subs          include every subtitle language
                      (subtitles are left out unless one of these is given)
  --ad                use audio-description tracks instead of regular audio
  --list              print the available streams and exit

Source:
  --url URL           use this master playlist (URL or local file) instead
                      of discovering it from apple.com/apple-events
  --asl               use the American Sign Language version of the event
  --timeout SEC       network timeout in seconds or as a duration (default 20)

Playback:
  --no-launch         write the playlist and print its path; don't play
  --keep-playlist [FOLDER]
                      keep the playlist in FOLDER (created if needed), or
                      in your Downloads folder, instead of deleting it when
                      the player exits
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
  %[1]s --subs
  %[1]s --best --sub-lang en --ad
  %[1]s --url https://example.com/master.m3u8 --no-launch
  %[1]s --best --keep-playlist
  %[1]s --best --keep-playlist Videos/Playlists
  %[1]s --best -- --fs --volume=70
`, name, buildVersion())
}
