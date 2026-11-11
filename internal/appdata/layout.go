// Package appdata owns the application data lock and explicit development reset.
package appdata

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const ResetMarker = "reset-in-progress"

// NoSymlinks rejects redirecting any component of a managed path. Missing
// components are allowed only so a fresh instance can create its directories.
func NoSymlinks(path string) error {
	absolute, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(absolute, current), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, e := os.Lstat(current)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("managed path contains a symbolic link: %s", current)
		}
	}
	return nil
}

type Lock struct{ file *os.File }

func Acquire(dir string) (*Lock, error) {
	if e := NoSymlinks(dir); e != nil {
		return nil, e
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	filename := filepath.Join(dir, "app.lock")
	if e := NoSymlinks(filename); e != nil {
		return nil, e
	}
	file, e := os.OpenFile(filename, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	info, e := file.Stat()
	if e != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("data lock must be a regular file")
	}
	if e = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		file.Close()
		return nil, errors.New("application data is already in use; stop this instance normally before reset")
	}
	return &Lock{file}, nil
}
func (l *Lock) Close() error {
	unlock := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	return errors.Join(unlock, l.file.Close())
}
func CheckReady(dir string) error {
	_, e := os.Lstat(filepath.Join(dir, ResetMarker))
	if e == nil {
		return errors.New("development reset incomplete: run make reset-dev to finish")
	}
	if !os.IsNotExist(e) {
		return e
	}
	for _, name := range []string{"app.sqlite", "app.sqlite-wal", "app.sqlite-shm", "upgrade-snapshots", "cache"} {
		if e := NoSymlinks(filepath.Join(dir, name)); e != nil {
			return e
		}
	}
	return nil
}
