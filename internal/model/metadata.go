package model

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

type RemoteError struct {
	Code           string
	Retryable      bool
	ActionRequired bool
	Wait           time.Duration
}

func (e *RemoteError) Error() string { return e.Code }

type DictionaryRelease struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Digest  string `json:"digest"`
}

var ErrInput = errors.New("invalid_input")
var ErrJobActive = errors.New("metadata_in_progress")

func NewID() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Metadata is the bounded canonical source representation, independent of SQL.
type Metadata struct {
	RemotePageCount *int64    `json:"remote_page_count"`
	RemoteSizeBytes *int64    `json:"remote_size_bytes"`
	Title           *string   `json:"title"`
	TitleOriginal   *string   `json:"title_original"`
	Category        *string   `json:"category"`
	Language        *string   `json:"language"`
	Rating          *float64  `json:"rating"`
	PublishedAt     *string   `json:"published_at"`
	Description     *string   `json:"description"`
	Tags            []TagName `json:"tags"`
}
type TagName struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}
type Override struct {
	Op    string          `json:"op"`
	Value json.RawMessage `json:"value,omitempty"`
}
type Source struct {
	ID              string   `json:"id"`
	Provider        string   `json:"provider"`
	ExternalID      string   `json:"external_id"`
	URL             string   `json:"url"`
	Method          string   `json:"match_method"`
	ContentRevision int64    `json:"matched_content_revision"`
	FetchedAt       int64    `json:"fetched_at"`
	Fields          Metadata `json:"fields"`
}
type MetadataDetails struct {
	Properties        *BookProperties     `json:"properties,omitempty"`
	Tags              []Tag               `json:"tags"`
	Fields            Metadata            `json:"fields"`
	Overrides         map[string]Override `json:"overrides"`
	Source            *Source             `json:"source"`
	State             string              `json:"state"`
	SelectionRevision int64               `json:"selection_revision"`
	ManualURL         string              `json:"manual_url"`
}
type Candidate struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}
type MetadataInput struct {
	LookupSHA1      *string `json:"lookup_sha1,omitempty"`
	LookupAlgorithm string  `json:"lookup_algorithm,omitempty"`
	URL             string  `json:"gallery_url,omitempty"`
	Force           bool    `json:"force_refresh,omitempty"`
}
type Tag struct {
	DictionaryState string `json:"dictionary_state,omitempty"`
	DictionaryMatch *bool  `json:"dictionary_match"`
	ID              string `json:"id"`
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	Translation     string `json:"translation"`
	Origin          string `json:"translation_origin"`
	Revision        int64  `json:"revision"`
}
type Event struct {
	ID         int64  `json:"event_id"`
	Type       string `json:"type"`
	ResourceID string `json:"resource_id"`
	Revision   int64  `json:"revision"`
}
type TranslationVersion struct {
	ID          string `json:"version"`
	Source      string `json:"source"`
	Digest      string `json:"sha256"`
	CacheKey    string `json:"-"`
	ActivatedAt int64  `json:"activated_at"`
}
