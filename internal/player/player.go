// Package player locates mpv / mpv.net and hands the terminal over to it.
package player

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ProtocolWhitelist lets lavf's HLS demuxer follow the https URLs inside a
// local playlist; by default it only allows file,crypto,data for local files.
const ProtocolWhitelist = `--demuxer-lavf-o=protocol_whitelist="file,crypto,data,https,http,tls,tcp"`

// experimentalSubs is appended to the demuxer options when the playlist
// carries subtitles: lavf's HLS demuxer ignores subtitle renditions
// ("Can't support the subtitle") unless strict compliance is relaxed.
const experimentalSubs = ",strict=experimental"

// Kind identifies the player family.
type Kind int

const (
	MPV Kind = iota
	MPVNet
)

func (k Kind) String() string {
	if k == MPVNet {
		return "mpvnet"
	}
	return "mpv"
}

// Player is a resolved player executable.
type Player struct {
	Kind Kind
	Path string
}

// Find resolves the player. Explicit paths win; with both given, mpvnet is
// used on Windows and mpv elsewhere. Otherwise Windows prefers mpvnet on
// PATH and falls back to mpv; other systems use mpv.
func Find(mpvPath, mpvnetPath string) (*Player, error) {
	windows := runtime.GOOS == "windows"
	switch {
	case mpvnetPath != "" && (mpvPath == "" || windows):
		return explicit(MPVNet, mpvnetPath)
	case mpvPath != "":
		return explicit(MPV, mpvPath)
	}

	if windows {
		if p := lookup("mpvnet", mpvnetLocations()); p != "" {
			return &Player{Kind: MPVNet, Path: p}, nil
		}
	}
	if p := lookup("mpv", mpvLocations()); p != "" {
		return &Player{Kind: MPV, Path: p}, nil
	}
	if windows {
		return nil, errors.New("neither mpvnet nor mpv was found on PATH; install one or pass --mpvnet/--mpv")
	}
	return nil, errors.New("mpv was not found on PATH; install it or pass --mpv")
}

func explicit(k Kind, p string) (*Player, error) {
	resolved, err := exec.LookPath(p)
	if err != nil {
		return nil, fmt.Errorf("--%s %s: %w", k, p, err)
	}
	return &Player{Kind: k, Path: resolved}, nil
}

// lookup searches PATH, then well-known install locations.
func lookup(name string, extra []string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, p := range extra {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func mpvnetLocations() []string {
	var out []string
	for _, env := range []string{"LOCALAPPDATA", "ProgramFiles", "ProgramFiles(x86)"} {
		if dir := os.Getenv(env); dir != "" {
			sub := "mpv.net"
			if env == "LOCALAPPDATA" {
				sub = filepath.Join("Programs", "mpv.net")
			}
			out = append(out, filepath.Join(dir, sub, "mpvnet.exe"))
		}
	}
	return out
}

func mpvLocations() []string {
	switch runtime.GOOS {
	case "darwin":
		out := []string{"/Applications/mpv.app/Contents/MacOS/mpv", "/opt/homebrew/bin/mpv", "/usr/local/bin/mpv"}
		if home, err := os.UserHomeDir(); err == nil {
			out = append(out, filepath.Join(home, "Applications/mpv.app/Contents/MacOS/mpv"))
		}
		return out
	case "windows":
		var out []string
		if dir := os.Getenv("ProgramFiles"); dir != "" {
			out = append(out, filepath.Join(dir, "mpv", "mpv.exe"))
		}
		if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
			out = append(out, filepath.Join(dir, "Microsoft", "WinGet", "Links", "mpv.exe"))
		}
		return out
	}
	return []string{"/usr/local/bin/mpv", "/snap/bin/mpv"}
}

// Options configure the player command line.
type Options struct {
	Title string
	// Subtitles enables lavf's experimental HLS subtitle support.
	Subtitles bool
	// SubLangs, when set, become --slang so mpv selects them on start.
	SubLangs []string
	// Extra are passed through verbatim (arguments after "--").
	Extra []string
}

// Args builds the full argv (excluding argv[0]) for playlist.
func (p *Player) Args(playlist string, o Options) []string {
	lavf := ProtocolWhitelist
	if o.Subtitles {
		lavf += experimentalSubs
	}
	args := []string{lavf}
	if p.Kind == MPVNet {
		// mpv.net defaults to a single instance: a second launch forwards
		// its files and exits at once, which would end our wait (and delete
		// the playlist) while playback continues elsewhere.
		args = append(args, "--process-instance=multi")
	}
	if o.Title != "" {
		args = append(args, "--force-media-title="+o.Title)
	}
	if len(o.SubLangs) > 0 {
		args = append(args, "--slang="+strings.Join(o.SubLangs, ","))
	}
	args = append(args, o.Extra...)
	return append(args, playlist)
}

// Run starts the player attached to this terminal and waits for it, so the
// shell sees a single command lasting until the player quits. cleanup runs
// after the player exits. The player's exit code is returned.
//
// Interrupts are left to the player (it shares the terminal and receives
// Ctrl+C itself); if it has not exited a few seconds after one, it is
// killed, which covers GUI players such as mpv.net.
func (p *Player) Run(args []string, cleanup func()) (int, error) {
	cmd := exec.Command(p.Path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, interruptSignals...)
	defer signal.Stop(sigs)

	if err := cmd.Start(); err != nil {
		cleanup()
		return 1, fmt.Errorf("starting %s: %w", p.Kind, err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var err error
	var kill <-chan time.Time
wait:
	for {
		select {
		case err = <-done:
			break wait
		case <-sigs:
			if kill == nil {
				kill = time.After(3 * time.Second)
			}
		case <-kill:
			cmd.Process.Kill()
		}
	}
	cleanup()

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr):
		if code := exitErr.ExitCode(); code >= 0 {
			return code, nil
		}
		return 130, nil // killed by a signal
	}
	return 1, err
}
