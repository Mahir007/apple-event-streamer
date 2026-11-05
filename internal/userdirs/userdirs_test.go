package userdirs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestTermuxDownloads(t *testing.T) {
	home := t.TempDir()
	want := filepath.Join(home, "storage", "downloads")
	if _, err := termuxDownloads(home); err == nil || !strings.Contains(err.Error(), "termux-setup-storage") {
		t.Errorf("before termux-setup-storage: %v", err)
	}
	if _, err := os.Stat(want); err == nil {
		t.Error("the folder was created")
	}

	if err := os.MkdirAll(filepath.Join(home, "storage"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, want); err != nil {
		t.Skip("can't make symlinks here:", err)
	}
	if got, err := termuxDownloads(home); err != nil || got != want {
		t.Errorf("after termux-setup-storage: %q, %v", got, err)
	}
}

func TestDownloads(t *testing.T) {
	if runtime.GOOS == "android" {
		t.Skip("needs termux-setup-storage")
	}
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
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" || runtime.GOOS == "android" {
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
