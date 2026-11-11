package model

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"
)

type TagSuggestionInput struct {
	Namespace string    `json:"namespace"`
	Query     string    `json:"q"`
	Exclude   []TagName `json:"exclude"`
	Page      int       `json:"page"`
	PageSize  int       `json:"page_size"`
	Snapshot  string    `json:"snapshot_revision,omitempty"`
}
type TagSuggestion struct {
	Followed       bool  `json:"followed"`
	FollowRevision int64 `json:"follow_revision"`
	TagName
	Translation     string  `json:"translation"`
	Origin          string  `json:"translation_origin"`
	Usage           int     `json:"usage_count"`
	LocalExists     bool    `json:"local_exists"`
	TagID           *string `json:"tag_id"`
	DictionaryMatch *bool   `json:"dictionary_match"`
}
type TagSuggestions struct {
	Items           []TagSuggestion `json:"items"`
	Total           int             `json:"total"`
	Page            int             `json:"page"`
	PageSize        int             `json:"page_size"`
	Snapshot        string          `json:"snapshot_revision"`
	DictionaryState string          `json:"dictionary_state"`
}

func ValidateTagSuggestions(v TagSuggestionInput) (TagSuggestionInput, error) {
	v.Namespace = strings.ToLower(strings.TrimSpace(v.Namespace))
	if v.Page == 0 {
		v.Page = 1
	}
	if v.PageSize == 0 {
		v.PageSize = 50
	}
	if v.Page < 1 || v.Page > 10000000 || v.PageSize < 1 || v.PageSize > 100 || len(v.Query) > 512 || !utf8.ValidString(v.Query) || strings.ContainsRune(v.Query, 0) || len(v.Exclude) > 200 || v.Page > 1 && v.Snapshot == "" || v.Namespace != "" && !namespacePattern.MatchString(v.Namespace) {
		return v, ErrInput
	}
	seen := map[TagName]bool{}
	out := []TagName{}
	for _, tag := range v.Exclude {
		tag = CanonicalTag(tag)
		raw, _ := json.Marshal([]TagName{tag})
		if ValidateField("tags", raw) != nil {
			return v, ErrInput
		}
		if !seen[tag] {
			out = append(out, tag)
			seen[tag] = true
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	v.Exclude = out
	return v, nil
}
