package store

import (
	"context"
	"database/sql"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
	"slices"
)

const summaryCoverColumns = `w.id,w.preferred_edition_id,f.content_revision,COALESCE(cs.revision,0)`
const summaryCoverVersions = ` JOIN files f ON f.edition_id=w.preferred_edition_id LEFT JOIN cover_selections cs ON cs.edition_id=w.preferred_edition_id`
const summaryCoverSource = `works w` + summaryCoverVersions

// These queries only read database identity/version information. They never open
// image files, populate a cache, or change the persisted organization model.
func workCoverSummaries(ctx context.Context, tx *sql.Tx, ids []string) (map[string]*model.WorkCoverSummary, error) {
	out := map[string]*model.WorkCoverSummary{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, e := scopeRows(ctx, tx, "SELECT "+summaryCoverColumns+" FROM "+summaryCoverSource+" WHERE w.id IN("+placeholders(len(ids))+")", stringArgs(ids)...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		v := &model.WorkCoverSummary{}
		if e = rows.Scan(&v.WorkID, &v.EditionID, &v.ContentRevision, &v.CoverRevision); e != nil {
			return nil, e
		}
		out[v.WorkID] = v
	}
	return out, rows.Err()
}
func seriesCoverSummaries(ctx context.Context, tx *sql.Tx, ids []string) (map[string]*model.WorkCoverSummary, error) {
	out := map[string]*model.WorkCoverSummary{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, e := scopeRows(ctx, tx, `SELECT sm.series_id,`+summaryCoverColumns+` FROM series_members sm JOIN works w ON w.id=sm.work_id`+summaryCoverVersions+` WHERE sm.series_id IN(`+placeholders(len(ids))+`) AND sm.position=(SELECT min(first.position) FROM series_members first WHERE first.series_id=sm.series_id)`, stringArgs(ids)...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		v := &model.WorkCoverSummary{}
		if e = rows.Scan(&id, &v.WorkID, &v.EditionID, &v.ContentRevision, &v.CoverRevision); e != nil {
			return nil, e
		}
		out[id] = v
	}
	return out, rows.Err()
}
func enrichWorkCovers(ctx context.Context, tx *sql.Tx, items []model.Work) error {
	ids := make([]string, 0, len(items))
	for _, w := range items {
		ids = append(ids, w.ID)
	}
	covers, e := workCoverSummaries(ctx, tx, ids)
	if e != nil {
		return e
	}
	for i, w := range items {
		if c := covers[w.ID]; c != nil {
			items[i].ContentRevision = c.ContentRevision
			items[i].CoverRevision = c.CoverRevision
		}
	}
	return nil
}
func enrichSeriesCovers(ctx context.Context, tx *sql.Tx, items []model.Series) error {
	ids := make([]string, 0, len(items))
	for _, s := range items {
		ids = append(ids, s.ID)
	}
	covers, e := seriesCoverSummaries(ctx, tx, ids)
	if e != nil {
		return e
	}
	for i, s := range items {
		items[i].Cover = covers[s.ID]
	}
	return nil
}
func enrichSeriesProposalCovers(ctx context.Context, tx *sql.Tx, items []rec.SeriesProposal) error {
	works, series := []string{}, []string{}
	for _, p := range items {
		if p.Kind == "add_to_series" {
			series = append(series, p.Target)
		} else if len(p.Order) > 0 {
			works = append(works, p.Order[0])
		}
	}
	workCovers, e := workCoverSummaries(ctx, tx, works)
	if e != nil {
		return e
	}
	seriesCovers, e := seriesCoverSummaries(ctx, tx, series)
	if e != nil {
		return e
	}
	for i, p := range items {
		if p.Kind == "add_to_series" {
			items[i].Cover = seriesCovers[p.Target]
		} else if len(p.Order) > 0 {
			items[i].Cover = workCovers[p.Order[0]]
		}
	}
	return nil
}

// Bound presentation previews to three newly suggested Works per card, in one
// read snapshot. They are excluded from frozen plans and matching fingerprints.
func enrichSeriesNewPreviews(ctx context.Context, tx *sql.Tx, items []rec.SeriesProposal) error {
	ids := []string{}
	seen := map[string]bool{}
	for _, p := range items {
		n := 0
		for _, id := range p.Order {
			if slices.Contains(p.NewIDs, id) {
				if !seen[id] {
					ids = append(ids, id)
					seen[id] = true
				}
				n++
				if n == 3 {
					break
				}
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, e := scopeRows(ctx, tx, "SELECT "+workColumns+" FROM "+workSource+" WHERE w.id IN("+placeholders(len(ids))+")", stringArgs(ids)...)
	if e != nil {
		return e
	}
	works := []model.Work{}
	for rows.Next() {
		w, err := scanWork(rows)
		if err != nil {
			rows.Close()
			return err
		}
		works = append(works, w)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if e = enrichWorkCovers(ctx, tx, works); e != nil {
		return e
	}
	byID := map[string]model.Work{}
	for _, w := range works {
		byID[w.ID] = w
	}
	for i, p := range items {
		items[i].NewPreviews = []rec.SeriesMember{}
		for _, id := range p.Order {
			if slices.Contains(p.NewIDs, id) {
				if w, ok := byID[id]; ok {
					items[i].NewPreviews = append(items[i].NewPreviews, rec.SeriesMember{WorkID: w.ID, Name: w.Name, Title: w.Title, TitleOriginal: w.TitleOriginal, EditionID: w.PreferredEditionID, EditionCount: w.EditionCount, ContentRevision: w.ContentRevision, CoverRevision: w.CoverRevision})
				}
				if len(items[i].NewPreviews) == 3 {
					break
				}
			}
		}
	}
	return nil
}
