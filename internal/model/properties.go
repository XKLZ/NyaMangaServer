package model

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"

	"nyamangaserver.local/server/internal/textkey"
)

const PropertyRuleVersion = 1

var ErrMultipleLanguages = errors.New("multiple_languages_require_array")
var ErrLanguageFields = errors.New("conflicting_language_fields")

type PropertyValue struct {
	Value    json.RawMessage `json:"value"`
	Origin   string          `json:"origin"`
	State    string          `json:"state"`
	Evidence []string        `json:"evidence"`
}
type Property struct {
	PropertyValue
	Automatic PropertyValue `json:"automatic"`
}
type BookProperties struct {
	RuleVersion   int      `json:"rule_version"`
	Languages     Property `json:"languages"`
	Translated    Property `json:"translated"`
	ColorMode     Property `json:"color_mode"`
	Censorship    Property `json:"censorship"`
	ExtraneousAds Property `json:"extraneous_ads"`
}

func IsProperty(k string) bool {
	switch k {
	case "languages", "translated", "color_mode", "censorship", "extraneous_ads":
		return true
	}
	return false
}
func CanonicalLanguage(s string) string { return textkey.Normalize(s) }
func LanguageValue(s string) bool {
	s = CanonicalLanguage(s)
	return s != "translated" && s != "rewrite" && ValidateField("language", jsonBytes(s)) == nil
}
func jsonBytes(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func property(v any, origin, state string, evidence []string) PropertyValue {
	if evidence == nil {
		evidence = []string{}
	}
	return PropertyValue{jsonBytes(v), origin, state, evidence}
}

var markerPattern = regexp.MustCompile(`\(([^()]*)\)|（([^（）]*)）|\[([^\[\]]*)\]|［([^［］]*)］|【([^【】]*)】|\{([^{}]*)\}|｛([^｛｝]*)｝|「([^「」]*)」|『([^『』]*)』|〈([^〈〉]*)〉|《([^《》]*)》|〔([^〔〕]*)〕`)

// InferProperties is deterministic and never changes source tags or overrides.
func InferProperties(fields Metadata, sourceLanguage *string, overrides map[string]Override) BookProperties {
	tags := map[string]bool{}
	languages := []string{}
	for _, t := range fields.Tags {
		t = CanonicalTag(t)
		tags[t.Namespace+":"+t.Name] = true
		if t.Namespace == "language" && LanguageValue(t.Name) {
			languages = append(languages, CanonicalLanguage(t.Name))
		}
	}
	sort.Strings(languages)
	languages = uniqueLanguages(languages)
	lang := property(nil, "none", "unknown", nil)
	if len(languages) > 0 {
		ev := []string{}
		for _, s := range languages {
			ev = append(ev, "language:"+s)
		}
		lang = property(languages, "tags", "normal", ev)
	} else if sourceLanguage != nil && LanguageValue(*sourceLanguage) {
		lang = property([]string{CanonicalLanguage(*sourceLanguage)}, "source", "normal", []string{*sourceLanguage})
	}
	markers := map[string]bool{}
	title := ""
	if fields.Title != nil {
		title += *fields.Title
	}
	if fields.TitleOriginal != nil {
		title += " " + *fields.TitleOriginal
	}
	for _, match := range markerPattern.FindAllStringSubmatch(title, -1) {
		for _, m := range match[1:] {
			if m != "" {
				markers[textkey.Normalize(m)] = true
			}
		}
	}
	type rule struct {
		value   any
		tag     string
		markers []string
	}
	infer := func(rules []rule) PropertyValue {
		values := map[string]any{}
		ev := []string{}
		origin := "tags"
		for _, r := range rules {
			if r.tag != "" && tags[r.tag] {
				values[string(jsonBytes(r.value))] = r.value
				ev = append(ev, r.tag)
			}
		}
		if len(values) == 0 {
			origin = "title"
			for _, r := range rules {
				for _, m := range r.markers {
					if markers[m] {
						values[string(jsonBytes(r.value))] = r.value
						ev = append(ev, "title:["+m+"]")
					}
				}
			}
		}
		if len(values) == 0 {
			return property(nil, "none", "unknown", nil)
		}
		if len(values) > 1 {
			return property(nil, origin, "conflict", ev)
		}
		for _, v := range values {
			return property(v, origin, "normal", ev)
		}
		panic("unreachable")
	}
	out := BookProperties{RuleVersion: PropertyRuleVersion}
	set := func(k string, dst *Property, auto PropertyValue) {
		*dst = Property{auto, auto}
		if k == "languages" {
			if old, ok := overrides["language"]; ok {
				legacy := property(nil, "legacy_manual", "unknown", nil)
				if old.Op == "set" {
					var s string
					_ = json.Unmarshal(old.Value, &s)
					legacy.Evidence = []string{s}
					if LanguageValue(s) {
						legacy = property([]string{CanonicalLanguage(s)}, "legacy_manual", "normal", []string{s})
					}
				}
				dst.PropertyValue = legacy
			}
		}
		if o, ok := overrides[k]; ok && o.Op != "inherit" {
			dst.PropertyValue = property(nil, "manual", "unknown", nil)
			if o.Op == "set" {
				dst.Value = append(json.RawMessage(nil), o.Value...)
				dst.State = "normal"
			}
		}
	}
	set("languages", &out.Languages, lang)
	set("translated", &out.Translated, infer([]rule{{true, "language:translated", []string{"翻译", "翻訳", "translated"}}}))
	set("color_mode", &out.ColorMode, infer([]rule{{"color", "other:full color", []string{"彩色", "カラー", "full color"}}, {"monochrome", "", []string{"黑白", "白黒", "monochrome"}}}))
	set("censorship", &out.Censorship, infer([]rule{{"uncensored", "other:uncensored", []string{"无修正", "無修正", "uncensored"}}, {"mosaic", "other:mosaic censorship", []string{"马赛克修正", "モザイク修正", "mosaic censorship"}}, {"full", "other:full censorship", []string{"完全修正", "full censorship"}}}))
	set("extraneous_ads", &out.ExtraneousAds, infer([]rule{{true, "other:extraneous ads", []string{"额外广告", "extraneous ads"}}, {false, "", []string{"无额外广告", "no extraneous ads"}}}))
	return out
}
func uniqueLanguages(v []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range v {
		s = CanonicalLanguage(s)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
func Languages(v Property) []string { var out []string; _ = json.Unmarshal(v.Value, &out); return out }
func CanonicalLanguages(v json.RawMessage) json.RawMessage {
	var out []string
	_ = json.Unmarshal(v, &out)
	return jsonBytes(uniqueLanguages(out))
}

type LanguageFilter struct {
	AnyOf          []string `json:"any_of,omitempty"`
	IncludeUnknown bool     `json:"include_unknown,omitempty"`
}
type PropertyFilter struct {
	Languages     *LanguageFilter `json:"languages,omitempty"`
	Translated    []string        `json:"translated,omitempty"`
	ColorMode     []string        `json:"color_mode,omitempty"`
	Censorship    []string        `json:"censorship,omitempty"`
	ExtraneousAds []string        `json:"extraneous_ads,omitempty"`
}

func ValidatePropertyFilter(v *PropertyFilter) error {
	if v == nil {
		return nil
	}
	if l := v.Languages; l != nil {
		if len(l.AnyOf) > 200 || len(l.AnyOf) == 0 && !l.IncludeUnknown {
			return ErrInput
		}
		for i, s := range l.AnyOf {
			if !LanguageValue(s) {
				return ErrInput
			}
			l.AnyOf[i] = CanonicalLanguage(s)
		}
		l.AnyOf = uniqueLanguages(l.AnyOf)
		sort.Strings(l.AnyOf)
	}
	for _, x := range []struct {
		v       []string
		allowed string
	}{{v.Translated, "yes,no,unknown"}, {v.ColorMode, "monochrome,color,unknown"}, {v.Censorship, "uncensored,mosaic,full,unknown"}, {v.ExtraneousAds, "yes,no,unknown"}} {
		if len(x.v) > len(strings.Split(x.allowed, ",")) {
			return ErrInput
		}
		seen := map[string]bool{}
		for _, s := range x.v {
			if seen[s] || !strings.Contains(","+x.allowed+",", ","+s+",") {
				return ErrInput
			}
			seen[s] = true
		}
	}
	return nil
}
