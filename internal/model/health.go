package model

import (
	"encoding/json"
	"errors"
)

var ErrHealthMaintenance = errors.New("maintenance_active")
var ErrHealthStale = errors.New("health_result_stale")
var ErrHealthRecovery = errors.New("health_recovery_required")

const HealthRulesVersion = 2

type HealthCheckInput struct {
	CheckID    string   `json:"-"`
	RequestKey string   `json:"request_key"`
	Mode       string   `json:"mode"`
	LibraryIDs []string `json:"library_ids,omitempty"`
	MaxBytes   int64    `json:"max_bytes,omitempty"`
}
type HealthCheck struct {
	ID              string                    `json:"id"`
	JobID           string                    `json:"job_id"`
	Mode            string                    `json:"mode"`
	Scope           HealthCheckInput          `json:"scope"`
	RulesVersion    int                       `json:"rules_version"`
	Phase           int                       `json:"phase"`
	Cursor          string                    `json:"cursor"`
	Checked         int64                     `json:"checked"`
	SourceBytesRead int64                     `json:"source_bytes_read"`
	TargetCounts    map[string]int            `json:"target_counts"`
	BytesRead       int64                     `json:"bytes_read"`
	CreatedAt       int64                     `json:"created_at"`
	FinishedAt      *int64                    `json:"finished_at"`
	Revision        int64                     `json:"revision"`
	ErrorCode       string                    `json:"error_code"`
	State           string                    `json:"state"`
	Categories      map[string]map[string]int `json:"categories"`
	Counts          map[string]int            `json:"counts"`
	Completion      string                    `json:"completion"`
}
type HealthFinding struct {
	Private        json.RawMessage `json:"-"`
	ID             string          `json:"id"`
	CheckID        string          `json:"check_id"`
	Category       string          `json:"category"`
	ObjectKind     string          `json:"object_kind"`
	ObjectID       string          `json:"object_id"`
	ObjectRevision int64           `json:"object_revision"`
	Fingerprint    string          `json:"fingerprint"`
	Status         string          `json:"status"`
	Code           string          `json:"code"`
	Repair         string          `json:"repair"`
	Details        json.RawMessage `json:"details"`
	CheckedAt      int64           `json:"checked_at"`
}
type HealthAction struct {
	RequiresSources    bool               `json:"requires_sources"`
	Dictionary         map[TagName]string `json:"-"`
	FindingID          string             `json:"finding_id"`
	Kind               string             `json:"kind"`
	ObjectID           string             `json:"object_id"`
	Fingerprint        string             `json:"fingerprint"`
	PreferredEditionID string             `json:"preferred_edition_id,omitempty"`
	AllowSources       bool               `json:"allow_sources,omitempty"`
}
type HealthPlanInput struct {
	RequestKey        string            `json:"request_key"`
	CheckID           string            `json:"check_id"`
	FindingIDs        []string          `json:"finding_ids"`
	PreferredEditions map[string]string `json:"preferred_editions,omitempty"`
	AllowSources      bool              `json:"allow_sources,omitempty"`
}
type HealthPlan struct {
	ID            string         `json:"id"`
	CheckID       string         `json:"check_id"`
	Revision      int64          `json:"revision"`
	Actions       []HealthAction `json:"actions"`
	PausedTasks   int            `json:"paused_tasks"`
	SnapshotBytes int64          `json:"snapshot_bytes"`
	RequiredBytes int64          `json:"required_bytes"`
	Rollback      string         `json:"rollback"`
	ExpiresAt     int64          `json:"expires_at"`
}
type HealthExecuteInput struct {
	RequestKey         string `json:"request_key"`
	ExpectedRevision   int64  `json:"expected_revision"`
	ConfirmMaintenance bool   `json:"confirm_maintenance"`
}
type HealthRepair struct {
	MaterialsCleaned bool               `json:"materials_cleaned"`
	JournalState     string             `json:"journal_state,omitempty"`
	Operation        string             `json:"operation"`
	ID               string             `json:"id"`
	PlanID           string             `json:"plan_id"`
	JobID            string             `json:"job_id"`
	State            string             `json:"state"`
	Revision         int64              `json:"revision"`
	Checkpoint       int                `json:"checkpoint"`
	ErrorCode        string             `json:"error_code"`
	CreatedAt        int64              `json:"created_at"`
	UpdatedAt        int64              `json:"updated_at"`
	PausedJobs       []HealthPausedJob  `json:"paused_jobs"`
	Steps            []HealthRepairStep `json:"steps"`
}
type HealthRepairStep struct {
	FileState string       `json:"file_state,omitempty"`
	Ordinal   int          `json:"ordinal"`
	Action    HealthAction `json:"action"`
	State     string       `json:"state"`
	ErrorCode string       `json:"error_code"`
}
type HealthState struct {
	Phase       string  `json:"phase"`
	RepairID    *string `json:"repair_id"`
	Revision    int64   `json:"revision"`
	UpdatedAt   int64   `json:"updated_at"`
	PausedTasks int     `json:"paused_tasks"`
	CanContinue bool    `json:"can_continue"`
	CanRollback bool    `json:"can_rollback"`
}
type HealthPage[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// Internal IO descriptors never serialize host paths or private source values.
type HealthObject struct {
	Kind, ID, RootKey, Relative, RootIdentity, Encoding, Key, Digest, Role, Fingerprint string
	Revision, ContentRevision, Size, Mtime, Width, Height                               int64
	ObservedDigest                                                                      string
	ObservedSize, ObservedMtime                                                         int64
	Disabled                                                                            bool
	Extra                                                                               json.RawMessage
}

type HealthPausedJob struct {
	ID                 string `json:"id"`
	PreviousState      string `json:"previous_state"`
	PreviousGeneration int64  `json:"previous_generation"`
}
