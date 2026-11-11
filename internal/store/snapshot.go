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
	// VACUUM cannot run inside the principal-bound write transaction. Pin the
	// exclusive writer connection only for this offline snapshot operation;
	// ordinary business writes must keep using the authorization wrapper.
	conn, e := s.Write.Conn(ctx)
	if e != nil {
		os.Remove(name)
		return e
	}
	defer conn.Close()
	if _, e = conn.ExecContext(ctx, "VACUUM INTO ?", name); e != nil {
		os.Remove(name)
		return e
	}
	return nil
}
