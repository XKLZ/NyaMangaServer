package model

// Source searches are isolated previews. Selecting one stages an import
// candidate; it never publishes an external source or edits manual fields.
type SourceSearchInput struct {
	Mode             string `json:"mode"`
	Query            string `json:"q"`
	RequestKey       string `json:"request_key"`
	ExpectedRevision int64  `json:"expected_revision"`
}
type SourceSearch struct {
	ActivePage        int           `json:"active_page"`
	ID                string        `json:"id"`
	BatchID           string        `json:"batch_id"`
	EditionID         string        `json:"edition_id"`
	Mode              string        `json:"mode"`
	Query             string        `json:"q"`
	Site              string        `json:"site"`
	Revision          int64         `json:"revision"`
	JobID             string        `json:"job_id"`
	State             string        `json:"state"`
	Error             string        `json:"error_code"`
	Continuation      string        `json:"continuation"` // unknown, more, end, limited
	Loaded            int           `json:"loaded_count"`
	Input             MetadataInput `json:"-"`
	ContentRevision   int64         `json:"content_revision"`
	SelectionRevision int64         `json:"selection_revision"`
	EditionRevision   int64         `json:"edition_revision"`
	MemberRevision    int64         `json:"member_revision"`
	Next              string        `json:"-"`
}
type SourceSearchResult struct {
	Tags      []Tag           `json:"tags"`
	ID        string          `json:"id"`
	URL       string          `json:"url"`
	Title     string          `json:"title"`
	Position  int             `json:"position"`
	Revision  int64           `json:"revision"`
	State     string          `json:"state"`
	Error     string          `json:"error_code"`
	Fields    *Metadata       `json:"fields,omitempty"`
	PageCount *int            `json:"page_count"`
	Result    *RemoteMetadata `json:"-"`
}
type SourceSearchPage struct {
	Search   SourceSearch         `json:"search"`
	Items    []SourceSearchResult `json:"items"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
}
type SearchPage struct {
	Items []Candidate
	Next  string
}
type FetchResult struct {
	URL      string
	Metadata *RemoteMetadata
	Error    string
}
type SearchLoad struct {
	Page             int    `json:"page"`
	ExpectedRevision int64  `json:"expected_revision"`
	RequestKey       string `json:"request_key"`
}
type SearchSelect struct {
	ResultID         string `json:"result_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	ResultRevision   int64  `json:"result_revision"`
	MemberRevision   int64  `json:"member_revision"`
	EditionRevision  int64  `json:"edition_revision"`
}
type SearchOperation struct {
	SeedOnly bool   `json:"seed_only,omitempty"`
	SearchID string `json:"search_id"`
	Page     int    `json:"page"`
}
