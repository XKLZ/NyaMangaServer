package store

import (
	"context"
	"database/sql"
	"errors"
	"nyamangaserver.local/server/internal/model"
)

const workColumns = `w.id,w.library_id,w.name_override,w.preferred_edition_id,w.revision,(SELECT count(*) FROM editions e WHERE e.work_id=w.id),COALESCE(sm.series_id,''),COALESCE(sm.position,0),COALESCE(w.name_override,eb.title,f.filename_title),COALESCE(eb.title,f.filename_title),COALESCE(json_extract(eb.fields_json,'$.title_original'),'')`
const workSource = ` works w JOIN files f ON f.edition_id=w.preferred_edition_id LEFT JOIN effective_editions eb ON eb.edition_id=w.preferred_edition_id LEFT JOIN series_members sm ON sm.work_id=w.id`

func scanWork(r scanner) (model.Work, error) {
	var w model.Work
	e := r.Scan(&w.ID, &w.LibraryID, &w.NameOverride, &w.PreferredEditionID, &w.Revision, &w.EditionCount, &w.SeriesID, &w.Position, &w.Name, &w.Title, &w.TitleOriginal)
	if errors.Is(e, sql.ErrNoRows) {
		e = model.ErrNotFound
	}
	return w, e
}
func (s *Store) Work(ctx context.Context, id string) (model.Work, error) {
	return scanWork(scopeRow(ctx, s.Read, "SELECT "+workColumns+" FROM "+workSource+" WHERE w.id=?", id))
}
func (s *Store) WorkPage(ctx context.Context, library, query string, page, size int, minimum ...int) ([]model.Work, int, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback()
	if e = validateSearchLibrary(ctx, tx, library); e != nil {
		return nil, 0, e
	}
	where := ` WHERE (?='' OR w.library_id=?) AND (?='' OR instr(nya_normalize_v1(COALESCE(w.name_override,eb.title,f.filename_title)),nya_normalize_v1(?))>0)`
	args := []any{library, library, query, query}
	if len(minimum) > 0 {
		where += " AND (SELECT count(*) FROM editions ed WHERE ed.work_id=w.id)>=?"
		args = append(args, minimum[0])
	}
	var total int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM "+workSource+where, args...).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT "+workColumns+" FROM "+workSource+where+" ORDER BY nya_natural_v1(COALESCE(w.name_override,eb.title,f.filename_title)),w.id LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if e != nil {
		return nil, 0, e
	}
	out := []model.Work{}
	for rows.Next() {
		w, x := scanWork(rows)
		if x != nil {
			rows.Close()
			return nil, 0, x
		}
		out = append(out, w)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, 0, e
	}
	if e = enrichWorkCovers(ctx, tx, out); e != nil {
		return nil, 0, e
	}
	return out, total, tx.Commit()
}
func (s *Store) WorkEditions(ctx context.Context, id string, page, size int) ([]model.Edition, int, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback()
	var count int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM editions WHERE work_id=?", id).Scan(&count); e != nil {
		return nil, 0, e
	}
	if count == 0 {
		return nil, 0, model.ErrNotFound
	}
	rows, e := scopeRows(ctx, tx, "SELECT "+s.editionColumns()+" FROM "+editionRecordSQL+" editions WHERE work_id=? ORDER BY (id=(SELECT preferred_edition_id FROM works WHERE id=?)) DESC,id LIMIT ? OFFSET ?", id, id, size, (page-1)*size)
	if e != nil {
		return nil, 0, e
	}
	out := []model.Edition{}
	for rows.Next() {
		w, x := scanEdition(rows)
		if x != nil {
			rows.Close()
			return nil, 0, x
		}
		out = append(out, *w)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, 0, e
	}
	return out, count, tx.Commit()
}

func compactSeries(ctx context.Context, tx *sql.Tx, id string) error {
	rows, e := scopeRows(ctx, tx, "SELECT work_id FROM series_members WHERE series_id=? ORDER BY position", id)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM series_members WHERE series_id=?", id); e != nil {
		return e
	}
	for i, v := range ids {
		if _, e = tx.ExecContext(ctx, "INSERT INTO series_members VALUES(?,?,?)", id, v, i+1); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, "UPDATE series SET revision=revision+1 WHERE id=?", id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO events(type,resource_id,revision,created_at) SELECT 'series',id,revision,unixepoch() FROM series WHERE id=?", id); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "UPDATE catalog_state SET revision=revision+1")
	return e
}

