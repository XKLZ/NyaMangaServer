package model

type SearchInput struct {
	Properties      *PropertyFilter `json:"properties,omitempty"`
	MetadataState   string          `json:"metadata_state,omitempty"`
	Admin           bool            `json:"-"`
	Visibility      string          `json:"visibility,omitempty"`
	IncludeDisabled bool            `json:"include_disabled,omitempty"`
	Query           string          `json:"query"`
	LibraryID       string          `json:"library_id"`
	FileState       string          `json:"file_state"`
	Scope           string          `json:"scope"`
	Collapse        string          `json:"collapse"`
	Sort            string          `json:"sort"`
	Order           string          `json:"order"`
	Page            int             `json:"page,omitempty"`
	PageSize        int             `json:"page_size"`
	Snapshot        string          `json:"snapshot_revision,omitempty"`
}
type SearchNode struct {
	Op          string
	Field       string
	Value       string
	Prefix      bool
	Left, Right *SearchNode
}
type SearchHit struct {
	Metadata       MetadataDetails `json:"-"`
	Kind           string          `json:"kind"`
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Representative Edition         `json:"-"`
	MatchCount     int             `json:"match_count"`
	MatchIDs       []string        `json:"match_ids"`
	OtherVersion   bool            `json:"other_version_matches"`
	Favorite       string          `json:"favorite_state"`
	State          UserState       `json:"user_state"`
}
type SearchResult struct {
	Items    []SearchHit `json:"items"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
	Snapshot string      `json:"snapshot_revision"`
}
type MatchResult struct {
	Items    []EditionMatch `json:"items"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Snapshot string         `json:"snapshot_revision"`
}
type SavedSearch struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	QueryVersion int         `json:"query_version"`
	Conditions   SearchInput `json:"conditions"`
	Revision     int64       `json:"revision"`
}
