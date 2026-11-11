package httpapi

import (
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
)

// HTTP-only fields cannot alter frozen organization/worker input JSON.
type seriesResponse struct {
	model.Series
	Cover *model.WorkCoverSummary `json:"cover"`
}

func seriesView(s model.Series) seriesResponse { return seriesResponse{Series: s, Cover: s.Cover} }
func seriesPageView(p model.SeriesList) any {
	items := make([]seriesResponse, 0, len(p.Items))
	for _, s := range p.Items {
		items = append(items, seriesView(s))
	}
	return struct {
		model.SeriesList
		Items []seriesResponse `json:"items"`
	}{p, items}
}

type seriesProposalResponse struct {
	rec.SeriesProposal
	NewPreviews []rec.SeriesMember      `json:"new_member_previews"`
	Cover       *model.WorkCoverSummary `json:"cover"`
}

func seriesProposalView(p rec.SeriesProposal) seriesProposalResponse {
	return seriesProposalResponse{SeriesProposal: p, Cover: p.Cover, NewPreviews: p.NewPreviews}
}
func seriesProposalPageView(p rec.SeriesProposalPage) any {
	items := make([]seriesProposalResponse, 0, len(p.Items))
	for _, s := range p.Items {
		items = append(items, seriesProposalView(s))
	}
	return struct {
		rec.SeriesProposalPage
		Items []seriesProposalResponse `json:"items"`
	}{p, items}
}
