package store

import (
	"context"
	"database/sql"
	"nyamangaserver.local/server/internal/model"
)

func (s *Store) EditionDetails(ctx context.Context, account, id string) (*model.Edition, model.MetadataDetails, model.UserState, model.WorkContext, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	g := model.WorkContext{Works: []model.Work{}, Editions: []model.WorkEdition{}, Series: []model.Series{}, Expected: map[string]int64{}}
	if e != nil {
		return nil, model.MetadataDetails{}, model.UserState{}, g, e
	}
	defer tx.Rollback()
	b, e := scanEdition(scopeRow(ctx, tx, "SELECT "+s.editionColumns()+" FROM "+editionRecordSQL+" editions WHERE id=?", id))
	if e != nil {
		return nil, model.MetadataDetails{}, model.UserState{}, g, e
	}
	d, e := metadataDetails(ctx, tx, id)
	if e != nil {
		return nil, d, model.UserState{}, g, e
	}
	ed, e := scanWorkEdition(scopeRow(ctx, tx, "SELECT "+workEditionColumns+" FROM "+workEditionSource+" WHERE e.id=?", account, id))
	if e != nil {
		return nil, d, model.UserState{}, g, e
	}
	w, e := scanWork(scopeRow(ctx, tx, "SELECT "+workColumns+" FROM "+workSource+" WHERE w.id=?", b.WorkID))
	if e != nil {
		return nil, d, ed.State, g, e
	}
	g.Works = append(g.Works, w)
	g.Editions = append(g.Editions, ed)
	g.Expected["work:"+w.ID] = w.Revision
	g.Expected["edition:"+id] = ed.Revision
	if w.SeriesID != "" {
		v := model.Series{WorkIDs: []string{}}
		e = scopeRow(ctx, tx, "SELECT id,library_id,name,revision,(SELECT count(*) FROM series_members WHERE series_id=series.id) FROM series WHERE id=?", w.SeriesID).Scan(&v.ID, &v.LibraryID, &v.Name, &v.Revision, &v.MemberCount)
		if e != nil {
			return nil, d, ed.State, g, e
		}
		g.Series = append(g.Series, v)
		g.Expected["series:"+v.ID] = v.Revision
	}
	g.Catalog, e = catalogRevision(ctx, tx)
	if e != nil {
		return nil, d, ed.State, g, e
	}
	page := model.WorkEditionPage{Items: g.Editions}
	if e = enrichVersionMembers(ctx, tx, &page); e != nil {
		return nil, d, ed.State, g, e
	}
	g.Editions = page.Items
	if s.Version >= 26 {
		page := []model.Edition{*b}
		if e = readingForEditions(ctx, tx, page); e != nil {
			return nil, d, ed.State, g, e
		}
		*b = page[0]
	}
	return b, d, ed.State, g, tx.Commit()
}
