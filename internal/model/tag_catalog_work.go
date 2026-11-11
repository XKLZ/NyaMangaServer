package model

// TagCatalogWork is a Work card, not combined Edition metadata or a relationship
// diagnosis. Its cover representative can differ from the inherited-name source.
type TagCatalogWork struct {
	TitleOriginal string           `json:"title_original"`
	ID            string           `json:"id"`
	LibraryID     string           `json:"library_id"`
	Name          string           `json:"name"`
	Revision      int64            `json:"revision"`
	Series        *TagWorkSeries   `json:"series"`
	Cover         WorkCoverSummary `json:"cover"`
}
type TagWorkSeries struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
	Revision int64  `json:"revision"`
}
type TagWorkPage struct {
	Items    []TagCatalogWork `json:"items"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	Snapshot string           `json:"snapshot"`
}
