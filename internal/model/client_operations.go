package model

import (
	"encoding/json"
	"errors"
)

var ErrOfflineDependency = errors.New("offline_dependency_blocked")

type OfflineQueue struct {
	ClientIdentity
	AccountID        string `json:"account_id"`
	ClientInstanceID string `json:"client_instance_id"`
}

type OfflineOperationsRequest struct {
	OfflineQueue
	Operations []OfflineOperation `json:"operations"`
}

type OfflineStatusRequest struct {
	OfflineQueue
	OperationIDs []string `json:"operation_ids"`
}

type OfflineOperation struct {
	ID         string               `json:"operation_id"`
	Sequence   int64                `json:"local_sequence"`
	ObjectID   string               `json:"object_id"`
	Type       string               `json:"type"`
	Expected   *int64               `json:"expected_revision,omitempty"`
	Previous   string               `json:"previous_operation_id,omitempty"`
	Session    string               `json:"offline_session_id,omitempty"`
	ReportedAt json.RawMessage      `json:"occurred_at,omitempty"`
	Body       OfflineOperationBody `json:"body"`
}

type OfflineOperationBody struct {
	ContentRevision     int64 `json:"content_revision,omitempty"`
	ParsingRevision     int64 `json:"parsing_revision,omitempty"`
	AcceptContentChange bool  `json:"accept_content_change,omitempty"`
	AcceptParsingChange bool  `json:"accept_parsing_change,omitempty"`
	PageIndex           *int  `json:"page_index,omitempty"`
	Value               *bool `json:"value,omitempty"`
}

func (o OfflineOperation) Scope() (object, state string) {
	switch o.Type {
	case "start", "restart", "progress", "complete", "uncomplete", "reset":
		return "edition", "reading"
	case "favorite", "hidden":
		return "edition", "edition_state"
	case "follow", "unfollow":
		return "tag", "tag_follow"
	default:
		return "", ""
	}
}

type OfflineOperationResult struct {
	ID           string          `json:"operation_id"`
	Sequence     int64           `json:"local_sequence,omitempty"`
	HTTPStatus   int             `json:"http_status"`
	Status       string          `json:"status"`
	Error        string          `json:"error_code,omitempty"`
	Type         string          `json:"type,omitempty"`
	Revision     int64           `json:"revision"`
	ReceivedAt   int64           `json:"received_at,omitempty"`
	EventAt      int64           `json:"event_at"`
	ReportedAt   json.RawMessage `json:"reported_at,omitempty"`
	Adjusted     bool            `json:"adjusted,omitempty"`
	SessionID    string          `json:"session_id,omitempty"`
	Reading      *ReadingState   `json:"reading,omitempty"`
	EditionState *UserState      `json:"edition_state,omitempty"`
	TagFollow    *TagFollow      `json:"tag_follow,omitempty"`
}
