package model

import "errors"

var ErrReadingRevision = errors.New("reading_revision_conflict")
var ErrReadingContent = errors.New("reading_content_changed")
var ErrReadingConfirmation = errors.New("reading_content_confirmation_required")
var ErrReadingUnknownPages = errors.New("reading_page_count_unknown")
var ErrReadingIdempotency = errors.New("reading_idempotency_conflict")

type ReadingState struct {
	ParsingRevision        int64  `json:"parsing_revision"`
	CurrentParsingRevision int64  `json:"current_parsing_revision"`
	ParsingStale           bool   `json:"parsing_stale"`
	EditionID              string `json:"edition_id"`
	PageIndex              *int   `json:"page_index"`
	RecordedPageCount      *int   `json:"recorded_page_count"`
	ContentRevision        int64  `json:"content_revision"`
	CurrentContentRevision int64  `json:"current_content_revision"`
	CurrentPageCount       *int   `json:"current_page_count"`
	Stale                  bool   `json:"stale"`
	Completed              bool   `json:"completed"`
	CompletedAt            *int64 `json:"completed_at"`
	LastReadAt             *int64 `json:"last_read_at"`
	ReadCount              int64  `json:"read_count"`
	Revision               int64  `json:"revision"`
}
type ReadingWrite struct {
	ParsingRevision     int64  `json:"parsing_revision"`
	AcceptParsingChange bool   `json:"accept_parsing_change"`
	Expected            int64  `json:"expected_revision"`
	ContentRevision     int64  `json:"content_revision"`
	IdempotencyKey      string `json:"idempotency_key"`
	Action              string `json:"action"`
	PageIndex           *int   `json:"page_index,omitempty"`
	AcceptContentChange bool   `json:"accept_content_change"`
	SessionID           string `json:"session_id,omitempty"`
}
type ReadingSessionStart struct {
	ParsingRevision     int64  `json:"parsing_revision"`
	AcceptParsingChange bool   `json:"accept_parsing_change"`
	Expected            int64  `json:"expected_revision"`
	ContentRevision     int64  `json:"content_revision"`
	IdempotencyKey      string `json:"idempotency_key"`
	AcceptContentChange bool   `json:"accept_content_change"`
}
type ReadingSession struct {
	ID              string       `json:"id"`
	StartedAt       int64        `json:"started_at"`
	ContentRevision int64        `json:"content_revision"`
	State           ReadingState `json:"state"`
}
