package store

import (
	"context"
	"database/sql"
	"errors"

	"nyamangaserver.local/server/internal/model"
)

const clientEditionTitle = `COALESCE(NULLIF(json_extract(eb.fields_json,'$.title_original'),''),NULLIF(w.name_override,''),NULLIF(eb.title,''),f.filename_title)`
const clientEditionColumns = `ed.id,` + clientEditionTitle + `,COALESCE(cs.revision,0),f.content_revision,f.parsing_revision,NULLIF(f.page_count,0),f.file_state,COALESCE(u.hidden,0)`
const clientEditionJoins = `JOIN files f ON f.edition_id=ed.id JOIN works w ON w.id=ed.work_id
	LEFT JOIN effective_editions eb ON eb.edition_id=ed.id LEFT JOIN cover_selections cs ON cs.edition_id=ed.id
	LEFT JOIN edition_user_states u ON u.edition_id=ed.id AND u.account_id=?`

func clientEditionTargets(v *model.ClientEdition) []any {
	return []any{&v.ID, &v.Title, &v.CoverRevision, &v.ContentRevision, &v.ParsingRevision, &v.PageCount, &v.FileState, &v.Hidden}
}

func (s *Store) ClientReadingContext(ctx context.Context, id, requested, intent string) (model.ClientReadingContext, error) {
	out := model.ClientReadingContext{WorkID: id, Editions: []model.ClientEdition{}}
	account, e := readingAccount(ctx)
	if e != nil {
		return out, e
	}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var preferred string
	e = scopeRow(ctx, tx, `SELECT w.library_id,w.preferred_edition_id FROM works w
		JOIN libraries l ON l.id=w.library_id WHERE w.id=? AND l.trashed_at IS NULL`, id).Scan(&out.LibraryID, &preferred)
	if errors.Is(e, sql.ErrNoRows) {
		return out, model.ErrNotFound
	}
	if e != nil {
		return out, e
	}
	l, e := scanLibrary(scopeRow(ctx, tx, "SELECT "+libraryColumns+" FROM libraries WHERE id=?", out.LibraryID))
	if e != nil {
		return out, e
	}
	out.SourceLibrary = *l
	rows, e := scopeRows(ctx, tx, `SELECT `+clientEditionColumns+`,r.last_read_at,COALESCE(r.completed,0)
		FROM editions ed `+clientEditionJoins+` LEFT JOIN reading_states r ON r.edition_id=ed.id AND r.account_id=?
		WHERE ed.work_id=? AND ed.trashed_at IS NULL AND (COALESCE(u.hidden,0)=0 OR ed.id=?)
		ORDER BY (ed.id=?) DESC,ed.id`, account, account, id, requested, preferred)
	if e != nil {
		return out, e
	}
	var resumeID string
	var lastRead *int64
	for rows.Next() {
		var v model.ClientEdition
		var at *int64
		var completed bool
		if e = rows.Scan(append(clientEditionTargets(&v), &at, &completed)...); e != nil {
			rows.Close()
			return out, e
		}
		out.Editions = append(out.Editions, v)
		if v.ID == preferred {
			out.PreferredEditionID = &v.ID
		}
		if !v.Hidden && !completed && at != nil && (lastRead == nil || *at > *lastRead || *at == *lastRead && v.ID < resumeID) {
			resumeID, lastRead = v.ID, at
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(out.Editions) == 0 {
		return out, model.ErrNotFound
	}
	selection := out.Editions[0].ID
	out.SelectionReason = "visible_fallback"
	switch {
	case requested != "":
		selection, out.SelectionReason = requested, "explicit"
	case intent == "continue" && resumeID != "":
		selection, out.SelectionReason = resumeID, "continue"
	case out.PreferredEditionID != nil:
		selection, out.SelectionReason = *out.PreferredEditionID, "preferred"
	}
	for _, v := range out.Editions {
		if v.ID == selection {
			out.Edition = v
			break
		}
	}
	if out.Edition.ID == "" {
		return out, model.ErrNotFound
	}
	out.Reading, e = readingState(ctx, tx, account, selection, out.Edition.ContentRevision, out.Edition.PageCount)
	if e != nil {
		return out, e
	}
	out.RequiresContentConfirmation = out.Reading.ContentRevision != out.Edition.ContentRevision
	out.RequiresParsingConfirmation = out.Reading.ParsingStale
	if e = clientSeriesContext(ctx, tx, account, &out); e != nil {
		return out, e
	}
	return out, tx.Commit()
}

func clientSeriesContext(ctx context.Context, tx *sql.Tx, account string, out *model.ClientReadingContext) error {
	var v model.ClientSeries
	e := scopeRow(ctx, tx, `SELECT s.id,s.name,m.position FROM series_members m JOIN series s ON s.id=m.series_id WHERE m.work_id=?`,
		out.WorkID).Scan(&v.ID, &v.Name, &v.Position)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	out.Series = &v
	return scopeRow(ctx, tx, `WITH visible_members AS (
		SELECT m.work_id,m.position FROM series_members m JOIN works w ON w.id=m.work_id WHERE m.series_id=? AND
		EXISTS(SELECT 1 FROM editions ed LEFT JOIN edition_user_states u ON u.edition_id=ed.id AND u.account_id=?
			WHERE ed.work_id=w.id AND ed.trashed_at IS NULL AND COALESCE(u.hidden,0)=0)
	) SELECT (SELECT work_id FROM visible_members WHERE position<? ORDER BY position DESC LIMIT 1),
		(SELECT work_id FROM visible_members WHERE position>? ORDER BY position LIMIT 1)`, v.ID, account, v.Position, v.Position).
		Scan(&out.PreviousWorkID, &out.NextWorkID)
}
