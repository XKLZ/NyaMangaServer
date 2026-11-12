package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"math"
	"nyamangaserver.local/server/internal/model"
	"strings"
)

func (s *Store) SetTagFollow(ctx context.Context, account, id string, expected int64, followed bool) (model.TagFollow, error) {
	out := model.TagFollow{TagID: id}
	if account == "" || id == "" || len(id) > 128 || strings.ContainsRune(id, 0) || expected < 0 || expected == math.MaxInt64 {
		return out, model.ErrInput
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	out, e = setTagFollow(ctx, tx, account, id, expected, followed)
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}

func setTagFollow(ctx context.Context, tx *sql.Tx, account, id string, expected int64, followed bool) (model.TagFollow, error) {
	out := model.TagFollow{TagID: id}
	if expected < 0 || expected == math.MaxInt64 {
		return out, model.ErrInput
	}
	var e error
	var found string
	if e = scopeRow(ctx, tx, "SELECT id FROM accounts WHERE id=?", account).Scan(&found); e != nil {
		if e == sql.ErrNoRows {
			return out, model.ErrNotFound
		}
		return out, e
	}
	if _, e = tagAnchor(ctx, tx, id); e != nil {
		return out, e
	}
	e = scopeRow(ctx, tx, "SELECT followed,revision FROM tag_follows WHERE account_id=? AND tag_id=?", account, id).Scan(&out.Followed, &out.Revision)
	if e != nil && e != sql.ErrNoRows {
		return out, e
	}
	if expected != out.Revision {
		return out, model.ErrTagFollowConflict
	}
	if followed == out.Followed {
		return out, nil
	}
	if out.Revision == math.MaxInt64 {
		return out, model.ErrTagFollowConflict
	}
	out.Revision++
	out.Followed = followed
	if _, e = tx.ExecContext(ctx, `INSERT INTO tag_follows(account_id,tag_id,followed,revision) VALUES(?,?,?,?) ON CONFLICT(account_id,tag_id) DO UPDATE SET followed=excluded.followed,revision=excluded.revision`, account, id, followed, out.Revision); e != nil {
		return out, e
	}
	result, e := tx.ExecContext(ctx, `INSERT INTO tag_follow_epochs(account_id,revision) VALUES(?,1) ON CONFLICT(account_id) DO UPDATE SET revision=revision+1 WHERE revision<9223372036854775807`, account)
	if e != nil {
		return out, e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return out, e
	}
	if n != 1 {
		return out, model.ErrTagFollowConflict
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO events(type,resource_id,revision,created_at,owner_account_id) VALUES('tag_follow',?,?,unixepoch(),?)`, id, out.Revision, account); e != nil {
		return out, e
	}
	return out, nil
}
func tagFollowSnapshot(ctx context.Context, q queryer, snapshot string) (string, error) {
	account := model.TagAccount(ctx)
	var epoch int64
	e := scopeRow(ctx, q, "SELECT revision FROM tag_follow_epochs WHERE account_id=?", account).Scan(&epoch)
	if e != nil && e != sql.ErrNoRows {
		return "", e
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:tag-follows:%d:%s:%d", snapshot, len(account), account, epoch)))), nil
}

// Exactly one bounded query for the entire page (including both relation ends).
func enrichTagFollows(ctx context.Context, q queryer, tags ...*model.CatalogTag) error {
	if len(tags) == 0 {
		return nil
	}
	ids := map[string][]*model.CatalogTag{}
	args := []any{model.TagAccount(ctx)}
	marks := []string{}
	for _, t := range tags {
		t.Followed = false
		t.FollowRevision = 0
		if _, ok := ids[t.ID]; !ok {
			marks = append(marks, "?")
			args = append(args, t.ID)
		}
		ids[t.ID] = append(ids[t.ID], t)
	}
	rows, e := scopeRows(ctx, q, "SELECT tag_id,followed,revision FROM tag_follows WHERE account_id=? AND tag_id IN("+strings.Join(marks, ",")+")", args...)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var state bool
		var rev int64
		if e = rows.Scan(&id, &state, &rev); e != nil {
			return e
		}
		for _, t := range ids[id] {
			t.Followed = state
			t.FollowRevision = rev
		}
	}
	return rows.Err()
}
