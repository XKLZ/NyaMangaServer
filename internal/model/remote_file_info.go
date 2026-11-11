package model

type RemoteFileInfoBackfill struct {
	DryRun   bool             `json:"dry_run"`
	Expected map[string]int64 `json:"expected_revisions"`
}
type RemoteFileInfoRecord struct {
	ID              string `json:"id"`
	Revision        int64  `json:"revision"`
	Status          string `json:"status"`
	RemotePageCount *int64 `json:"remote_page_count"`
	RemoteSizeBytes *int64 `json:"remote_size_bytes"`
}
type RemoteFileInfoReport struct {
	DryRun  bool                   `json:"dry_run"`
	Updated int                    `json:"updated"`
	Items   []RemoteFileInfoRecord `json:"items"`
}
