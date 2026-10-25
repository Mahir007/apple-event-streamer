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
	return filepath.Join(home, "Downloads"), nil
}
