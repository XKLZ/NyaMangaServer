package model

type ClientIdentity struct {
	InstanceID string `json:"instance_id"`
	CacheEpoch string `json:"cache_epoch"`
}

type ClientScopeLibrary struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
}

type ClientConfiguration struct {
	Identity          ClientIdentity
	HealthMaintenance bool
	BackgroundHold    bool
	Libraries         []ClientScopeLibrary
}

type ClientReadability struct {
	Readable bool   `json:"readable"`
	Reason   string `json:"reason"`
}

type ClientEdition struct {
	ID              string            `json:"id"`
	Title           string            `json:"title"`
	CoverRevision   int64             `json:"cover_revision"`
	CoverURL        string            `json:"cover_url"`
	ContentRevision int64             `json:"content_revision"`
	ParsingRevision int64             `json:"parsing_revision"`
	PageCount       *int              `json:"page_count"`
	Readability     ClientReadability `json:"readability"`
	FileState       string            `json:"-"`
	Hidden          bool              `json:"-"`
}

type ClientSeries struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

type ClientReadingContext struct {
	WorkID                      string          `json:"work_id"`
	LibraryID                   string          `json:"library_id"`
	Edition                     ClientEdition   `json:"edition"`
	SelectionReason             string          `json:"selection_reason"`
	Editions                    []ClientEdition `json:"editions"`
	PreferredEditionID          *string         `json:"preferred_edition_id"`
	ReadableAlternativeID       *string         `json:"readable_alternative_id"`
	Series                      *ClientSeries   `json:"series"`
	PreviousWorkID              *string         `json:"previous_work_id"`
	NextWorkID                  *string         `json:"next_work_id"`
	Reading                     ReadingState    `json:"reading"`
	RequiresContentConfirmation bool            `json:"requires_content_confirmation"`
	RequiresParsingConfirmation bool            `json:"requires_parsing_confirmation"`
	PagesURL                    string          `json:"pages_url"`
	SourceLibrary               Library         `json:"-"`
}

type ClientSearchInput struct {
	Query      string          `json:"query"`
	LibraryID  string          `json:"library_id"`
	Scope      string          `json:"scope,omitempty"`
	FileState  string          `json:"file_state,omitempty"`
	Properties *PropertyFilter `json:"properties,omitempty"`
	Sort       string          `json:"sort,omitempty"`
	Order      string          `json:"order,omitempty"`
	Page       int             `json:"page,omitempty"`
	PageSize   int             `json:"page_size,omitempty"`
	Snapshot   string          `json:"snapshot_revision,omitempty"`
}

type ClientWorkHit struct {
	WorkID             string        `json:"work_id"`
	LibraryID          string        `json:"library_id"`
	PreferredEditionID *string       `json:"preferred_edition_id"`
	EntryEditionID     string        `json:"entry_edition_id"`
	ResumeEditionID    *string       `json:"resume_edition_id"`
	Title              string        `json:"title"`
	Edition            ClientEdition `json:"edition"`
	Series             *ClientSeries `json:"series"`
	Reading            ReadingState  `json:"reading"`
}

type ClientSearchResult struct {
	Items         []ClientWorkHit `json:"items"`
	Page          int             `json:"page"`
	PageSize      int             `json:"page_size"`
	Total         int             `json:"total"`
	Snapshot      string          `json:"snapshot_revision"`
	SourceLibrary Library         `json:"-"`
}
