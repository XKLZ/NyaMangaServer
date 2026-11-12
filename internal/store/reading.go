package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"nyamangaserver.local/server/internal/model"
	"time"
)

const readingColumns = "page_index,recorded_page_count,content_revision,completed,completed_at,last_read_at,read_count,revision"

func readingAccount(ctx context.Context) (string, error) {
	a, ok := model.Actor(ctx)
	if !ok || a.ID == "" {
		return "", model.ErrForbidden
	}
	return a.ID, nil
}
func readingFile(ctx context.Context, q queryer, id string) (int64, *int, error) {
	var rev int64
	var pages int
	e := scopeRow(ctx, q, "SELECT content_revision,page_count FROM files WHERE edition_id=?", id).Scan(&rev, &pages)
	if e == sql.ErrNoRows {
		e = model.ErrNotFound
	}
	var total *int
	if pages > 0 {
		total = &pages
	}
	return rev, total, e
}
func scanReading(row scanner, id string, current int64, total *int) (model.ReadingState, error) {
	out := model.ReadingState{EditionID: id, ContentRevision: current, CurrentContentRevision: current, CurrentPageCount: total, RecordedPageCount: total}
	e := row.Scan(&out.PageIndex, &out.RecordedPageCount, &out.ContentRevision, &out.Completed, &out.CompletedAt, &out.LastReadAt, &out.ReadCount, &out.Revision)
	if e == sql.ErrNoRows {
		e = nil
	}
	out.Stale = out.ContentRevision != current
	return out, e
}
func readingState(ctx context.Context, q queryer, account, id string, current int64, total *int) (model.ReadingState, error) {
	out, e := scanReading(scopeRow(ctx, q, "SELECT "+readingColumns+" FROM reading_states WHERE account_id=? AND edition_id=?", account, id), id, current, total)
	if e != nil {
		return out, e
	}
	out.CurrentParsingRevision, e = currentParsing(ctx, q, id)
	if e != nil {
		return out, e
	}
	out.ParsingRevision = out.CurrentParsingRevision
	if out.Revision > 0 && hasParsing(ctx, q) {
		e = scopeRow(ctx, q, "SELECT parsing_revision FROM reading_states WHERE account_id=? AND edition_id=?", account, id).Scan(&out.ParsingRevision)
	}
	out.ParsingStale = out.ParsingRevision != out.CurrentParsingRevision
	out.Stale = out.Stale || out.ParsingStale
	return out, e
}
func (s *Store) ReadingState(ctx context.Context, id string) (model.ReadingState, error) {
	account, e := readingAccount(ctx)
	if e != nil {
		return model.ReadingState{}, e
	}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return model.ReadingState{}, e
	}
	defer tx.Rollback()
	rev, total, e := readingFile(ctx, tx, id)
	if e != nil {
		return model.ReadingState{}, e
	}
	out, e := readingState(ctx, tx, account, id, rev, total)
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func saveReading(ctx context.Context, tx *sql.Tx, account string, v model.ReadingState, expected int64) error {
	r, e := tx.ExecContext(ctx, `INSERT INTO reading_states(account_id,edition_id,page_index,recorded_page_count,content_revision,completed,completed_at,last_read_at,read_count,revision) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(account_id,edition_id) DO UPDATE SET page_index=excluded.page_index,recorded_page_count=excluded.recorded_page_count,content_revision=excluded.content_revision,completed=excluded.completed,completed_at=excluded.completed_at,last_read_at=excluded.last_read_at,read_count=excluded.read_count,revision=excluded.revision WHERE reading_states.revision=?`, account, v.EditionID, v.PageIndex, v.RecordedPageCount, v.ContentRevision, v.Completed, v.CompletedAt, v.LastReadAt, v.ReadCount, v.Revision, expected)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return model.ErrReadingRevision
	}
	if hasParsing(ctx, tx) {
		_, e = tx.ExecContext(ctx, "UPDATE reading_states SET parsing_revision=? WHERE account_id=? AND edition_id=?", max(1, v.ParsingRevision), account, v.EditionID)
	}
	return e
}
func prepareReading(v *model.ReadingState, expected, content int64, accept bool, parsing int64, acceptParsing bool) error {
	if parsing == 0 {
		parsing = 1
	}
	if parsing != v.CurrentParsingRevision {
		return model.ErrReadingContent
	}
	if v.ParsingStale && !acceptParsing {
		return model.ErrReadingConfirmation
	}
	if v.Revision != expected || expected == math.MaxInt64 {
		return model.ErrReadingRevision
	}
	if content != v.CurrentContentRevision {
		return model.ErrReadingContent
	}
	if v.Stale {
		if v.ContentRevision != content && !accept {
			return model.ErrReadingConfirmation
		}
		v.PageIndex = nil
		v.Completed = false
		v.CompletedAt = nil
		v.ContentRevision = content
		v.ParsingRevision = v.CurrentParsingRevision
		v.ParsingStale = false
		v.Stale = false
	}
	if v.CurrentPageCount != nil || v.PageIndex == nil {
		v.RecordedPageCount = v.CurrentPageCount
	}
	return nil
}
func (s *Store) WriteReadingState(ctx context.Context, id string, in model.ReadingWrite) (model.ReadingState, error) {
	account, e := readingAccount(ctx)
	if e != nil {
		return model.ReadingState{}, e
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return model.ReadingState{}, e
	}
	defer tx.Rollback()
	out, e := writeReadingState(ctx, tx, account, id, in, time.Now().Unix(), false)
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}

