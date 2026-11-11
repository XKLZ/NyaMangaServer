package provider

import (
	"bytes"
	"encoding/json"
	"nyamangaserver.local/server/internal/model"
	"strconv"
)

// ParseRemoteFileInfo is strictly offline. It accepts exact EH API integers,
// verifies the source gid, and never substitutes local archive observations.
func ParseRemoteFileInfo(raw json.RawMessage, expectedGID string) (*int64, *int64, error) {
	identity, e := strconv.ParseUint(expectedGID, 10, 64)
	if e != nil || identity == 0 || strconv.FormatUint(identity, 10) != expectedGID {
		return nil, nil, model.ErrInput
	}
	var item struct {
		GID   json.RawMessage `json:"gid"`
		Pages json.RawMessage `json:"filecount"`
		Size  json.RawMessage `json:"filesize"`
	}
	if len(raw) > 4<<20 || json.Unmarshal(raw, &item) != nil || textNumber(item.GID) != expectedGID {
		return nil, nil, model.ErrInput
	}
	parse := func(v json.RawMessage) (*int64, error) {
		if len(v) == 0 || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil, nil
		}
		text := textNumber(v)
		if text == "" {
			return nil, model.ErrInput
		}
		for _, c := range text {
			if c < '0' || c > '9' {
				return nil, model.ErrInput
			}
		}
		n, e := strconv.ParseInt(text, 10, 64)
		if e != nil {
			return nil, model.ErrInput
		}
		return &n, nil
	}
	pages, e := parse(item.Pages)
	if e != nil {
		return nil, nil, e
	}
	size, e := parse(item.Size)
	return pages, size, e
}
