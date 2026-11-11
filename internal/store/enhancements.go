package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"math"
	"nyamangaserver.local/server/internal/model"
)

// Legacy schemas exist only in migration tooling/tests. Never infer defaults
// from account, manga or task counts.
func enhancementEnabled(ctx context.Context, q queryer, feature string) (bool, error) {
	var exists int
	if e := scopeRow(ctx, q, "SELECT count(*) FROM sqlite_master WHERE name='enhancement_settings'").Scan(&exists); e != nil {
		return false, e
	}
	if exists == 0 {
		return true, nil
	}
	var enabled bool
	var state string
	e := scopeRow(ctx, q, "SELECT enabled,state FROM enhancement_settings WHERE feature=?", feature).Scan(&enabled, &state)
	return enabled && state == "stable", e
}
func enhancementGate(ctx context.Context, q queryer, feature string) error {
	if feature == "" {
		return nil
	}
	enabled, e := enhancementEnabled(ctx, q, feature)
	if e != nil {
		return e
	}
	if !enabled {
		return model.ErrEnhancementDisabled
	}
	return nil
}
func (s *Store) EnhancementAllowed(ctx context.Context, feature string) error {
	return enhancementGate(ctx, s.Read, feature)
}
func enhancementImpact(ctx context.Context, q queryer, feature string) (int, string, error) {
	rows, e := scopeRows(ctx, q, "SELECT id,generation,state FROM jobs WHERE type=? AND state IN('queued','running') ORDER BY id", model.EnhancementJobType(feature))
	if e != nil {
		return 0, "", e
	}
	defer rows.Close()
	hash := sha256.New()
	n := 0
	for rows.Next() {
		var id, state string
		var generation int64
		if e = rows.Scan(&id, &generation, &state); e != nil {
			return 0, "", e
		}
		fmt.Fprintf(hash, "%d:%s:%d:%s\n", len(id), id, generation, state)
		n++
	}
	return n, fmt.Sprintf("%x", hash.Sum(nil)), rows.Err()
}
func transitionScan(r scanner) (model.EnhancementTransition, error) {
	var t model.EnhancementTransition
	e := r.Scan(&t.ID, &t.Feature, &t.Attempt, &t.State, &t.TargetEnabled, &t.Error, &t.AffectedTasks, &t.CreatedAt, &t.UpdatedAt)
	return t, e
}

const transitionCols = "id,feature,attempt,state,target_enabled,error_code,affected_tasks,created_at,updated_at"

