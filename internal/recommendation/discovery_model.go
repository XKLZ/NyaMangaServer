package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
)

const EvidenceVersion = "work-discovery-pairs-v1"
const MaxDiscoveryComparisons = 20_000_000
const MaxDiscoveryEdges = 250_000
const MaxDiscoveryEvidenceBytes = 128 << 20
const MaxDiscoveryProposals = 100_000

var ErrSelection = errors.New("proposal_selection_requires_resolve")

// Evidence identity excludes organization and edit counters; actual matching
// inputs, file identity and long-term image material remain explicit.
func EvidenceFingerprint(v InputEdition) string {
	v.IndexedAt = 0
	v.DisplayProperties = nil
	v.WorkID = ""
	v.OrganizationRevision = 0
	v.Revision = 0
	v.Fingerprint = ""
	v.Book.CustomLabels = nil
	return Fingerprint(v)
}

type DiscoveryRun struct {
	BaselineState string    `json:"baseline_state"`
	FailureStage  string    `json:"failure_stage,omitempty"`
	FailureReason string    `json:"failure_reason,omitempty"`
	FailureRow    int       `json:"failure_row"`
	Resources     Resources `json:"resources"`

	ExecutionVersion string `json:"execution_version,omitempty"`
	ReusedPairs      int64  `json:"reused_pairs"`
	ComputedPairs    int64  `json:"computed_pairs"`
	ReusedFeatures   int    `json:"reused_features"`
	PreparedFeatures int    `json:"prepared_features"`
	JobID            string `json:"job_id"`
	LibraryID        string `json:"library_id"`
	NextRow          int    `json:"next_row"`
	Phase            string `json:"phase"`
	EvidenceVersion  string `json:"evidence_version"`
}
type PairEvidence struct {
	A      int             `json:"a"`
	B      int             `json:"b"`
	Tier   string          `json:"tier"`
	Score  *float64        `json:"score"`
	Signal json.RawMessage `json:"signal"`
}
type PairBlock struct {
	Reused   int            `json:"reused"`
	Row      int            `json:"row"`
	NextRow  int            `json:"next_row"`
	Compared int            `json:"compared"`
	HardBits []byte         `json:"hard_bits"`
	Edges    []PairEvidence `json:"edges"`
}
type PairGraph struct {
	IDs   []string
	Edges []PairEvidence
	Hard  map[int][]byte
}
type ProposalMember struct {
	Tags            []model.TagName            `json:"-"`
	TitleOriginal   string                     `json:"title_original,omitempty"`
	CoverRevision   int64                      `json:"cover_revision"`
	Properties      map[string]CompactProperty `json:"properties"`
	Suggested       PresetSuggestion           `json:"label_suggestion"`
	ID              string                     `json:"id"`
	WorkID          string                     `json:"work_id"`
	Title           string                     `json:"title"`
	Languages       []string                   `json:"languages"`
	Labels          []string                   `json:"version_labels"`
	ContentRevision int64                      `json:"content_revision"`
	Role            string                     `json:"role"`
}
type Proposal struct {
	ID         string             `json:"id"`
	JobID      string             `json:"job_id"`
	Key        string             `json:"proposal_key"`
	Kind       string             `json:"kind"`
	Target     string             `json:"target_work_id"`
	TargetName string             `json:"target_work_name"`
	Tier       string             `json:"tier"`
	Score      *float64           `json:"matching_signal,omitempty"`
	Primary    bool               `json:"primary"`
	Revision   int64              `json:"revision"`
	State      string             `json:"state"`
	Members    []ProposalMember   `json:"members"`
	NewIDs     []string           `json:"new_edition_ids"`
	Risks      []string           `json:"risks"`
	Blocked    bool               `json:"blocked"`
	Effects    []model.WorkEffect `json:"effects"`
	Evidence   map[string]string  `json:"input_fingerprints"`
	Expected   map[string]int64   `json:"expected_revisions"`
	Command    model.WorkCommand  `json:"-"`
	CanUndo    bool               `json:"can_undo"`
}
type ProposalPage struct {
	Items    []Proposal `json:"items"`
	Total    int        `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
}
type ProposalSelection struct {
	Revision int64    `json:"expected_revision"`
	IDs      []string `json:"member_ids"`
}
type DiscoveryRepository interface {
	CreateDiscoveryJob(context.Context, string, string, ...string) (*model.Job, error)
	DiscoveryRun(context.Context, string) (*DiscoveryRun, error)
	SaveDiscoveryBlock(context.Context, model.Job, PairBlock) error
	PublishDiscoveryProposals(context.Context, model.Job) error
	RebuildDiscoveryPending(context.Context) (bool, error)
	WorkProposals(context.Context, string, string, string, string, bool, int, int, ...string) (ProposalPage, error)
	WorkProposal(context.Context, string) (Proposal, error)
	WorkProposalAlternatives(context.Context, string, int, int) (ProposalPage, error)
	ResolveWorkProposal(context.Context, string, ProposalSelection) (Proposal, error)
	DecideWorkProposal(context.Context, string, string, string, ProposalSelection) (Proposal, error)
}
