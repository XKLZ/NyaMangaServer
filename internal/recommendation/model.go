package recommendation

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
)

const Algorithm = "work-cover-recommendation-v4.1"

var ErrStale = errors.New("work_recommendation_stale")
var ErrAlreadyAccepted = errors.New("recommendation_already_accepted")

type InputEdition struct {
	ParserVersion        string                `json:"parser_version,omitempty"`
	ParsingRevision      int64                 `json:"parsing_revision,omitempty"`
	DirectoryDigest      string                `json:"directory_digest,omitempty"`
	PreprocessingKey     string                `json:"-"`
	DisplayProperties    *model.BookProperties `json:"display_properties,omitempty"`
	CoverRevision        int64                 `json:"cover_revision,omitempty"`
	Book                 BookInput             `json:"book"`
	WorkID               string                `json:"work_id"`
	IndexedAt            int64                 `json:"indexed_at,omitempty"`
	ContentRevision      int64                 `json:"content_revision"`
	Revision             int64                 `json:"revision"`
	OrganizationRevision int64                 `json:"organization_revision"`
	FileState            string                `json:"file_state"`
	LibraryIdentity      string                `json:"library_identity"`
	Encoding             string                `json:"encoding"`
	Material             *model.Asset          `json:"material,omitempty"`
	Fingerprint          string                `json:"fingerprint"`
}
type InputWork struct {
	TotalEditionCount int `json:"total_edition_count,omitempty"`
	WorkInput
	Revision    int64  `json:"revision"`
	Fingerprint string `json:"fingerprint"`
}
type Pool struct {
	Editions []InputEdition `json:"editions"`
	Works    []InputWork    `json:"works"`
}

