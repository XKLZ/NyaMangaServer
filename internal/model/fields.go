package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var namespacePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
var fieldLimits = map[string]int{"title": 512, "title_original": 512, "category": 128, "language": 64, "description": 8192}

func ValidateField(field string, value json.RawMessage) error {
	if limit, ok := fieldLimits[field]; ok {
		var v string
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &v) != nil || !utf8.ValidString(v) || strings.ContainsRune(v, 0) || utf8.RuneCountInString(v) > limit || len(v) > limit*4 {
			return ErrInput
		}
		if field != "description" && strings.TrimSpace(v) == "" {
			return ErrInput
		}
		return nil
	}
	switch field {
	case "languages":
		var v []string
		if json.Unmarshal(value, &v) != nil || len(v) == 0 || len(v) > 200 {
			return ErrInput
		}
		for _, s := range v {
			if !LanguageValue(s) {
				return ErrInput
			}
		}
	case "translated", "extraneous_ads":
		var v bool
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &v) != nil {
			return ErrInput
		}
	case "color_mode", "censorship":
		var v string
		if json.Unmarshal(value, &v) != nil {
			return ErrInput
		}
		allowed := map[string]bool{"monochrome": true, "color": true}
		if field == "censorship" {
			allowed = map[string]bool{"uncensored": true, "mosaic": true, "full": true}
		}
		if !allowed[v] {
			return ErrInput
		}
	case "rating":
		var v float64
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &v) != nil || math.IsNaN(v) || v < 0 || v > 5 {
			return ErrInput
		}
	case "published_at":
		var v string
		if json.Unmarshal(value, &v) != nil {
			return ErrInput
		}
		if _, e := time.Parse(time.RFC3339, v); e != nil {
			return ErrInput
		}
	case "tags":
		var tags []TagName
		d := json.NewDecoder(bytes.NewReader(value))
		d.DisallowUnknownFields()
		if d.Decode(&tags) != nil || tags == nil || len(tags) > 200 {
			return ErrInput
		}
		seen := map[TagName]bool{}
		for _, v := range tags {
			if !namespacePattern.MatchString(v.Namespace) || !utf8.ValidString(v.Name) || strings.TrimSpace(v.Name) == "" || strings.ContainsRune(v.Name, 0) || utf8.RuneCountInString(v.Name) > 128 || len(v.Name) > 512 || seen[CanonicalTag(v)] {
				return ErrInput
			}
			seen[CanonicalTag(v)] = true
		}
	default:
		return ErrInput
	}
	return nil
}
func ValidateOverrides(fields map[string]Override) error {
	if len(fields) == 0 || len(fields) > 13 {
		return ErrInput
	}
	if _, a := fields["language"]; a {
		if _, b := fields["languages"]; b {
			return ErrLanguageFields
		}
	}
	for field, v := range fields {
		if _, ok := fieldLimits[field]; !ok && !IsProperty(field) && field != "rating" && field != "published_at" && field != "tags" {
			return ErrInput
		}
		switch v.Op {
		case "set":
			if e := ValidateField(field, v.Value); e != nil {
				return e
			}
		case "empty", "inherit":
			if len(v.Value) > 0 {
				return ErrInput
			}
		default:
			return ErrInput
		}
	}
	return nil
}
func ValidateMetadata(v Metadata) error {
	if v.RemotePageCount != nil && *v.RemotePageCount < 0 || v.RemoteSizeBytes != nil && *v.RemoteSizeBytes < 0 {
		return ErrInput
	}
	if v.Tags == nil {
		v.Tags = []TagName{}
	}
	b, e := json.Marshal(v)
	if e != nil {
		return ErrInput
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(b, &fields)
	for k, b := range fields {
		if k == "remote_page_count" || k == "remote_size_bytes" {
			continue
		}
		if string(b) != "null" {
			if e := ValidateField(k, b); e != nil {
				return fmt.Errorf("invalid source field %s: %w", k, e)
			}
		}
	}
	return nil
}

type RemoteMetadata struct {
	Thumbnail  string `json:"thumbnail,omitempty"`
	ExternalID string
	URL        string
	Method     string
	Fields     Metadata
	Raw        json.RawMessage
}

// CanonicalTag keeps the raw provider response separate from normalized identity.
func CanonicalTag(v TagName) TagName {
	return TagName{Namespace: strings.ToLower(strings.TrimSpace(v.Namespace)), Name: strings.ToLower(strings.Join(strings.Fields(v.Name), " "))}
}
