package httpapi

import "nyamangaserver.local/server/internal/model"

type libraryResponse struct {
	EditionCount int                  `json:"edition_count"`
	WorkCount    int                  `json:"work_count"`
	ActiveImport *model.ImportSummary `json:"active_import"`
	Disabled     bool                 `json:"disabled"`
	TrashedAt    *int64               `json:"trashed_at"`
	Availability string               `json:"availability,omitempty"`
	ID           string               `json:"id"`
	RootKey      string               `json:"root_key"`
	Name         string               `json:"display_name"`
	Encoding     string               `json:"legacy_zip_encoding"`
	Interval     *int                 `json:"scan_interval_minutes"`
	Identity     string               `json:"-"`
	Revision     int64                `json:"revision"`
	LastScan     *string              `json:"last_complete_scan_id"`
	Error        string               `json:"error_code"`
}
type fileResponse struct {
	model.FileParsing
	EditionID       string  `json:"edition_id"`
	Path            string  `json:"relative_path"`
	Size            int64   `json:"size"`
	Mtime           int64   `json:"mtime_ns"`
	ContentRevision int64   `json:"content_revision"`
	SHA1            *string `json:"representative_sha1"`
	Algorithm       string  `json:"representative_algorithm"`
	PageCount       int     `json:"page_count"`
	State           string  `json:"file_state"`
	Error           string  `json:"error_code"`
	IndexedAt       int64   `json:"indexed_at"`
}
type bookResponse struct {
	ReadingState         *model.ReadingState `json:"reading_state,omitempty"`
	TitleOriginal        string              `json:"title_original"`
	WorkID               string              `json:"work_id"`
	OrganizationRevision int64               `json:"organization_revision"`
	File                 fileResponse        `json:"file"`

	RepresentativeAlgorithm string  `json:"representative_algorithm"`
	TrashedAt               *int64  `json:"trashed_at"`
	LibraryDisabled         bool    `json:"library_disabled"`
	CoverRevision           int64   `json:"cover_revision"`
	ID                      string  `json:"id"`
	LibraryID               string  `json:"library_id"`
	Path                    string  `json:"relative_path"`
	Title                   string  `json:"title"`
	Size                    int64   `json:"size"`
	Mtime                   int64   `json:"mtime_ns"`
	ContentRevision         int64   `json:"content_revision"`
	SHA1                    *string `json:"representative_sha1"`
	PageCount               int     `json:"page_count"`
	State                   string  `json:"file_state"`
	Error                   string  `json:"error_code"`
	IndexedAt               int64   `json:"indexed_at"`
	Revision                int64   `json:"revision"`
}
type pageResponse struct {
	Index     int    `json:"page_index"`
	Entry     int    `json:"-"`
	Name      string `json:"display_name"`
	MediaType string `json:"media_type"`
}
type jobResponse struct {
	ScanCompleted    int     `json:"scan_completed"`
	ScanPartial      int     `json:"scan_partial"`
	ScanBlocked      int     `json:"scan_blocked"`
	ScanWarnings     int     `json:"scan_warning_count"`
	ActorID          string  `json:"actor_id"`
	ActorACLRevision int64   `json:"actor_acl_revision"`
	Mode             string  `json:"mode"`
	Type             string  `json:"type"`
	ParentID         *string `json:"parent_job_id"`
	ID               string  `json:"id"`
	LibraryID        string  `json:"library_id"`
	State            string  `json:"state"`
	Stage            string  `json:"stage"`
	Generation       int64   `json:"generation"`
	Total            *int    `json:"total"`
	Done             int     `json:"done"`
	Failed           int     `json:"failed"`
	Error            string  `json:"error_code"`
	Created          int64   `json:"created_at"`
	Updated          int64   `json:"updated_at"`
}
type itemResponse struct {
	ScanResult      *model.ScanResult `json:"scan_result,omitempty"`
	PreviewID       string            `json:"preview_id,omitempty"`
	Candidates      []model.Candidate `json:"candidates"`
	Attempt         int               `json:"attempt"`
	ContentRevision int64             `json:"content_revision"`
	JobID           string            `json:"job_id"`
	Ordinal         int               `json:"ordinal"`
	Path            string            `json:"relative_path"`
	Size            int64             `json:"size"`
	Mtime           int64             `json:"mtime_ns"`
	State           string            `json:"state"`
	Error           string            `json:"error_code"`
	BookID          *string           `json:"book_id"`
}

// Public plan projection shared by preview POST and history/progress GET.
// Input.Options contains frozen file identities and other private execution
// material; never expose it or add it to this response type.
type maintenancePlanResponse struct {
	ID        string         `json:"id"`
	Operation string         `json:"operation"`
	Expires   int64          `json:"expires_at"`
	Summary   map[string]any `json:"summary"`
	Affected  []string       `json:"affected"`
	Protected []string       `json:"protected"`
	JobID     string         `json:"job_id,omitempty"`
}

