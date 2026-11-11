package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"nyamangaserver.local/server/internal/appdata"
	"os"
	"path/filepath"
	"strings"
)

type OfflineMaterial struct {
	Name   string `json:"name"`
	Digest string `json:"sha256"`
	Size   int64  `json:"size"`
}
type OfflineDiagnosis struct {
	Readable      bool              `json:"readable"`
	IdentityValid bool              `json:"identity_valid"`
	LedgerValid   bool              `json:"ledger_valid"`
	Schema        int               `json:"schema"`
	Integrity     string            `json:"integrity"`
	ErrorCode     string            `json:"error_code"`
	Preserved     bool              `json:"preserved"`
	Materials     []OfflineMaterial `json:"materials,omitempty"`
}

func DiagnoseOffline(ctx context.Context, dir, out string, preserve bool, files fs.FS) (OfflineDiagnosis, error) {
	report := OfflineDiagnosis{Integrity: "not_checked"}
	var e error
	dir, e = filepath.Abs(dir)
	if e != nil {
		return report, fmt.Errorf("unsafe_data_directory")
	}
	out, e = filepath.Abs(out)
	if e != nil {
		return report, fmt.Errorf("unsafe_output")
	}
	expected := map[int]string{}
	entries, e := fs.ReadDir(files, ".")
	if e != nil {
		return report, fmt.Errorf("migration_manifest_unavailable")
	}
	for _, entry := range entries {
		var version int
		if _, e = fmt.Sscanf(entry.Name(), "%03d_", &version); e == nil && strings.HasSuffix(entry.Name(), ".sql") {
			raw, err := fs.ReadFile(files, entry.Name())
			if err != nil {
				return report, fmt.Errorf("migration_manifest_unavailable")
			}
			expected[version] = fmt.Sprintf("%x", sha256.Sum256(raw))
		}
	}

	info, e := os.Stat(dir)
	if e != nil || !info.IsDir() {
		return report, fmt.Errorf("data_directory_required")
	}
	if e = appdata.NoSymlinks(dir); e != nil {
		return report, fmt.Errorf("unsafe_data_directory")
	}
	if info, e = os.Lstat(filepath.Join(dir, "app.sqlite")); e != nil || !info.Mode().IsRegular() {
		return report, fmt.Errorf("existing_database_required")
	}
	lock, e := appdata.Acquire(dir)
	if e != nil {
		return report, fmt.Errorf("instance_in_use")
	}
	defer lock.Close()
	if !filepath.IsAbs(out) || out == dir || strings.HasPrefix(out, dir+string(filepath.Separator)) {
		return report, fmt.Errorf("separate_private_output_required")
	}
	if e = appdata.NoSymlinks(out); e != nil {
		return report, fmt.Errorf("unsafe_output")
	}
	if e = os.Mkdir(out, 0700); e != nil {
		return report, fmt.Errorf("new_private_output_required")
	}
	materials := filepath.Join(out, "materials")
	if e = os.Mkdir(materials, 0700); e != nil {
		return report, e
	}
	if !preserve {
		defer os.RemoveAll(materials)
	}
	for _, name := range []string{"app.sqlite", "app.sqlite-wal", "app.sqlite-shm", "app.sqlite-journal"} {
		source := filepath.Join(dir, name)
		if e = appdata.NoSymlinks(source); e != nil {
			return report, fmt.Errorf("unsafe_material")
		}
		f, e := os.Open(source)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return report, fmt.Errorf("material_unreadable")
		}
		stat, e := f.Stat()
		if e != nil || !stat.Mode().IsRegular() {
			f.Close()
			return report, fmt.Errorf("invalid_material")
		}
		target, e := os.OpenFile(filepath.Join(materials, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			f.Close()
			return report, e
		}
		hash := sha256.New()
		n, e := io.Copy(io.MultiWriter(target, hash), f)
		f.Close()
		sync := target.Sync()
		target.Close()
		if e != nil {
			return report, e
		}
		if sync != nil {
			return report, sync
		}
		digest := fmt.Sprintf("%x", hash.Sum(nil))
		verify, e := os.Open(source)
		if e != nil {
			return report, e
		}
		check := sha256.New()
		_, e = io.Copy(check, verify)
		verify.Close()
		if e != nil || fmt.Sprintf("%x", check.Sum(nil)) != digest {
			return report, fmt.Errorf("material_changed_during_preservation")
		}
		report.Materials = append(report.Materials, OfflineMaterial{Name: name, Digest: digest, Size: n})
	}
	work := filepath.Join(out, "diagnostic-copy")
	defer os.RemoveAll(work)
	if e = os.Mkdir(work, 0700); e != nil {
		return report, e
	}
	for _, m := range report.Materials {
		if m.Name == "app.sqlite-shm" {
			continue
		}
		source, e := os.Open(filepath.Join(materials, m.Name))
		if e != nil {
			return report, e
		}
		target, e := os.OpenFile(filepath.Join(work, m.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			source.Close()
			return report, e
		}
		_, e = io.Copy(target, source)
		source.Close()
		target.Close()
		if e != nil {
			return report, e
		}
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(work, "app.sqlite")}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e == nil {
		e = db.PingContext(ctx)
	}
	if e != nil {
		report.ErrorCode = "database_unreadable"
	} else {
		report.Readable = true
		var app, baseline string
		if e = scopeRow(ctx, db, "SELECT application,baseline FROM application_identity WHERE singleton=1").Scan(&app, &baseline); e == nil {
			report.IdentityValid = app == ApplicationID && baseline == BaselineID
		}
		rows, err := scopeRows(ctx, db, "SELECT version,checksum FROM schema_migrations ORDER BY version")
		report.LedgerValid = err == nil
		last := 0
		if err == nil {
			for rows.Next() {
				var version int
				var hash string
				if rows.Scan(&version, &hash) != nil || version != last+1 || hash != expected[version] {
					report.LedgerValid = false
				}
				last = version
			}
			if rows.Err() != nil {
				report.LedgerValid = false
			}
			rows.Close()
		}
		if last != len(expected) {
			report.LedgerValid = false
		}
		report.Schema = last
		rows, err = scopeRows(ctx, db, "PRAGMA integrity_check")
		if err != nil {
			report.Integrity = "unavailable"
		} else {
			report.Integrity = "ok"
			for rows.Next() {
				var result string
				if rows.Scan(&result) != nil || result != "ok" {
					report.Integrity = "invalid"
				}
			}
			if rows.Err() != nil {
				report.Integrity = "unavailable"
			}
			rows.Close()
		}
	}
	if db != nil {
		db.Close()
	}
	_ = os.RemoveAll(work)
	report.Preserved = preserve
	if !preserve {
		_ = os.RemoveAll(materials)
		report.Materials = nil
	}
	raw, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		return report, e
	}
	if e = os.WriteFile(filepath.Join(out, "report.json"), append(raw, '\n'), 0600); e != nil {
		return report, e
	}
	return report, nil
}
