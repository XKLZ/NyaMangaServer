package userstate

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strings"

	"nyamangaserver.local/server/internal/model"
)

const OfflineBatchLimit = 100

var clientInstanceID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func validateQueue(ctx context.Context, q model.OfflineQueue) error {
	a, ok := model.Actor(ctx)
	if !ok || a.ID != q.AccountID {
		return model.ErrForbidden
	}
	if q.InstanceID == "" || q.CacheEpoch == "" || len(q.InstanceID) > 128 || len(q.CacheEpoch) > 128 || !clientInstanceID.MatchString(q.ClientInstanceID) {
		return model.ErrInput
	}
	return nil
}

func (s *Service) ReplayOffline(ctx context.Context, in model.OfflineOperationsRequest, received int64) ([]model.OfflineOperationResult, error) {
	if e := validateQueue(ctx, in.OfflineQueue); e != nil {
		return nil, e
	}
	if len(in.Operations) == 0 || len(in.Operations) > OfflineBatchLimit {
		return nil, model.ErrInput
	}
	var sequence int64
	seen := map[string]bool{}
	for _, o := range in.Operations {
		if !readingKey.MatchString(o.ID) || o.Sequence <= sequence || seen[o.ID] {
			return nil, model.ErrInput
		}
		seen[o.ID], sequence = true, o.Sequence
	}
	out := make([]model.OfflineOperationResult, 0, len(in.Operations))
	for _, o := range in.Operations {
		if !validOfflineOperation(o) {
			out = append(out, model.OfflineOperationResult{ID: o.ID, Sequence: o.Sequence, Status: "rejected", HTTPStatus: 422, Error: "invalid_input"})
			continue
		}
		v, e := s.repo.ApplyOfflineOperation(ctx, in.OfflineQueue, o, received)
		switch {
		case e == nil:
		case errors.Is(e, model.ErrNotFound), errors.Is(e, model.ErrForbidden):
			v = model.OfflineOperationResult{ID: o.ID, Sequence: o.Sequence, Status: "inaccessible", HTTPStatus: 404}
		case errors.Is(e, model.ErrClientKeyConflict):
			v = model.OfflineOperationResult{ID: o.ID, Sequence: o.Sequence, Status: "conflict", HTTPStatus: 409, Error: e.Error()}
		default:
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}

func validOfflineOperation(o model.OfflineOperation) bool {
	if o.ObjectID == "" || len(o.ObjectID) > 128 || strings.ContainsRune(o.ObjectID, 0) || len(o.ReportedAt) > 256 ||
		(o.Expected == nil) == (o.Previous == "") || o.Expected != nil && (*o.Expected < 0 || *o.Expected == math.MaxInt64) ||
		o.Previous != "" && (!readingKey.MatchString(o.Previous) || o.Previous == o.ID) {
		return false
	}
	_, kind := o.Scope()
	if kind == "reading" {
		b := o.Body
		if b.ContentRevision < 1 || b.ParsingRevision < 1 || b.Value != nil || o.Session != "" && !readingKey.MatchString(o.Session) {
			return false
		}
		switch o.Type {
		case "start", "restart":
			return o.Session != "" && b.PageIndex == nil
		case "progress":
			return b.PageIndex != nil && *b.PageIndex >= 0
		case "complete":
			return b.PageIndex == nil || *b.PageIndex >= 0
		default:
			return b.PageIndex == nil
		}
	}
	b := o.Body
	if kind == "" || o.Session != "" || b.Value == nil || b.ContentRevision != 0 || b.ParsingRevision != 0 ||
		b.PageIndex != nil || b.AcceptContentChange || b.AcceptParsingChange {
		return false
	}
	return kind != "tag_follow" || *b.Value == (o.Type == "follow")
}

func (s *Service) OfflineStatus(ctx context.Context, in model.OfflineStatusRequest) ([]model.OfflineOperationResult, error) {
	if e := validateQueue(ctx, in.OfflineQueue); e != nil {
		return nil, e
	}
	if len(in.OperationIDs) == 0 || len(in.OperationIDs) > OfflineBatchLimit {
		return nil, model.ErrInput
	}
	for _, id := range in.OperationIDs {
		if !readingKey.MatchString(id) {
			return nil, model.ErrInput
		}
	}
	return s.repo.OfflineOperationStatus(ctx, in.OfflineQueue, in.OperationIDs)
}
