package model_test

import (
	"encoding/json"
	"nyamangaserver.local/server/internal/model"
	"reflect"
	"testing"
	"time"
)

// This is a target-field protection test, not a TreeC parser or converter.
func TestTreeCAnonymousFieldProtectionMatrix(t *testing.T) {
	title := "Original remote"
	remotePages, remoteBytes := int64(987), int64(123456789)
	rawSources := []struct {
		SourceID string
		GID      string
		Raw      json.RawMessage
	}{{"source-a", "123", json.RawMessage(`{"title":"variant a"}`)}, {"source-b", "123", json.RawMessage(`{"title":"variant b"}`)}}
	raw, _ := json.Marshal(rawSources)
	var restoredSources []struct {
		SourceID string
		GID      string
		Raw      json.RawMessage
	}
	if e := json.Unmarshal(raw, &restoredSources); e != nil || len(restoredSources) != 2 || string(restoredSources[0].Raw) == string(restoredSources[1].Raw) {
		t.Fatal("same gid source differences collapsed", e)
	}
	original := model.Metadata{Title: &title, RemotePageCount: &remotePages, RemoteSizeBytes: &remoteBytes, Tags: []model.TagName{{Namespace: "artist", Name: "anonymous"}}}
	effective, e := model.ProjectMetadata(original, map[string]model.Override{"title": {Op: "set", Value: json.RawMessage(`"Manual retained"`)}})
	if e != nil || *effective.Title != "Manual retained" || *effective.RemotePageCount != 987 {
		t.Fatal(effective, e)
	}
	unknown := model.InferProperties(model.Metadata{}, nil, nil)
	if string(unknown.Translated.Value) == "false" || string(unknown.ExtraneousAds.Value) == "false" {
		t.Fatal("default false invented opposite evidence", unknown)
	}
	manual := model.InferProperties(original, nil, map[string]model.Override{"translated": {Op: "set", Value: json.RawMessage(`false`)}})
	if string(manual.Translated.Value) != "false" || manual.Translated.Origin != "manual" {
		t.Fatal("explicit false lost", manual)
	}
	labels := []string{"  Anonymous label  ", "自定义 第②版"}
	graph := model.WorkContext{Editions: []model.WorkEdition{{ID: "edition-a", WorkID: "work", LibraryID: "library", Labels: labels}, {ID: "edition-b", WorkID: "work", LibraryID: "library", Labels: []string{"same gid still distinct"}}}, Works: []model.Work{{ID: "work", LibraryID: "library", PreferredEditionID: "edition-b", EditionIDs: []string{"edition-b", "edition-a"}}}, Series: []model.Series{{ID: "series", LibraryID: "library", WorkIDs: []string{"work"}}}}
	raw, e = json.Marshal(graph)
	if e != nil {
		t.Fatal(e)
	}
	var restored model.WorkContext
	if e = json.Unmarshal(raw, &restored); e != nil || len(restored.Editions) != 2 || !reflect.DeepEqual(restored.Editions[0].Labels, labels) || !reflect.DeepEqual(restored.Works[0].EditionIDs, []string{"edition-b", "edition-a"}) || restored.Works[0].PreferredEditionID != "edition-b" {
		t.Fatal("identity, order or raw manual label lost", restored, e)
	}
	local := model.File{PageCount: 2, Size: 100, ContentRevision: 7}
	if local.PageCount == int(*effective.RemotePageCount) || local.Size == *effective.RemoteSizeBytes {
		t.Fatal("remote/local conflated")
	}
	milliseconds := int64(1700000000123)
	seconds := time.UnixMilli(milliseconds).Unix()
	if seconds != 1700000000 || seconds == milliseconds {
		t.Fatal("millisecond/second mapping implicit")
	}
	reading := model.ReadingState{EditionID: "edition-a", ContentRevision: 7, PageIndex: new(int), Completed: true, ReadCount: 9, LastReadAt: &seconds}
	raw, _ = json.Marshal(reading)
	var decoded model.ReadingState
	if e = json.Unmarshal(raw, &decoded); e != nil || decoded.PageIndex == nil || *decoded.PageIndex != 0 || decoded.ReadCount != 9 || !decoded.Completed || *decoded.LastReadAt != seconds {
		t.Fatal("personal reading fields lost", decoded, e)
	}
}