func (s *Store) WorkEditionPage(ctx context.Context, account, id, query string, page, size int) (model.WorkEditionPage, error) {
	out := model.WorkEditionPage{Items: []model.WorkEdition{}, Page: page, PageSize: size, Expected: map[string]int64{}}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var rev int64
	if e = scopeRow(ctx, tx, "SELECT revision FROM works WHERE id=?", id).Scan(&rev); e != nil {
		if e == sql.ErrNoRows {
			e = model.ErrNotFound
		}
		return out, e
	}
	out.Expected["work:"+id] = rev
	from := workEditionSource + ` WHERE e.work_id=? AND (?='' OR instr(nya_normalize_v1(COALESCE(eb.title,f.filename_title)),nya_normalize_v1(?))>0)`
	args := []any{account, id, query, query}
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM "+from, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT "+workEditionColumns+" FROM "+from+" ORDER BY (e.id=(SELECT preferred_edition_id FROM works WHERE id=?)) DESC,e.id LIMIT ? OFFSET ?", append(args, id, size, (page-1)*size)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		v, x := scanWorkEdition(rows)
		if x != nil {
			rows.Close()
			return out, x
		}
		out.Items = append(out.Items, v)
		out.Expected["edition:"+v.ID] = v.Revision
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if e = enrichVersionMembers(ctx, tx, &out); e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *Store) SeriesPage(ctx context.Context, library, query string, page, size int) (model.SeriesList, error) {
	out := model.SeriesList{Items: []model.Series{}, Page: page, PageSize: size}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = validateSearchLibrary(ctx, tx, library); e != nil {
		return out, e
	}
	where := ` WHERE (?='' OR library_id=?) AND (?='' OR instr(nya_normalize_v1(name),nya_normalize_v1(?))>0)`
	args := []any{library, library, query, query}
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM series"+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT id,library_id,name,revision,(SELECT count(*) FROM series_members m WHERE m.series_id=series.id) FROM series"+where+" ORDER BY nya_natural_v1(name),id LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		v := model.Series{WorkIDs: []string{}}
		if e = rows.Scan(&v.ID, &v.LibraryID, &v.Name, &v.Revision, &v.MemberCount); e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if e = enrichSeriesCovers(ctx, tx, out.Items); e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *Store) SeriesDetail(ctx context.Context, id string, summary ...bool) (model.Series, error) {
	v := model.Series{WorkIDs: []string{}}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	e = scopeRow(ctx, tx, "SELECT id,library_id,name,revision,(SELECT count(*) FROM series_members m WHERE m.series_id=series.id) FROM series WHERE id=?", id).Scan(&v.ID, &v.LibraryID, &v.Name, &v.Revision, &v.MemberCount)
	if e == sql.ErrNoRows {
		e = model.ErrNotFound
	}
	if e != nil {
		return v, e
	}
	// The default remains a compact summary. Explicit order reads contain IDs
	// only (at most the domain limit of 1000), never unpaged member metadata.
	if len(summary) > 0 && !summary[0] {
		rows, err := scopeRows(ctx, tx, "SELECT work_id FROM series_members WHERE series_id=? ORDER BY position", id)
		if err != nil {
			return v, err
		}
		for rows.Next() {
			var work string
			if err = rows.Scan(&work); err != nil {
				rows.Close()
				return v, err
			}
			v.WorkIDs = append(v.WorkIDs, work)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return v, err
		}
	}
	items := []model.Series{v}
	if e = enrichSeriesCovers(ctx, tx, items); e != nil {
		return v, e
	}
	return items[0], tx.Commit()
}
func (s *Store) SeriesWorkPage(ctx context.Context, id, query string, page, size int) (model.SeriesWorkPage, error) {
	out := model.SeriesWorkPage{Items: []model.Work{}, Page: page, PageSize: size, Expected: map[string]int64{}}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var rev int64
	if e = scopeRow(ctx, tx, "SELECT revision FROM series WHERE id=?", id).Scan(&rev); e != nil {
		if e == sql.ErrNoRows {
			e = model.ErrNotFound
		}
		return out, e
	}
	out.Expected["series:"+id] = rev
	where := ` WHERE sm.series_id=? AND (?='' OR instr(nya_normalize_v1(COALESCE(w.name_override,eb.title,f.filename_title)),nya_normalize_v1(?))>0)`
	args := []any{id, query, query}
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM "+workSource+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT "+workColumns+" FROM "+workSource+where+" ORDER BY sm.position LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		v, x := scanWork(rows)
		if x != nil {
			rows.Close()
			return out, x
		}
		out.Items = append(out.Items, v)
		out.Expected["work:"+v.ID] = v.Revision
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if e = enrichWorkCovers(ctx, tx, out.Items); e != nil {
		return out, e
	}
	return out, tx.Commit()
}
