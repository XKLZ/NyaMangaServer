package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"log/slog"
	"net/http/httptest"
	"nyamangaserver.local/server/internal/importflow"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/internal/testfixture"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type importThumbnailRemote struct {
	calls   atomic.Int32
	invalid atomic.Bool
}

func (r *importThumbnailRemote) FetchThumbnail(ctx context.Context, url string) ([]byte, error) {
	r.calls.Add(1)
	if r.invalid.Load() {
		return []byte("<svg></svg>"), nil
	}
	return (&testfixture.Remote{}).FetchThumbnail(ctx, url)
}

func TestImportHTTPAnonymousCompleteFlowAndGates(t *testing.T) { anonymousCompleteImport(t, false) }
func TestAuthorizationEditorCompleteImport(t *testing.T)       { anonymousCompleteImport(t, true) }
func anonymousCompleteImport(t *testing.T, isEditor bool) {
	h, cookie, csrf := metadataHandler(t)
	var debugLogs bytes.Buffer
	h.logger = slog.New(slog.NewTextHandler(&debugLogs, nil))
	db := handlerDB(t, h)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := filepath.Join(h.c.Roots[0].Path, "合成1.cbz")
	raw, e := os.ReadFile(source)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(h.c.Roots[0].Path, "合成3.cbz"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	recommendations := rec.New(db, h.catalog, &testfixture.RecommendationRunner{}, h.c.DataDir, h.logger)
	h.WithRecommendations(recommendations)
	images := &importThumbnailRemote{}
	imports := importflow.New(db, h.catalog, h.logger, func() { h.catalog.Wake(); h.metadata.Wake(); recommendations.Wake() }).WithCovers(images, h.c.DataDir)
	h.WithImports(imports)
	h.metadata.Start(ctx)
	recommendations.Start(ctx)
	imports.Start(ctx)
	t.Cleanup(func() {
		shutdown, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		imports.Shutdown(shutdown)
		h.metadata.Shutdown(shutdown)
		recommendations.Shutdown(shutdown)
	})
	doc, e := openapi3.NewLoader().LoadFromFile("../../api/openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	if e = doc.Validate(ctx); e != nil {
		t.Fatal(e)
	}
	doc.Servers[0].URL = "http://localhost/api/v1"
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		b, _ := json.Marshal(body)
		w := request(t, h, method, "/api/v1"+path, string(b), cookie, csrf)
		if w.Code >= 500 {
			t.Log(debugLogs.String())
		}
		code(t, w, want)
		if strings.HasPrefix(path, "/import-batches") {
			r := httptest.NewRequest(method, "http://localhost/api/v1"+path, bytes.NewReader(b))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", h.c.PublicURL)
			r.Header.Set("X-CSRF-Token", csrf)
			route, params, err := router.FindRoute(r)
			if err != nil {
				t.Fatal(err)
			}
			in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}}
			if want < 400 {
				if err = openapi3filter.ValidateRequest(ctx, in); err != nil {
					t.Fatal(path, err)
				}
			}
			out := &openapi3filter.ResponseValidationInput{RequestValidationInput: in, Status: w.Code, Header: w.Header()}
			out.SetBodyBytes(w.Body.Bytes())
			if err = openapi3filter.ValidateResponse(ctx, out); err != nil {
				t.Fatal(path, err)
			}
		}

		if strings.Contains(w.Body.String(), h.c.Roots[0].Path) || strings.Contains(w.Body.String(), `"Raw"`) {
			t.Fatal("private source/path leaked")
		}
		return w.Body.Bytes()
	}
	var library model.Library
	lraw := call("POST", "/libraries", map[string]any{"root_key": "fixture", "display_name": "匿名导入"}, 201)
	var lr struct {
		ID string `json:"id"`
	}
	json.Unmarshal(lraw, &lr)
	library.ID = lr.ID
	if isEditor {
		admin, e := db.Account(ctx)
		if e != nil {
			t.Fatal(e)
		}
		adminCtx := model.WithActor(ctx, *admin)
		a, e := h.svc.CreateAccount(adminCtx, "flow-editor", "anonymous editor password")
		if e != nil {
			t.Fatal(e)
		}
		if e = h.svc.UpdateAccount(adminCtx, a.ID, model.AccountUpdate{Expected: 1, Role: "user", Enabled: true, Grants: map[string]string{lr.ID: "editor"}}, nil); e != nil {
			t.Fatal(e)
		}
		if e = h.svc.ChangePassword(ctx, a.ID, "anonymous editor password", "new anonymous editor password"); e != nil {
			t.Fatal(e)
		}
		login := request(t, h, "POST", "/api/v1/auth/login", `{"username":"flow-editor","password":"new anonymous editor password"}`, nil, "")
		code(t, login, 200)
		cookie, csrf = sessionResponse(t, login)
	}

	create := map[string]any{"library_id": lr.ID, "request_key": "anonymous-import-http", "allow_metadata_network": false}
	call("POST", "/import-batches", create, 422)
	create["allow_metadata_network"] = true
	var batch model.ImportBatch
	json.Unmarshal(call("POST", "/import-batches", create, 202), &batch)
	id := batch.ID
	var duplicate model.ImportBatch
	json.Unmarshal(call("POST", "/import-batches", create, 202), &duplicate)
	if duplicate.ID != id {
		t.Fatal("duplicate batch")
	}
	wait := func(stage string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			data := call("GET", "/import-batches/"+id, nil, 200)
			json.Unmarshal(data, &batch)
			if batch.Stage == stage && batch.State == "review" {
				return
			}
			if batch.State == "failed" || batch.State == "paused" || time.Now().After(deadline) {
				t.Fatal(batch)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	members := func() model.ImportPage[model.ImportMember] {
		var out model.ImportPage[model.ImportMember]
		json.Unmarshal(call("GET", "/import-batches/"+id+"/members?page_size=100", nil, 200), &out)
		return out
	}
	wait("metadata")
	if batch.Total != 4 {
		t.Fatal(batch)
	}
	call("POST", "/import-batches/"+id+"/control/continue", map[string]any{"expected_revision": batch.Revision}, 409)
	first := members().Items[0]
	call("POST", fmt.Sprintf("/import-batches/%s/members/%s/refetch", id, first.ID), map[string]any{"expected_revision": first.Revision, "gallery_url": "http://localhost/private"}, 422)
	// Pick an available archive; corrupt input is intentionally reviewed as skipped.
	for _, m := range members().Items {
		if m.Error == "no_match" || m.Error == "multiple_matches" {
			first = m
			break
		}
	}
	call("POST", fmt.Sprintf("/import-batches/%s/members/%s/refetch", id, first.ID), map[string]any{"expected_revision": first.Revision, "gallery_url": "https://e-hentai.org/g/1/0123456789/"}, 202)
	wait("metadata")
	var sourceCount int
	db.Read.QueryRow("SELECT count(*) FROM edition_sources WHERE edition_id IN(SELECT edition_id FROM import_members WHERE batch_id=?)", id).Scan(&sourceCount)
	if sourceCount != 0 {
		t.Fatal("candidate adopted early")
	}
	if images.calls.Load() != 0 {
		t.Fatal("member list fetched remote covers")
	}
	currentMembers := members()
	var candidate model.ImportMember
	for _, member := range currentMembers.Items {
		if member.ID == first.ID {
			candidate = member
		}
	}
	coverPath := fmt.Sprintf("/import-batches/%s/members/%s/candidate-cover?candidate_revision=%d", id, first.ID, candidate.Candidate.Revision)
	code(t, request(t, h, "GET", "/api/v1"+coverPath, "", nil, ""), 401)
	for range 2 {
		w := request(t, h, "GET", "/api/v1"+coverPath, "", cookie, csrf)
		code(t, w, 200)
		if w.Header().Get("Content-Type") != "image/png" || !bytes.HasPrefix(w.Body.Bytes(), []byte("\x89PNG")) {
			t.Fatal("unbounded image response")
		}
	}
	if images.calls.Load() != 1 {
		t.Fatal("image cache not used", images.calls.Load())
	}
	code(t, request(t, h, "GET", "/api/v1"+coverPath+"&url=https://evil.example/x", "", cookie, csrf), 400)
	old := candidate.Candidate.Revision
	if _, e = db.Write.Exec("UPDATE import_candidates SET revision=revision+1 WHERE batch_id=? AND edition_id=?", id, first.ID); e != nil {
		t.Fatal(e)
	}
	code(t, request(t, h, "GET", "/api/v1"+coverPath, "", cookie, csrf), 409)
	images.invalid.Store(true)
	code(t, request(t, h, "GET", fmt.Sprintf("/api/v1/import-batches/%s/members/%s/candidate-cover?candidate_revision=%d", id, first.ID, old+1), "", cookie, csrf), 503)
	images.invalid.Store(false)
	var selection model.ImportPage[model.ImportSelection]
	json.Unmarshal(call("GET", "/import-batches/"+id+"/review-selection?filter=ready&page_size=100", nil, 200), &selection)
	if selection.Total < 1 || selection.Items[0].CandidateRevision < 1 {
		t.Fatal(selection)
	}
	call("GET", "/import-batches/"+id+"/review-selection?filter=invalid", nil, 422)
	var editor struct {
		Book              model.Edition `json:"book"`
		MemberRevision    int64         `json:"member_revision"`
		CandidateRevision int64         `json:"candidate_revision"`
	}
	editorPath := fmt.Sprintf("/import-batches/%s/members/%s/editor", id, first.ID)
	json.Unmarshal(call("GET", editorPath, nil, 200), &editor)
	manual := model.ImportManualReview{ExpectedRevision: editor.Book.Revision + 1, MemberRevision: editor.MemberRevision, CandidateRevision: editor.CandidateRevision, Fields: map[string]model.Override{"description": {Op: "set", Value: json.RawMessage(`"Reviewed and edited"`)}}}
	manualPath := fmt.Sprintf("/import-batches/%s/members/%s/manual-review", id, first.ID)
	call("POST", manualPath, manual, 409)
	manual.ExpectedRevision = editor.Book.Revision
	call("POST", manualPath, manual, 200)
	call("POST", manualPath, manual, 409)
	reviews := []model.ImportReview{}
	for _, m := range members().Items {
		if m.ID == first.ID {
			continue
		}
		r := model.ImportReview{ID: m.ID, ExpectedRevision: m.Revision, Decision: "skip"}
		if m.ID == first.ID {
			if m.Candidate == nil {
				t.Fatal(m)
			}
			r.Decision = "accept"
			r.CandidateRevision = m.Candidate.Revision
		}
		reviews = append(reviews, r)
	}
	call("POST", "/import-batches/"+id+"/reviews", map[string]any{"items": reviews}, 200)
	json.Unmarshal(call("GET", "/import-batches/"+id, nil, 200), &batch)
	call("POST", "/import-batches/"+id+"/control/continue", map[string]any{"expected_revision": batch.Revision}, 200)
	wait("versions")
	var proposals rec.ProposalPage
	json.Unmarshal(call("GET", "/import-batches/"+id+"/work-proposals", nil, 200), &proposals)
	if len(proposals.Items) > 0 {
		p := proposals.Items[0]
		call("GET", "/import-batches/"+id+"/work-proposals/"+p.ID, nil, 200)
		call("GET", "/import-batches/"+id+"/work-proposals?alternatives=false&only_alternatives=true", nil, 422)
		call("POST", "/import-batches/"+id+"/work-proposals/"+p.ID+"/accept", rec.ProposalSelection{Revision: p.Revision, IDs: p.NewIDs}, 200)
	}
	completeOrganization := func() {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		var review model.OrganizationReview
		for {
			json.Unmarshal(call("GET", "/import-batches/"+id+"/organization-review", nil, 200), &review)
			if review.CanComplete {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("organization review did not settle", review)
			}
			time.Sleep(10 * time.Millisecond)
		}
		call("POST", "/import-batches/"+id+"/organization-review/complete", model.CompleteOrganizationReview{Stage: review.Stage, Revision: review.Revision, Token: strings.Repeat("0", 64)}, 409)
		call("POST", "/import-batches/"+id+"/organization-review/complete", model.CompleteOrganizationReview{Stage: review.Stage, Revision: review.Revision, Token: review.Token}, 200)
	}
	completeOrganization()
	wait("series")
	var series rec.SeriesProposalPage
	json.Unmarshal(call("GET", "/import-batches/"+id+"/series-proposals", nil, 200), &series)
	if len(series.Items) > 0 {
		p := series.Items[0]
		call("GET", "/import-batches/"+id+"/series-proposals/"+p.ID, nil, 200)
		call("GET", "/import-batches/"+id+"/series-proposals/"+p.ID+"/members", nil, 200)
		name := "匿名整批系列"
		call("POST", "/import-batches/"+id+"/series-proposals/"+p.ID+"/accept", rec.SeriesSelection{Revision: p.Revision, IDs: p.NewIDs, Name: &name, Customize: true, Order: p.Order}, 200)
	}
	completeOrganization()
	json.Unmarshal(call("GET", "/import-batches/"+id, nil, 200), &batch)
	if batch.State != "completed" {
		t.Fatal(batch)
	}
	if isEditor {
		var completed int
		if e := db.Read.QueryRow("SELECT count(*) FROM jobs j JOIN import_children c ON c.job_id=j.id WHERE c.batch_id=? AND j.type IN('work_recommendation','series_recommendation') AND j.state='completed'", id).Scan(&completed); e != nil || completed < 1 {
			t.Fatal("recommendation execution missing", completed, e)
		}
	}

	// A second identical scan is an empty batch: no network or recommendation child.
	create["request_key"] = "anonymous-empty-http"
	json.Unmarshal(call("POST", "/import-batches", create, 202), &batch)
	empty := batch.ID
	for deadline := time.Now().Add(5 * time.Second); ; {
		json.Unmarshal(call("GET", "/import-batches/"+empty, nil, 200), &batch)
		if batch.State == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(batch)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(batch.Jobs) != 1 {
		t.Fatal("empty batch started network jobs", batch.Jobs)
	}
}

func TestImportRetryCorruptFileKeepsFrozenMembershipAndOtherFetches(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	flow := importflow.New(db, h.catalog, h.logger, func() { h.catalog.Wake(); h.metadata.Wake() })
	h.WithImports(flow)
	h.metadata.Start(ctx)
	flow.Start(ctx)
	t.Cleanup(func() {
		shutdown, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		flow.Shutdown(shutdown)
		h.metadata.Shutdown(shutdown)
	})
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		w := request(t, h, method, "/api/v1"+path, string(raw), cookie, csrf)
		code(t, w, want)
		return w.Body.Bytes()
	}
	var lib struct {
		ID string `json:"id"`
	}
	json.Unmarshal(call("POST", "/libraries", map[string]string{"root_key": "fixture", "display_name": "匿名修复"}, 201), &lib)
	var b model.ImportBatch
	json.Unmarshal(call("POST", "/import-batches", map[string]any{"library_id": lib.ID, "request_key": "anonymous-repair-file", "allow_metadata_network": true}, 202), &b)
	id := b.ID
	wait := func() {
		t.Helper()
		for deadline := time.Now().Add(8 * time.Second); ; {
			json.Unmarshal(call("GET", "/import-batches/"+id, nil, 200), &b)
			if b.Stage == "metadata" && b.State == "review" {
				return
			}
			if b.State == "failed" || time.Now().After(deadline) {
				t.Fatal(b)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait()
	var members model.ImportPage[model.ImportMember]
	json.Unmarshal(call("GET", "/import-batches/"+id+"/members", nil, 200), &members)
	var broken model.ImportMember
	for _, v := range members.Items {
		if v.ScanError != "" {
			broken = v
		}
	}
	if broken.ID == "" {
		t.Fatal("missing corrupt fixture")
	}
	raw, e := os.ReadFile(filepath.Join(h.c.Roots[0].Path, "合成1.cbz"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(h.c.Roots[0].Path, broken.Filename), raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(h.c.Roots[0].Path, "later-not-in-batch.cbz"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	call("POST", "/import-batches/"+id+"/retry-files", map[string]any{"expected_revision": b.Revision, "edition_ids": []string{broken.ID}}, 202)
	wait()
	json.Unmarshal(call("GET", "/import-batches/"+id+"/members", nil, 200), &members)
	if members.Total != 3 {
		t.Fatal("retry added later files", members.Total)
	}
	for _, v := range members.Items {
		if v.ID == broken.ID && v.ScanError != "" {
			t.Fatal("repair not reflected", v)
		}
	}
	var n int
	e = db.Read.QueryRow("SELECT count(*) FROM job_items i JOIN import_children c ON c.job_id=i.job_id WHERE c.batch_id=? AND c.stage='metadata'", id).Scan(&n)
	if e != nil || n != 3 {
		t.Fatal("retry refetched previous members", n, e)
	}
}

func TestImportHTTPFailedRecommendationSkipHistoryAndAuthentication(t *testing.T) {
	h, cookie, csrf := metadataHandler(t)
	db := handlerDB(t, h)
	ctx := context.Background()
	if e := testfixture.SeedEdition(ctx, db.Write, "second", "lib", "second.cbz", "Anonymous second"); e != nil {
		t.Fatal(e)
	}
	imports := importflow.New(db, h.catalog, h.logger, func() {})
	h.WithImports(imports)
	if _, e := db.Write.Exec(`INSERT INTO import_batches(id,library_id,request_key,stage,state) VALUES('failed-import','lib','failed-import','series','processing');
 INSERT INTO import_child_requests(job_id,batch_id,stage) VALUES('failed-series','failed-import','series')`); e != nil {
		t.Fatal(e)
	}
	if e := db.MaterializeImport(ctx, model.ImportRequest{JobID: "failed-series", BatchID: "failed-import", LibraryID: "lib", Stage: "series"}); e != nil {
		t.Fatal(e)
	}
	j, e := db.Claim(ctx, "failed-series")
	if e != nil {
		t.Fatal(e)
	}
	if e = db.SaveSeriesDiagnostic(ctx, *j, "restore_checkpoint", "invalid_checkpoint"); e != nil {
		t.Fatal(e)
	}
	if e = db.FinishBackground(ctx, *j, "series_checkpoint_invalid"); e != nil {
		t.Fatal(e)
	}
	if e = db.ReconcileImports(ctx); e != nil {
		t.Fatal(e)
	}
	b, e := db.ImportBatch(ctx, "failed-import")
	if e != nil {
		t.Fatal(e)
	}
	base := "/api/v1/import-batches/failed-import"
	code(t, request(t, h, "POST", base+"/control/continue", fmt.Sprintf(`{"expected_revision":%d}`, b.Revision), cookie, csrf), 409)
	code(t, request(t, h, "POST", base+"/control/skip_recommendation", fmt.Sprintf(`{"expected_revision":%d}`, b.Revision), nil, ""), 401)
	code(t, request(t, h, "POST", base+"/control/skip_recommendation", fmt.Sprintf(`{"expected_revision":%d}`, b.Revision), cookie, "wrong"), 403)
	changed := request(t, h, "POST", base+"/control/skip_recommendation", fmt.Sprintf(`{"expected_revision":%d}`, b.Revision), cookie, csrf)
	code(t, changed, 200)
	var after model.ImportBatch
	if e = json.Unmarshal(changed.Body.Bytes(), &after); e != nil || after.State != "review" || len(after.SkippedStages) != 1 {
		t.Fatal(after, e)
	}
	w := request(t, h, "GET", "/api/v1/import-batches?library_id=lib&state=review&page_size=1", "", cookie, csrf)
	code(t, w, 200)
	var page model.ImportPage[model.ImportBatch]
	if e = json.Unmarshal(w.Body.Bytes(), &page); e != nil || page.Total != 1 || page.Items[0].TaskFailures[0].Phase != "restore_checkpoint" {
		t.Fatal(page, e)
	}
	code(t, request(t, h, "GET", "/api/v1/import-batches?state=invalid", "", cookie, csrf), 422)
	w = request(t, h, "GET", "/api/v1/libraries", "", cookie, csrf)
	code(t, w, 200)
	if !strings.Contains(w.Body.String(), `"edition_count":2`) || !strings.Contains(w.Body.String(), `"active_import":{"id":"failed-import"`) {
		t.Fatal(w.Body.String())
	}
	// A historical completion is never rewritten, but its failed child remains visible.
	if _, e = db.Write.Exec("UPDATE import_batches SET state='completed',stage='finished' WHERE id='failed-import'"); e != nil {
		t.Fatal(e)
	}
	w = request(t, h, "GET", base, "", cookie, csrf)
	code(t, w, 200)
	if e = json.Unmarshal(w.Body.Bytes(), &after); e != nil || after.State != "completed" || len(after.TaskFailures) != 1 {
		t.Fatal(after, e)
	}
}
