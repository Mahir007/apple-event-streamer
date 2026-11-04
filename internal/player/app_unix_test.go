//go:build unix

package player

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// asAndroid makes this package act as it does on Android, with the test
// system's shell standing in for Android's.
func asAndroid(t *testing.T) {
	t.Helper()
	oldAndroid, oldShell, oldTimeout, oldSettle := android, systemShell, loadTimeout, settleTime
	android, systemShell, loadTimeout, settleTime = true, "/bin/sh", 5*time.Second, 200*time.Millisecond
	t.Cleanup(func() {
		android, systemShell, loadTimeout, settleTime = oldAndroid, oldShell, oldTimeout, oldSettle
	})
}

// fakeAm puts a stand-in for am first on PATH. It records its arguments, one
// per line, and then runs body. It returns the file holding the arguments.
func fakeAm(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := fmt.Sprintf("#!/bin/sh\nfor a in \"$@\"; do echo \"$a\"; done >%q\n%s\n", args, body)
	if err := os.WriteFile(filepath.Join(dir, "am"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return args
}

func noLog(string, ...any) {}

func TestFindOnAndroid(t *testing.T) {
	asAndroid(t)
	p, err := Find("", "")
	if err != nil || p.Kind != MPVApp {
		t.Fatalf("Find = %+v, %v", p, err)
	}
	// --mpv means a player in Termux, run the usual way.
	p, err = Find("/bin/sh", "")
	if err != nil || p.Kind != MPV {
		t.Fatalf("Find with --mpv = %+v, %v", p, err)
	}
}

// TestOpenInApp plays the part of the mpv app: am returns at once, then the
// playlist is fetched, twice, as mpv on the desktop does.
func TestOpenInApp(t *testing.T) {
	asAndroid(t)
	args := fakeAm(t, "")
	const playlist = "#EXTM3U\n"

	got := make(chan string, 1)
	go func() {
		var url string
		for range 100 {
			if b, err := os.ReadFile(args); err == nil {
				if i := slices.Index(strings.Fields(string(b)), "-d"); i >= 0 {
					url = strings.Fields(string(b))[i+1]
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		for range 2 {
			resp, err := http.Get(url)
			if err != nil {
				got <- err.Error()
				return
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(b) != playlist || resp.Header.Get("Content-Type") != hlsType {
				got <- fmt.Sprintf("got %q, %s", b, resp.Header.Get("Content-Type"))
				return
			}
		}
		got <- ""
	}()

	code, err := (&Player{Kind: MPVApp, Path: "am"}).OpenInApp([]byte(playlist), "Apple Event · SDR", noLog)
	if err != nil || code != 0 {
		t.Fatalf("OpenInApp = %d, %v", code, err)
	}
	if msg := <-got; msg != "" {
		t.Fatal(msg)
	}

	b, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for sc := bufio.NewScanner(strings.NewReader(string(b))); sc.Scan(); {
		lines = append(lines, sc.Text())
	}
	for _, want := range []string{"start", "android.intent.action.VIEW", hlsType, appComponent, "Apple Event · SDR"} {
		if !slices.Contains(lines, want) {
			t.Errorf("am args %q lack %q", lines, want)
		}
	}
}

func TestOpenInAppNotFetched(t *testing.T) {
	asAndroid(t)
	loadTimeout = 100 * time.Millisecond
	fakeAm(t, "")
	code, err := (&Player{Kind: MPVApp, Path: "am"}).OpenInApp(nil, "", noLog)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "didn't ask for the playlist") {
		t.Fatalf("OpenInApp = %d, %v", code, err)
	}
}

func TestOpenInAppNotInstalled(t *testing.T) {
	asAndroid(t)
	fakeAm(t, `echo 'Error type 3'; echo 'Error: Activity class {is.xyz.mpv/is.xyz.mpv.MPVActivity} does not exist.'; exit 2`)
	code, err := (&Player{Kind: MPVApp, Path: "am"}).OpenInApp(nil, "", noLog)
	if code != 1 || err == nil || !strings.Contains(err.Error(), "isn't installed") {
		t.Fatalf("OpenInApp = %d, %v", code, err)
	}
}

func TestAmError(t *testing.T) {
	for _, c := range []struct {
		out  string
		exit error
		want string
	}{
		{"Starting: Intent { act=android.intent.action.VIEW }\n", nil, ""},
		{"Warning: Activity not started, intent has been delivered to currently running top-most instance.\n", nil, ""},
		// Some versions of am exit 0 after an error.
		{"Error type 3\nError: Activity class {x} does not exist.\n", nil, "isn't installed"},
		{"Error: Activity not started, unknown error code -2\n", nil, "isn't installed"},
		{"Error: Activity not started, unknown error code -4\n", nil, "didn't start: Activity not started, unknown error code -4"},
		{"Error: something else\n", nil, "didn't start: something else"},
		{"sh: am: not found\n", fmt.Errorf("exit status 127"), "am: not found"},
	} {
		err := amError([]byte(c.out), c.exit)
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("amError(%q) = %v, want %q", c.out, err, c.want)
		}
	}
}

// TestRunOnAndroid checks that a player started through the system shell gets
// its arguments unchanged and its exit code is passed on.
func TestRunOnAndroid(t *testing.T) {
	asAndroid(t)
	out := filepath.Join(t.TempDir(), "args")
	p := script(t, fmt.Sprintf("for a in \"$@\"; do echo \"$a\"; done >%q\nexit 3", out))
	code, err := p.Run([]string{"--title=a b", `c"d`}, func() {})
	if err != nil || code != 3 {
		t.Fatalf("Run = %d, %v", code, err)
	}
	b, _ := os.ReadFile(out)
	if string(b) != "--title=a b\nc\"d\n" {
		t.Errorf("args = %q", b)
	}
}