func writeReadingState(ctx context.Context, tx *sql.Tx, account, id string, in model.ReadingWrite, now int64, offline bool) (model.ReadingState, error) {
	rev, total, e := readingFile(ctx, tx, id)
	if e != nil {
		return model.ReadingState{}, e
	}
	var out model.ReadingState
	var hash string
	if !offline {
		raw, _ := json.Marshal(in)
		hash = fmt.Sprintf("%x", sha256.Sum256(raw))
		var previousHash, response string
		e = scopeRow(ctx, tx, "SELECT request_hash,response_json FROM reading_state_requests WHERE account_id=? AND edition_id=? AND idempotency_key=?", account, id, in.IdempotencyKey).Scan(&previousHash, &response)
		if e == nil {
			if previousHash != hash {
				return out, model.ErrReadingIdempotency
			}
			if e = json.Unmarshal([]byte(response), &out); e != nil {
				return out, e
			}
			out.CurrentContentRevision = rev
			out.CurrentPageCount = total
			out.CurrentParsingRevision, e = currentParsing(ctx, tx, id)
			if e != nil {
				return out, e
			}
			if out.ParsingRevision == 0 {
				out.ParsingRevision = 1
			}
			out.ParsingStale = out.ParsingRevision != out.CurrentParsingRevision
			out.Stale = out.ContentRevision != rev || out.ParsingStale
			return out, nil
		}
		if e != sql.ErrNoRows {
			return out, e
		}
	}
	out, e = readingState(ctx, tx, account, id, rev, total)
	if e != nil {
		return out, e
	}
	if e = prepareReading(&out, in.Expected, in.ContentRevision, in.AcceptContentChange, in.ParsingRevision, in.AcceptParsingChange); e != nil {
		return out, e
	}
	if in.SessionID != "" {
		var sessionContent int64
		e = scopeRow(ctx, tx, "SELECT content_revision FROM reading_sessions WHERE id=? AND account_id=? AND edition_id=?", in.SessionID, account, id).Scan(&sessionContent)
		if e == sql.ErrNoRows {
			return out, model.ErrNotFound
		}
		if e != nil {
			return out, e
		}
		var sessionParsing int64 = 1
		if hasParsing(ctx, tx) {
			if e = scopeRow(ctx, tx, "SELECT parsing_revision FROM reading_sessions WHERE id=?", in.SessionID).Scan(&sessionParsing); e != nil {
				return out, e
			}
		}
		if sessionContent != rev || sessionParsing != out.CurrentParsingRevision {
			return out, model.ErrReadingContent
		}
	}
	if in.PageIndex != nil {
		if total == nil {
			return out, model.ErrReadingUnknownPages
		}
		if *in.PageIndex < 0 || *in.PageIndex >= *total {
			return out, model.ErrInput
		}
	}
	switch in.Action {
	case "progress":
		if in.PageIndex == nil {
			return out, model.ErrInput
		}
		out.PageIndex = in.PageIndex
		setReadingActivity(&out, now, offline)
	case "complete":
		if in.PageIndex != nil {
			out.PageIndex = in.PageIndex
		}
		if !out.Completed {
			out.Completed = true
			out.CompletedAt = &now
		}
		setReadingActivity(&out, now, offline)
	case "uncomplete":
		if in.PageIndex != nil {
			return out, model.ErrInput
		}
		out.Completed = false
		out.CompletedAt = nil
	case "reset":
		if in.PageIndex != nil {
			return out, model.ErrInput
		}
		out.PageIndex = nil
		out.Completed = false
		out.CompletedAt = nil
	case "heartbeat":
		if in.PageIndex != nil {
			return out, model.ErrInput
		}
		setReadingActivity(&out, now, offline)
	case "confirm_content":
		if !in.AcceptContentChange || in.PageIndex != nil {
			return out, model.ErrInput
		}
	default:
		return out, model.ErrInput
	}
	if out.PageIndex != nil && (out.RecordedPageCount == nil || *out.PageIndex >= *out.RecordedPageCount) {
		return out, model.ErrReadingContent
	}
	out.Revision++
	if e = saveReading(ctx, tx, account, out, in.Expected); e != nil {
		return out, e
	}
	if !offline {
		raw, _ := json.Marshal(out)
		if _, e = tx.ExecContext(ctx, "INSERT INTO reading_state_requests VALUES(?,?,?,?,?)", account, id, in.IdempotencyKey, hash, string(raw)); e != nil {
			return out, e
		}
	}
	return out, nil
}

