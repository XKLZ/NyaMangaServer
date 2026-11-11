package organization

import (
	"nyamangaserver.local/server/internal/model"
	"slices"
)

// PlanWorkAddition moves only singleton Editions. Existing target members and its
// Series identity are preserved; it never merges two existing multi-Edition Works.
func PlanWorkAddition(input model.WorkContext, target string, ids []string) (model.WorkResult, error) {
	if len(ids) < 1 || len(ids) > 99 || !unique(ids) {
		return model.WorkResult{}, model.ErrInput
	}
	var dest *model.Work
	for i := range input.Works {
		if input.Works[i].ID == target {
			dest = &input.Works[i]
		}
	}
	if dest == nil || len(dest.EditionIDs) < 2 {
		return model.WorkResult{}, model.ErrMembership
	}
	if len(dest.EditionIDs)+len(ids) > 100 {
		return model.WorkResult{}, model.ErrCapacity
	}
	for _, id := range ids {
		var owner *model.Work
		for i := range input.Works {
			if slices.Contains(input.Works[i].EditionIDs, id) {
				owner = &input.Works[i]
			}
		}
		if owner == nil || len(owner.EditionIDs) != 1 {
			return model.WorkResult{}, model.ErrMembership
		}
		if owner.LibraryID != dest.LibraryID {
			return model.WorkResult{}, model.ErrLibraryConflict
		}
		if owner.SeriesID != "" && dest.SeriesID != "" && owner.SeriesID != dest.SeriesID {
			return model.WorkResult{}, model.ErrSeriesConflict
		}
	}
	current := input
	out := model.WorkResult{ID: target, Effects: []model.WorkEffect{}}
	for _, id := range ids {
		command := model.WorkCommand{Action: "edition_move", EditionID: id, TargetWorkID: target, Expected: current.Expected}
		next, e := PlanWork(current, command)
		if e != nil {
			return next, e
		}
		out.Effects = append(out.Effects, next.Effects...)
		current = next.Context
		out.Command = next.Command
	}
	out.Context = current
	return out, nil
}
