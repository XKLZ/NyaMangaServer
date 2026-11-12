package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"nyamangaserver.local/server/internal/model"
)

type offlineReceipt struct {
	Client, ObjectKind, ObjectID, StateKind, Digest string
	Result                                          model.OfflineOperationResult
}

func receiptScan(row scanner) (offlineReceipt, error) {
	var v offlineReceipt
	var result string
	err := row.Scan(&v.Client, &v.ObjectKind, &v.ObjectID, &v.StateKind, &v.Digest, &result)
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal([]byte(result), &v.Result)
}

const receiptColumns = "client_instance_id,object_kind,object_id,state_kind,request_digest,result_json"

func receiptByID(ctx context.Context, q queryer, in model.OfflineQueue, id string) (offlineReceipt, error) {
	return receiptScan(q.QueryRowContext(ctx, "SELECT "+receiptColumns+" FROM client_offline_receipts WHERE account_id=? AND cache_epoch=? AND operation_id=?", in.AccountID, in.CacheEpoch, id))
}

func checkOfflineQueue(ctx context.Context, q queryer, in model.OfflineQueue) error {
	a, ok := model.Actor(ctx)
	if !ok || a.ID != in.AccountID {
		return model.ErrForbidden
	}
	if device, ok := model.DeviceActor(ctx); ok {
		if err := checkDeviceActor(ctx, q, a.ID, device); err != nil {
			return err
		}
	}
	identity, e := clientIdentity(ctx, q)
	if e != nil {
		return e
	}
	if identity != in.ClientIdentity {
		return model.ErrClientEpoch
	}
	return nil
}

func requireOfflineObject(ctx context.Context, q queryer, kind, id string) error {
	var found string
	var err error
	switch kind {
	case "edition":
		err = scopeRow(ctx, q, `SELECT ed.id FROM editions ed JOIN libraries l ON l.id=ed.library_id
			WHERE ed.id=? AND ed.trashed_at IS NULL AND l.trashed_at IS NULL`, id).Scan(&found)
	case "tag":
		err = scopeRow(ctx, q, "SELECT id FROM tags WHERE id=?", id).Scan(&found)
	default:
		return model.ErrInput
	}
	if errors.Is(err, sql.ErrNoRows) {
		return model.ErrNotFound
	}
	return err
}

