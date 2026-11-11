package recommendation

import (
	"encoding/json"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/textkey"
	"regexp"
	"strings"
)

const PresetRuleVersion = "edition-preset-labels-evidence-v2"

type CompactProperty struct {
	Value  any    `json:"value"`
	State  string `json:"state"`
	Origin string `json:"origin"`
}
type Preset struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type PresetSuggestion struct {
	RuleVersion string   `json:"rule_version"`
	Presets     []Preset `json:"presets"`
	NeedsReview bool     `json:"needs_manual_review"`
}

var presetNames = map[string]string{"original": "原版", "translated": "译本", "monochrome": "黑白", "color": "彩色", "censored": "修正", "uncensored": "无修", "dl": "DL", "special": "特别"}

var presetMarkers = map[string]map[string][]string{
	"translated": {"original": {"untranslated", "未翻译", "未翻譯", "未翻訳"}, "translated": {"translated", "translation", "翻译", "翻譯", "翻訳", "汉化", "漢化", "英訳"}},
	"color_mode": {"monochrome": {"monochrome", "black and white", "黑白", "白黒"}, "color": {"full color", "full colour", "color", "colour", "全彩", "彩色", "カラー", "カラー化"}},
	"censorship": {"censored": {"censored", "mosaic", "mosaic censorship", "full censorship", "修正", "有修", "有修正", "马赛克", "馬賽克", "马赛克修正", "完全修正"}, "uncensored": {"uncensored", "无修", "無修", "无修正", "無修正", "无码", "無碼"}},
	"dl":         {"dl": {"dl", "digital", "digital edition", "digital version", "電子版", "电子版", "電子書籍", "电子书"}},
	"special":    {"special": {"special edition", "extended edition", "特别版", "特別版", "增补版", "增補版", "増補版"}},
}
var presetBracket = regexp.MustCompile(`\(([^()]*)\)|（([^（）]*)）|\[([^\[\]]*)\]|［([^［］]*)］|【([^【】]*)】|\{([^{}]*)\}|「([^「」]*)」|『([^『』]*)』`)
var translationCredit = regexp.MustCompile(`^[\p{L}\p{N}\s・ー_-]{0,64}(?:汉化|漢化)(?:组|組)?$`)
var dlSuffix = regexp.MustCompile(`(?:^|[\s_-])(?:dl|digital(?: edition| version)?|電子版|电子版|電子書籍|电子书)$`)

func CompactProperties(input InputEdition) map[string]CompactProperty {
	props := input.DisplayProperties
	if props == nil {
		title, original := input.Book.Title, input.Book.TitleOriginal
		fields := model.Metadata{Title: &title, TitleOriginal: &original, Tags: []model.TagName{}}
		for _, t := range input.Book.Tags {
			fields.Tags = append(fields.Tags, model.TagName{Namespace: t[0], Name: t[1]})
		}
		inferred := model.InferProperties(fields, nil, nil)
		props = &inferred
	}
	out := map[string]CompactProperty{}
	for name, p := range map[string]model.Property{"translated": props.Translated, "color_mode": props.ColorMode, "censorship": props.Censorship, "extraneous_ads": props.ExtraneousAds} {
		var value any
		if len(p.Value) > 0 {
			_ = json.Unmarshal(p.Value, &value)
		}
		out[name] = CompactProperty{value, p.State, p.Origin}
	}
	tags := []model.TagName{}
	for _, t := range input.Book.Tags {
		tags = append(tags, model.TagName{Namespace: t[0], Name: t[1]})
	}
	for _, dimension := range []string{"dl", "special"} {
		value := inferredPreset(ProposalMember{Title: input.Book.Title, TitleOriginal: input.Book.TitleOriginal, Tags: tags}, dimension)
		p := CompactProperty{State: "unknown", Origin: "none"}
		if value != "" {
			p = CompactProperty{Value: value, State: "normal", Origin: "markers"}
		}
		out[dimension] = p
	}
	return out
}

// inferredPreset only accepts explicit tags or complete title decorations. It
// never treats a missing marker as evidence for its opposite.
func inferredPreset(m ProposalMember, dimension string) string {
	hits := map[string]bool{}
	match := func(text string, translation bool) {
		text = textkey.Normalize(text)
		for value, markers := range presetMarkers[dimension] {
			for _, marker := range markers {
				if text == marker {
					hits[value] = true
				}
			}
		}
		if translation && translationCredit.MatchString(text) {
			hits["translated"] = true
		}
	}
	for _, tag := range m.Tags {
		ns := textkey.Normalize(tag.Namespace)
		if (dimension == "translated" && ns == "language") || (dimension != "translated" && (ns == "other" || ns == "misc")) {
			match(tag.Name, false)
		}
	}
	for _, text := range []string{m.Title, m.TitleOriginal} {
		for _, group := range presetBracket.FindAllStringSubmatch(text, -1) {
			for _, marker := range group[1:] {
				if marker != "" {
					match(marker, dimension == "translated")
				}
			}
		}
		normalized := textkey.Normalize(text)
		if dimension == "dl" && dlSuffix.MatchString(normalized) {
			hits["dl"] = true
		}
		if dimension == "special" {
			for _, marker := range presetMarkers[dimension]["special"] {
				if normalized == marker || strings.HasSuffix(normalized, " "+marker) {
					hits["special"] = true
				}
			}
		}
	}
	if len(hits) == 1 {
		for v := range hits {
			return v
		}
	}
	return ""
}

func presetValues(m ProposalMember) []string {
	out := []string{}
	for _, dimension := range []string{"translated", "censorship", "color_mode", "dl", "special"} {
		p, exists := m.Properties[dimension]
		value := ""
		if exists && p.State != "conflict" {
			switch dimension {
			case "translated":
				if v, ok := p.Value.(bool); ok {
					if v {
						value = "translated"
					} else {
						value = "original"
					}
				}
			case "color_mode":
				if v, ok := p.Value.(string); ok && (v == "color" || v == "monochrome") {
					value = v
				}
			case "dl", "special":
				if v, ok := p.Value.(string); ok && v == dimension {
					value = v
				}
			case "censorship":
				if v, ok := p.Value.(string); ok {
					if v == "uncensored" {
						value = v
					} else if v == "mosaic" || v == "full" {
						value = "censored"
					}
				}
			}
		}
		// A manual explicit unknown or a conflicting effective value must not be
		// filled again from title decorations. Custom labels are never evidence.
		if value == "" && p.Origin != "manual" && p.State != "conflict" {
			value = inferredPreset(m, dimension)
		}
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

// SuggestPresets preserves every reliable dimension, even when shared by all
// members or already distinguishable by language. No fallback label is invented.
func SuggestPresets(members []ProposalMember) {
	for i, m := range members {
		suggestion := PresetSuggestion{RuleVersion: PresetRuleVersion, Presets: []Preset{}}
		if len(members) > 1 {
			for _, value := range presetValues(m) {
				suggestion.Presets = append(suggestion.Presets, Preset{value, presetNames[value]})
			}
			suggestion.NeedsReview = len(suggestion.Presets) == 0
		}
		members[i].Suggested = suggestion
	}
}
