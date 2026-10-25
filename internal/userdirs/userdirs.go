// Package userdirs finds the user's Downloads folder the way each platform
// defines it: the Downloads known folder on Windows (which follows the user
// if they move it), XDG_DOWNLOAD_DIR from user-dirs.dirs on Linux and other
// Unix desktops (often translated, e.g. ~/Téléchargements), ~/Downloads on
// macOS.
package userdirs

import (
	"bufio"
	"bytes"
	"errors"
	pathpkg "path"
	"path/filepath"
	"strings"
)

// Downloads returns the absolute path of the user's Downloads folder. The
// folder may not exist yet.
func Downloads() (string, error) {
	dir, err := downloads()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(dir) {
		return "", errors.New("couldn't find your Downloads folder")
	}
	return filepath.Clean(dir), nil
}

// xdgDownloadDir returns XDG_DOWNLOAD_DIR from the contents of a
// user-dirs.dirs file, or "" when it is missing, disabled (set to $HOME
// itself) or not in the documented form: a double-quoted "$HOME/..." or
// absolute path. XDG paths are Unix paths, hence package path.
func xdgDownloadDir(config []byte, home string) string {
	sc := bufio.NewScanner(bytes.NewReader(config))
	for sc.Scan() {
		v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "XDG_DOWNLOAD_DIR=")
		if !ok || len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
			continue
		}
		var b strings.Builder // unquote: \x is x
		for i := 1; i < len(v)-1; i++ {
			if v[i] == '\\' && i+1 < len(v)-1 {
				i++
			}
			b.WriteByte(v[i])
		}
		path := b.String()
		if rest, ok := strings.CutPrefix(path, "$HOME"); ok && (rest == "" || rest[0] == '/') {
			path = home + rest
		}
		if !pathpkg.IsAbs(path) || pathpkg.Clean(path) == pathpkg.Clean(home) {
			return ""
		}
		return path
	}
	return ""
}
