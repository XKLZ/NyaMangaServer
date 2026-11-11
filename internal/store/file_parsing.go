package store

import (
	"context"
	"database/sql"
	"nyamangaserver.local/server/internal/model"
)

func (s *Store) SetFileEncoding(ctx context.Context, id string, in model.EncodingUpdate, parsed model.FileParsing, pages int) (*model.Edition, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = managementGuard(ctx, tx, id); e != nil && e != model.ErrDisabled {
		return nil, e
	}
	var rev, content, parsing int64
	if e = scopeRow(ctx, tx, "SELECT e.revision,f.content_revision,f.parsing_revision FROM editions e JOIN files f ON f.edition_id=e.id WHERE e.id=?", id).Scan(&rev, &content, &parsing); e == sql.ErrNoRows {
		return nil, model.ErrNotFound
	} else if e != nil {
		return nil, e
	}
	if rev != in.Expected || content != in.ContentRevision || parsing != in.ParsingRevision {
		return nil, model.ErrConflict
	}
	if in.Encoding != nil && *in.Encoding != "utf8" && *in.Encoding != "gb18030" && *in.Encoding != "shift_jis" {
		return nil, model.ErrInput
	}
	if parsed.DirectoryDigest == "" || in.DirectoryDigest != parsed.DirectoryDigest {
		return nil, model.ErrConflict
	}
	if parsed.ParsingRevision < parsing || parsed.ParsingRevision > parsing+1 {
		return nil, model.ErrInput
	}
	var unchanged bool
	if e = scopeRow(ctx, tx, "SELECT encoding_override IS ? AND effective_encoding=? AND parser_version=? AND directory_digest=? AND page_count=? FROM files WHERE edition_id=?", in.Encoding, parsed.EffectiveEncoding, parsed.ParserVersion, parsed.DirectoryDigest, pages, id).Scan(&unchanged); e != nil {
		return nil, e
	}
	if unchanged {
		if e = tx.Commit(); e != nil {
			return nil, e
		}
		return s.Edition(ctx, id)
	}
	if _, e = tx.ExecContext(ctx, `UPDATE files SET encoding_override=?,effective_encoding=?,parsing_revision=?,parser_version=?,directory_digest=?,page_count=?,representative_sha1=CASE WHEN effective_encoding=? AND parser_version=? AND directory_digest=? THEN representative_sha1 ELSE NULL END,file_state='available',error_code='' WHERE edition_id=?`, in.Encoding, parsed.EffectiveEncoding, parsed.ParsingRevision, parsed.ParserVersion, parsed.DirectoryDigest, pages, parsed.EffectiveEncoding, parsed.ParserVersion, parsed.DirectoryDigest, id); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE editions SET revision=revision+1 WHERE id=?", id); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.Edition(ctx, id)
}

func hasParsing(ctx context.Context, q queryer) bool {
	var n int
	return q.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('files') WHERE name='parsing_revision'").Scan(&n) == nil && n == 1
}
func currentParsing(ctx context.Context, q queryer, id string) (int64, error) {
	if !hasParsing(ctx, q) {
		return 1, nil
	}
	var revision int64
	e := scopeRow(ctx, q, "SELECT parsing_revision FROM files WHERE edition_id=?", id).Scan(&revision)
	return revision, e
}