func offlineHash(v any) string {
	b, _ := json.Marshal(v)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func offlineEvent(result *model.OfflineOperationResult, reported json.RawMessage, received int64) {
	result.ReceivedAt, result.EventAt, result.ReportedAt = received, received, reported
	if len(reported) == 0 {
		return
	}
	at, err := strconv.ParseInt(string(reported), 10, 64)
	if err != nil || at < 0 || at > received && (received < 0 || at-received > 300) {
		result.Adjusted = true
		return
	}
	result.EventAt = at
}

func (s *Store) ApplyOfflineOperation(ctx context.Context, in model.OfflineQueue, o model.OfflineOperation, received int64) (model.OfflineOperationResult, error) {
	out := model.OfflineOperationResult{ID: o.ID, Sequence: o.Sequence, Type: o.Type}
	offlineEvent(&out, o.ReportedAt, received)
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = checkOfflineQueue(ctx, tx, in); e != nil {
		return out, e
	}
	object, state := o.Scope()
	if e = requireOfflineObject(ctx, tx, object, o.ObjectID); e != nil {
		return out, e
	}
	digest := offlineHash(o)
	old, e := receiptByID(ctx, tx, in, o.ID)
	if e == nil {
		if old.Digest != digest || old.Client != in.ClientInstanceID {
			return out, model.ErrClientKeyConflict
		}
		if old.Result.Status != "blocked" {
			return old.Result, tx.Commit()
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}

	var allowed bool
	if e = tx.QueryRowContext(ctx, "SELECT phase='idle' FROM health_state WHERE singleton=1").Scan(&allowed); e != nil {
		return out, e
	}
	if !allowed {
		return out, model.ErrHealthMaintenance
	}
	expected, previous, e := offlineExpected(ctx, tx, in, o, object, state)
	var sessionKey *string
	if e == nil {
		if _, e = tx.ExecContext(ctx, "SAVEPOINT apply_offline_operation"); e != nil {
			return out, e
		}
		sessionKey, e = s.applyOfflineState(ctx, tx, in, o, expected, &out)
		if e != nil {
			sessionKey = nil
			if _, rollback := tx.ExecContext(ctx, "ROLLBACK TO apply_offline_operation"); rollback != nil {
				return out, rollback
			}
		}
		if _, release := tx.ExecContext(ctx, "RELEASE apply_offline_operation"); release != nil {
			return out, release
		}
	}
	if e != nil {
		if e = offlineResultError(ctx, tx, in, o, state, &out, e); e != nil {
			return out, e
		}
	} else {
		out.Status, out.HTTPStatus = "applied", 200
	}
	data, e := json.Marshal(out)
	if e != nil {
		return out, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO client_offline_receipts(account_id,cache_epoch,operation_id,client_instance_id,object_kind,object_id,state_kind,
		previous_operation_id,request_digest,session_key,received_at,result_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(account_id,cache_epoch,operation_id) DO UPDATE SET previous_operation_id=excluded.previous_operation_id,
		session_key=excluded.session_key,received_at=excluded.received_at,result_json=excluded.result_json`,
		in.AccountID, in.CacheEpoch, o.ID, in.ClientInstanceID, object, o.ObjectID, state, previous, digest, sessionKey, received, string(data))
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}

func offlineExpected(ctx context.Context, tx *sql.Tx, in model.OfflineQueue, o model.OfflineOperation, object, state string) (int64, *string, error) {
	if o.Expected != nil {
		return *o.Expected, nil, nil
	}
	prev, e := receiptByID(ctx, tx, in, o.Previous)
	if errors.Is(e, sql.ErrNoRows) {
		return 0, nil, model.ErrOfflineDependency
	}
	if e != nil {
		return 0, nil, e
	}
	if prev.Client != in.ClientInstanceID || prev.ObjectKind != object || prev.ObjectID != o.ObjectID || prev.StateKind != state ||
		prev.Result.Sequence >= o.Sequence {
		return 0, nil, model.ErrInput
	}
	if prev.Result.Status != "applied" {
		return 0, &o.Previous, model.ErrOfflineDependency
	}
	return prev.Result.Revision, &o.Previous, nil
}

func (s *Store) applyOfflineState(ctx context.Context, tx *sql.Tx, in model.OfflineQueue, o model.OfflineOperation, expected int64, out *model.OfflineOperationResult) (*string, error) {
	_, state := o.Scope()
	switch state {
	case "reading":
		return s.applyOfflineReading(ctx, tx, in, o, expected, out)
	case "edition_state":
		p := model.StatePatch{Expected: expected}
		if o.Type == "favorite" {
			p.Favorite = o.Body.Value
		} else {
			p.Hidden = o.Body.Value
		}
		v, e := setState(ctx, tx, in.AccountID, o.ObjectID, p)
		if e == nil {
			out.Revision, out.EditionState = v.Revision, &v
		}
		return nil, e
	case "tag_follow":
		v, e := setTagFollow(ctx, tx, in.AccountID, o.ObjectID, expected, *o.Body.Value)
		if e == nil {
			out.Revision, out.TagFollow = v.Revision, &v
		}
		return nil, e
	default:
		return nil, model.ErrInput
	}
}

func (s *Store) applyOfflineReading(ctx context.Context, tx *sql.Tx, in model.OfflineQueue, o model.OfflineOperation, expected int64, out *model.OfflineOperationResult) (*string, error) {
	b := o.Body
	start := o.Type == "start" || o.Type == "restart"
	var sessionKey, sessionID string
	if o.Session != "" {
		sessionKey = "offline:" + offlineHash([]string{in.CacheEpoch, in.ClientInstanceID, o.ObjectID, o.Session})
		old, e := receiptScan(tx.QueryRowContext(ctx, "SELECT "+receiptColumns+" FROM client_offline_receipts WHERE account_id=? AND cache_epoch=? AND session_key=?",
			in.AccountID, in.CacheEpoch, sessionKey))
		switch {
		case e == nil:
			if start {
				if old.Result.Type != o.Type || old.Result.Reading == nil || old.Result.Reading.ContentRevision != b.ContentRevision ||
					old.Result.Reading.ParsingRevision != b.ParsingRevision {
					return nil, model.ErrReadingIdempotency
				}
				out.Revision, out.SessionID, out.Reading = old.Result.Revision, old.Result.SessionID, old.Result.Reading
				return nil, nil
			}
			sessionID = old.Result.SessionID
		case errors.Is(e, sql.ErrNoRows):
			if !start {
				return nil, model.ErrOfflineDependency
			}
		default:
			return nil, e
		}
	}
	if start {
		if sessionKey == "" {
			return nil, model.ErrInput
		}
		req := model.ReadingSessionStart{Intent: o.Type, Expected: expected, ContentRevision: b.ContentRevision, ParsingRevision: b.ParsingRevision,
			IdempotencyKey: sessionKey, AcceptContentChange: b.AcceptContentChange, AcceptParsingChange: b.AcceptParsingChange}
		v, e := s.startReadingSession(ctx, tx, in.AccountID, o.ObjectID, req, out.EventAt, true)
		if e != nil {
			return nil, e
		}
		out.Revision, out.SessionID, out.Reading = v.State.Revision, v.ID, &v.State
		return &sessionKey, nil
	}
	req := model.ReadingWrite{Action: o.Type, Expected: expected, ContentRevision: b.ContentRevision, ParsingRevision: b.ParsingRevision, PageIndex: b.PageIndex,
		AcceptContentChange: b.AcceptContentChange, AcceptParsingChange: b.AcceptParsingChange, SessionID: sessionID}
	v, e := writeReadingState(ctx, tx, in.AccountID, o.ObjectID, req, out.EventAt, true)
	if e == nil {
		out.Revision, out.SessionID, out.Reading = v.Revision, sessionID, &v
	}
	return nil, e
}

func offlineResultError(ctx context.Context, tx *sql.Tx, in model.OfflineQueue, o model.OfflineOperation, state string, out *model.OfflineOperationResult, e error) error {
	switch {
	case errors.Is(e, model.ErrOfflineDependency):
		out.Status, out.HTTPStatus, out.Error = "blocked", 409, e.Error()
		return nil
	case errors.Is(e, model.ErrInput), errors.Is(e, model.ErrReadingUnknownPages), errors.Is(e, model.ErrCapacity):
		out.Status, out.HTTPStatus, out.Error = "rejected", 422, e.Error()
		return nil
	case errors.Is(e, model.ErrConflict), errors.Is(e, model.ErrTagFollowConflict), errors.Is(e, model.ErrReadingRevision),
		errors.Is(e, model.ErrReadingIdempotency), errors.Is(e, model.ErrReadingContent), errors.Is(e, model.ErrReadingConfirmation):
		out.Status, out.HTTPStatus, out.Error = "conflict", 409, e.Error()
		if errors.Is(e, model.ErrConflict) {
			out.Error = "revision_conflict"
		}
	default:
		return e
	}
	switch state {
	case "reading":
		rev, pages, err := readingFile(ctx, tx, o.ObjectID)
		if err != nil {
			return err
		}
		v, err := readingState(ctx, tx, in.AccountID, o.ObjectID, rev, pages)
		if err != nil {
			return err
		}
		out.Revision, out.Reading = v.Revision, &v
	case "edition_state":
		v, err := userState(ctx, tx, in.AccountID, o.ObjectID)
		if err != nil {
			return err
		}
		out.Revision, out.EditionState = v.Revision, &v
	case "tag_follow":
		v := model.TagFollow{TagID: o.ObjectID}
		err := scopeRow(ctx, tx, "SELECT followed,revision FROM tag_follows WHERE account_id=? AND tag_id=?", in.AccountID, o.ObjectID).Scan(&v.Followed, &v.Revision)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		out.Revision, out.TagFollow = v.Revision, &v
	}
	return nil
}

func (s *Store) OfflineOperationStatus(ctx context.Context, in model.OfflineQueue, ids []string) ([]model.OfflineOperationResult, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = checkOfflineQueue(ctx, tx, in); e != nil {
		return nil, e
	}
	out := make([]model.OfflineOperationResult, 0, len(ids))
	for _, id := range ids {
		v, e := receiptByID(ctx, tx, in, id)
		if errors.Is(e, sql.ErrNoRows) || e == nil && v.Client != in.ClientInstanceID {
			out = append(out, model.OfflineOperationResult{ID: id, Status: "unknown", HTTPStatus: 404})
			continue
		}
		if e != nil {
			return nil, e
		}
		e = requireOfflineObject(ctx, tx, v.ObjectKind, v.ObjectID)
		if errors.Is(e, model.ErrNotFound) {
			out = append(out, model.OfflineOperationResult{ID: id, Status: "inaccessible", HTTPStatus: 404})
			continue
		}
		if e != nil {
			return nil, e
		}
		out = append(out, v.Result)
	}
	return out, tx.Commit()
}
