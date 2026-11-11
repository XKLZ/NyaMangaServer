package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
	"slices"
)

// One bounded metadata query plus one label query for the entire page, never N+1.
func enrichVersionMembers(ctx context.Context, tx *sql.Tx, page *model.WorkEditionPage) error {
	if len(page.Items) == 0 || !automaticLabelsAvailable(ctx, tx) {
		return nil
	}
	ids := []string{}
	at := map[string]int{}
	for i, m := range page.Items {
		ids = append(ids, m.ID)
		at[m.ID] = i
		page.Items[i].LabelState = &model.VersionLabelState{Mode: "legacy", Revision: 0, Items: []model.VersionLabel{}}
	}
	rows, e := scopeRows(ctx, tx, `SELECT e.id,COALESCE(eb.fields_json,'{}'),COALESCE(p.properties_json,'{}'),COALESCE(cs.revision,0),e.id=w.preferred_edition_id,COALESCE(lp.mode,'auto'),COALESCE(lp.revision,0),COALESCE(lp.rule_version,''),COALESCE(lp.needs_review,0) FROM editions e JOIN works w ON w.id=e.work_id LEFT JOIN effective_editions eb ON eb.edition_id=e.id LEFT JOIN effective_edition_properties p ON p.edition_id=e.id LEFT JOIN cover_selections cs ON cs.edition_id=e.id LEFT JOIN edition_label_policy lp ON lp.edition_id=e.id WHERE e.id IN(`+placeholders(len(ids))+`)`, stringArgs(ids)...)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id, raw, props string
		var cover, revision int64
		var preferred, review bool
		var mode, rule string
		if e = rows.Scan(&id, &raw, &props, &cover, &preferred, &mode, &revision, &rule, &review); e != nil {
			rows.Close()
			return e
		}
		m := &page.Items[at[id]]
		var fields model.Metadata
		var p model.BookProperties
		if e = json.Unmarshal([]byte(raw), &fields); e != nil {
			rows.Close()
			return e
		}
		// InferProperties shares effective/automatic RawMessage storage. Do not
		// overlay persisted JSON onto it: decoding automatic.value could mutate
		// the effective value through that alias (e.g. "color" -> nullor").
		if props == "{}" {
			p = model.InferProperties(fields, nil, nil)
		} else if e = json.Unmarshal([]byte(props), &p); e != nil {
			rows.Close()
			return e
		}
		if fields.TitleOriginal != nil {
			m.TitleOriginal = *fields.TitleOriginal
		}
		m.Languages = model.Languages(p.Languages)
		m.Tags = fields.Tags
		m.Properties = &p
		m.CoverRevision = cover
		m.Preferred = preferred
		m.LabelState = &model.VersionLabelState{Mode: mode, Revision: revision, RuleVersion: rule, NeedsReview: review, Items: []model.VersionLabel{}}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	rows, e = scopeRows(ctx, tx, `SELECT edition_id,label,origin,preset_id FROM (SELECT edition_id,label,'manual' AS origin,'' AS preset_id,position FROM edition_version_labels WHERE edition_id IN(`+placeholders(len(ids))+`) UNION ALL SELECT edition_id,label,'automatic',preset_id,position FROM edition_automatic_labels WHERE edition_id IN(`+placeholders(len(ids))+`)) ORDER BY edition_id,origin DESC,position`, append(stringArgs(ids), stringArgs(ids)...)...)
	if e != nil {
		return e
	}
	for rows.Next() {
		var id string
		var l model.VersionLabel
		if e = rows.Scan(&id, &l.Label, &l.Origin, &l.PresetID); e != nil {
			rows.Close()
			return e
		}
		m := &page.Items[at[id]]
		m.LabelState.Items = append(m.LabelState.Items, l)
		if !slices.Contains(m.Labels, l.Label) {
			m.Labels = append(m.Labels, l.Label)
		}
	}
	e = rows.Err()
	rows.Close()
	return e
}

