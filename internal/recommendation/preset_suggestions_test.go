package recommendation

import (
	"encoding/json"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func TestPresetSuggestionsKeepReliableEvidenceRegardlessOfPeers(t *testing.T) {
	members := []ProposalMember{
		{ID: "a", Languages: []string{"japanese"}, Properties: map[string]CompactProperty{"translated": {Value: false}, "color_mode": {Value: "monochrome"}}},
		{ID: "b", Languages: []string{"chinese"}, Properties: map[string]CompactProperty{"translated": {Value: true}, "color_mode": {Value: "color"}}},
	}
	SuggestPresets(members)
	for i, want := range [][]string{{"original", "monochrome"}, {"translated", "color"}} {
		if len(members[i].Suggested.Presets) != len(want) {
			t.Fatal(members)
		}
		for j, v := range want {
			if members[i].Suggested.Presets[j].ID != v {
				t.Fatal(members)
			}
		}
	}
	members[1].Properties = nil
	SuggestPresets(members)
	if len(members[0].Suggested.Presets) != 2 || len(members[1].Suggested.Presets) != 0 || !members[1].Suggested.NeedsReview {
		t.Fatal(members)
	}
}
func TestPresetMarkersUnknownConflictAndDigitalBoundaries(t *testing.T) {
	for _, tc := range []struct {
		title string
		ids   []string
	}{
		{"[漢化] [無修正] [全彩] [DL] [特別版] Anonymous", []string{"translated", "uncensored", "color", "dl", "special"}},
		{"Anonymous Digital", []string{"dl"}},
		{"（電子版） Anonymous", []string{"dl"}},
		{"[未翻訳] [白黒] [有修正] Anonymous", []string{"original", "censored", "monochrome"}},
		{"[修订版] Handle Digitalization", nil},
		{"[DLsite] Artist digital", []string{"dl"}},
		{"[彩色] [黑白] Anonymous", nil},
	} {
		members := []ProposalMember{{ID: "a", Title: tc.title}, {ID: "b"}}
		SuggestPresets(members)
		if len(members[0].Suggested.Presets) != len(tc.ids) {
			t.Fatal(tc.title, members[0].Suggested)
		}
		for i, v := range tc.ids {
			if members[0].Suggested.Presets[i].ID != v {
				t.Fatal(tc.title, members[0].Suggested)
			}
		}
	}
}
func TestPresetDisplayUsesManualUnknownAndDoesNotChangeMatchingIdentity(t *testing.T) {
	a := InputEdition{Book: BookInput{ID: "a", Title: "[彩色] Anonymous", Tags: [][2]string{{"other", "full color"}}}}
	b := a
	b.DisplayProperties = &model.BookProperties{ColorMode: model.Property{PropertyValue: model.PropertyValue{Value: json.RawMessage(`null`), Origin: "manual", State: "explicit_unknown"}}}
	if CompactProperties(a)["color_mode"].Value != "color" || CompactProperties(b)["color_mode"].Value != nil {
		t.Fatal("manual unknown not respected")
	}
	if EditionFingerprint(a) != EditionFingerprint(b) || EvidenceFingerprint(a) != EvidenceFingerprint(b) {
		t.Fatal("display-only fields changed matching")
	}
	b.CoverRevision = 2
	if EditionFingerprint(a) != EditionFingerprint(b) || EvidenceFingerprint(a) == EvidenceFingerprint(b) {
		t.Fatal("cover choice identity not tracked in discovery")
	}
	members := []ProposalMember{{ID: "a", Title: "[特别版] Anonymous", Properties: map[string]CompactProperty{}}, {ID: "b", Title: "Anonymous", Properties: map[string]CompactProperty{}}}
	SuggestPresets(members)
	if len(members[0].Suggested.Presets) != 1 || members[0].Suggested.Presets[0].ID != "special" {
		t.Fatal("unknown standard edition inferred", members)
	}
}

func TestPresetManualEmptyBlocksMarkersAndCustomLabelsNeverBecomeEvidence(t *testing.T) {
	members := []ProposalMember{{ID: "a", Title: "[彩色] Anonymous", Labels: []string{"译本", "DL"}, Properties: map[string]CompactProperty{"color_mode": {Origin: "manual", Value: nil, State: "unknown"}}}, {ID: "b"}}
	SuggestPresets(members)
	if len(members[0].Suggested.Presets) != 0 {
		t.Fatal(members[0].Suggested)
	}
	members[0].Properties = map[string]CompactProperty{"color_mode": {Origin: "tags", Value: nil, State: "conflict"}}
	SuggestPresets(members)
	if len(members[0].Suggested.Presets) != 0 {
		t.Fatal("conflict was overridden", members[0].Suggested)
	}
}
