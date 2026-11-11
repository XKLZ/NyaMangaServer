package model

import "encoding/json"

type MaintenanceInput struct {
	Operation string           `json:"operation"`
	BookIDs   []string         `json:"book_ids,omitempty"`
	LibraryID string           `json:"library_id,omitempty"`
	Expected  map[string]int64 `json:"expected_revisions"`
	Options   json.RawMessage  `json:"options,omitempty"`
}
type MaintenancePlan struct {
	ID        string           `json:"id"`
	Operation string           `json:"operation"`
	Expires   int64            `json:"expires_at"`
	Summary   map[string]any   `json:"summary"`
	Affected  []string         `json:"affected"`
	Protected []string         `json:"protected"`
	JobID     string           `json:"job_id,omitempty"`
	Input     MaintenanceInput `json:"-"`
}
type CoverSelection struct {
	OrderVerified    bool    `json:"-"`
	ParsingRevision  int64   `json:"parsing_revision"`
	EntryIndex       *int    `json:"-"`
	EntryName        *string `json:"-"`
	DirectoryVersion string  `json:"directory_version,omitempty"`
	Mode             string  `json:"mode"`
	PageIndex        *int    `json:"page_index,omitempty"`
	AssetID          *string `json:"asset_id,omitempty"`
	Revision         int64   `json:"revision"`
	ContentRevision  int64   `json:"content_revision"`
	State            string  `json:"state"`
}
type Asset struct {
	ID, Key, Digest string
	Size            int64
}
type SourcePreview struct {
	ID                string          `json:"id"`
	BookID            string          `json:"book_id"`
	JobID             string          `json:"job_id"`
	State             string          `json:"state"`
	Expires           int64           `json:"expires_at"`
	Fields            Metadata        `json:"fields"`
	Source            *Source         `json:"source"`
	Current           MetadataDetails `json:"current"`
	SharedBookCount   int             `json:"shared_book_count"`
	ExpectedRevision  int64           `json:"expected_revision"`
	SelectionRevision int64           `json:"selection_revision"`
	ContentRevision   int64           `json:"content_revision"`
}
