//go:build !windows

package fsutil

import (
	"fmt"
	"os"
	"syscall"
)

func EnsureSingleLink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if ok && stat.Nlink > 1 {
		return fmt.Errorf("managed file has multiple hard links: %s", path)
	}
	return nil
}
