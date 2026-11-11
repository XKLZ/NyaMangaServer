package model

import (
	"encoding/json"
	"testing"
)

func TestOverrideValidation(t *testing.T) {
	for _, fields := range []map[string]Override{
		{"unknown": {Op: "empty"}}, {"rating": {Op: "set", Value: json.RawMessage(`6`)}},
		{"title": {Op: "set", Value: json.RawMessage(`null`)}}, {"title": {Op: "inherit", Value: json.RawMessage(`"x"`)}},
		{"tags": {Op: "set", Value: json.RawMessage(`[{"namespace":"../bad","name":"a"}]`)}},
		{"tags": {Op: "set", Value: json.RawMessage(`[{"namespace":"artist","name":"a"},{"namespace":"artist","name":"a"}]`)}},
	} {
		if ValidateOverrides(fields) == nil {
			t.Fatal("accepted invalid fields", fields)
		}
	}
	if e := ValidateOverrides(map[string]Override{"title": {Op: "set", Value: json.RawMessage(`"中文标题"`)}, "tags": {Op: "set", Value: json.RawMessage(`[]`)}}); e != nil {
		t.Fatal(e)
	}
}

func TestCanonicalTagIdentityAndNullableRating(t *testing.T) {
	if e := ValidateField("tags", []byte(`[{"namespace":"artist","name":" Anonymous "},{"namespace":"artist","name":"anonymous"}]`)); e == nil {
		t.Fatal("canonical duplicate accepted")
	}
	if e := ValidateField("rating", []byte(" null ")); e == nil {
		t.Fatal("null rating treated as zero")
	}
	tag := CanonicalTag(TagName{Namespace: "artist", Name: "  Anonymous   Artist "})
	if tag.Name != "anonymous artist" {
		t.Fatal(tag)
	}
}
