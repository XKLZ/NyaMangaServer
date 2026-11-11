package organization

import (
	"bytes"
	"context"
	"encoding/json"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/textkey"
	"reflect"
	"slices"
	"strings"
)

type WorkPlanner func(model.WorkContext, model.WorkCommand) (model.WorkResult, error)
type WorkRepository interface {
	EditionDetails(context.Context, string, string) (*model.Edition, model.MetadataDetails, model.UserState, model.WorkContext, error)
	WorkContext(context.Context, string, []string, []string, []string) (model.WorkContext, error)
	ChangeWork(context.Context, string, model.WorkCommand, bool, WorkPlanner) (model.WorkResult, error)
	WorkPage(context.Context, string, string, int, int, ...int) ([]model.Work, int, error)
	Work(context.Context, string) (model.Work, error)
	WorkEditions(context.Context, string, int, int) ([]model.Edition, int, error)
	WorkEditionPage(context.Context, string, string, string, int, int) (model.WorkEditionPage, error)
	SeriesPage(context.Context, string, string, int, int) (model.SeriesList, error)
	SeriesDetail(context.Context, string, ...bool) (model.Series, error)
	SeriesWorkPage(context.Context, string, string, int, int) (model.SeriesWorkPage, error)
}

func (s *Service) WorkContext(ctx context.Context, account string, editions, works, series []string) (model.WorkContext, error) {
	if len(editions)+len(works)+len(series) < 1 || len(editions) > 1000 || len(works) > 1000 || len(series) > 2 || !unique(editions) || !unique(works) || !unique(series) {
		return model.WorkContext{}, model.ErrInput
	}
	return s.repo.WorkContext(ctx, account, editions, works, series)
}
func (s *Service) Works(ctx context.Context, library, q string, page, size int, minimum ...int) (model.WorkList, error) {
	out := model.WorkList{Page: page, PageSize: size}
	if !validPage(page, size) || !validText(q, 512, true) {
		return out, model.ErrInput
	}
	var e error
	if len(minimum) > 0 && (minimum[0] < 1 || minimum[0] > 100) {
		return out, model.ErrInput
	}
	out.Items, out.Total, e = s.repo.WorkPage(ctx, library, q, page, size, minimum...)
	return out, e
}
func (s *Service) Work(ctx context.Context, id string) (model.Work, error) {
	return s.repo.Work(ctx, id)
}
func (s *Service) Editions(ctx context.Context, account, id, q string, page, size int) (model.WorkEditionPage, error) {
	if !validPage(page, size) || !validText(q, 512, true) {
		return model.WorkEditionPage{}, model.ErrInput
	}
	return s.repo.WorkEditionPage(ctx, account, id, q, page, size)
}
func (s *Service) SeriesPage(ctx context.Context, library, q string, page, size int) (model.SeriesList, error) {
	if !validPage(page, size) || !validText(q, 512, true) {
		return model.SeriesList{}, model.ErrInput
	}
	return s.repo.SeriesPage(ctx, library, q, page, size)
}
func (s *Service) Series(ctx context.Context, id string, summary ...bool) (model.Series, error) {
	return s.repo.SeriesDetail(ctx, id, summary...)
}
func (s *Service) SeriesWorks(ctx context.Context, id, q string, page, size int) (model.SeriesWorkPage, error) {
	if !validPage(page, size) || !validText(q, 512, true) {
		return model.SeriesWorkPage{}, model.ErrInput
	}
	return s.repo.SeriesWorkPage(ctx, id, q, page, size)
}
func validPage(page, size int) bool { return page > 0 && page <= 10000000 && size > 0 && size <= 100 }
func (s *Service) ChangeWork(ctx context.Context, account string, c model.WorkCommand, preview bool) (model.WorkResult, error) {
	if (c.LegacyItemIDsSet && c.WorkIDsSet) || (c.LegacyItemIDs != nil && c.WorkIDs != nil) {
		return model.WorkResult{}, model.ErrAliasFields
	}
	if c.LegacyItemIDs != nil {
		c.WorkIDs = c.LegacyItemIDs
		c.LegacyItemIDs = nil
	}
	c.LegacyItemIDsSet = false
	if c.Action == "edition_labels" && c.Labels == nil {
		return model.WorkResult{}, model.ErrInput
	}
	if c.Action == "series_members" && c.WorkIDs == nil && c.EditionIDs == nil {
		return model.WorkResult{}, model.ErrInput
	}
	if c.Action == "series_members" && c.EditionIDs != nil && len(c.EditionIDs) == 0 {
		c.WorkIDs = []string{}
		c.EditionIDs = nil
	}
	if len(c.NewWorkIDs) > 99 {
		return model.WorkResult{}, model.ErrInput
	}
	for _, id := range append([]string{c.NewWorkID}, mapValues(c.NewWorkIDs)...) {
		if !validText(id, 128, true) {
			return model.WorkResult{}, model.ErrInput
		}
	}

	if len(c.EditionIDs) > 1000 || len(c.WorkIDs) > 1000 || len(c.Expected) > 2500 || !unique(c.EditionIDs) || !unique(c.WorkIDs) {
		return model.WorkResult{}, model.ErrInput
	}
	allowed := []string{"work_name", "work_preferred", "edition_labels", "edition_move", "work_split", "work_merge", "work_separate", "work_favorite", "series_create", "series_rename", "series_delete", "series_members", "series_add", "series_remove", "series_move", "series_sort", "series_transfer"}
	if !slices.Contains(allowed, c.Action) {
		return model.WorkResult{}, model.ErrInput
	}
	if c.Action == "work_split" && c.NewWorkID == "" {
		id, e := model.NewID()
		if e != nil {
			return model.WorkResult{}, e
		}
		c.NewWorkID = id
	}
	if c.Action == "series_create" && c.ID == "" {
		id, e := model.NewID()
		if e != nil {
			return model.WorkResult{}, e
		}
		c.ID = id
	}
	if c.Action == "work_separate" && preview && len(c.NewWorkIDs) == 0 {
		g, e := s.repo.WorkContext(ctx, account, nil, []string{c.ID}, nil)
		if e != nil {
			return model.WorkResult{}, e
		}
		c.NewWorkIDs = map[string]string{}
		for _, w := range g.Works {
			if w.ID == c.ID {
				for _, id := range w.EditionIDs {
					if id != w.PreferredEditionID {
						n, e := model.NewID()
						if e != nil {
							return model.WorkResult{}, e
						}
						c.NewWorkIDs[id] = n
					}
				}
			}
		}
	}
	return s.repo.ChangeWork(ctx, account, c, preview, PlanWork)
}

