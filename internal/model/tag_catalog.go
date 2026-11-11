package model

import "errors"

var ErrTagProjection = errors.New("tag_projection_unavailable")

// TagCatalogInput always describes one explicit library. Relation and edition
// lists share the same snapshot identity and use stable tag/edition tie breakers.
type TagCatalogInput struct {
	FollowedOnly  bool   `json:"followed_only"`
	View          string `json:"view,omitempty"`
	LibraryID     string `json:"library_id"`
	IncludeUnused bool   `json:"include_unused"`
	Category      string `json:"category"`
	Namespace     string `json:"namespace"`
	Query         string `json:"q"`
	Sort          string `json:"sort"`
	Order         string `json:"order"`
	Page          int    `json:"page"`
	PageSize      int    `json:"page_size"`
	Snapshot      string `json:"snapshot"`
	RelatedTagID  string `json:"related_tag_id"`
	Partition     string `json:"partition"`
}
type CatalogTag struct {
	Followed            bool       `json:"followed"`
	FollowRevision      int64      `json:"follow_revision"`
	Images              *TagImages `json:"images,omitempty"`
	ID                  string     `json:"id"`
	Namespace           string     `json:"namespace"`
	Name                string     `json:"name"`
	Translation         string     `json:"translation"`
	TranslationOrigin   string     `json:"translation_origin"`
	TranslationRevision int64      `json:"translation_revision"`
	Category            string     `json:"category"`
	Placeholder         bool       `json:"placeholder"`
	EditionCount        int        `json:"edition_count"`
	WorkCount           int        `json:"work_count"`
}
type TagCatalogPage struct {
	Items    []CatalogTag `json:"items"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
	Snapshot string       `json:"snapshot"`
}
type TagCatalogDetail struct {
	CatalogTag
	LibraryID                string `json:"library_id"`
	LibraryDisabled          bool   `json:"library_disabled"`
	UnclassifiedEditionCount int    `json:"unclassified_edition_count"`
	Snapshot                 string `json:"snapshot"`
}
type CharacterRelation struct {
	Parody                 CatalogTag `json:"parody"`
	Character              CatalogTag `json:"character"`
	Decision               string     `json:"decision"`
	Revision               int64      `json:"revision"`
	State                  string     `json:"state"`
	StateLabel             string     `json:"state_label"`
	EvidenceEditionCount   int        `json:"evidence_edition_count"`
	SeedEditionCount       int        `json:"seed_edition_count"`
	EditionCount           int        `json:"edition_count"`
	WorkCount              int        `json:"work_count"`
	InferredEditionCount   int        `json:"inferred_edition_count"`
	SuppressedEditionCount int        `json:"suppressed_edition_count"`
	CompetingEditionCount  int        `json:"competing_edition_count"`
}
type TagRelationPage struct {
	Items                    []CharacterRelation `json:"items"`
	Total                    int                 `json:"total"`
	Page                     int                 `json:"page"`
	PageSize                 int                 `json:"page_size"`
	UnclassifiedEditionCount int                 `json:"unclassified_edition_count"`
	Snapshot                 string              `json:"snapshot"`
}
type EditionCharacterRelation struct {
	ParodyName           string `json:"parody_name"`
	ParodyTranslation    string `json:"parody_translation"`
	CharacterName        string `json:"character_name"`
	CharacterTranslation string `json:"character_translation"`
	ParodyID             string `json:"parody_id"`
	CharacterID          string `json:"character_id"`
	State                string `json:"state"`
	StateLabel           string `json:"state_label"`
	Decision             string `json:"decision"`
	DecisionRevision     int64  `json:"decision_revision"`
	Suppressed           bool   `json:"suppressed"`
	SuppressionRevision  int64  `json:"suppression_revision"`
}
type TagCatalogEdition struct {
	TitleOriginal        string                     `json:"title_original"`
	ID                   string                     `json:"id"`
	LibraryID            string                     `json:"library_id"`
	WorkID               string                     `json:"work_id"`
	Title                string                     `json:"title"`
	FileState            string                     `json:"file_state"`
	Revision             int64                      `json:"revision"`
	OrganizationRevision int64                      `json:"organization_revision"`
	Relations            []EditionCharacterRelation `json:"relations"`
}
type TagEditionPage struct {
	Items     []TagCatalogEdition `json:"items"`
	Total     int                 `json:"total"`
	WorkCount int                 `json:"work_count"`
	Page      int                 `json:"page"`
	PageSize  int                 `json:"page_size"`
	Snapshot  string              `json:"snapshot"`
}
type TagRelationWrite struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Decision         string `json:"decision,omitempty"`
}
type TagRelationDecision struct {
	ParodyID    string `json:"parody_id"`
	CharacterID string `json:"character_id"`
	Decision    string `json:"decision"`
	Revision    int64  `json:"revision"`
	Scope       string `json:"scope"`
}
type CharacterSuppression struct {
	EditionID   string `json:"edition_id"`
	ParodyID    string `json:"parody_id"`
	CharacterID string `json:"character_id"`
	Suppressed  bool   `json:"suppressed"`
	Revision    int64  `json:"revision"`
	Scope       string `json:"scope"`
}
