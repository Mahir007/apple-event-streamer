package userdirs

import "os"

func downloads() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return termuxDownloads(home)
}
