package model

type InstanceFile struct {
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type InstanceSource struct {
	EditionID string `json:"edition_id"`
	LibraryID string `json:"library_id"`
	Path      string `json:"relative_path"`
	Size      int64  `json:"size"`
	Mtime     int64  `json:"mtime_ns"`
	State     string `json:"file_state"`
	Revision  int64  `json:"content_revision"`
}
type InstanceRoot struct {
	Name      string `json:"display_name"`
	LibraryID string `json:"library_id"`
	RootKey   string `json:"root_key"`
	Identity  string `json:"identity"`
}
type InstanceInventory struct {
	Ledger   string           `json:"ledger_sha256"`
	Schema   string           `json:"schema_sha256"`
	Version  int              `json:"schema_version"`
	Counts   map[string]int64 `json:"counts"`
	Required []InstanceFile   `json:"required_files"`
	Roots    []InstanceRoot   `json:"roots"`
	Sources  []InstanceSource `json:"-"`
}

type InstanceRootMapping struct {
	LibraryID     string  `json:"library_id"`
	TargetRootKey *string `json:"target_root_key"`
}
type InstanceRebase struct {
	LibraryID     string
	SourceRootKey string
	TargetRootKey *string
	Identity      string
}
type InstanceStat struct {
	EditionID string
	Mtime     int64
}
