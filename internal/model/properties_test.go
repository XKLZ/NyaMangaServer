package model

import (
	"encoding/json"
	"testing"
)

func TestPropertyInferenceOwnershipAndUnknown(t *testing.T) {
	title := "[黑白][完全修正][无额外广告] 完整标题"
	fields := Metadata{Title: &title, Tags: []TagName{{"language", "japanese"}, {"language", "english"}, {"language", "translated"}, {"other", "full color"}, {"other", "uncensored"}, {"other", "mosaic censorship"}}}
	v := InferProperties(fields, nil, nil)
	if string(v.Languages.Value) != `["english","japanese"]` || string(v.Translated.Value) != "true" || string(v.ColorMode.Value) != `"color"` || v.Censorship.State != "conflict" || string(v.Censorship.Value) != "null" || string(v.ExtraneousAds.Value) != "false" {
		t.Fatal(v)
	}
	v = InferProperties(fields, nil, map[string]Override{"languages": {Op: "empty"}, "censorship": {Op: "set", Value: json.RawMessage(`"full"`)}, "translated": {Op: "set", Value: json.RawMessage(`false`)}})
	if v.Languages.State != "unknown" || v.Languages.Origin != "manual" || len(Languages(Property{PropertyValue: v.Languages.Automatic})) != 2 || string(v.Censorship.Value) != `"full"` || v.Censorship.Automatic.State != "conflict" || string(v.Translated.Value) != "false" {
		t.Fatal(v)
	}
	v = InferProperties(Metadata{}, nil, nil)
	if v.ColorMode.State != "unknown" || string(v.Translated.Value) != "null" || len(Languages(v.Languages)) != 0 {
		t.Fatal(v)
	}
	legacy := map[string]Override{"language": {Op: "set", Value: json.RawMessage(`"旧人工语言"`)}}
	v = InferProperties(fields, nil, legacy)
	if string(v.Languages.Value) != `["旧人工语言"]` || v.Languages.Origin != "legacy_manual" {
		t.Fatal(v)
	}
	legacy["language"] = Override{Op: "set", Value: json.RawMessage(`"translated"`)}
	v = InferProperties(fields, nil, legacy)
	if v.Languages.State != "unknown" || len(v.Languages.Evidence) != 1 {
		t.Fatal(v)
	}
}
func TestPropertyValidation(t *testing.T) {
	for k, v := range map[string]string{"languages": `["日本語","english"]`, "translated": `false`, "extraneous_ads": `true`, "color_mode": `"monochrome"`, "censorship": `"mosaic"`} {
		if e := ValidateOverrides(map[string]Override{k: {Op: "set", Value: json.RawMessage(v)}}); e != nil {
			t.Fatal(k, e)
		}
	}
	for k, v := range map[string]string{"languages": `[]`, "translated": `null`, "extraneous_ads": `"true"`, "color_mode": `"other"`, "censorship": `0`} {
		if e := ValidateOverrides(map[string]Override{k: {Op: "set", Value: json.RawMessage(v)}}); e == nil {
			t.Fatal(k, "accepted")
		}
	}
	if e := ValidateOverrides(map[string]Override{"languages": {Op: "empty"}, "language": {Op: "inherit"}}); e != ErrLanguageFields {
		t.Fatal(e)
	}
}