func Fingerprint(v any) string {
	raw, _ := json.Marshal(v)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func EditionFingerprint(e InputEdition) string {
	e.IndexedAt = 0
	e.DisplayProperties = nil
	e.CoverRevision = 0
	e.Fingerprint = ""
	return Fingerprint(e)
}
func WorkFingerprint(w InputWork, editions map[string]InputEdition) string {
	values := []string{w.ID, fmt.Sprint(w.Revision), w.PreferredEditionID}
	for _, id := range w.EditionIDs {
		values = append(values, id, editions[id].Fingerprint)
	}
	return Fingerprint(values)
}

type Feature struct {
	Fingerprint      string          `json:"fingerprint"`
	InputSHA         string          `json:"input_sha256"`
	AlgorithmVersion string          `json:"algorithm_version"`
	ModelSHA         string          `json:"model_sha256"`
	Value            json.RawMessage `json:"feature"`
	Digest           string          `json:"feature_digest"`
}
type FeatureResult struct {
	Features     map[string]Feature `json:"features"`
	MissingCover bool               `json:"missing_cover"`
}
type Candidate struct {
	WorkID           string          `json:"work_id"`
	QueryID          string          `json:"query_edition_id"`
	Tier             string          `json:"tier"`
	Score            *float64        `json:"matching_score"`
	SupportingID     string          `json:"supporting_edition_id"`
	PreferredID      string          `json:"preferred_edition_id"`
	Evidence         json.RawMessage `json:"member_evidence"`
	Risks            []string        `json:"risks"`
	Conflicting      []string        `json:"conflicting_member_ids"`
	Algorithm        string          `json:"algorithm_version"`
	LabelSuggestions json.RawMessage `json:"label_suggestions"`
	AutomaticMerge   bool            `json:"automatic_merge"`
}
type Result struct {
	Algorithm      string      `json:"algorithm_version"`
	Items          []Candidate `json:"items"`
	CandidateCount int         `json:"candidate_count"`
	Truncated      bool        `json:"truncated"`
	MaxResults     int         `json:"max_results"`
	Resources      Resources   `json:"resources"`
}
type Feedback struct {
	Relation string          `json:"relation"`
	Note     string          `json:"note"`
	Labels   json.RawMessage `json:"labels"`
	Expected int64           `json:"expected_revision"`
}
type Record struct {
	ID               string    `json:"id"`
	JobID            string    `json:"job_id"`
	QueryID          *string   `json:"query_edition_id"`
	TargetID         *string   `json:"target_work_id"`
	Tier             string    `json:"tier"`
	Rank             int       `json:"rank"`
	Result           Candidate `json:"result"`
	Revision         int64     `json:"revision"`
	Feedback         *Feedback `json:"feedback"`
	FeedbackRevision int64     `json:"feedback_revision"`
	Accepted         bool      `json:"accepted"`
	Created          int64     `json:"created_at"`
}
type LayerSummary struct {
	Tier              string   `json:"tier"`
	SameWork          int      `json:"same_work"`
	SameSeries        int      `json:"same_series"`
	Unrelated         int      `json:"unrelated"`
	Uncertain         int      `json:"uncertain"`
	Unreviewed        int      `json:"unreviewed"`
	ObservedPrecision *float64 `json:"observed_precision"`
}
type QualitySummary struct {
	Layers []LayerSummary `json:"layers"`
	Misses int            `json:"recorded_misses"`
	Scope  string         `json:"scope"`
}
type Miss struct {
	ID       string          `json:"id"`
	QueryID  *string         `json:"query_edition_id"`
	TargetID *string         `json:"target_work_id"`
	Note     string          `json:"note"`
	Snapshot json.RawMessage `json:"input_snapshot"`
	Created  int64           `json:"created_at"`
}
type MissPage struct {
	Items    []Miss `json:"items"`
	Total    int    `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}
type Page struct {
	PoolCount      int               `json:"pool_count"`
	InputErrors    map[string]string `json:"input_errors,omitempty"`
	Summary        *QualitySummary   `json:"summary,omitempty"`
	Resources      *Resources        `json:"resources,omitempty"`
	PreparedCount  int               `json:"prepared_count"`
	QuerySummaries map[string]any    `json:"query_summaries,omitempty"`
	Items          []Record          `json:"items"`
	Total          int               `json:"total"`
	Page           int               `json:"page"`
	PageSize       int               `json:"page_size"`
}
type Preview struct {
	RecommendationID string           `json:"recommendation_id"`
	Revision         int64            `json:"revision"`
	Organization     model.WorkResult `json:"organization"`
}
type AcceptInput struct {
	Revision int64             `json:"expected_revision"`
	Command  model.WorkCommand `json:"command"`
	Note     string            `json:"note"`
}
type MissInput struct {
	QueryID  string `json:"query_edition_id"`
	TargetID string `json:"target_work_id"`
	Note     string `json:"note"`
}

type Repository interface {
	JobContext(context.Context, string) (context.Context, error)
	CreateRecommendationJob(context.Context, string, string, []string) (*model.Job, error)
	RecommendationIndexState(context.Context) (map[string]any, error)
	RecommendationJobs(context.Context) ([]model.Job, error)
	RecommendationPool(context.Context, string) (Pool, error)
	SaveRecommendationFeatures(context.Context, model.Job, InputEdition, FeatureResult) error
	RecommendationProgress(context.Context, model.Job, int, Resources, map[string]string) error
	PublishRecommendations(context.Context, model.Job, model.Item, Result) error
	RecommendationPage(context.Context, string, string, string, int, int) (Page, error)
	RecommendationFeedback(context.Context, string, string, Feedback) error
	RecommendationPreview(context.Context, string, string, model.WorkCommand, organization.WorkPlanner) (Preview, error)
	AcceptRecommendation(context.Context, string, string, AcceptInput, organization.WorkPlanner) (model.WorkResult, error)
	RecommendationMiss(context.Context, string, string, MissInput) error
	RecommendationMisses(context.Context, string, int, int) (MissPage, error)
	Job(context.Context, string) (*model.Job, error)
	Items(context.Context, string, int, int, bool) ([]model.Item, error)
	Claim(context.Context, string) (*model.Job, error)
	ControlJob(context.Context, string, string, string) error
	FailBackgroundItem(context.Context, model.Job, model.Item, string) error
	FinishBackground(context.Context, model.Job, string) error
	ManagementGuard(context.Context, string) error
}
