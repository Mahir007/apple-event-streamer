package userdirs

import "golang.org/x/sys/windows"

func downloads() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Downloads, windows.KF_FLAG_DEFAULT)
}
