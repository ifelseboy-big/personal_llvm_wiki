package fsutil

import (
	"errors"
	"os"
	"path/filepath"
)

// AtomicWrite stages data beside the target and replaces it. On platforms where
// rename cannot replace an existing file, it uses a same-directory backup and
// restores it if the final rename fails.
func AtomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".llm-wiki-stage-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err == nil {
		syncParent(path)
		return nil
	} else if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
		return err
	}
	backup := tmp + ".previous"
	if err := os.Rename(path, backup); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Rename(backup, path)
		return err
	}
	_ = os.Remove(backup)
	syncParent(path)
	return nil
}

func syncParent(path string) {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return
	}
	defer dir.Close()
	_ = dir.Sync()
}
