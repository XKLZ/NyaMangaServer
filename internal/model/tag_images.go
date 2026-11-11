package model

import "errors"

var ErrTagImageStorage = errors.New("tag_image_storage_unavailable")
var ErrTagImageType = errors.New("invalid_tag_image_type")

// Image revisions are independent per role and survive removal. Asset is internal
// IO data; HTTP never exposes storage keys or the original upload.
type TagImage struct {
	Role              string  `json:"role"`
	Revision          int64   `json:"revision"`
	AssetID           *string `json:"asset_id"`
	Width             int     `json:"width"`
	Height            int     `json:"height"`
	Format            string  `json:"format"`
	ProcessingVersion int     `json:"processing_version"`
	Asset             *Asset  `json:"-"`
}
type TagImages struct {
	TagID      string   `json:"tag_id"`
	Foreground TagImage `json:"foreground"`
	Background TagImage `json:"background"`
}

func EmptyTagImages(id string) TagImages {
	return TagImages{TagID: id, Foreground: TagImage{Role: "foreground", ProcessingVersion: 1}, Background: TagImage{Role: "background", ProcessingVersion: 1}}
}
func TagImageRole(role string) bool { return role == "foreground" || role == "background" }
