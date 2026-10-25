package userdirs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestXDGDownloadDir(t *testing.T) {
	const home = "/home/u"
	for config, want := range map[string]string{
		`XDG_DOWNLOAD_DIR="$HOME/Downloads"`: "/home/u/Downloads",
		"# comment\nXDG_DESKTOP_DIR=\"$HOME/Desktop\"\nXDG_DOWNLOAD_DIR=\"$HOME/Téléchargements\"\n": "/home/u/Téléchargements",
		`  XDG_DOWNLOAD_DIR="/data/dl"  `:       "/data/dl",
		`XDG_DOWNLOAD_DIR="$HOME/My \"Files\""`: `/home/u/My "Files"`,
		`XDG_DOWNLOAD_DIR="$HOME"`:              "", // disabled
		`XDG_DOWNLOAD_DIR="$HOME/"`:             "",
		`XDG_DOWNLOAD_DIR="$HOMEWORK/x"`:        "", // not $HOME
		`XDG_DOWNLOAD_DIR="Downloads"`:          "", // relative
		`XDG_DOWNLOAD_DIR=$HOME/Downloads`:      "", // unquoted
		`XDG_DOWNLOAD_DIR="`:                    "",
		`#XDG_DOWNLOAD_DIR="/x"`:                "",
		``:                                      "",
	} {
		if got := xdgDownloadDir([]byte(config), home); got != want {
			t.Errorf("xdgDownloadDir(%q) = %q, want %q", config, got, want)
		}
	}
}

func TestDownloads(t *testing.T) {
	dir, err := Downloads()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("%q is not absolute", dir)
	}
	t.Logf("Downloads: %s", dir)
}

func TestDownloadsFollowsXDGConfig(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("XDG user dirs are for Linux and other Unix desktops")
	}
	home, config := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", config)
	if got, _ := Downloads(); got != filepath.Join(home, "Downloads") {
		t.Errorf("without user-dirs.dirs: %q", got)
	}
	os.WriteFile(filepath.Join(config, "user-dirs.dirs"), []byte(`XDG_DOWNLOAD_DIR="$HOME/Téléchargements"`), 0o644)
	if got, _ := Downloads(); got != filepath.Join(home, "Téléchargements") {
		t.Errorf("with user-dirs.dirs: %q", got)
	}
}
