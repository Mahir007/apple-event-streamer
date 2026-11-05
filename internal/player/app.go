package player

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"
)

const (
	appComponent = "is.xyz.mpv/.MPVActivity"
	playlistPath = "/apple-event.m3u8"
	hlsType      = "application/vnd.apple.mpegurl"
)

var (
	// loadTimeout is how long the mpv app gets to ask for the playlist. It can
	// take a while to start on a slow device.
	loadTimeout = time.Minute
	// settleTime is how long to keep serving after the last request. Some
	// builds of mpv fetch the playlist more than once while opening it.
	settleTime = 5 * time.Second
)

// OpenInApp serves playlist on 127.0.0.1, opens it in the mpv app and returns
// once the app has fetched it. Android has no way to wait for the app to close
// or to read its exit code, so playback carries on after this returns.
//
// The app can't be given a file: Termux's temporary folder is private to it,
// and shared storage needs a permission granted in both apps. Over HTTP it
// needs neither.
func (p *Player) OpenInApp(playlist []byte, title string, logf func(string, ...any)) (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 1, fmt.Errorf("serving the playlist: %w", err)
	}
	fetched := make(chan struct{}, 1)
	srv := &http.Server{Handler: playlistHandler(playlist, fetched), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(l)
	defer srv.Close()

	url := "http://" + l.Addr().String() + playlistPath
	logf("serving the playlist at %s", url)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, interruptSignals...)
	defer signal.Stop(sigs)

	am := command(p.Path, intentArgs(url, title)...)
	started := make(chan error, 1)
	go func() {
		out, err := am.CombinedOutput()
		started <- amError(out, err)
	}()

	timeout := time.After(loadTimeout)
	var settled <-chan time.Time
	for {
		select {
		case err := <-started:
			if err != nil {
				return 1, err
			}
		case <-fetched:
			logf("the mpv app fetched the playlist")
			timeout = nil
			settled = time.After(settleTime)
		case <-settled:
			return 0, nil
		case <-timeout:
			return 1, errors.New("the mpv app didn't ask for the playlist; check that it opened")
		case <-sigs:
			return 130, nil
		}
	}
}

func playlistHandler(playlist []byte, fetched chan<- struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != playlistPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", hlsType)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(playlist))
		if r.Method == http.MethodGet {
			select {
			case fetched <- struct{}{}:
			default:
			}
		}
	})
}

// intentArgs returns the am arguments that open url in the mpv app. An intent
// can only carry a URI, a title and a few other fields, not mpv options.
func intentArgs(url, title string) []string {
	args := []string{"start", "-a", "android.intent.action.VIEW", "-t", hlsType, "-n", appComponent, "-d", url}
	if title != "" {
		args = append(args, "--es", "title", title)
	}
	return args
}

// amError finds a failure in am's output, given how it exited.
func amError(out []byte, err error) error {
	for _, line := range strings.Split(string(out), "\n") {
		msg, ok := strings.CutPrefix(strings.TrimSpace(line), "Error: ")
		if !ok {
			continue
		}
		// Older versions of am give the number of START_CLASS_NOT_FOUND.
		if strings.Contains(msg, "does not exist") || strings.Contains(msg, "unknown error code -2") {
			return errors.New("the mpv app isn't installed; install it or pass --mpv")
		}
		return fmt.Errorf("the mpv app didn't start: %s", msg)
	}
	if err != nil {
		return fmt.Errorf("the mpv app didn't start: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
