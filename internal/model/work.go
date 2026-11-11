package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

var ErrPreferredRequired = errors.New("preferred_edition_required")
var ErrAliasFields = errors.New("conflicting_alias_fields")

type VersionLabel struct {
	Label    string `json:"label"`
	Origin   string `json:"origin"`
	PresetID string `json:"preset_id,omitempty"`
}
type VersionLabelState struct {
	Mode        string         `json:"mode"`
	Revision    int64          `json:"revision"`
	RuleVersion string         `json:"rule_version"`
	NeedsReview bool           `json:"needs_review"`
	Items       []VersionLabel `json:"items"`
}
type WorkEdition struct {
	Tags            []TagName          `json:"-"`
	TitleOriginal   string             `json:"title_original,omitempty"`
	Languages       []string           `json:"languages"`
	CoverRevision   int64              `json:"cover_revision"`
	Preferred       bool               `json:"preferred"`
	LabelState      *VersionLabelState `json:"label_state,omitempty"`
	Properties      *BookProperties    `json:"properties,omitempty"`
	ID              string             `json:"id"`
	WorkID          string             `json:"work_id"`
	LibraryID       string             `json:"library_id"`
	Title           string             `json:"title"`
	ContentRevision int64              `json:"content_revision"`
	Revision        int64              `json:"organization_revision"`
	Labels          []string           `json:"version_labels"`
	State           UserState          `json:"user_state"`
}
type WorkContext struct {
	Works    []Work           `json:"works"`
	Editions []WorkEdition    `json:"editions"`
	Series   []Series         `json:"series"`
	Expected map[string]int64 `json:"expected_revisions"`
	Catalog  string           `json:"snapshot_revision"`
}
type WorkCommand struct {
	WorkIDsSet                  bool              `json:"-"`
	LegacyItemIDsSet            bool              `json:"-"`
	Action                      string            `json:"action"`
	ID                          string            `json:"id,omitempty"`
	LibraryID                   string            `json:"library_id,omitempty"`
	TargetWorkID                string            `json:"target_work_id,omitempty"`
	TargetSeriesID              string            `json:"target_series_id,omitempty"`
	NewWorkID                   string            `json:"new_work_id,omitempty"`
	NewWorkIDs                  map[string]string `json:"new_work_ids,omitempty"`
	EditionID                   string            `json:"edition_id,omitempty"`
	EditionIDs                  []string          `json:"edition_ids,omitempty"`
	WorkIDs                     []string          `json:"work_ids"`
	LegacyItemIDs               []string          `json:"item_ids,omitempty"`
	PreferredEditionID          string            `json:"preferred_edition_id,omitempty"`
	RemainingPreferredEditionID string            `json:"remaining_preferred_edition_id,omitempty"`
	KeepSeries                  bool              `json:"keep_series,omitempty"`
	Position                    int               `json:"position,omitempty"`
	Name                        string            `json:"name,omitempty"`
	NameOp                      string            `json:"name_op,omitempty"`
	Labels                      []string          `json:"version_labels"`
	Value                       *bool             `json:"value,omitempty"`
	Expected                    map[string]int64  `json:"expected_revisions"`
	Snapshot                    string            `json:"snapshot_revision,omitempty"`
}
type WorkEffect struct {
	EditionID    string `json:"edition_id,omitempty"`
	WorkID       string `json:"work_id,omitempty"`
	FromWorkID   string `json:"from_work_id,omitempty"`
	ToWorkID     string `json:"to_work_id,omitempty"`
	FromSeriesID string `json:"from_series_id,omitempty"`
	ToSeriesID   string `json:"to_series_id,omitempty"`
	RemovedWork  bool   `json:"removed_work,omitempty"`
}
type WorkResult struct {
	Context WorkContext  `json:"context"`
	Command WorkCommand  `json:"command"`
	Effects []WorkEffect `json:"effects"`
	ID      string       `json:"id"`
	Preview bool         `json:"preview"`
}
type WorkList struct {
	Items    []Work `json:"items"`
	Total    int    `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}
type SeriesList struct {
	Items    []Series `json:"items"`
	Total    int      `json:"total"`
	Page     int      `json:"page"`
	PageSize int      `json:"page_size"`
}

type WorkEditionPage struct {
	Items    []WorkEdition    `json:"items"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	Expected map[string]int64 `json:"expected_revisions"`
}
type SeriesWorkPage struct {
	Items    []Work           `json:"items"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	Expected map[string]int64 `json:"expected_revisions"`
}

// Preserve field presence for the legacy alias without weakening strict decoding.
func (c *WorkCommand) UnmarshalJSON(raw []byte) error {
	type plain WorkCommand
	var v plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&v); e != nil {
		return e
	}
	if e := decoder.Decode(new(any)); e != io.EOF {
		return errors.New("trailing command data")
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(raw, &fields); e != nil {
		return e
	}
	_, v.WorkIDsSet = fields["work_ids"]
	_, v.LegacyItemIDsSet = fields["item_ids"]
	*c = WorkCommand(v)
	return nil
}
