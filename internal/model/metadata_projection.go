package model

import "encoding/json"

// ProjectMetadata is shared by saved projections and read-only import previews.
// Overrides stay independent: a candidate never deletes an existing manual value.
func ProjectMetadata(source Metadata, overrides map[string]Override) (Metadata, error) {
	raw, err := json.Marshal(source)
	if err != nil {
		return Metadata{}, err
	}
	fields := map[string]json.RawMessage{}
	if err = json.Unmarshal(raw, &fields); err != nil {
		return Metadata{}, err
	}
	for key, v := range overrides {
		if v.Op == "inherit" {
			continue
		}
		value := v.Value
		if v.Op == "empty" {
			value = json.RawMessage(`null`)
			if key == "tags" {
				value = json.RawMessage(`[]`)
			}
		}
		fields[key] = value
	}
	raw, err = json.Marshal(fields)
	if err != nil {
		return Metadata{}, err
	}
	var result Metadata
	err = json.Unmarshal(raw, &result)
	if result.Tags == nil {
		result.Tags = []TagName{}
	}
	return result, err
}

// MatchingMetadataJSON preserves the pre-026 projection byte shape. Remote file
// measurements belong to source display, never search/recommendation input.
func MatchingMetadataJSON(v Metadata) ([]byte, error) {
	return json.Marshal(struct {
		Metadata
		RemotePageCount *int64 `json:"remote_page_count,omitempty"`
		RemoteSizeBytes *int64 `json:"remote_size_bytes,omitempty"`
	}{Metadata: v})
}