func automaticLabelsAvailable(ctx context.Context, q queryer) bool {
	var n int
	return scopeRow(ctx, q, "SELECT count(*) FROM sqlite_master WHERE name='edition_label_policy'").Scan(&n) == nil && n == 1
}
func manualVersionLabels(ctx context.Context, tx *sql.Tx, id string, labels []string) error {
	if !automaticLabelsAvailable(ctx, tx) {
		return nil
	}
	if _, e := tx.ExecContext(ctx, "DELETE FROM edition_automatic_labels WHERE edition_id=?", id); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, "DELETE FROM edition_version_labels WHERE edition_id=?", id); e != nil {
		return e
	}
	for i, l := range labels {
		if _, e := tx.ExecContext(ctx, "INSERT INTO edition_version_labels VALUES(?,?,?)", id, i, l); e != nil {
			return e
		}
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO edition_label_policy(edition_id,mode) VALUES(?,'manual') ON CONFLICT(edition_id) DO UPDATE SET mode='manual',revision=revision+1,needs_review=0`, id)
	return e
}

func refreshWorkLabels(ctx context.Context, tx *sql.Tx, work string, activate bool) error {
	if !automaticLabelsAvailable(ctx, tx) {
		return nil
	}
	page := model.WorkEditionPage{Items: []model.WorkEdition{}, Expected: map[string]int64{}}
	rows, e := scopeRows(ctx, tx, "SELECT "+workEditionColumns+" FROM "+workEditionSource+" WHERE e.work_id=? ORDER BY e.id", "", work)
	if e != nil {
		return e
	}
	for rows.Next() {
		m, err := scanWorkEdition(rows)
		if err != nil {
			rows.Close()
			return err
		}
		page.Items = append(page.Items, m)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if e = enrichVersionMembers(ctx, tx, &page); e != nil {
		return e
	}
	members := []rec.ProposalMember{}
	for _, m := range page.Items {
		members = append(members, rec.ProposalMember{Tags: m.Tags, ID: m.ID, Title: m.Title, TitleOriginal: m.TitleOriginal, Languages: m.Languages, Properties: rec.CompactProperties(rec.InputEdition{DisplayProperties: m.Properties})})
	}
	rec.SuggestPresets(members)
	changed := false
	for i, m := range page.Items {
		policy := m.LabelState
		if policy.Mode == "manual" || (policy.Mode == "legacy" && !activate) {
			continue
		}
		presets := members[i].Suggested.Presets
		if len(page.Items) < 2 {
			presets = nil
		}
		// Unknown evidence is a no-op, including old automatic labels.
		if len(page.Items) > 1 && len(presets) == 0 {
			continue
		}
		previous := []rec.Preset{}
		for _, l := range policy.Items {
			if l.Origin == "automatic" {
				previous = append(previous, rec.Preset{ID: l.PresetID, Label: l.Label})
			}
		}
		// Compare identities, not query row order.
		equal := len(previous) == len(presets)
		for _, p := range presets {
			if !slices.Contains(previous, p) {
				equal = false
			}
		}
		review := len(page.Items) > 1 && members[i].Suggested.NeedsReview
		if equal && policy.Mode == "auto" && policy.RuleVersion == rec.PresetRuleVersion && policy.NeedsReview == review {
			continue
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM edition_automatic_labels WHERE edition_id=?", m.ID); e != nil {
			return e
		}
		for pos, p := range presets {
			if _, e = tx.ExecContext(ctx, "INSERT INTO edition_automatic_labels VALUES(?,?,?,?)", m.ID, pos, p.ID, p.Label); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO edition_label_policy(edition_id,mode,rule_version,needs_review) VALUES(?,'auto',?,?) ON CONFLICT(edition_id) DO UPDATE SET mode='auto',rule_version=excluded.rule_version,needs_review=excluded.needs_review,revision=revision+1`, m.ID, rec.PresetRuleVersion, review); e != nil {
			return e
		}
		changed = true
	}
	if changed {
		_, e = tx.ExecContext(ctx, "INSERT INTO events(type,resource_id,revision,created_at) SELECT 'work',id,revision,unixepoch() FROM works WHERE id=?", work)
	}
	return e
}

// Explicit generation enables legacy empty labels. Resetting manual labels requires
// an explicitly selected Edition; it never deletes custom strings.
func (s *Store) GenerateVersionLabels(ctx context.Context, account, work string, expected map[string]int64, restore []string) (model.WorkEditionPage, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return model.WorkEditionPage{}, e
	}
	defer tx.Rollback()
	c, e := loadWorkContext(ctx, tx, account, nil, []string{work}, nil, true)
	if e != nil {
		return model.WorkEditionPage{}, e
	}
	for key, want := range c.Expected {
		if want != expected[key] {
			return model.WorkEditionPage{}, model.ErrConflict
		}
	}
	for _, id := range restore {
		if !slices.ContainsFunc(c.Editions, func(m model.WorkEdition) bool { return m.ID == id }) {
			return model.WorkEditionPage{}, model.ErrInput
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO edition_label_policy(edition_id,mode) VALUES(?,'auto') ON CONFLICT(edition_id) DO UPDATE SET mode='auto',revision=revision+1`, id); e != nil {
			return model.WorkEditionPage{}, e
		}
	}
	if e = refreshWorkLabels(ctx, tx, work, true); e != nil {
		return model.WorkEditionPage{}, e
	}
	if e = tx.Commit(); e != nil {
		return model.WorkEditionPage{}, e
	}
	return s.WorkEditionPage(ctx, account, work, "", 1, 100)
}

func refreshEditionWorkLabels(ctx context.Context, tx *sql.Tx, id string) error {
	if !automaticLabelsAvailable(ctx, tx) {
		return nil
	}
	var work string
	e := scopeRow(ctx, tx, "SELECT work_id FROM editions WHERE id=?", id).Scan(&work)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	return refreshWorkLabels(ctx, tx, work, false)
}

// Only presentation suggestions are refreshed. Frozen input fingerprints,
// expected revisions, identities and organization commands remain untouched.
func refreshProposalLabelViews(ctx context.Context, tx *sql.Tx, plans []rec.Proposal) error {
	page := model.WorkEditionPage{Items: []model.WorkEdition{}}
	seen := map[string]bool{}
	for _, p := range plans {
		if p.State != "pending" {
			continue
		}
		for _, m := range p.Members {
			if !seen[m.ID] {
				seen[m.ID] = true
				page.Items = append(page.Items, model.WorkEdition{ID: m.ID, Title: m.Title})
			}
		}
	}
	if err := enrichVersionMembers(ctx, tx, &page); err != nil {
		return err
	}
	by := map[string]model.WorkEdition{}
	for _, m := range page.Items {
		by[m.ID] = m
	}
	for i := range plans {
		if plans[i].State != "pending" {
			continue
		}
		for j := range plans[i].Members {
			m := &plans[i].Members[j]
			current := by[m.ID]
			m.Tags = current.Tags
			m.Languages = current.Languages
			m.Properties = rec.CompactProperties(rec.InputEdition{DisplayProperties: current.Properties})
		}
		rec.SuggestPresets(plans[i].Members)
	}
	return nil
}
