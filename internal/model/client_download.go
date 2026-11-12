package model

import "errors"

var ErrDownloadBusy = errors.New("download_busy")
var ErrDownloadPending = errors.New("download_not_ready")
var ErrClientKeyConflict = errors.New("client_idempotency_conflict")
var ErrClientEpoch = errors.New("cache_epoch_changed")

const DownloadSegment = 8 << 20
const DownloadMaxRange = 16 << 20

type DownloadRequest struct {
	ContentRevision int64  `json:"content_revision"`
	Key             string `json:"idempotency_key"`
}

type Download struct {
	ID              string `json:"id"`
	EditionID       string `json:"edition_id"`
	ContentRevision int64  `json:"content_revision"`
	Length          int64  `json:"length"`
	SHA256          string `json:"sha256,omitempty"`
	State           string `json:"state"`
	Error           string `json:"error_code,omitempty"`
	AccountID       string `json:"-"`
	Epoch           string `json:"-"`
	Source          string `json:"-"`
	Key             string `json:"-"`
}
