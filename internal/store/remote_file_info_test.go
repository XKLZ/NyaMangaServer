package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
	"testing"
)

func remoteFingerprint(t *testing.T, s *Store) (string, string, int64, string) {
	t.Helper()
	ctx := context.Background()
	tx, e := s.Read.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	pool, e := recommendationPool(ctx, tx, []string{"a"})
	if e != nil {
		t.Fatal(e)
	}
	token, e := discoveryToken(ctx, tx, "a")
	if e != nil {
		t.Fatal(e)
	}
	var revision int64
	var projection string
	if e = tx.QueryRow("SELECT revision FROM editions WHERE id='a'").Scan(&revision); e != nil {
		t.Fatal(e)
	}
	if e = tx.QueryRow("SELECT fields_json FROM effective_editions WHERE edition_id='a'").Scan(&projection); e != nil {
		t.Fatal(e)
	}
	return rec.Fingerprint(pool), token, revision, projection
}
func TestRemoteFileInfoBackfillAndRefreshPreserveMatchingInputs(t *testing.T) {
	s, ctx := aclFixture(t)
	root := context.Background()
	title := "remote original"
	remote := model.RemoteMetadata{ExternalID: "123", URL: "https://e-hentai.org/g/123/0123456789/", Method: "manual", Fields: model.Metadata{Title: &title, Tags: []model.TagName{}}, Raw: []byte(`{"gid":123,"filecount":"987","filesize":123456789}`)}
	tx, e := s.Write.BeginTx(root, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = publishSource(root, tx, "a", remote); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var source string
	if e = s.Read.QueryRow("SELECT metadata_id FROM edition_sources WHERE edition_id='a'").Scan(&source); e != nil {
		t.Fatal(e)
	}
	before, token, rev, projection := remoteFingerprint(t, s)
	healthy := func() {
		t.Helper()
		findings, e := s.HealthRules(ctx["admin"], model.HealthObject{Kind: "edition", ID: "a"})
		if e != nil {
			t.Fatal(e)
		}
		seen := false
		for _, f := range findings {
			if f.Category == "projections" {
				seen = true
				if f.Status != "normal" {
					t.Fatal("false projection corruption", f)
				}
			}
		}
		if !seen {
			t.Fatal("projection check missing")
		}
	}
	healthy()

	plan, e := s.BackfillRemoteFileInfo(ctx["admin"], model.RemoteFileInfoBackfill{DryRun: true, Expected: map[string]int64{source: 1}})
	if e != nil || len(plan.Items) != 1 || plan.Items[0].Status != "fillable" || plan.Updated != 0 {
		t.Fatal(plan, e)
	}
	report, e := s.BackfillRemoteFileInfo(ctx["admin"], model.RemoteFileInfoBackfill{Expected: map[string]int64{source: 1}})
	if e != nil || report.Updated != 1 {
		t.Fatal(report, e)
	}
	if _, e = s.BackfillRemoteFileInfo(ctx["admin"], model.RemoteFileInfoBackfill{Expected: map[string]int64{source: 1}}); !errors.Is(e, model.ErrConflict) {
		t.Fatal("stale backfill", e)
	}
	report, e = s.BackfillRemoteFileInfo(ctx["admin"], model.RemoteFileInfoBackfill{Expected: map[string]int64{source: 2}})
	if e != nil || report.Updated != 0 {
		t.Fatal("not idempotent", report, e)
	}
	if _, e = s.BackfillRemoteFileInfo(ctx["editor"], model.RemoteFileInfoBackfill{DryRun: true, Expected: map[string]int64{source: 2}}); !errors.Is(e, model.ErrForbidden) {
		t.Fatal("ordinary backfill", e)
	}
	details, e := s.MetadataDetails(ctx["viewer"], "a")
	if e != nil || details.Fields.RemotePageCount == nil || *details.Fields.RemotePageCount != 987 || details.Source.Fields.RemoteSizeBytes == nil {
		t.Fatal(details, e)
	}
	file, e := s.Edition(ctx["viewer"], "a")
	if e != nil || file.PageCount != 2 || file.Size != 1 || file.ContentRevision != 1 {
		t.Fatal("remote overwrote local", file, e)
	}
	healthy()
	// A normal provider refresh that changes only remote measurements must not
	// replace the existing matching projection or Edition revision either.
	p, b := int64(1000), int64(2000)
	remote.Fields.RemotePageCount = &p
	remote.Fields.RemoteSizeBytes = &b
	tx, e = s.Write.BeginTx(ctx["editor"], nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = publishSource(ctx["editor"], tx, "a", remote); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	after, afterToken, afterRev, afterProjection := remoteFingerprint(t, s)
	if before != after || token != afterToken || rev != afterRev || projection != afterProjection {
		t.Fatal("remote measurements changed recommendation inputs")
	}
	healthy()
	if e = model.ValidateOverrides(map[string]model.Override{"remote_page_count": {Op: "set", Value: json.RawMessage(`99`)}}); e == nil {
		t.Fatal("remote manual override accepted")
	}
}
func TestRemoteFileInfoMissingInvalidRawAndConflictNeverInventValues(t *testing.T) {
	s, ctx := aclFixture(t)
	cases := []struct{ id, raw, normalized, want string }{{"1", `{"gid":1}`, `{"title":"manual retained"}`, "raw_unavailable"}, {"2", `{"gid":3,"filecount":9}`, `{}`, "raw_invalid"}, {"4", `{"gid":4,"filecount":10}`, `{"remote_page_count":8}`, "raw_conflict"}}
	for _, c := range cases {
		if _, e := s.Write.Exec("INSERT INTO external_metadata VALUES(?,'ehentai',?,?,?,1,1,1)", c.id, c.id, c.normalized, c.raw); e != nil {
			t.Fatal(e)
		}
		out, e := s.BackfillRemoteFileInfo(ctx["admin"], model.RemoteFileInfoBackfill{Expected: map[string]int64{c.id: 1}})
		if e != nil || out.Updated != 0 || out.Items[0].Status != c.want {
			t.Fatal(c.id, out, e)
		}
		var actual string
		if e = s.Read.QueryRow("SELECT normalized_json FROM external_metadata WHERE id=?", c.id).Scan(&actual); e != nil || actual != c.normalized {
			t.Fatal("raw failure altered values", c.id, actual, e)
		}
	}
}
func TestRemoteFileInfoBatchCASRollsBackEarlierUpdates(t *testing.T) {
	s, ctx := aclFixture(t)
	for i, id := range []string{"a-source", "b-source"} {
		if _, e := s.Write.Exec("INSERT INTO external_metadata VALUES(?,'ehentai',?,'{}',?,1,1,1)", id, fmt.Sprint(123+i), fmt.Sprintf(`{"gid":%d,"filecount":9}`, 123+i)); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.BackfillRemoteFileInfo(ctx["admin"], model.RemoteFileInfoBackfill{Expected: map[string]int64{"a-source": 1, "b-source": 2}}); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	var normalized string
	var rev int64
	if e := s.Read.QueryRow("SELECT normalized_json,revision FROM external_metadata WHERE id='a-source'").Scan(&normalized, &rev); e != nil || normalized != "{}" || rev != 1 {
		t.Fatal("batch committed partially", normalized, rev, e)
	}
}
