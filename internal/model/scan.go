package model

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

// ScanResult is additive to the legacy task/item state and error code.
type ScanIssue struct {
	Stage    string `json:"stage"`
	Code     string `json:"code"`
	Resource string `json:"resource,omitempty"`
	Actual   int64  `json:"actual,omitempty"`
	Limit    int64  `json:"limit,omitempty"`
}
type ScanResult struct {
	InputParsingFingerprint string            `json:"input_parsing_fingerprint"`
	InputParsingRevision    int64             `json:"input_parsing_revision"`
	Classification          string            `json:"classification"`
	ParsingRevision         int64             `json:"parsing_revision"`
	ContentRevision         int64             `json:"content_revision"`
	ParserVersion           string            `json:"parser_version"`
	EffectiveEncoding       string            `json:"effective_encoding"`
	DirectoryDigest         string            `json:"directory_digest"`
	Stages                  map[string]string `json:"stages"`
	Issues                  []ScanIssue       `json:"issues"`
	WarningCount            int               `json:"warning_count"`
	SourceJobID             string            `json:"source_job_id,omitempty"`
	SourceOrdinal           int               `json:"source_ordinal,omitempty"`
}
type FileParsing struct {
	EncodingOverride  *string `json:"encoding_override"`
	ParsingRevision   int64   `json:"parsing_revision"`
	ParserVersion     string  `json:"parser_version"`
	DirectoryDigest   string  `json:"directory_digest"`
	EffectiveEncoding string  `json:"effective_encoding"`
}
type EncodingCandidate struct {
	Encoding        string `json:"encoding"`
	Valid           bool   `json:"valid"`
	PageCount       int    `json:"page_count"`
	Sample          []Page `json:"sample"`
	DirectoryDigest string `json:"directory_digest"`
	Error           string `json:"error_code,omitempty"`
}
type EncodingPreview struct {
	EditionID         string              `json:"edition_id"`
	Revision          int64               `json:"revision"`
	ContentRevision   int64               `json:"content_revision"`
	ParsingRevision   int64               `json:"parsing_revision"`
	InheritedEncoding string              `json:"inherited_encoding"`
	Candidates        []EncodingCandidate `json:"candidates"`
	RequiresChoice    bool                `json:"requires_choice"`
}
type EncodingUpdate struct {
	Expected        int64   `json:"expected_revision"`
	ContentRevision int64   `json:"content_revision"`
	ParsingRevision int64   `json:"parsing_revision"`
	Encoding        *string `json:"encoding"`
	DirectoryDigest string  `json:"directory_digest"`
}

func (p FileParsing) Fingerprint() string {
	raw, _ := json.Marshal(p)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

var ErrParsingChanged = errors.New("parsing_changed")
