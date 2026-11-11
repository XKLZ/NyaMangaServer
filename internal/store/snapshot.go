package store

import (
	"context"
	"os"
	"path/filepath"
)

func (s *Store) PrivateSnapshot(ctx context.Context, dir, prefix string) error {
	root := filepath.Join(dir, "upgrade-snapshots")
	if e := os.MkdirAll(root, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(root, prefix+"*.sqlite")
	if e != nil {
		return e
	}
	name := f.Name()
	if e = f.Close(); e != nil {
		return e
	}
	if _, e = s.Write.ExecContext(ctx, "VACUUM INTO ?", name); e != nil {
		os.Remove(name)
		return e
	}
	return nil
}
