package store

import (
	"context"
	"database/sql"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"strings"
	"time"
)

type Library = model.Library
type Edition = model.Edition
type Page = model.Page
type Job = model.Job
type Item = model.Item

var ErrNotFound = model.ErrNotFound
var ErrScanActive = model.ErrScanActive
var ErrStaleJob = model.ErrStaleJob

type scanner interface{ Scan(...any) error }

// editionRecordSQL is a read aggregate over the actual one-to-one tables.
// SQL writes always address editions or files explicitly.
const editionRecordSQL = `(SELECT e.id,e.library_id,e.work_id,e.revision,e.organization_revision,e.trashed_at,f.relative_path,f.filename_title,f.size,f.mtime_ns,f.content_revision,f.representative_sha1,f.representative_algorithm,f.legacy_cover_sha1,f.page_count,f.file_state,f.error_code,f.indexed_at FROM editions e JOIN files f ON f.edition_id=e.id)`
const libraryColumns = "id,root_key,display_name,legacy_zip_encoding,scan_interval_minutes,root_identity,revision,last_complete_scan_id,last_error,disabled,trashed_at"
const editionColumns = "id,library_id,relative_path,filename_title,COALESCE((SELECT title FROM effective_editions WHERE edition_id=editions.id),filename_title),size,mtime_ns,content_revision,representative_sha1,page_count,file_state,error_code,indexed_at,revision,trashed_at,COALESCE((SELECT disabled FROM libraries WHERE id=editions.library_id),0),COALESCE((SELECT revision FROM cover_selections WHERE edition_id=editions.id),0)"
const jobColumns = "id,COALESCE(library_id,''),state,stage,generation,total,done,failed,error_code,created_at,updated_at,type,parent_job_id"