func (s *Store) Enhancements(ctx context.Context) (model.Enhancements, error) {
	out := model.Enhancements{Features: map[string]model.EnhancementFeature{}}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = scopeRow(ctx, tx, "SELECT revision FROM enhancement_revision WHERE singleton=1").Scan(&out.Revision); e != nil {
		return out, e
	}
	for _, feature := range []string{model.EnhancementWork, model.EnhancementSeries, model.EnhancementDictionary} {
		var v model.EnhancementFeature
		var id sql.NullString
		if e = scopeRow(ctx, tx, "SELECT enabled,state,transition_id FROM enhancement_settings WHERE feature=?", feature).Scan(&v.Enabled, &v.State, &id); e != nil {
			return out, e
		}
		v.EffectiveEnabled = v.Enabled && v.State == "stable"
		if v.AffectedTasks, v.ImpactToken, e = enhancementImpact(ctx, tx, feature); e != nil {
			return out, e
		}
		if id.Valid {
			t, err := transitionScan(scopeRow(ctx, tx, "SELECT "+transitionCols+" FROM enhancement_transitions WHERE id=?", id.String))
			if err != nil {
				return out, err
			}
			v.Transition = &t
		}
		out.Features[feature] = v
	}

	var galleryTable int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM sqlite_master WHERE name='gallery_highlight_settings'").Scan(&galleryTable); e != nil {
		return out, e
	}
	if galleryTable > 0 {
		var enabled bool
		if e = scopeRow(ctx, tx, "SELECT enabled FROM gallery_highlight_settings WHERE singleton=1").Scan(&enabled); e != nil {
			return out, e
		}
		out.Features[model.EnhancementGallery] = model.EnhancementFeature{Enabled: enabled, EffectiveEnabled: enabled, State: "stable", ResourceState: "ready"}
	}
	return out, tx.Commit()
}
func enhancementEvent(ctx context.Context, tx *sql.Tx) error {
	result, e := tx.ExecContext(ctx, "UPDATE enhancement_revision SET revision=revision+1 WHERE singleton=1 AND revision<9223372036854775807")
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return model.ErrConflict
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO events(type,resource_id,revision,created_at) SELECT 'enhancements','global',revision,unixepoch() FROM enhancement_revision WHERE singleton=1")
	return e
}
func (s *Store) ChangeEnhancement(ctx context.Context, in model.EnhancementPatch) (*model.EnhancementTransition, error) {
	if (model.EnhancementJobType(in.Feature) == "" && in.Feature != model.EnhancementGallery) || in.Expected < 1 || in.Expected == math.MaxInt64 || (in.RetryTransitionID == "" && in.Enabled == nil) || (in.RetryTransitionID != "" && (in.Enabled != nil || in.ConfirmPause || in.ImpactToken != "")) {
		return nil, model.ErrInput
	}
	if in.Feature == model.EnhancementGallery {
		s.galleryPublish.Lock()
		defer s.galleryPublish.Unlock()
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var revision int64
	if e = scopeRow(ctx, tx, "SELECT revision FROM enhancement_revision WHERE singleton=1").Scan(&revision); e != nil {
		return nil, e
	}
	if revision != in.Expected {
		return nil, model.ErrConflict
	}

	if in.Feature == model.EnhancementGallery {
		if in.RetryTransitionID != "" || in.ConfirmPause || in.ImpactToken != "" {
			return nil, model.ErrInput
		}
		var enabled bool
		if e = scopeRow(ctx, tx, "SELECT enabled FROM gallery_highlight_settings WHERE singleton=1").Scan(&enabled); e != nil {
			return nil, e
		}
		if enabled == *in.Enabled {
			return nil, tx.Commit()
		}
		r, err := tx.ExecContext(ctx, "UPDATE gallery_highlight_settings SET enabled=?,revision=revision+1 WHERE singleton=1 AND revision<9223372036854775807", *in.Enabled)
		if err != nil {
			return nil, err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n != 1 {
			return nil, model.ErrConflict
		}
		if e = enhancementEvent(ctx, tx); e != nil {
			return nil, e
		}
		return nil, tx.Commit()
	}
	var enabled bool
	var state string
	var id sql.NullString
	var attempt int64
	if e = scopeRow(ctx, tx, "SELECT enabled,state,transition_id,attempt FROM enhancement_settings WHERE feature=?", in.Feature).Scan(&enabled, &state, &id, &attempt); e != nil {
		return nil, e
	}
	if in.RetryTransitionID != "" {
		if state != "failed" || !id.Valid || id.String != in.RetryTransitionID {
			return nil, model.ErrEnhancementTransition
		}
		if attempt == math.MaxInt64 {
			return nil, model.ErrInput
		}
		attempt++
		if _, e = tx.ExecContext(ctx, "UPDATE enhancement_settings SET state=(SELECT CASE WHEN target_enabled=1 THEN 'enabling' ELSE 'disabling' END FROM enhancement_transitions WHERE id=?),attempt=? WHERE feature=?", id.String, attempt, in.Feature); e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE enhancement_transitions SET state='pending',error_code='',attempt=?,updated_at=unixepoch() WHERE id=?", attempt, id.String); e != nil {
			return nil, e
		}
		if s.Version >= 25 {
			if a, ok := model.Actor(ctx); ok {
				if _, e = tx.ExecContext(ctx, "UPDATE enhancement_transitions SET actor_id=?,actor_acl_revision=(SELECT acl_revision FROM account_profiles WHERE account_id=?) WHERE id=?", a.ID, a.ID, id.String); e != nil {
					return nil, e
				}
			}
		}
	} else {
		if state != "stable" {
			return nil, model.ErrEnhancementTransition
		}
		if enabled == *in.Enabled {
			return nil, tx.Commit()
		}
		if *in.Enabled && in.Feature != model.EnhancementDictionary {
			if _, e = tx.ExecContext(ctx, "UPDATE enhancement_settings SET enabled=1,transition_id=NULL WHERE feature=?", in.Feature); e != nil {
				return nil, e
			}
			if e = enhancementEvent(ctx, tx); e != nil {
				return nil, e
			}
			return nil, tx.Commit()
		}
		count, token, err := enhancementImpact(ctx, tx, in.Feature)
		if err != nil {
			return nil, err
		}
		if !*in.Enabled && (count > 0 && (!in.ConfirmPause || in.ImpactToken != token) || in.ConfirmPause && in.ImpactToken != token) {
			return nil, model.ErrEnhancementImpact
		}
		tid, err := model.NewID()
		if err != nil {
			return nil, err
		}
		id = sql.NullString{String: tid, Valid: true}
		attempt = 1
		if _, e = tx.ExecContext(ctx, "INSERT INTO enhancement_transitions(id,feature,attempt,state,target_enabled,error_code,affected_tasks,created_at,updated_at) VALUES(?,?,1,'pending',?,'',?,unixepoch(),unixepoch())", tid, in.Feature, *in.Enabled, count); e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE enhancement_settings SET enabled=?,runtime_armed=0,state=?,transition_id=?,attempt=1 WHERE feature=?", *in.Enabled, map[bool]string{true: "enabling", false: "disabling"}[*in.Enabled], tid, in.Feature); e != nil {
			return nil, e
		}
		if !*in.Enabled {
			if _, e = tx.ExecContext(ctx, "UPDATE jobs SET state='paused',generation=generation+1,error_code='enhancement_disabled',updated_at=unixepoch() WHERE type=? AND state IN('queued','running')", model.EnhancementJobType(in.Feature)); e != nil {
				return nil, e
			}
		}
	}
	if in.RetryTransitionID == "" && in.Enabled != nil && !*in.Enabled {
		stage := ""
		if in.Feature == model.EnhancementWork {
			stage = "versions"
		}
		if in.Feature == model.EnhancementSeries {
			stage = "series"
		}
		if stage != "" {
			if _, e = tx.ExecContext(ctx, "UPDATE import_batches SET state='paused',error_code='enhancement_disabled',revision=revision+1,updated_at=unixepoch() WHERE stage=? AND state IN('processing','review')", stage); e != nil {
				return nil, e
			}
		}
	}

	if e = enhancementEvent(ctx, tx); e != nil {
		return nil, e
	}
	t, e := transitionScan(scopeRow(ctx, tx, "SELECT "+transitionCols+" FROM enhancement_transitions WHERE id=?", id.String))
	if e != nil {
		return nil, e
	}
	return &t, tx.Commit()
}
func (s *Store) PendingEnhancements(ctx context.Context) ([]model.EnhancementTransition, error) {
	rows, e := scopeRows(ctx, s.Read, "SELECT "+transitionCols+" FROM enhancement_transitions WHERE state='pending' ORDER BY created_at,id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.EnhancementTransition{}
	for rows.Next() {
		t, e := transitionScan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) FinishEnhancement(ctx context.Context, t model.EnhancementTransition, code string) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	state := "completed"
	featureState := "stable"
	if code != "" {
		state = "failed"
		featureState = "failed"
	}
	r, e := tx.ExecContext(ctx, "UPDATE enhancement_transitions SET state=?,error_code=?,updated_at=unixepoch() WHERE id=? AND attempt=? AND state='pending' AND EXISTS(SELECT 1 FROM enhancement_settings WHERE feature=? AND transition_id=? AND attempt=?)", state, code, t.ID, t.Attempt, t.Feature, t.ID, t.Attempt)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return model.ErrEnhancementTransition
	}
	if _, e = tx.ExecContext(ctx, "UPDATE enhancement_settings SET state=? WHERE feature=? AND transition_id=? AND attempt=?", featureState, t.Feature, t.ID, t.Attempt); e != nil {
		return e
	}
	if e = enhancementEvent(ctx, tx); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) ServiceDatabase(ctx context.Context) (int, string, error) {
	var baseline string
	e := scopeRow(ctx, s.Read, "SELECT baseline FROM application_identity WHERE singleton=1").Scan(&baseline)
	return s.Version, baseline, e
}