func setReadingActivity(v *model.ReadingState, now int64, offline bool) {
	if !offline || v.LastReadAt == nil || now > *v.LastReadAt {
		v.LastReadAt = &now
	}
}

func (s *Store) StartReadingSession(ctx context.Context, id string, in model.ReadingSessionStart) (model.ReadingSession, error) {
	var out model.ReadingSession
	account, e := readingAccount(ctx)
	if e != nil {
		return out, e
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	out, e = s.startReadingSession(ctx, tx, account, id, in, time.Now().Unix(), false)
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}

func (s *Store) startReadingSession(ctx context.Context, tx *sql.Tx, account, id string, in model.ReadingSessionStart, now int64, offline bool) (model.ReadingSession, error) {
	var out model.ReadingSession
	intent := in.Intent
	if intent == "" {
		intent = model.ReadingStart
	}
	if intent != model.ReadingStart && (intent != model.ReadingRestart || s.Version < 29) {
		return out, model.ErrInput
	}

	rev, total, e := readingFile(ctx, tx, id)
	if e != nil {
		return out, e
	}
	intentColumn := "'start'"
	if s.Version >= 29 {
		intentColumn = "intent"
	}
	var recordedIntent string
	e = scopeRow(ctx, tx, "SELECT id,content_revision,started_at,"+intentColumn+" FROM reading_sessions WHERE account_id=? AND edition_id=? AND idempotency_key=?", account, id, in.IdempotencyKey).
		Scan(&out.ID, &out.ContentRevision, &out.StartedAt, &recordedIntent)
	if e == nil {
		if out.ContentRevision != in.ContentRevision || recordedIntent != intent {
			return out, model.ErrReadingIdempotency
		}
		if hasParsing(ctx, tx) {
			var recorded int64
			if e = scopeRow(ctx, tx, "SELECT parsing_revision FROM reading_sessions WHERE id=?", out.ID).Scan(&recorded); e != nil {
				return out, e
			}
			if recorded != max(1, in.ParsingRevision) {
				return out, model.ErrReadingIdempotency
			}
		}

		out.State, e = readingState(ctx, tx, account, id, rev, total)
		if e != nil {
			return out, e
		}
		return out, nil
	}
	if e != sql.ErrNoRows {
		return out, e
	}
	out.State, e = readingState(ctx, tx, account, id, rev, total)
	if e != nil {
		return out, e
	}
	if e = prepareReading(&out.State, in.Expected, in.ContentRevision, in.AcceptContentChange, in.ParsingRevision, in.AcceptParsingChange); e != nil {
		return out, e
	}
	if intent == model.ReadingRestart {
		if total == nil {
			return out, model.ErrReadingUnknownPages
		}
		page := 0
		out.State.PageIndex = &page
		out.State.Completed = false
		out.State.CompletedAt = nil
	}
	if out.State.ReadCount == math.MaxInt64 {
		return out, model.ErrCapacity
	}
	out.ID, e = model.NewID()
	if e != nil {
		return out, e
	}
	out.StartedAt = now
	out.ContentRevision = rev
	out.State.ReadCount++
	setReadingActivity(&out.State, now, offline)
	out.State.Revision++
	if e = saveReading(ctx, tx, account, out.State, in.Expected); e != nil {
		return out, e
	}
	if s.Version >= 29 {
		if _, e = tx.ExecContext(ctx, "INSERT INTO reading_sessions(id,account_id,edition_id,idempotency_key,content_revision,started_at,parsing_revision,intent) VALUES(?,?,?,?,?,?,?,?)",
			out.ID, account, id, in.IdempotencyKey, rev, out.StartedAt, out.State.ParsingRevision, intent); e != nil {
			return out, e
		}
		return out, nil
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO reading_sessions(id,account_id,edition_id,idempotency_key,content_revision,started_at) VALUES(?,?,?,?,?,?)", out.ID, account, id, in.IdempotencyKey, rev, out.StartedAt); e != nil {
		return out, e
	}
	if hasParsing(ctx, tx) {
		if _, e = tx.ExecContext(ctx, "UPDATE reading_sessions SET parsing_revision=? WHERE id=?", out.State.ParsingRevision, out.ID); e != nil {
			return out, e
		}
	}
	return out, nil
}

// Hydrate the page in one query. GET/list never create reading sessions or rows.
func readingForEditions(ctx context.Context, q queryer, books []model.Edition) error {
	account, e := readingAccount(ctx)
	if e != nil {
		return nil
	}
	if len(books) == 0 {
		return nil
	}
	ids := make([]string, len(books))
	index := map[string]int{}
	for i, b := range books {
		ids[i] = b.ID
		index[b.ID] = i
	}
	args := append([]any{account}, stringArgs(ids)...)
	extra := ",1,1"
	if hasParsing(ctx, q) {
		extra = ",f.parsing_revision,COALESCE(r.parsing_revision,f.parsing_revision)"
	}
	rows, e := scopeRows(ctx, q, `SELECT f.edition_id,f.content_revision,f.page_count,r.page_index,CASE WHEN r.revision IS NULL THEN NULLIF(f.page_count,0) ELSE r.recorded_page_count END,COALESCE(r.content_revision,f.content_revision),COALESCE(r.completed,0),r.completed_at,r.last_read_at,COALESCE(r.read_count,0),COALESCE(r.revision,0)`+extra+` FROM files f LEFT JOIN reading_states r ON r.edition_id=f.edition_id AND r.account_id=? WHERE f.edition_id IN(`+placeholders(len(ids))+`)`, args...)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var current int64
		var pages int
		var state model.ReadingState
		if e = rows.Scan(&id, &current, &pages, &state.PageIndex, &state.RecordedPageCount, &state.ContentRevision, &state.Completed, &state.CompletedAt, &state.LastReadAt, &state.ReadCount, &state.Revision, &state.CurrentParsingRevision, &state.ParsingRevision); e != nil {
			return e
		}
		state.EditionID = id
		state.CurrentContentRevision = current
		if pages > 0 {
			state.CurrentPageCount = &pages
		}
		state.ParsingStale = state.ParsingRevision != state.CurrentParsingRevision
		state.Stale = state.ContentRevision != current || state.ParsingStale
		if i, ok := index[id]; ok {
			books[i].ReadingState = &state
		}
	}
	return rows.Err()
}
func readingForMap(ctx context.Context, q queryer, books map[string]model.Edition) error {
	page := make([]model.Edition, 0, len(books))
	for _, b := range books {
		page = append(page, b)
	}
	if e := readingForEditions(ctx, q, page); e != nil {
		return e
	}
	for _, b := range page {
		books[b.ID] = b
	}
	return nil
}
