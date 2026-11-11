package model

import "errors"

var ErrOrganizationModelChanged = errors.New("organization_model_changed")

var ErrLibraryConflict = errors.New("library_conflict")
var ErrSeriesConflict = errors.New("series_conflict")
var ErrCapacity = errors.New("organization_capacity")
var ErrLabelFields = errors.New("conflicting_version_label_fields")
var ErrMultipleVersionLabels = errors.New("multiple_version_labels_require_array")

var ErrMembership = errors.New("membership_conflict")
var ErrDisabled = errors.New("library_disabled")
var ErrTrashed = errors.New("resource_trashed")
var ErrBusy = errors.New("resource_busy")
var ErrPreviewExpired = errors.New("preview_expired")
var ErrSnapshot = errors.New("search_snapshot_expired")

type UserState struct {
	Favorite bool  `json:"favorite"`
	Hidden   bool  `json:"hidden"`
	Revision int64 `json:"revision"`
}
type StatePatch struct {
	Expected int64 `json:"expected_revision"`
	Favorite *bool `json:"favorite,omitempty"`
	Hidden   *bool `json:"hidden,omitempty"`
}

// EditionMatch is a physical match row, even when the result is a Work or Series.
type EditionMatch struct {
	TitleOriginal   string    `json:"title_original"`
	ID              string    `json:"book_id"`
	WorkID          string    `json:"work_id"`
	LibraryID       string    `json:"library_id"`
	Title           string    `json:"title"`
	ContentRevision int64     `json:"content_revision"`
	State           UserState `json:"user_state"`
}
type Series struct {
	// Query-only presentation data. Excluded from persisted organization snapshots.
	Cover       *WorkCoverSummary `json:"-"`
	ID          string            `json:"id"`
	LibraryID   string            `json:"library_id"`
	Name        string            `json:"name"`
	Revision    int64             `json:"revision"`
	MemberCount int               `json:"member_count"`
	WorkIDs     []string          `json:"work_ids"`
	WorkNames   map[string]string `json:"work_names,omitempty"`
}

type WorkCoverSummary struct {
	WorkID          string `json:"work_id"`
	EditionID       string `json:"edition_id"`
	ContentRevision int64  `json:"content_revision"`
	CoverRevision   int64  `json:"cover_revision"`
}
