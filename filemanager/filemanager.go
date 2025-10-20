package filemanager

import (
	"edev/config"
	"edev/user"
	"os"
	"path/filepath"
)

// ensureDir creates the given directory path if it does not exist.
// It behaves like "mkdir -p", creating all necessary parent directories.
func ensureDir(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(absPath)
	if err == nil {
		if info.IsDir() {
			return absPath, nil
		}
		return "", os.ErrExist
	}

	if os.IsNotExist(err) {
		err = os.MkdirAll(absPath, 0o755)
		if err != nil {
			return "", err
		}
		return absPath, nil
	}

	return "", err
}

// UploadPath returns the upload directory path for the given user.
// It ensures that the directory exists, creating it if necessary.
func UploadPath(u user.User) (string, error) {
	wd := config.Cfg.UploadPath

	path := filepath.Join(
		wd,
		"uploads",
		u.ReferenceID,
		"temp")

	absPath, err := ensureDir(path)
	if err != nil {
		return "", err
	}

	return absPath, nil
}