func response(v any) any {
	switch x := v.(type) {
	case model.MaintenancePlan:
		return maintenancePlanResponse{ID: x.ID, Operation: x.Operation, Expires: x.Expires, Summary: x.Summary, Affected: x.Affected, Protected: x.Protected, JobID: x.JobID}
	case *model.MaintenancePlan:
		if x == nil {
			return nil
		}
		return response(*x)

	case model.Library:
		return libraryResponse{EditionCount: x.EditionCount, WorkCount: x.WorkCount, ActiveImport: x.ActiveImport, Disabled: x.Disabled, TrashedAt: x.TrashedAt, Availability: x.Availability, ID: x.ID, RootKey: x.RootKey, Name: x.Name, Encoding: x.Encoding, Interval: x.Interval, Identity: x.Identity, Revision: x.Revision, LastScan: x.LastScan, Error: x.Error}
	case *model.Library:
		if x == nil {
			return nil
		}
		return response(*x)
	case []model.Library:
		out := make([]any, len(x))
		for i := range x {
			out[i] = response(x[i])
		}
		return out
	case model.Edition:
		return bookResponse{ReadingState: x.ReadingState, TitleOriginal: x.TitleOriginal, WorkID: x.WorkID, OrganizationRevision: x.OrganizationRevision, File: fileResponse{FileParsing: x.FileParsing, EditionID: x.ID, Path: x.Path, Size: x.Size, Mtime: x.Mtime, ContentRevision: x.ContentRevision, SHA1: x.SHA1, Algorithm: x.RepresentativeAlgorithm, PageCount: x.PageCount, State: x.State, Error: x.Error, IndexedAt: x.IndexedAt}, TrashedAt: x.TrashedAt, LibraryDisabled: x.LibraryDisabled, CoverRevision: x.CoverRevision, ID: x.ID, LibraryID: x.LibraryID, Path: x.Path, Title: x.Title, Size: x.Size, Mtime: x.Mtime, ContentRevision: x.ContentRevision, SHA1: x.SHA1, RepresentativeAlgorithm: x.RepresentativeAlgorithm, PageCount: x.PageCount, State: x.State, Error: x.Error, IndexedAt: x.IndexedAt, Revision: x.Revision}
	case *model.Edition:
		if x == nil {
			return nil
		}
		return response(*x)
	case []model.Edition:
		out := make([]any, len(x))
		for i := range x {
			out[i] = response(x[i])
		}
		return out
	case model.Page:
		return pageResponse{Index: x.Index, Entry: x.Entry, Name: x.Name, MediaType: x.MediaType}
	case *model.Page:
		if x == nil {
			return nil
		}
		return response(*x)
	case []model.Page:
		out := make([]any, len(x))
		for i := range x {
			out[i] = response(x[i])
		}
		return out
	case *model.ImportBatch:
		if x == nil {
			return nil
		}
		return struct {
			*model.ImportBatch
			Jobs any `json:"jobs"`
		}{x, response(x.Jobs)}
	case model.ImportBatch:
		return response(&x)
	case model.ImportPage[model.ImportBatch]:
		items := []any{}
		for _, b := range x.Items {
			items = append(items, response(b))
		}
		return map[string]any{"items": items, "total": x.Total, "page": x.Page, "page_size": x.PageSize}
	case model.Job:
		return jobResponse{ScanCompleted: x.ScanCompleted, ScanPartial: x.ScanPartial, ScanBlocked: x.ScanBlocked, ScanWarnings: x.ScanWarnings, ActorID: x.ActorID, ActorACLRevision: x.ActorACLRevision, Mode: x.Mode, Type: x.Type, ParentID: x.ParentID, ID: x.ID, LibraryID: x.LibraryID, State: x.State, Stage: x.Stage, Generation: x.Generation, Total: x.Total, Done: x.Done, Failed: x.Failed, Error: x.Error, Created: x.Created, Updated: x.Updated}
	case *model.Job:
		if x == nil {
			return nil
		}
		return response(*x)
	case []model.Job:
		out := make([]any, len(x))
		for i := range x {
			out[i] = response(x[i])
		}
		return out
	case model.Item:
		candidates := x.Candidates
		if candidates == nil {
			candidates = []model.Candidate{}
		}
		return itemResponse{ScanResult: x.ScanResult, PreviewID: x.PreviewID, Candidates: candidates, Attempt: x.Attempt, ContentRevision: x.ContentRevision, JobID: x.JobID, Ordinal: x.Ordinal, Path: x.Path, Size: x.Size, Mtime: x.Mtime, State: x.State, Error: x.Error, BookID: x.BookID}
	case *model.Item:
		if x == nil {
			return nil
		}
		return response(*x)
	case []model.Item:
		out := make([]any, len(x))
		for i := range x {
			out[i] = response(x[i])
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = response(v)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = response(v)
		}
		return out
	default:
		return v
	}
}