func scanLibrary(r scanner) (*Library, error) {
	var v Library
	e := r.Scan(&v.ID, &v.RootKey, &v.Name, &v.Encoding, &v.Interval, &v.Identity, &v.Revision, &v.LastScan, &v.Error, &v.Disabled, &v.TrashedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &v, e
}
func scanEdition(r scanner) (*Edition, error) {
	var v Edition
	e := r.Scan(&v.ID, &v.LibraryID, &v.Path, &v.FilenameTitle, &v.Title, &v.Size, &v.Mtime, &v.ContentRevision, &v.SHA1, &v.PageCount, &v.State, &v.Error, &v.IndexedAt, &v.Revision, &v.TrashedAt, &v.LibraryDisabled, &v.CoverRevision, &v.RepresentativeAlgorithm, &v.LegacyCoverSHA1, &v.WorkID, &v.OrganizationRevision, &v.TitleOriginal, &v.EncodingOverride, &v.ParsingRevision, &v.ParserVersion, &v.DirectoryDigest, &v.EffectiveEncoding)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &v, e
}
func (s *Store) jobColumns() string {
	columns := jobColumns
	if s.Version >= 9 {
		columns += ",mode"
	} else {
		columns += ",'edition'"
	}
	if s.Version >= 25 {
		columns += ",COALESCE(actor_id,''),actor_acl_revision"
	} else {
		columns += ",'',0"
	}
	if s.Version >= 27 {
		for _, classification := range []string{"completed", "partial", "blocked"} {
			columns += ",(SELECT count(*) FROM job_items ji WHERE ji.job_id=jobs.id AND ji.scan_result_json!='' AND json_extract(ji.scan_result_json,'$.classification')='" + classification + "')"
		}
		columns += ",(SELECT COALESCE(sum(json_extract(NULLIF(ji.scan_result_json,''),'$.warning_count')),0) FROM job_items ji WHERE ji.job_id=jobs.id)"
	} else {
		columns += ",0,0,0,0"
	}
	return columns
}
func scanJob(r scanner) (*Job, error) {
	var v Job
	e := r.Scan(&v.ID, &v.LibraryID, &v.State, &v.Stage, &v.Generation, &v.Total, &v.Done, &v.Failed, &v.Error, &v.Created, &v.Updated, &v.Type, &v.ParentID, &v.Mode, &v.ActorID, &v.ActorACLRevision, &v.ScanCompleted, &v.ScanPartial, &v.ScanBlocked, &v.ScanWarnings)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &v, e
}
func (s *Store) Libraries(ctx context.Context) ([]Library, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	rows, e := scopeRows(ctx, tx, "SELECT "+libraryColumns+" FROM libraries ORDER BY display_name,id")
	if e != nil {
		return nil, e
	}
	out := []Library{}
	index := map[string]int{}
	for rows.Next() {
		v, err := scanLibrary(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		index[v.ID] = len(out)
		out = append(out, *v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if s.Version >= 7 {
		rows, e = scopeRows(ctx, tx, "SELECT library_id,count(*),count(DISTINCT work_id) FROM editions WHERE trashed_at IS NULL GROUP BY library_id")
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var id string
			var editions, works int
			if e = rows.Scan(&id, &editions, &works); e != nil {
				rows.Close()
				return nil, e
			}
			if i, ok := index[id]; ok {
				out[i].EditionCount, out[i].WorkCount = editions, works
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	if s.Version >= 12 {
		rows, e = scopeRows(ctx, tx, "SELECT id,library_id,stage,state FROM import_batches WHERE state NOT IN('completed','cancelled')")
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			v := &model.ImportSummary{}
			var library string
			if e = rows.Scan(&v.ID, &library, &v.Stage, &v.State); e != nil {
				rows.Close()
				return nil, e
			}
			if i, ok := index[library]; ok {
				out[i].ActiveImport = v
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	return out, tx.Commit()
}
func (s *Store) Library(ctx context.Context, id string) (*Library, error) {
	return scanLibrary(scopeRow(ctx, s.Read, "SELECT "+libraryColumns+" FROM libraries WHERE id=?", id))
}
func (s *Store) SaveLibrary(ctx context.Context, v Library, expected int64) error {
	var due any
	if v.Interval != nil {
		due = time.Now().Add(time.Duration(*v.Interval) * time.Minute).Unix()
	}
	if expected == 0 {
		columns := "id,root_key,display_name,legacy_zip_encoding,scan_interval_minutes,next_scan_at,root_identity,revision"
		values := "?,?,?,?,?,?,?,1"
		args := []any{v.ID, v.RootKey, v.Name, v.Encoding, v.Interval, due, v.Identity}
		if s.Version >= 25 {
			columns += ",scan_actor_id"
			values += ",?"
			var owner any
			if a, ok := model.Actor(ctx); ok {
				owner = a.ID
			}
			args = append(args, owner)
		}
		_, e := s.Write.ExecContext(ctx, "INSERT INTO libraries("+columns+") VALUES("+values+")", args...)
		if e != nil && strings.Contains(e.Error(), "UNIQUE") {
			return ErrConflict
		}
		return e
	}

	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var active int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM jobs WHERE type='scan' AND library_id=? AND state IN ('queued','running','paused')", v.ID).Scan(&active); e != nil {
		return e
	}
	if active > 0 {
		return ErrScanActive
	}
	var oldEncoding string
	var oldInterval *int
	if e = scopeRow(ctx, tx, "SELECT legacy_zip_encoding,scan_interval_minutes FROM libraries WHERE id=? AND revision=?", v.ID, expected).Scan(&oldEncoding, &oldInterval); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return ErrConflict
		}
		return e
	}
	r, e := tx.ExecContext(ctx, "UPDATE libraries SET root_key=?,display_name=?,legacy_zip_encoding=?,scan_interval_minutes=?,next_scan_at=?,root_identity=?,revision=revision+1 WHERE id=? AND revision=?", v.RootKey, v.Name, v.Encoding, v.Interval, due, v.Identity, v.ID, expected)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	if s.Version >= 25 && v.Interval != nil && (oldInterval == nil || *oldInterval != *v.Interval) {
		if a, ok := model.Actor(ctx); ok {
			if _, e = tx.ExecContext(ctx, "UPDATE libraries SET scan_actor_id=? WHERE id=?", a.ID, v.ID); e != nil {
				return e
			}
		}
	}
	if oldEncoding != v.Encoding {
		if _, e = tx.ExecContext(ctx, "UPDATE editions SET revision=revision+1 WHERE id IN(SELECT edition_id FROM files WHERE library_id=? AND file_state!='missing')", v.ID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE files SET content_revision=content_revision+1,file_state='needs_review',error_code='index_settings_changed',page_count=0,representative_sha1=NULL WHERE library_id=? AND file_state!='missing'", v.ID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE libraries SET last_complete_scan_id=NULL,last_error='index_settings_changed' WHERE id=?", v.ID); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (s *Store) editionColumns() string {
	columns := editionColumns + ",representative_algorithm,legacy_cover_sha1,work_id,organization_revision,COALESCE((SELECT json_extract(fields_json,'$.title_original') FROM effective_editions WHERE edition_id=editions.id),'')"
	if s.Version >= 27 {
		for _, c := range []string{"encoding_override", "parsing_revision", "parser_version", "directory_digest", "effective_encoding"} {
			columns += ",(SELECT " + c + " FROM files WHERE edition_id=editions.id)"
		}
	} else {
		columns += ",NULL,1,'natural-bmp-v2','',''"
	}
	return columns
}
