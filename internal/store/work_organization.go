package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	"slices"
	"strings"
)

const editionLabelsSQL = `COALESCE((SELECT json_group_array(label) FROM (SELECT label FROM edition_version_labels vl WHERE vl.edition_id=e.id ORDER BY position)),'[]')`
const workEditionColumns = `e.id,e.work_id,e.library_id,COALESCE(eb.title,f.filename_title),f.content_revision,e.organization_revision,` + editionLabelsSQL + `,COALESCE(u.favorite,0),COALESCE(u.hidden,0),COALESCE(u.revision,0)`
const workEditionSource = ` editions e JOIN files f ON f.edition_id=e.id LEFT JOIN effective_editions eb ON eb.edition_id=e.id LEFT JOIN edition_user_states u ON u.edition_id=e.id AND u.account_id=?`

func scanWorkEdition(r scanner) (model.WorkEdition, error) {
	v := model.WorkEdition{Labels: []string{}}
	var raw string
	e := r.Scan(&v.ID, &v.WorkID, &v.LibraryID, &v.Title, &v.ContentRevision, &v.Revision, &raw, &v.State.Favorite, &v.State.Hidden, &v.State.Revision)
	if errors.Is(e, sql.ErrNoRows) {
		e = model.ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal([]byte(raw), &v.Labels)
	}
	return v, e
}
func loadSeries(ctx context.Context, q queryer, id string) (model.Series, error) {
	v := model.Series{ID: id, WorkIDs: []string{}, WorkNames: map[string]string{}}
	e := scopeRow(ctx, q, "SELECT library_id,name,revision FROM series WHERE id=?", id).Scan(&v.LibraryID, &v.Name, &v.Revision)
	if errors.Is(e, sql.ErrNoRows) {
		return v, model.ErrNotFound
	}
	if e != nil {
		return v, e
	}
	rows, e := scopeRows(ctx, q, "SELECT sm.work_id,COALESCE(w.name_override,eb.title,f.filename_title) FROM series_members sm JOIN works w ON w.id=sm.work_id JOIN files f ON f.edition_id=w.preferred_edition_id LEFT JOIN effective_editions eb ON eb.edition_id=w.preferred_edition_id WHERE sm.series_id=? ORDER BY sm.position", id)
	if e != nil {
		return v, e
	}
	for rows.Next() {
		var id, name string
		if e = rows.Scan(&id, &name); e != nil {
			rows.Close()
			return v, e
		}
		v.WorkIDs = append(v.WorkIDs, id)
		v.WorkNames[id] = name
	}
	e = rows.Err()
	rows.Close()
	v.MemberCount = len(v.WorkIDs)
	return v, e
}
func loadWorkContext(ctx context.Context, q queryer, account string, editionIDs, workIDs, seriesIDs []string, expand bool) (model.WorkContext, error) {
	g := model.WorkContext{Works: []model.Work{}, Editions: []model.WorkEdition{}, Series: []model.Series{}, Expected: map[string]int64{}}
	var e error
	g.Catalog, e = catalogRevision(ctx, q)
	if e != nil {
		return g, e
	}
	selected := map[string]bool{}
	for _, id := range workIDs {
		selected[id] = true
	}
	if len(editionIDs) > 0 {
		rows, e := scopeRows(ctx, q, "SELECT id,work_id FROM editions WHERE id IN("+placeholders(len(editionIDs))+")", stringArgs(editionIDs)...)
		if e != nil {
			return g, e
		}
		n := 0
		for rows.Next() {
			var id, work string
			if e = rows.Scan(&id, &work); e != nil {
				rows.Close()
				return g, e
			}
			selected[work] = true
			n++
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return g, e
		}
		if n != len(editionIDs) {
			return g, model.ErrNotFound
		}
	}
	related := map[string]bool{}
	for _, id := range seriesIDs {
		related[id] = true
	}
	initial := []string{}
	for id := range selected {
		initial = append(initial, id)
	}
	slices.Sort(initial)
	if len(initial) > 0 {
		rows, e := scopeRows(ctx, q, "SELECT "+workColumns+" FROM "+workSource+" WHERE w.id IN("+placeholders(len(initial))+") ORDER BY w.id", stringArgs(initial)...)
		if e != nil {
			return g, e
		}
		n := 0
		for rows.Next() {
			w, e := scanWork(rows)
			if e != nil {
				rows.Close()
				return g, e
			}
			g.Works = append(g.Works, w)
			g.Expected["work:"+w.ID] = w.Revision
			if w.SeriesID != "" {
				related[w.SeriesID] = true
			}
			n++
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return g, e
		}
		if n != len(initial) {
			return g, model.ErrNotFound
		}
	}
	if len(related) > 2 {
		return g, model.ErrSeriesConflict
	}
	orderedSeries := []string{}
	for id := range related {
		orderedSeries = append(orderedSeries, id)
	}
	slices.Sort(orderedSeries)
	known := map[string]bool{}
	for _, w := range g.Works {
		known[w.ID] = true
	}
	extra := []string{}
	for _, id := range orderedSeries {
		v, e := loadSeries(ctx, q, id)
		if e != nil {
			return g, e
		}
		g.Series = append(g.Series, v)
		g.Expected["series:"+id] = v.Revision
		for _, work := range v.WorkIDs {
			if !known[work] {
				extra = append(extra, work)
				known[work] = true
			}
		}
	}
	if len(extra) > 0 {
		rows, e := scopeRows(ctx, q, "SELECT "+workColumns+" FROM "+workSource+" WHERE w.id IN("+placeholders(len(extra))+") ORDER BY w.id", stringArgs(extra)...)
		if e != nil {
			return g, e
		}
		for rows.Next() {
			w, e := scanWork(rows)
			if e != nil {
				rows.Close()
				return g, e
			}
			g.Works = append(g.Works, w)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return g, e
		}
	}
	if len(initial) > 0 && expand {
		rows, e := scopeRows(ctx, q, "SELECT "+workEditionColumns+" FROM "+workEditionSource+" WHERE e.work_id IN("+placeholders(len(initial))+") ORDER BY e.id", append([]any{account}, stringArgs(initial)...)...)
		if e != nil {
			return g, e
		}
		for rows.Next() {
			v, e := scanWorkEdition(rows)
			if e != nil {
				rows.Close()
				return g, e
			}
			g.Editions = append(g.Editions, v)
			if len(g.Editions) > 1000 {
				rows.Close()
				return g, model.ErrCapacity
			}
			g.Expected["edition:"+v.ID] = v.Revision
			for i := range g.Works {
				if g.Works[i].ID == v.WorkID {
					if g.Works[i].EditionIDs == nil {
						g.Works[i].EditionIDs = []string{}
					}
					g.Works[i].EditionIDs = append(g.Works[i].EditionIDs, v.ID)
					break
				}
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return g, e
		}
	} else if len(editionIDs) > 0 {
		rows, e := scopeRows(ctx, q, "SELECT "+workEditionColumns+" FROM "+workEditionSource+" WHERE e.id IN("+placeholders(len(editionIDs))+") ORDER BY e.id", append([]any{account}, stringArgs(editionIDs)...)...)
		if e != nil {
			return g, e
		}
		for rows.Next() {
			v, e := scanWorkEdition(rows)
			if e != nil {
				rows.Close()
				return g, e
			}
			g.Editions = append(g.Editions, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return g, e
		}
	}
	slices.SortFunc(g.Works, func(a, b model.Work) int { return strings.Compare(a.ID, b.ID) })
	return g, nil
}
func (s *Store) WorkContext(ctx context.Context, account string, editions, works, series []string) (model.WorkContext, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return model.WorkContext{}, e
	}
	defer tx.Rollback()
	g, e := loadWorkContext(ctx, tx, account, editions, works, series, true)
	if e != nil {
		return g, e
	}
	page := model.WorkEditionPage{Items: g.Editions}
	if e = enrichVersionMembers(ctx, tx, &page); e != nil {
		return g, e
	}
	g.Editions = page.Items
	return g, tx.Commit()
}
func (s *Store) ChangeWork(ctx context.Context, account string, c model.WorkCommand, preview bool, plan organization.WorkPlanner) (model.WorkResult, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return model.WorkResult{}, e
	}
	defer tx.Rollback()
	result, e := changeWorkTx(ctx, tx, account, c, preview, plan)
	if e != nil || preview {
		return result, e
	}
	return result, tx.Commit()
}
func changeWorkTx(ctx context.Context, tx *sql.Tx, account string, c model.WorkCommand, preview bool, plan organization.WorkPlanner) (model.WorkResult, error) {
	var e error
	ids := append([]string{}, c.EditionIDs...)
	if c.EditionID != "" {
		ids = append(ids, c.EditionID)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	works := append([]string{}, c.WorkIDs...)
	if strings.HasPrefix(c.Action, "work_") {
		works = append(works, c.ID)
	}
	if c.TargetWorkID != "" {
		works = append(works, c.TargetWorkID)
	}
	slices.Sort(works)
	works = slices.Compact(works)
	series := []string{}
	if strings.HasPrefix(c.Action, "series_") && c.Action != "series_create" {
		series = append(series, c.ID)
	}
	if c.TargetSeriesID != "" {
		series = append(series, c.TargetSeriesID)
	}
	slices.Sort(series)
	series = slices.Compact(series)
	before, e := loadWorkContext(ctx, tx, account, ids, works, series, !strings.HasPrefix(c.Action, "series_"))
	if errors.Is(e, model.ErrNotFound) && len(c.Expected) > 0 {
		return model.WorkResult{}, model.ErrConflict
	}
	if e != nil {
		return model.WorkResult{}, e
	}
	// Guard only Editions whose ownership or labels are actually written.
	// Work revisions guard whole membership; unrelated labels must not block naming.
	touched := map[string]bool{}
	switch c.Action {
	case "edition_move", "edition_labels":
		touched[c.EditionID] = true
	case "work_split":
		for _, id := range c.EditionIDs {
			touched[id] = true
		}
	case "work_merge":
		for _, ed := range before.Editions {
			if slices.Contains(c.WorkIDs, ed.WorkID) {
				touched[ed.ID] = true
			}
		}
	case "work_separate":
		for _, w := range before.Works {
			if w.ID == c.ID {
				for _, id := range w.EditionIDs {
					if id != w.PreferredEditionID {
						touched[id] = true
					}
				}
			}
		}
	}
	relation := c.Action == "edition_move" || c.Action == "work_split" || c.Action == "work_merge" || c.Action == "work_separate" || strings.HasPrefix(c.Action, "series_")
	for key := range before.Expected {
		if strings.HasPrefix(key, "edition:") && !touched[strings.TrimPrefix(key, "edition:")] {
			delete(before.Expected, key)
		}
		if strings.HasPrefix(key, "series:") && !relation {
			delete(before.Expected, key)
		}
	}
	if c.Action == "series_create" {
		var used int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM series WHERE id=?", c.ID).Scan(&used); e != nil {
			return model.WorkResult{}, e
		}
		if used > 0 {
			return model.WorkResult{}, model.ErrMembership
		}

		if e = validateSearchLibrary(ctx, tx, c.LibraryID); e != nil {
			return model.WorkResult{}, e
		}
		if c.LibraryID == "" {
			return model.WorkResult{}, model.ErrInput
		}
	}
	if c.Action == "series_rename" || c.Action == "series_delete" || c.Action == "series_sort" {
		before.Expected = map[string]int64{}
		for _, v := range before.Series {
			before.Expected["series:"+v.ID] = v.Revision
		}
	}
	if c.Action == "work_favorite" {
		for _, ed := range before.Editions {
			before.Expected["state:"+ed.ID] = ed.State.Revision
		}
	}
	libs := []string{}
	for _, w := range before.Works {
		if !slices.Contains(libs, w.LibraryID) {
			libs = append(libs, w.LibraryID)
		}
	}
	if len(libs) > 0 {
		var busy int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM maintenance_blocks mb JOIN jobs j ON j.id=mb.job_id WHERE mb.library_id IN("+placeholders(len(libs))+") AND j.state IN('queued','running')", stringArgs(libs)...).Scan(&busy); e != nil {
			return model.WorkResult{}, e
		}
		if busy > 0 {
			return model.WorkResult{}, model.ErrBusy
		}
	}
	newIDs := []string{}
	if c.NewWorkID != "" {
		newIDs = append(newIDs, c.NewWorkID)
	}
	for _, id := range c.NewWorkIDs {
		newIDs = append(newIDs, id)
	}
	if len(newIDs) > 0 {
		var used int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM works WHERE id IN("+placeholders(len(newIDs))+")", stringArgs(newIDs)...).Scan(&used); e != nil {
			return model.WorkResult{}, e
		}
		if used > 0 {
			return model.WorkResult{}, model.ErrMembership
		}
	}

	result, e := plan(before, c)
	if e != nil {
		return result, e
	}
	result.Preview = preview
	if preview {
		return result, nil
	}
	if c.Action == "edition_labels" {
		if e = manualVersionLabels(ctx, tx, c.EditionID, func() []string {
			for _, ed := range result.Context.Editions {
				if ed.ID == c.EditionID {
					return ed.Labels
				}
			}
			return c.Labels
		}()); e != nil {
			return result, e
		}
	}
	if e = persistWorkContext(ctx, tx, account, before, &result.Context); e != nil {
		return result, e
	}
	if e = checkOrganization(ctx, tx); e != nil {
		return result, e
	}
	result.Context.Catalog, e = catalogRevision(ctx, tx)
	if e != nil {
		return result, e
	}
	return result, nil
}
func persistWorkContext(ctx context.Context, tx *sql.Tx, account string, before model.WorkContext, after *model.WorkContext) error {
	oldW := map[string]model.Work{}
	newW := map[string]bool{}
	oldE := map[string]model.WorkEdition{}
	oldS := map[string]model.Series{}
	newS := map[string]bool{}
	for _, w := range before.Works {
		oldW[w.ID] = w
	}
	for _, v := range before.Editions {
		oldE[v.ID] = v
	}
	for _, v := range before.Series {
		oldS[v.ID] = v
	}
	exec := func(sql string, args ...any) error { _, e := tx.ExecContext(ctx, sql, args...); return e }
	for _, v := range after.Series {
		newS[v.ID] = true
	}
	for id, old := range oldS {
		var new *model.Series
		for i := range after.Series {
			if after.Series[i].ID == id {
				new = &after.Series[i]
			}
		}
		if new == nil || !slices.Equal(old.WorkIDs, new.WorkIDs) {
			if e := exec("DELETE FROM series_members WHERE series_id=?", id); e != nil {
				return e
			}
		}
	}
	for _, w := range after.Works {
		newW[w.ID] = true
		old, exists := oldW[w.ID]
		if !exists {
			if e := exec("INSERT INTO works(id,library_id,name_override,preferred_edition_id,revision) VALUES(?,?,?,?,?)", w.ID, w.LibraryID, w.NameOverride, w.PreferredEditionID, w.Revision); e != nil {
				return e
			}
		} else if old.Revision != w.Revision {
			r, e := tx.ExecContext(ctx, "UPDATE works SET name_override=?,preferred_edition_id=?,revision=? WHERE id=? AND revision=?", w.NameOverride, w.PreferredEditionID, w.Revision, w.ID, old.Revision)
			if e != nil {
				return e
			}
			n, _ := r.RowsAffected()
			if n != 1 {
				return model.ErrConflict
			}
		}
	}
	for i := range after.Editions {
		v := &after.Editions[i]
		old, exists := oldE[v.ID]
		if !exists {
			return model.ErrNotFound
		}
		if old.WorkID != v.WorkID || old.Revision != v.Revision {
			r, e := tx.ExecContext(ctx, "UPDATE editions SET work_id=?,organization_revision=? WHERE id=? AND organization_revision=?", v.WorkID, v.Revision, v.ID, old.Revision)
			if e != nil {
				return e
			}
			n, _ := r.RowsAffected()
			if n != 1 {
				return model.ErrConflict
			}
		}
		if !slices.Equal(old.Labels, v.Labels) {
			if e := exec("DELETE FROM edition_version_labels WHERE edition_id=?", v.ID); e != nil {
				return e
			}
			for i, label := range v.Labels {
				if e := exec("INSERT INTO edition_version_labels VALUES(?,?,?)", v.ID, i, label); e != nil {
					return e
				}
			}
			if e := exec("UPDATE catalog_state SET revision=revision+1"); e != nil {
				return e
			}
		}
		if old.State.Favorite != v.State.Favorite {
			state, e := setState(ctx, tx, account, v.ID, model.StatePatch{Expected: old.State.Revision, Favorite: &v.State.Favorite})
			if e != nil {
				return e
			}
			v.State = state
			after.Expected["state:"+v.ID] = state.Revision
		}
	}
	for id, w := range oldW {
		if !newW[id] {
			if e := exec("DELETE FROM works WHERE id=? AND revision=?", id, w.Revision); e != nil {
				return e
			}
			if e := exec("UPDATE catalog_state SET revision=revision+1"); e != nil {
				return e
			}
			if e := exec("INSERT INTO events(type,resource_id,revision,created_at) VALUES('work',?,?,unixepoch())", id, w.Revision+1); e != nil {
				return e
			}
		}
	}
	for _, v := range after.Series {
		old, exists := oldS[v.ID]
		if !exists {
			if e := exec("INSERT INTO series VALUES(?,?,?,?)", v.ID, v.LibraryID, v.Name, v.Revision); e != nil {
				return e
			}
		} else if old.Revision != v.Revision {
			r, e := tx.ExecContext(ctx, "UPDATE series SET name=?,revision=? WHERE id=? AND revision=?", v.Name, v.Revision, v.ID, old.Revision)
			if e != nil {
				return e
			}
			n, _ := r.RowsAffected()
			if n != 1 {
				return model.ErrConflict
			}
		}
		if !exists || !slices.Equal(old.WorkIDs, v.WorkIDs) {
			for i, id := range v.WorkIDs {
				if e := exec("INSERT INTO series_members VALUES(?,?,?)", v.ID, id, i+1); e != nil {
					return e
				}
			}
		}
		if !exists || old.Revision != v.Revision {
			if e := exec("UPDATE catalog_state SET revision=revision+1"); e != nil {
				return e
			}
			if e := exec("INSERT INTO events(type,resource_id,revision,created_at) VALUES('series',?,?,unixepoch())", v.ID, v.Revision); e != nil {
				return e
			}
		}
	}
	for id, v := range oldS {
		if !newS[id] {
			if e := exec("DELETE FROM series WHERE id=? AND revision=?", id, v.Revision); e != nil {
				return e
			}
			if e := exec("UPDATE catalog_state SET revision=revision+1"); e != nil {
				return e
			}
			if e := exec("INSERT INTO events(type,resource_id,revision,created_at) VALUES('series',?,?,unixepoch())", id, v.Revision+1); e != nil {
				return e
			}
		}
	}
	// Only membership changes activate legacy-empty policies; metadata refresh keeps
	// historical groups unchanged until the user explicitly generates labels.
	affected := map[string]bool{}
	for _, v := range after.Editions {
		if old, ok := oldE[v.ID]; ok && old.WorkID != v.WorkID {
			affected[old.WorkID] = true
			affected[v.WorkID] = true
		}
	}
	for id := range affected {
		if e := refreshWorkLabels(ctx, tx, id, true); e != nil {
			return e
		}
	}
	return nil
}
