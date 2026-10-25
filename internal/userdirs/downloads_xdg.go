//go:build !windows && !darwin

package userdirs

import (
	"os"
	"path/filepath"
)

func downloads() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(config) {
		config = filepath.Join(home, ".config")
	}
	if b, err := os.ReadFile(filepath.Join(config, "user-dirs.dirs")); err == nil {
		if dir := xdgDownloadDir(b, home); dir != "" {
			return dir, nil
		}
	}
	return filepath.Join(home, "Downloads"), nil
}
