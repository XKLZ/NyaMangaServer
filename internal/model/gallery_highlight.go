package model

import "errors"

const EnhancementGallery = "local_gallery_highlight"

var ErrGalleryAuth = errors.New("gallery_highlight_unauthorized")
var ErrGalleryDisabled = errors.New("gallery_highlight_disabled")
var ErrGalleryConflict = errors.New("gallery_connection_revision_conflict")
var ErrGalleryCapacity = errors.New("gallery_connection_limit")

type GalleryConnection struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt *int64 `json:"expires_at"`
	RevokedAt *int64 `json:"revoked_at"`
	Revision  int64  `json:"revision"`
	State     string `json:"state"`
}
type GalleryConnections struct {
	Items    []GalleryConnection `json:"items"`
	Total    int                 `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
}
type GalleryConnectionCreate struct{}
type GalleryConnectionSecret struct {
	ProtocolVersion int               `json:"protocol_version"`
	Connection      GalleryConnection `json:"connection"`
	Token           string            `json:"token"`
}

// Internal grant has no wire fields or raw secret. Never populate from JSON.
type GalleryGrant struct {
	ID, AccountID                       string `json:"-"`
	Hash                                []byte `json:"-"`
	Revision, AuthRevision, ACLRevision int64  `json:"-"`
	ExpiresAt                           *int64 `json:"-"`
}
type GalleryLookupInput struct {
	Provider   string   `json:"provider"`
	GalleryIDs []string `json:"gallery_ids"`
}
type GalleryMatch struct {
	GalleryID string `json:"gallery_id"`
	State     string `json:"state"`
}
type GalleryLookup struct {
	ProtocolVersion int            `json:"protocol_version"`
	Provider        string         `json:"provider"`
	QueryVersion    string         `json:"query_version"`
	Items           []GalleryMatch `json:"items"`
}
type GalleryStatus struct {
	ProtocolVersion     int    `json:"protocol_version"`
	Provider            string `json:"provider"`
	MaxBatchSize        int    `json:"max_batch_size"`
	ConnectionExpiresAt *int64 `json:"connection_expires_at"`
}
