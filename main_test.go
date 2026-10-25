package main

import (
	"bytes"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mahir007/apple-event-streamer/internal/hls"
)

func TestKeepPlaylistFlag(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		keep       bool
		dir        string
		best       bool
		playerArgs []string
		wantErr    string
	}{
		{args: []string{}, keep: false},
		{args: []string{"--keep-playlist"}, keep: true},
		{args: []string{"-keep-playlist"}, keep: true},
		{args: []string{"--keep-playlist", "Videos/Playlists"}, keep: true, dir: "Videos/Playlists"},
		{args: []string{"--keep-playlist=Videos/Playlists"}, keep: true, dir: "Videos/Playlists"},
		{args: []string{"--keep-playlist", "dir with space"}, keep: true, dir: "dir with space"},
		{args: []string{"--keep-playlist", "--best"}, keep: true, best: true},
		{args: []string{"--best", "--keep-playlist", "x", "--no-launch"}, keep: true, dir: "x", best: true},
		{args: []string{"--keep-playlist=-odd"}, keep: true, dir: "-odd"},
		{args: []string{"--keep-playlist", "--", "--fs"}, keep: true, playerArgs: []string{"--fs"}},
		{args: []string{"--best", "--", "--keep-playlist", "x"}, best: true, playerArgs: []string{"--keep-playlist", "x"}},
		{args: []string{"--keep-playlist=false"}, keep: false},
		{args: []string{"--keep-playlist", ""}, wantErr: `unexpected argument ""`},
		{args: []string{"--keep-playlist", "a", "b"}, wantErr: `unexpected argument "b"`},
		{args: []string{"stray", "--keep-playlist"}, wantErr: `unexpected argument "stray"`},
	} {
		o, err := parseFlags(tc.args)
		switch {
		case tc.wantErr != "":
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%q: err = %v, want %q", tc.args, err, tc.wantErr)
			}
		case err != nil:
			t.Errorf("%q: %v", tc.args, err)
		case o.keep != tc.keep || o.keepDir != tc.dir || o.best != tc.best || !slices.Equal(o.playerArgs, tc.playerArgs):
			t.Errorf("%q: keep=%v dir=%q best=%v player=%q; want %v %q %v %q",
				tc.args, o.keep, o.keepDir, o.best, o.playerArgs, tc.keep, tc.dir, tc.best, tc.playerArgs)
		}
	}
}

func TestKeepFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)        // Unix
	t.Setenv("USERPROFILE", home) // Windows

	nested := filepath.Join(t.TempDir(), "a", "b")
	if got, err := keepFolder(nested); err != nil || got != nested {
		t.Fatalf("nested: %q, %v", got, err)
	}
	if st, err := os.Stat(nested); err != nil || !st.IsDir() {
		t.Fatalf("nested folder not created: %v", err)
	}

	want := filepath.Join(home, "Videos", "Playlists")
	if got, err := keepFolder("~/Videos/Playlists"); err != nil || got != want {
		t.Fatalf("~: %q, %v; want %q", got, err, want)
	}
	if got, err := keepFolder("~"); err != nil || got != home {
		t.Fatalf("bare ~: %q, %v", got, err)
	}
	// "~x" is not the home folder; it is a folder in the current one.
	cwd := t.TempDir()
	t.Chdir(cwd)
	if got, err := keepFolder("~x"); err != nil || got != filepath.Join(cwd, "~x") {
		t.Fatalf("~x: %q, %v", got, err)
	}

	file := filepath.Join(t.TempDir(), "f")
	os.WriteFile(file, nil, 0o644)
	if _, err := keepFolder(file); err == nil || !strings.Contains(err.Error(), "is a file, not a folder") {
		t.Fatalf("file in the way: %v", err)
	}
}

func TestKeepFolderPermission(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix permissions and a non-root user")
	}
	locked := t.TempDir()
	os.Chmod(locked, 0o555)
	defer os.Chmod(locked, 0o755)
	_, err := keepFolder(filepath.Join(locked, "x"))
	if err == nil || !strings.Contains(err.Error(), "couldn't create "+filepath.Join(locked, "x")+": permission denied") {
		t.Fatalf("err = %v", err)
	}
}

// TestRun goes through run without a player: reading a local playlist,
// choosing with presets, sweeping old playlists and writing the new one.
func TestRun(t *testing.T) {
	tmp := t.TempDir()
	for _, env := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(env, tmp)
	}
	touch := func(name string, age time.Duration) string {
		p := filepath.Join(tmp, name)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		mtime := time.Now().Add(-age)
		os.Chtimes(p, mtime, mtime)
		return p
	}
	stale := touch("apple-event-0123456789abcdef.m3u8", 48*time.Hour)
	fresh := touch("apple-event-fedcba9876543210.m3u8", 0)
	other := touch("apple-event-notours.m3u8", 48*time.Hour)
	ref := touch("ref", 0) // made the way playlists are, to compare modes

	code, err := run([]string{"--url", "internal/hls/testdata/vod.m3u8", "--best",
		"--range", "dv", "--audio", "atmos", "--sub-lang", "en", "--no-launch"})
	if err != nil || code != 0 {
		t.Fatalf("run = %d, %v", code, err)
	}

	if _, err := os.Stat(stale); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stale playlist not swept: %v", err)
	}
	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was swept: %v", p, err)
		}
	}

	all, _ := filepath.Glob(filepath.Join(tmp, "apple-event-*.m3u8"))
	written := slices.DeleteFunc(all, func(p string) bool { return p == fresh || p == other })
	if len(written) != 1 {
		t.Fatalf("want one new playlist, got %q", written)
	}
	st, _ := os.Stat(written[0])
	refSt, _ := os.Stat(ref)
	if st.Mode() != refSt.Mode() {
		t.Errorf("playlist mode %v, want %v", st.Mode(), refSt.Mode())
	}

	b, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatal(err)
	}
	m, err := hls.Parse(bytes.NewReader(b), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Variants) != 1 || !slices.ContainsFunc(m.Variants[0].Codecs(), func(c string) bool { return strings.HasPrefix(c, "dvh") }) {
		t.Errorf("want one Dolby Vision variant, got %d: %v", len(m.Variants), m.Variants[0].Codecs())
	}
	var subs []string
	for _, md := range m.Media {
		if md.Type() == "SUBTITLES" {
			subs = append(subs, md.Language())
		}
	}
	if !slices.Equal(subs, []string{"en"}) {
		t.Errorf("subtitles %q, want [en]", subs)
	}
	if !strings.Contains(string(b), "## Selection: Dolby Vision · Dolby Atmos") {
		t.Errorf("no selection comment in\n%s", b)
	}

	abs, _ := filepath.Abs("internal/hls/testdata/vod.m3u8")
	fileURL := (&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(abs), "/")}).String()
	if code, err := run([]string{"--url", fileURL, "--list"}); err != nil || code != 0 {
		t.Errorf("--list with %s = %d, %v", fileURL, code, err)
	}
	if _, err := run([]string{"--url", "missing.m3u8", "--best", "--no-launch"}); err == nil ||
		!strings.Contains(err.Error(), "neither an http(s) URL nor an existing file") {
		t.Errorf("missing file: %v", err)
	}
}