func armEnhancement(ctx context.Context, tx *sql.Tx, feature string) error {
	if feature != model.EnhancementWork && feature != model.EnhancementSeries {
		return nil
	}
	var exists int
	if e := scopeRow(ctx, tx, "SELECT count(*) FROM sqlite_master WHERE name='enhancement_settings'").Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		return nil
	}
	_, e := tx.ExecContext(ctx, "UPDATE enhancement_settings SET runtime_armed=1 WHERE feature=?", feature)
	return e
}
func (s *Store) EnhancementBackgroundAllowed(ctx context.Context, feature string) error {
	if e := enhancementGate(ctx, s.Read, feature); e != nil {
		return e
	}
	var exists int
	if e := scopeRow(ctx, s.Read, "SELECT count(*) FROM sqlite_master WHERE name='enhancement_settings'").Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		return nil
	}
	var armed bool
	if e := scopeRow(ctx, s.Read, "SELECT runtime_armed FROM enhancement_settings WHERE feature=?", feature).Scan(&armed); e != nil {
		return e
	}
	if !armed {
		return model.ErrEnhancementDisabled
	}
	return nil
}
func backgroundEnhancementGate(ctx context.Context, q queryer, feature string) error {
	if e := enhancementGate(ctx, q, feature); e != nil {
		return e
	}
	var exists int
	if e := scopeRow(ctx, q, "SELECT count(*) FROM sqlite_master WHERE name='enhancement_settings'").Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		return nil
	}
	var armed bool
	if e := scopeRow(ctx, q, "SELECT runtime_armed FROM enhancement_settings WHERE feature=?", feature).Scan(&armed); e != nil {
		return e
	}
	if !armed {
		return model.ErrEnhancementDisabled
	}
	return nil
}
