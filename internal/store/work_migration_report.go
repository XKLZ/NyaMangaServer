package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type workMigrationReport struct {
	Application string            `json:"application"`
	Baseline    string            `json:"baseline"`
	Schema      int               `json:"schema"`
	State       string            `json:"state"`
	Editions    int               `json:"edition_count"`
	Works       int               `json:"work_count"`
	Groups      map[string]string `json:"legacy_group_to_work"`
}

func prepareWorkMigrationReport(ctx context.Context, tx *sql.Tx) (*workMigrationReport, error) {
	r := &workMigrationReport{Application: ApplicationID, Baseline: BaselineID, Schema: 7, State: "prepared", Groups: map[string]string{}}
	rows, e := scopeRows(ctx, tx, "SELECT id,item_id FROM version_groups ORDER BY id")
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var id, work string
		if e = rows.Scan(&id, &work); e != nil {
			rows.Close()
			return nil, e
		}
		r.Groups[id] = work
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM books").Scan(&r.Editions); e != nil {
		return nil, e
	}
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM logical_items").Scan(&r.Works); e != nil {
		return nil, e
	}
	return r, nil
}
func writeWorkMigrationReport(dir string, r *workMigrationReport) error {
	parent := filepath.Join(dir, "upgrade-snapshots")
	if st, e := os.Lstat(parent); e == nil {
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return errors.New("unsafe migration report directory")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if e := os.MkdirAll(parent, 0700); e != nil {
		return e
	}
	if e := os.Chmod(parent, 0700); e != nil {
		return e
	}
	name := filepath.Join(parent, "work-edition-v7.json")
	if st, e := os.Lstat(name); e == nil {
		if !st.Mode().IsRegular() {
			return errors.New("unsafe migration report file")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	data, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(parent, ".work-edition-report-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(append(data, '\n'))
	}
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if e = os.Rename(tmp, name); e != nil {
		return e
	}
	d, e := os.Open(parent)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func finishWorkMigrationReport(dir string) error {
	name := filepath.Join(dir, "upgrade-snapshots", "work-edition-v7.json")
	st, e := os.Lstat(name)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() {
		return errors.New("unsafe migration report file")
	}
	b, e := os.ReadFile(name)
	if e != nil {
		return e
	}
	var r workMigrationReport
	if e = json.Unmarshal(b, &r); e != nil {
		return e
	}
	if r.Application != ApplicationID || r.Baseline != BaselineID || r.Schema != 7 {
		return fmt.Errorf("migration report identity mismatch")
	}
	if r.State == "complete" {
		return nil
	}
	if r.State != "prepared" {
		return errors.New("invalid migration report state")
	}
	r.State = "complete"
	return writeWorkMigrationReport(dir, &r)
}
