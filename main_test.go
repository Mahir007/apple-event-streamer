package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
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
