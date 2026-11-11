// Package model defines business values and errors independently of HTTP and SQL.
package model

import "errors"

var ErrInitialized = errors.New("already initialized")
var ErrConflict = errors.New("record changed")

var ErrNotFound = errors.New("not found")
var ErrScanActive = errors.New("scan already active")
var ErrStaleJob = errors.New("stale job execution")

type Library struct {
	EditionCount int
	WorkCount    int
	ActiveImport *ImportSummary
	Disabled     bool
	TrashedAt    *int64
	Availability string
	ID           string
	RootKey      string
	Name         string
	Encoding     string
	Interval     *int
	Identity     string
	Revision     int64
	LastScan     *string
	Error        string
}

// File is the read-only archive identity owned one-to-one by an Edition.
// An offline file remains registered; relinking never changes the Edition ID.
type File struct {
	FileParsing
	ScanResult              *ScanResult `json:"-"`
	Path                    string
	FilenameTitle           string
	Size                    int64
	Mtime                   int64
	ContentRevision         int64
	SHA1                    *string
	RepresentativeAlgorithm string
	LegacyCoverSHA1         *string
	PageCount               int
	State                   string
	Error                   string
	IndexedAt               int64
}

// Edition is the stable management identity for one File.
// File is embedded for service aggregates; persistence stores it separately.
type Edition struct {
	ReadingState *ReadingState `json:"-"`
	File
	ID                   string
	LibraryID            string
	WorkID               string
	TrashedAt            *int64
	LibraryDisabled      bool
	CoverRevision        int64
	Title                string
	TitleOriginal        string `json:"-"`
	Revision             int64
	OrganizationRevision int64
}

// Work groups Editions of the same principal content, including a single Edition.
type Work struct {
	Title              string   `json:"title"`
	TitleOriginal      string   `json:"title_original,omitempty"`
	CoverRevision      int64    `json:"cover_revision"`
	ContentRevision    int64    `json:"content_revision"`
	EditionIDs         []string `json:"edition_ids,omitempty"`
	ID                 string   `json:"id"`
	LibraryID          string   `json:"library_id"`
	Name               string   `json:"name"`
	NameOverride       *string  `json:"name_override"`
	PreferredEditionID string   `json:"preferred_edition_id"`
	Revision           int64    `json:"revision"`
	EditionCount       int      `json:"edition_count"`
	SeriesID           string   `json:"series_id,omitempty"`
	Position           int      `json:"position,omitempty"`
}
type Page struct {
	Index     int    `json:"page_index"`
	Entry     int    `json:"-"`
	Name      string `json:"display_name"`
	MediaType string `json:"media_type"`
}
type Job struct {
	ScanCompleted, ScanPartial, ScanBlocked, ScanWarnings int
	ActorID                                               string
	ActorACLRevision                                      int64
	Mode                                                  string
	Type                                                  string
	ParentID                                              *string
	ID                                                    string
	LibraryID                                             string
	State                                                 string
	Stage                                                 string
	Generation                                            int64
	Total                                                 *int
	Done                                                  int
	Failed                                                int
	Error                                                 string
	Created                                               int64
	Updated                                               int64
}
type Item struct {
	ScanResult        *ScanResult
	PreviewID         string
	ContentRevision   int64
	SelectionRevision int64
	Input             string
	Candidates        []Candidate
	Attempt           int
	JobID             string
	Ordinal           int
	Path              string
	Size              int64
	Mtime             int64
	State             string
	Error             string
	BookID            *string
}
type Account struct {
	ID, PasswordHash            string
	Username, Role              string
	Enabled, MustChangePassword bool
	ACLRevision                 int64
	LibraryGrants               map[string]string
	Revision                    int64
}
type Session struct {
	Hash             []byte
	AccountID        string
	Account          *Account
	Created, Expires int64
}
