package recommendation

import (
	"context"
	"nyamangaserver.local/server/internal/model"
	"slices"
)

const SeriesAlgorithm = "series-recommendation-v2.1"

type SeriesRun struct {
	Presentation string                  `json:"presentation_version,omitempty"`
	JobID        string                  `json:"job_id"`
	LibraryID    string                  `json:"library_id"`
	Pool         Pool                    `json:"pool"`
	Context      model.WorkContext       `json:"context"`
	Token        string                  `json:"input_token"`
	Features     map[string]PackedVector `json:"features"`
	FeaturesDone int                     `json:"features_done"`
	NextRow      int                     `json:"next_row"`
	Phase        string                  `json:"phase"`
	Edges        [][2]string             `json:"edges"`
}
type SeriesBlock struct {
	Row     int         `json:"row"`
	NextRow int         `json:"next_row"`
	Edges   [][2]string `json:"edges"`
}
type SeriesOrder struct {
	Suggested []string `json:"suggested_order"`
	WorkIDs   []string `json:"work_ids"`
	State     string   `json:"state"`
}
type SeriesRawPlan struct {
	ID      string      `json:"id"`
	Kind    string      `json:"kind"`
	WorkIDs []string    `json:"work_ids"`
	NewIDs  []string    `json:"new_work_ids"`
	Target  string      `json:"target_series_id"`
	Name    string      `json:"suggested_name"`
	Tier    string      `json:"tier"`
	Risks   []string    `json:"risks"`
	Order   SeriesOrder `json:"order"`
}
type SeriesOption struct {
	Kind          string      `json:"kind"`
	Target        string      `json:"target_id"`
	TargetWorkIDs []string    `json:"target_work_ids"`
	WorkIDs       []string    `json:"combined_work_ids"`
	Tier          string      `json:"tier"`
	Risks         []string    `json:"risks"`
	Order         SeriesOrder `json:"order"`
	ProposalID    string      `json:"proposal_id,omitempty"`
}
type SeriesPending struct {
	Title           string         `json:"title"`
	TitleOriginal   string         `json:"title_original,omitempty"`
	Name            string         `json:"name"`
	EditionID       string         `json:"preferred_edition_id"`
	ContentRevision int64          `json:"content_revision"`
	CoverRevision   int64          `json:"cover_revision"`
	WorkID          string         `json:"work_id"`
	Reason          string         `json:"reason"`
	Options         []SeriesOption `json:"options"`
}
type SeriesResult struct {
	Algorithm   string          `json:"algorithm_version"`
	Proposals   []SeriesRawPlan `json:"proposals"`
	Pending     []SeriesPending `json:"pending"`
	Unavailable []string        `json:"unavailable_series"`
}
type SeriesSelection struct {
	Customize bool     `json:"customize,omitempty"`
	Revision  int64    `json:"expected_revision"`
	IDs       []string `json:"work_ids,omitempty"`
	Name      *string  `json:"name,omitempty"`
	Order     []string `json:"order,omitempty"`
}
type SeriesProposal struct {
	StorageKey  string         `json:"-"`
	Scope       string         `json:"-"`
	NewPreviews []SeriesMember `json:"-"`
	// Read-only presentation data, never included in saved plans or fingerprints.
	Cover          *model.WorkCoverSummary `json:"-"`
	Presentation   string                  `json:"-"`
	NameRequired   bool                    `json:"name_required,omitempty"`
	OrderBasis     string                  `json:"order_basis,omitempty"`
	ID             string                  `json:"id"`
	JobID          string                  `json:"job_id"`
	LibraryID      string                  `json:"library_id"`
	Key            string                  `json:"-"`
	Kind           string                  `json:"kind"`
	Target         string                  `json:"target_series_id"`
	Name           string                  `json:"name"`
	NewIDs         []string                `json:"new_work_ids"`
	ExistingIDs    []string                `json:"existing_work_ids"`
	Order          []string                `json:"order"`
	OrderUncertain bool                    `json:"order_uncertain"`
	Tier           string                  `json:"tier"`
	Risks          []string                `json:"risks"`
	Primary        bool                    `json:"primary"`
	Revision       int64                   `json:"revision"`
	State          string                  `json:"state"`
	CanUndo        bool                    `json:"can_undo"`
	MemberCount    int                     `json:"member_count"`
	Expected       map[string]int64        `json:"-"`
	Inputs         map[string]string       `json:"-"`
}

type SeriesProposalFilter struct {
	OnlyAlternatives bool
	ImportBatchID    string
	TargetSeriesID   string
	Alternatives     bool
}

// Private persisted representation includes CAS evidence, unlike the HTTP DTO.
type StoredSeriesProposal struct {
	Presentation string            `json:"presentation_version,omitempty"`
	Proposal     SeriesProposal    `json:"proposal"`
	Expected     map[string]int64  `json:"expected"`
	Inputs       map[string]string `json:"inputs"`
}

// Semantic identity ignores display defaults and generated create-Series IDs.
func SeriesSemanticKey(p SeriesProposal) string {
	target := p.Target
	if p.Kind == "create_series" {
		target = ""
	}
	ids := append([]string{}, p.Order...)
	slices.Sort(ids)
	return Fingerprint([]any{SeriesAlgorithm, p.Kind, target, ids, p.Inputs})
}

type SeriesMember struct {
	Title           string `json:"title"`
	TitleOriginal   string `json:"title_original,omitempty"`
	ContentRevision int64  `json:"content_revision"`
	CoverRevision   int64  `json:"cover_revision"`
	WorkID          string `json:"work_id"`
	Name            string `json:"name"`
	EditionID       string `json:"preferred_edition_id"`
	EditionCount    int    `json:"edition_count"`
	Existing        bool   `json:"existing"`
}
type SeriesProposalPage struct {
	Items    []SeriesProposal `json:"items"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}
type SeriesMemberPage struct {
	Items    []SeriesMember `json:"items"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Revision int64          `json:"revision"`
}
type SeriesPendingPage struct {
	Unavailable int             `json:"unavailable_series_count"`
	Items       []SeriesPending `json:"items"`
	Total       int             `json:"total"`
	Page        int             `json:"page"`
	PageSize    int             `json:"page_size"`
}

func SeriesInput(v InputEdition) string {
	return Fingerprint(struct {
		ID, Library, Title, Original string
		Tags                         [][2]string
	}{v.Book.ID, v.Book.LibraryID, v.Book.Title, v.Book.TitleOriginal, v.Book.Tags})
}

type SeriesRepository interface {
	CreateSeriesDiscovery(context.Context, string, string, string) (*model.Job, error)
	SeriesRun(context.Context, string, bool) (SeriesRun, error)
	SaveSeriesFeature(context.Context, model.Job, int, InputEdition, *PackedVector) error
	SaveSeriesBlock(context.Context, model.Job, SeriesBlock) error
	PublishSeries(context.Context, model.Job, SeriesRun, SeriesResult, bool) error
	SeriesRefreshJob(context.Context) (string, error)
	StopSeriesRefresh(context.Context, string, string) error
	SeriesProposals(context.Context, string, string, string, string, int, int, ...SeriesProposalFilter) (SeriesProposalPage, error)
	SeriesProposal(context.Context, string) (SeriesProposal, error)
	SeriesMembers(context.Context, string, int, int) (SeriesMemberPage, error)
	SeriesPending(context.Context, string, int, int) (SeriesPendingPage, error)
	ResolveSeries(context.Context, string, SeriesSelection) (SeriesProposal, error)
	DecideSeries(context.Context, string, string, string, SeriesSelection) (SeriesProposal, error)
}