func PlanWork(input model.WorkContext, c model.WorkCommand) (model.WorkResult, error) {
	raw, _ := json.Marshal(input)
	var g model.WorkContext
	_ = json.Unmarshal(raw, &g)
	out := model.WorkResult{Context: g, Command: c, ID: c.ID, Effects: []model.WorkEffect{}}
	fail := func(e error) (model.WorkResult, error) { return out, e }
	for key, want := range input.Expected {
		if got, ok := c.Expected[key]; !ok || got != want {
			return fail(model.ErrConflict)
		}
	}
	wi := func(id string) int {
		for i, w := range g.Works {
			if w.ID == id {
				return i
			}
		}
		return -1
	}
	ei := func(id string) int {
		for i, e := range g.Editions {
			if e.ID == id {
				return i
			}
		}
		return -1
	}
	si := func(id string) int {
		for i, s := range g.Series {
			if s.ID == id {
				return i
			}
		}
		return -1
	}
	removeWork := func(id string) {
		for i := range g.Series {
			g.Series[i].WorkIDs = slices.DeleteFunc(g.Series[i].WorkIDs, func(v string) bool { return v == id })
		}
		g.Works = slices.DeleteFunc(g.Works, func(w model.Work) bool { return w.ID == id })
	}
	remainingPreferred := func(w *model.Work, moving []string) error {
		if !slices.Contains(moving, w.PreferredEditionID) {
			if c.RemainingPreferredEditionID != "" && c.RemainingPreferredEditionID != w.PreferredEditionID {
				return model.ErrInput
			}
			return nil
		}
		left := []string{}
		for _, id := range w.EditionIDs {
			if !slices.Contains(moving, id) {
				left = append(left, id)
			}
		}
		if len(left) == 0 {
			return nil
		}
		if c.RemainingPreferredEditionID == "" {
			return model.ErrPreferredRequired
		}
		if !slices.Contains(left, c.RemainingPreferredEditionID) {
			return model.ErrInput
		}
		w.PreferredEditionID = c.RemainingPreferredEditionID
		return nil
	}
	moveEdition := func(id string, to *model.Work) error {
		idx := ei(id)
		if idx < 0 {
			return model.ErrNotFound
		}
		ed := &g.Editions[idx]
		ed.WorkID = to.ID
		to.EditionIDs = append(to.EditionIDs, id)
		return nil
	}
	target := wi(c.ID)
	switch c.Action {
	case "work_name":
		if target < 0 {
			return fail(model.ErrNotFound)
		}
		w := &g.Works[target]
		switch c.NameOp {
		case "inherit":
			w.NameOverride = nil
		case "set":
			if !validText(c.Name, 512, false) {
				return fail(model.ErrInput)
			}
			v := strings.TrimSpace(c.Name)
			w.NameOverride = &v
		default:
			return fail(model.ErrInput)
		}
	case "work_preferred":
		if target < 0 {
			return fail(model.ErrNotFound)
		}
		w := &g.Works[target]
		if !slices.Contains(w.EditionIDs, c.PreferredEditionID) {
			return fail(model.ErrMembership)
		}
		w.PreferredEditionID = c.PreferredEditionID
	case "edition_labels":
		i := ei(c.EditionID)
		if i < 0 {
			return fail(model.ErrNotFound)
		}
		labels, e := cleanLabels(c.Labels)
		if e != nil {
			return fail(e)
		}
		g.Editions[i].Labels = labels
	case "edition_move":
		i := ei(c.EditionID)
		if i < 0 {
			return fail(model.ErrNotFound)
		}
		fromID := g.Editions[i].WorkID
		from := wi(fromID)
		to := wi(c.TargetWorkID)
		if from < 0 || to < 0 {
			return fail(model.ErrNotFound)
		}
		if from == to {
			return fail(model.ErrInput)
		}
		if g.Works[from].LibraryID != g.Works[to].LibraryID {
			return fail(model.ErrLibraryConflict)
		}
		if len(g.Works[to].EditionIDs) >= 100 {
			return fail(model.ErrCapacity)
		}
		if e := remainingPreferred(&g.Works[from], []string{c.EditionID}); e != nil {
			return fail(e)
		}
		fromSeries, toSeries := g.Works[from].SeriesID, g.Works[to].SeriesID
		if e := moveEdition(c.EditionID, &g.Works[to]); e != nil {
			return fail(e)
		}
		g.Works[from].EditionIDs = slices.DeleteFunc(g.Works[from].EditionIDs, func(id string) bool { return id == c.EditionID })
		empty := len(g.Works[from].EditionIDs) == 0
		out.Effects = append(out.Effects, model.WorkEffect{EditionID: c.EditionID, FromWorkID: fromID, ToWorkID: c.TargetWorkID, FromSeriesID: fromSeries, ToSeriesID: toSeries, RemovedWork: empty})
		if empty {
			removeWork(fromID)
		}
		out.ID = c.TargetWorkID
	case "work_split":
		if target < 0 {
			return fail(model.ErrNotFound)
		}
		source := &g.Works[target]
		if len(c.EditionIDs) == 0 || len(c.EditionIDs) >= len(source.EditionIDs) || c.NewWorkID == "" || wi(c.NewWorkID) >= 0 {
			return fail(model.ErrInput)
		}
		for _, id := range c.EditionIDs {
			if !slices.Contains(source.EditionIDs, id) {
				return fail(model.ErrMembership)
			}
		}
		if e := remainingPreferred(source, c.EditionIDs); e != nil {
			return fail(e)
		}
		preferred := c.PreferredEditionID
		if preferred == "" {
			preferred = slices.Min(c.EditionIDs)
		}
		if !slices.Contains(c.EditionIDs, preferred) {
			return fail(model.ErrInput)
		}
		fresh := model.Work{ID: c.NewWorkID, LibraryID: source.LibraryID, PreferredEditionID: preferred, Revision: 1, EditionIDs: []string{}}
		oldSeries := source.SeriesID
		if c.KeepSeries && oldSeries != "" {
			idx := si(oldSeries)
			if idx < 0 {
				return fail(model.ErrNotFound)
			}
			if len(g.Series[idx].WorkIDs) >= 1000 {
				return fail(model.ErrCapacity)
			}
			at := slices.Index(g.Series[idx].WorkIDs, source.ID)
			g.Series[idx].WorkIDs = slices.Insert(g.Series[idx].WorkIDs, at+1, fresh.ID)
		}
		for _, id := range c.EditionIDs {
			if e := moveEdition(id, &fresh); e != nil {
				return fail(e)
			}
			toSeries := ""
			if c.KeepSeries {
				toSeries = oldSeries
			}
			out.Effects = append(out.Effects, model.WorkEffect{EditionID: id, FromWorkID: source.ID, ToWorkID: fresh.ID, FromSeriesID: oldSeries, ToSeriesID: toSeries})
		}
		source.EditionIDs = slices.DeleteFunc(source.EditionIDs, func(id string) bool { return slices.Contains(c.EditionIDs, id) })
		g.Works = append(g.Works, fresh)
		out.ID = fresh.ID
	case "work_merge":
		if target < 0 || len(c.WorkIDs) == 0 || slices.Contains(c.WorkIDs, c.ID) {
			return fail(model.ErrInput)
		}
		selected := append([]string{c.ID}, c.WorkIDs...)
		sid := ""
		count := len(g.Works[target].EditionIDs)
		for _, id := range selected {
			i := wi(id)
			if i < 0 {
				return fail(model.ErrNotFound)
			}
			if g.Works[i].LibraryID != g.Works[target].LibraryID {
				return fail(model.ErrLibraryConflict)
			}
			if id != c.ID {
				count += len(g.Works[i].EditionIDs)
			}
			if g.Works[i].SeriesID != "" {
				if sid != "" && sid != g.Works[i].SeriesID {
					return fail(model.ErrSeriesConflict)
				}
				sid = g.Works[i].SeriesID
			}
		}
		if count > 100 {
			return fail(model.ErrCapacity)
		}
		if sid != "" {
			j := si(sid)
			seq := []string{}
			inserted := false
			for _, id := range g.Series[j].WorkIDs {
				if slices.Contains(selected, id) {
					if !inserted {
						seq = append(seq, c.ID)
						inserted = true
					}
				} else {
					seq = append(seq, id)
				}
			}
			g.Series[j].WorkIDs = seq
		}
		for _, id := range c.WorkIDs {
			from := wi(id)
			to := wi(c.ID)
			members := append([]string{}, g.Works[from].EditionIDs...)
			for _, ed := range members {
				if e := moveEdition(ed, &g.Works[to]); e != nil {
					return fail(e)
				}
				out.Effects = append(out.Effects, model.WorkEffect{EditionID: ed, FromWorkID: id, ToWorkID: c.ID, FromSeriesID: g.Works[from].SeriesID, ToSeriesID: sid, RemovedWork: true})
			}
			g.Works = slices.DeleteFunc(g.Works, func(w model.Work) bool { return w.ID == id })
		}
	case "work_separate":
		if target < 0 {
			return fail(model.ErrNotFound)
		}
		source := &g.Works[target]
		if len(source.EditionIDs) < 2 {
			return fail(model.ErrInput)
		}
		count := len(source.EditionIDs) - 1
		if len(c.NewWorkIDs) != count {
			return fail(model.ErrInput)
		}
		sid := source.SeriesID
		idx := si(sid)
		if c.KeepSeries && sid != "" && len(g.Series[idx].WorkIDs)+count > 1000 {
			return fail(model.ErrCapacity)
		}
		ids := append([]string{}, source.EditionIDs...)
		slices.Sort(ids)
		created := []model.Work{}
		used := map[string]bool{}
		for _, ed := range ids {
			if ed == source.PreferredEditionID {
				continue
			}
			id := c.NewWorkIDs[ed]
			if id == "" || wi(id) >= 0 || used[id] {
				return fail(model.ErrInput)
			}
			used[id] = true
			fresh := model.Work{ID: id, LibraryID: source.LibraryID, PreferredEditionID: ed, EditionIDs: []string{}, Revision: 1}
			if e := moveEdition(ed, &fresh); e != nil {
				return fail(e)
			}
			created = append(created, fresh)
			toSid := ""
			if c.KeepSeries {
				toSid = sid
			}
			out.Effects = append(out.Effects, model.WorkEffect{EditionID: ed, FromWorkID: source.ID, ToWorkID: id, FromSeriesID: sid, ToSeriesID: toSid})
		}
		source.EditionIDs = []string{source.PreferredEditionID}
		if c.KeepSeries && sid != "" {
			at := slices.Index(g.Series[idx].WorkIDs, source.ID) + 1
			newIDs := []string{}
			for _, w := range created {
				newIDs = append(newIDs, w.ID)
			}
			g.Series[idx].WorkIDs = slices.Insert(g.Series[idx].WorkIDs, at, newIDs...)
		}
		g.Works = append(g.Works, created...)
	case "work_favorite":
		if target < 0 || c.Value == nil {
			return fail(model.ErrInput)
		}
		for i := range g.Editions {
			if g.Editions[i].WorkID == c.ID {
				if rev, ok := c.Expected["state:"+g.Editions[i].ID]; !ok || rev != g.Editions[i].State.Revision {
					return fail(model.ErrConflict)
				}
				g.Editions[i].State.Favorite = *c.Value
			}
		}
	case "series_create":
		if c.ID == "" || si(c.ID) >= 0 || !validText(c.Name, 200, false) || c.LibraryID == "" {
			return fail(model.ErrInput)
		}
		g.Series = append(g.Series, model.Series{ID: c.ID, LibraryID: c.LibraryID, Name: strings.TrimSpace(c.Name), Revision: 1, WorkIDs: []string{}})
		fallthrough
	case "series_members", "series_add", "series_remove", "series_move", "series_sort", "series_rename", "series_delete", "series_transfer":
		j := si(c.ID)
		if j < 0 {
			return fail(model.ErrNotFound)
		}
		series := &g.Series[j]
		ids := append([]string{}, c.WorkIDs...)
		if len(c.EditionIDs) > 0 {
			if len(ids) > 0 {
				return fail(model.ErrAliasFields)
			}
			for _, id := range c.EditionIDs {
				i := ei(id)
				if i < 0 {
					return fail(model.ErrNotFound)
				}
				work := g.Editions[i].WorkID
				if !slices.Contains(ids, work) {
					ids = append(ids, work)
				}
			}
		}
		switch c.Action {
		case "series_create", "series_members", "series_add":
			if c.Action == "series_add" && len(ids) == 0 {
				return fail(model.ErrInput)
			}
			for _, id := range ids {
				i := wi(id)
				if i < 0 {
					return fail(model.ErrNotFound)
				}
				if g.Works[i].LibraryID != series.LibraryID {
					return fail(model.ErrLibraryConflict)
				}
				if g.Works[i].SeriesID != "" && g.Works[i].SeriesID != series.ID {
					return fail(model.ErrSeriesConflict)
				}
			}
			if c.Action == "series_add" {
				for _, id := range ids {
					if !slices.Contains(series.WorkIDs, id) {
						series.WorkIDs = append(series.WorkIDs, id)
					}
				}
			} else {
				series.WorkIDs = ids
			}
		case "series_remove":
			if len(ids) != 1 || !slices.Contains(series.WorkIDs, ids[0]) {
				return fail(model.ErrMembership)
			}
			series.WorkIDs = slices.DeleteFunc(series.WorkIDs, func(id string) bool { return id == ids[0] })
		case "series_move":
			if len(ids) != 1 || !slices.Contains(series.WorkIDs, ids[0]) || c.Position < 0 || c.Position >= len(series.WorkIDs) {
				return fail(model.ErrInput)
			}
			series.WorkIDs = slices.DeleteFunc(series.WorkIDs, func(id string) bool { return id == ids[0] })
			series.WorkIDs = slices.Insert(series.WorkIDs, c.Position, ids[0])
		case "series_sort":
			if c.Snapshot == "" || c.Snapshot != g.Catalog {
				return fail(model.ErrSnapshot)
			}
			slices.SortStableFunc(series.WorkIDs, func(a, b string) int {
				wa, wb := wi(a), wi(b)
				cmp := bytes.Compare(textkey.Natural(g.Works[wa].Name), textkey.Natural(g.Works[wb].Name))
				if cmp == 0 {
					return strings.Compare(a, b)
				}
				return cmp
			})
		case "series_rename":
			if !validText(c.Name, 200, false) {
				return fail(model.ErrInput)
			}
			series.Name = strings.TrimSpace(c.Name)
		case "series_delete":
			g.Series = slices.Delete(g.Series, j, j+1)
		case "series_transfer":
			to := si(c.TargetSeriesID)
			if to < 0 {
				return fail(model.ErrNotFound)
			}
			if to == j || len(ids) == 0 {
				return fail(model.ErrInput)
			}
			if g.Series[to].LibraryID != series.LibraryID {
				return fail(model.ErrLibraryConflict)
			}
			for _, id := range ids {
				if !slices.Contains(series.WorkIDs, id) {
					return fail(model.ErrMembership)
				}
			}
			series.WorkIDs = slices.DeleteFunc(series.WorkIDs, func(id string) bool { return slices.Contains(ids, id) })
			g.Series[to].WorkIDs = append(g.Series[to].WorkIDs, ids...)
		}
	default:
		return fail(model.ErrInput)
	}
	// Recompute derived ownership, names and revisions from the final graph.
	for i := range g.Series {
		if len(g.Series[i].WorkIDs) > 1000 || !unique(g.Series[i].WorkIDs) {
			return fail(model.ErrCapacity)
		}
		g.Series[i].MemberCount = len(g.Series[i].WorkIDs)
		for _, old := range input.Series {
			if old.ID == g.Series[i].ID && !reflect.DeepEqual(old.WorkIDs, g.Series[i].WorkIDs) || old.ID == g.Series[i].ID && old.Name != g.Series[i].Name {
				g.Series[i].Revision = old.Revision + 1
			}
		}
	}
	owner := map[string]string{}
	for _, s := range g.Series {
		for _, id := range s.WorkIDs {
			if owner[id] != "" {
				return fail(model.ErrSeriesConflict)
			}
			owner[id] = s.ID
		}
	}
	for i := range g.Works {
		w := &g.Works[i]
		w.SeriesID = ""
		w.Position = 0
		for _, s := range g.Series {
			if at := slices.Index(s.WorkIDs, w.ID); at >= 0 {
				w.SeriesID = s.ID
				w.Position = at + 1
			}
		}
		if w.EditionIDs != nil {
			if len(w.EditionIDs) < 1 || len(w.EditionIDs) > 100 || !unique(w.EditionIDs) || !slices.Contains(w.EditionIDs, w.PreferredEditionID) {
				return fail(model.ErrMembership)
			}
			w.EditionCount = len(w.EditionIDs)
		}
		if w.NameOverride != nil {
			w.Name = *w.NameOverride
		} else {
			idx := ei(w.PreferredEditionID)
			if idx >= 0 {
				w.Name = g.Editions[idx].Title
			}
		}
		for _, old := range input.Works {
			if old.ID == w.ID && !reflect.DeepEqual(old, *w) {
				w.Revision = old.Revision + 1
			}
		}
	}
	for i := range g.Editions {
		ed := &g.Editions[i]
		for _, old := range input.Editions {
			if old.ID == ed.ID && (old.WorkID != ed.WorkID || !reflect.DeepEqual(old.Labels, ed.Labels)) {
				ed.Revision = old.Revision + 1
			}
		}
	}
	g.Expected = map[string]int64{}
	for _, w := range g.Works {
		_, guarded := input.Expected["work:"+w.ID]
		existing := false
		for _, old := range input.Works {
			if old.ID == w.ID {
				existing = true
				break
			}
		}
		if guarded || !existing {
			g.Expected["work:"+w.ID] = w.Revision
		}
	}
	for _, ed := range g.Editions {
		g.Expected["edition:"+ed.ID] = ed.Revision
	}
	for _, s := range g.Series {
		g.Expected["series:"+s.ID] = s.Revision
	}
	for i := range g.Series {
		g.Series[i].WorkNames = map[string]string{}
		for _, id := range g.Series[i].WorkIDs {
			index := wi(id)
			if index >= 0 {
				g.Series[i].WorkNames[id] = g.Works[index].Name
			}
		}
	}
	out.Context = g
	return out, nil
}

func mapValues(v map[string]string) []string {
	out := []string{}
	for _, id := range v {
		out = append(out, id)
	}
	return out
}

func (s *Service) GenerateVersionLabels(ctx context.Context, account, id string, expected map[string]int64, restore []string) (model.WorkEditionPage, error) {
	r, ok := s.repo.(interface {
		GenerateVersionLabels(context.Context, string, string, map[string]int64, []string) (model.WorkEditionPage, error)
	})
	if !ok || len(expected) < 1 || len(expected) > 2500 || len(restore) > 100 || !unique(restore) {
		return model.WorkEditionPage{}, model.ErrInput
	}
	return r.GenerateVersionLabels(ctx, account, id, expected, restore)
}
