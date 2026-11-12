package store

import (
	"context"
	"database/sql"
	"errors"

	"nyamangaserver.local/server/internal/model"
)

const downloadColumns = "d.id,d.edition_id,d.content_revision,d.length,d.sha256,d.state,d.error_code,d.account_id,d.cache_epoch,d.source_identity,d.request_key"

func scanDownload(row scanner) (model.Download, error) {
	var d model.Download
	err := row.Scan(&d.ID, &d.EditionID, &d.ContentRevision, &d.Length, &d.SHA256, &d.State, &d.Error, &d.AccountID, &d.Epoch, &d.Source, &d.Key)
	if errors.Is(err, sql.ErrNoRows) {
		err = model.ErrNotFound
	}
	return d, err
}

func (s *Store) PrepareDownload(ctx context.Context, d model.Download) (model.Download, error) {
	account, err := readingAccount(ctx)
	if err != nil {
		return d, err
	}
	tx, err := s.Write.BeginTx(ctx, nil)
	if err != nil {
		return d, err
	}
	defer tx.Rollback()
	identity, err := clientIdentity(ctx, tx)
	if err != nil {
		return d, err
	}
	if identity.CacheEpoch != d.Epoch {
		return d, model.ErrClientEpoch
	}
	var current int64
	err = scopeRow(ctx, tx, `SELECT f.content_revision FROM files f JOIN editions e ON e.id=f.edition_id
		JOIN libraries l ON l.id=e.library_id WHERE e.id=? AND e.trashed_at IS NULL AND l.trashed_at IS NULL AND l.disabled=0`, d.EditionID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return d, model.ErrNotFound
	}
	if err != nil {
		return d, err
	}
	if current != d.ContentRevision {
		return d, model.ErrConflict
	}
	old, err := scanDownload(tx.QueryRowContext(ctx, "SELECT "+downloadColumns+" FROM client_downloads d WHERE d.account_id=? AND d.cache_epoch=? AND d.request_key=?", account, d.Epoch, d.Key))
	if err == nil {
		if old.EditionID != d.EditionID || old.ContentRevision != d.ContentRevision {
			return d, model.ErrClientKeyConflict
		}
		if old.Source != d.Source {
			return d, model.ErrConflict
		}
		return old, tx.Commit()
	}
	if !errors.Is(err, model.ErrNotFound) {
		return d, err
	}
	d.AccountID = account
	d.State = "preparing"
	err = tx.QueryRowContext(ctx, `SELECT sha256 FROM client_downloads WHERE account_id=? AND cache_epoch=? AND edition_id=?
		AND content_revision=? AND source_identity=? AND state='ready' LIMIT 1`, account, d.Epoch, d.EditionID, d.ContentRevision, d.Source).Scan(&d.SHA256)
	switch {
	case err == nil:
		d.State = "ready"
	case errors.Is(err, sql.ErrNoRows):
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM client_downloads WHERE cache_epoch=? AND state='preparing'", d.Epoch).Scan(&n); err != nil {
			return d, err
		}
		if n >= 100 {
			return d, model.ErrDownloadBusy
		}
	default:
		return d, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO client_downloads(id,account_id,edition_id,cache_epoch,request_key,content_revision,
		source_identity,length,state,sha256,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,unixepoch())`, d.ID, account, d.EditionID, d.Epoch, d.Key, d.ContentRevision, d.Source, d.Length, d.State, d.SHA256)
	if err != nil {
		return d, err
	}
	return d, tx.Commit()
}

func (s *Store) Download(ctx context.Context, id string) (model.Download, error) {
	a, err := readingAccount(ctx)
	if err != nil {
		return model.Download{}, err
	}
	d, err := scanDownload(scopeRow(ctx, s.Read, `SELECT `+downloadColumns+` FROM client_downloads d JOIN editions e ON e.id=d.edition_id
		JOIN libraries l ON l.id=e.library_id WHERE d.id=? AND d.account_id=? AND e.trashed_at IS NULL AND l.trashed_at IS NULL`, id, a))
	if err != nil {
		return d, err
	}
	identity, err := s.ClientIdentity(ctx)
	if err == nil && d.Epoch != identity.CacheEpoch {
		err = model.ErrClientEpoch
	}
	return d, err
}

func (s *Store) AbandonDownload(ctx context.Context, id string) error {
	d, err := s.Download(ctx, id)
	if err != nil {
		return err
	}
	_, err = s.Write.ExecContext(ctx, "UPDATE client_downloads SET state='abandoned',sha256='',error_code='' WHERE id=? AND account_id=? AND state<>'abandoned'", d.ID, d.AccountID)
	return err
}

// Worker methods run with the application context. Publication independently
// rechecks the current owner, library and version in the same SQL transaction.
func (s *Store) NextDownload(ctx context.Context) (model.Download, error) {
	return scanDownload(s.Read.QueryRowContext(ctx, `SELECT `+downloadColumns+` FROM client_downloads d
		JOIN instance_client_identity i ON i.cache_epoch=d.cache_epoch
		WHERE d.state='preparing' AND (SELECT phase FROM health_state WHERE singleton=1)='idle'
		ORDER BY d.created_at,d.id LIMIT 1`))
}

func (s *Store) FinishDownload(ctx context.Context, d model.Download, digest, reason string) error {
	_, err := s.Write.ExecContext(ctx, `WITH valid AS MATERIALIZED (
		SELECT EXISTS(SELECT 1 FROM files f JOIN editions e ON e.id=f.edition_id JOIN libraries l ON l.id=e.library_id
		JOIN account_profiles p ON p.account_id=? WHERE f.edition_id=? AND f.content_revision=? AND f.file_state='available'
		AND e.trashed_at IS NULL AND l.trashed_at IS NULL AND l.disabled=0 AND p.enabled=1 AND p.must_change_password=0
		AND (p.role='admin' OR EXISTS(SELECT 1 FROM account_library_grants g WHERE g.account_id=p.account_id AND g.library_id=l.id))) allowed
	) UPDATE client_downloads SET state=CASE WHEN ?<>'' AND (SELECT allowed FROM valid) THEN 'ready' ELSE 'failed' END,
		sha256=CASE WHEN (SELECT allowed FROM valid) THEN ? ELSE '' END,
		error_code=CASE WHEN (SELECT allowed FROM valid) THEN ? ELSE 'source_unavailable' END
		WHERE id=? AND state='preparing' AND cache_epoch=(SELECT cache_epoch FROM instance_client_identity WHERE singleton=1)`,
		d.AccountID, d.EditionID, d.ContentRevision, digest, digest, reason, d.ID)
	return err
}
