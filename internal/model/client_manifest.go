package model

type OfflineManifestRequest struct {
	ContentRevision int64 `json:"content_revision"`
	ParsingRevision int64 `json:"parsing_revision"`
}

type OfflineManifest struct {
	ID string `json:"id"`
	ClientIdentity
	AccountID             string           `json:"account_id"`
	LibraryID             string           `json:"library_id"`
	EditionID             string           `json:"edition_id"`
	ContentRevision       int64            `json:"content_revision"`
	ParsingRevision       int64            `json:"parsing_revision"`
	StructureDigest       string           `json:"structure_digest"`
	PageCount             int              `json:"page_count"`
	DirectoryVersion      string           `json:"directory_version"`
	RepresentationVersion string           `json:"representation_version"`
	State                 string           `json:"state"`
	Metadata              *OfflineMetadata `json:"metadata,omitempty"`
}

type OfflineMetadata struct {
	Digest        string        `json:"digest"`
	WorkID        string        `json:"work_id"`
	Title         string        `json:"title"`
	CoverRevision int64         `json:"cover_revision"`
	Series        *ClientSeries `json:"series"`
}

type OfflinePage struct {
	Page
	ImageURL string `json:"image_url"`
}

type OfflinePages struct {
	EditionID       string        `json:"edition_id"`
	ContentRevision int64         `json:"content_revision"`
	ParsingRevision int64         `json:"parsing_revision"`
	ManifestID      string        `json:"manifest_id"`
	StructureDigest string        `json:"structure_digest"`
	Page            int           `json:"page"`
	PageSize        int           `json:"page_size"`
	Total           int           `json:"total"`
	Items           []OfflinePage `json:"items"`
}

type OfflineCheckRequest struct {
	ClientIdentity
	Objects []OfflineCheckObject `json:"objects"`
}

type OfflineCheckObject struct {
	ID             string `json:"id"`
	MetadataDigest string `json:"metadata_digest,omitempty"`
}

type OfflineCheckResult struct {
	ID       string           `json:"id"`
	Status   string           `json:"status"`
	Metadata *OfflineMetadata `json:"metadata,omitempty"`
}
