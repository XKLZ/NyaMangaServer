package model

// ImportBatch persists orchestration; worker jobs retain their existing lifecycle.
type ImportBatch struct {
	EnhancementStages map[string]ImportEnhancementStage `json:"enhancement_stages"`
	TaskFailures      []ImportTaskFailure               `json:"task_failures"`
	CurrentFailure    *ImportTaskFailure                `json:"current_failure"`
	CanRetry          bool                              `json:"can_retry"`
	StageDecisions    map[string]ImportStageDecision    `json:"stage_decisions"`
	SkippedStages     []string                          `json:"skipped_stages"`
	Counts            map[string]int                    `json:"counts"`
	ID                string                            `json:"id"`
	LibraryID         string                            `json:"library_id"`
	Stage             string                            `json:"stage"`
	State             string                            `json:"state"`
	Revision          int64                             `json:"revision"`
	Error             string                            `json:"error_code"`
	CreatedAt         int64                             `json:"created_at"`
	UpdatedAt         int64                             `json:"updated_at"`
	Total             int                               `json:"total"`
	Pending           int                               `json:"pending"`
	Jobs              []Job                             `json:"jobs"`
}
type ImportStageDecision struct {
	Decision  string `json:"decision"`
	JobID     string `json:"job_id"`
	CreatedAt int64  `json:"created_at"`
}
type ImportTaskFailure struct {
	JobID  string `json:"job_id"`
	Stage  string `json:"stage"`
	Code   string `json:"error_code"`
	Phase  string `json:"phase,omitempty"`
	Reason string `json:"reason,omitempty"`
}
type ImportSummary struct {
	ID    string `json:"id"`
	Stage string `json:"stage"`
	State string `json:"state"`
}
type ImportFilter struct{ LibraryID, State string }
type ImportCandidate struct {
	Tags              []Tag          `json:"tags"`
	Revision          int64          `json:"revision"`
	Generation        int64          `json:"request_generation"`
	ContentRevision   int64          `json:"content_revision"`
	SelectionRevision int64          `json:"selection_revision"`
	Input             MetadataInput  `json:"input"`
	Result            RemoteMetadata `json:"-"`
	Fields            Metadata       `json:"fields"`
	URL               string         `json:"url"`
	Method            string         `json:"match_method"`
	SharedEditions    int            `json:"shared_editions"`
}
type ImportMember struct {
	TitleOriginal     string `json:"title_original"`
	MatchCount        int    `json:"match_count"`
	MatchListComplete bool   `json:"match_list_complete"`
	SourceSearchID    string `json:"source_search_id,omitempty"`
	WorkName          string `json:"work_name"`
	SeriesName        string `json:"series_name"`
	RequestedURL      string `json:"requested_url"`
	EditionID         string `json:"edition_id"`
	EditionRevision   int64  `json:"edition_revision"`
	CoverRevision     int64  `json:"cover_revision"`
	HasMetadata       bool   `json:"has_metadata"`
	Acceptable        bool   `json:"candidate_acceptable"`
	Status            string `json:"status"`

	ID              string           `json:"id"`
	Filename        string           `json:"filename"`
	Title           string           `json:"title"`
	Change          string           `json:"change_kind"`
	Error           string           `json:"error_code"`
	ScanError       string           `json:"scan_error"`
	Decision        string           `json:"decision"`
	Revision        int64            `json:"revision"`
	ContentRevision int64            `json:"content_revision"`
	WorkID          string           `json:"work_id"`
	SeriesID        string           `json:"series_id"`
	Candidate       *ImportCandidate `json:"candidate,omitempty"`
}
type ImportPage[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}
type ImportReview struct {
	EditionRevision   int64  `json:"expected_edition_revision,omitempty"`
	ID                string `json:"id"`
	ExpectedRevision  int64  `json:"expected_revision"`
	CandidateRevision int64  `json:"candidate_revision,omitempty"`
	Decision          string `json:"decision"`
}
type ImportRequest struct {
	Parent    string `json:"parent_job_id,omitempty"`
	Ordinals  []int  `json:"ordinals,omitempty"`
	JobID     string
	BatchID   string
	LibraryID string
	Stage     string
	IDs       []string `json:"edition_ids"`
	URL       string   `json:"gallery_url,omitempty"`
}

// Selection identities are small immutable review snapshots, not full candidates.
type ImportSelection struct {
	HasMetadata       bool   `json:"has_metadata"`
	EditionRevision   int64  `json:"expected_edition_revision"`
	ID                string `json:"id"`
	Revision          int64  `json:"expected_revision"`
	CandidateRevision int64  `json:"candidate_revision"`
	Acceptable        bool   `json:"candidate_acceptable"`
	Status            string `json:"status"`
	Decision          string `json:"decision"`
}
type ImportManualReview struct {
	ExpectedRevision  int64               `json:"expected_revision"`
	MemberRevision    int64               `json:"expected_member_revision"`
	CandidateRevision int64               `json:"candidate_revision"`
	Fields            map[string]Override `json:"fields"`
}
type ImportEditor struct {
	Edition           Edition         `json:"edition"`
	Metadata          MetadataDetails `json:"metadata"`
	MemberRevision    int64           `json:"member_revision"`
	CandidateRevision int64           `json:"candidate_revision"`
}

// OrganizationReview is a read-only, stage-scoped gate snapshot.
type OrganizationReview struct {
	Failure               *ImportTaskFailure `json:"failure"`
	CanRetry              bool               `json:"can_retry"`
	RecommendationSkipped bool               `json:"recommendation_skipped"`
	Stage                 string             `json:"stage"`
	State                 string             `json:"state"`
	Revision              int64              `json:"expected_revision"`
	Pending               int                `json:"pending_proposals"`
	Revalidating          bool               `json:"revalidating"`
	Active                bool               `json:"active_tasks"`
	CanComplete           bool               `json:"can_complete"`
	Token                 string             `json:"snapshot_token"`
}
type CompleteOrganizationReview struct {
	Stage    string `json:"stage"`
	Revision int64  `json:"expected_revision"`
	Token    string `json:"snapshot_token"`
}

type ImportEnhancementStage struct {
	Enabled bool   `json:"enabled"`
	State   string `json:"state"`
	Reason  string `json:"reason"`
}
