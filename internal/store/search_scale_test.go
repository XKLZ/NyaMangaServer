package store

import (
	"context"
	"encoding/json"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/search"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Explicit opt-in: generates only anonymous database rows, no comics or network.
func TestSearchScale(t *testing.T) {
	if os.Getenv("NYAMANGA_SEARCH_SCALE") != "1" {
		t.Skip("run make search-scale for isolated 30k index verification")
	}
	s, _ := orgFixture(t)
	ctx := context.Background()
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	statements := []string{
		`WITH RECURSIVE n(i) AS(VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<30000) INSERT INTO works(id,library_id,preferred_edition_id,revision) SELECT CASE WHEN i<=6000 THEN printf('scale-work-%04d',(i+1)/2) ELSE printf('i:scale-%05d',i) END,'lib',printf('scale-%05d',i),1 FROM n WHERE i>6000 OR i%2=1`,
		`WITH RECURSIVE n(i) AS(VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<30000) INSERT INTO editions(id,library_id,work_id,revision,organization_revision) SELECT printf('scale-%05d',i),'lib',CASE WHEN i<=6000 THEN printf('scale-work-%04d',(i+1)/2) ELSE printf('i:scale-%05d',i) END,1,1 FROM n`,
		`WITH RECURSIVE n(i) AS(VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<30000) INSERT INTO files(edition_id,library_id,relative_path,filename_title,size,mtime_ns,content_revision,representative_algorithm,page_count,file_state,indexed_at) SELECT printf('scale-%05d',i),'lib',printf('anonymous/%05d.cbz',i),printf('匿名第%d卷',i),1,1,1,'content-page8-v1',0,'available',i FROM n`,
		`INSERT INTO search_documents SELECT edition_id,nya_normalize_v1(filename_title),'',nya_natural_v1(filename_title) FROM files WHERE edition_id LIKE 'scale-%'`,
		`INSERT INTO tags VALUES('scale-tag','artist','anonymous')`,
		`INSERT INTO effective_edition_tags SELECT edition_id,'scale-tag' FROM files WHERE edition_id LIKE 'scale-%' AND indexed_at%3=0`,
		`WITH RECURSIVE n(i) AS(VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<100) INSERT INTO series SELECT printf('scale-series-%03d',i),'lib',printf('匿名系列%d',i),1 FROM n`,
		`INSERT INTO series_members SELECT printf('scale-series-%03d',(CAST(substr(id,12) AS INTEGER)-1)/30+1),id,(CAST(substr(id,12) AS INTEGER)-1)%30+1 FROM works WHERE id LIKE 'scale-work-%'`,
		`INSERT INTO edition_user_states SELECT 'a',edition_id,1,0,1 FROM files WHERE edition_id LIKE 'scale-%' AND indexed_at%7=0`,
	}

	for _, sql := range statements {
		if _, e = tx.ExecContext(ctx, sql); e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = checkOrganization(ctx, s.Read); e != nil {
		t.Fatal(e)
	}
	svc := search.New(s)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	type sample struct {
		Query, Collapse string
		Total           int
		Milliseconds    float64
	}
	samples := []sample{}
	for _, v := range []model.SearchInput{{Collapse: "none", Sort: "title", Order: "asc"}, {Query: "artist:anonymous", Collapse: "series"}, {Query: "第10", Collapse: "versions"}, {Scope: "favorites", Collapse: "series"}, {Query: "-artist:anonymous OR title:第2", Collapse: "series"}} {
		start := time.Now()
		r, e := svc.Search(ctx, "a", v)
		if e != nil || len(r.Items) > 24 {
			t.Fatal(r, e)
		}
		samples = append(samples, sample{v.Query, v.Collapse, r.Total, float64(time.Since(start).Microseconds()) / 1000})
	}
	v, node, e := search.Validate(model.SearchInput{Query: "artist:anonymous", Collapse: "series"})
	if e != nil {
		t.Fatal(e)
	}
	cte, args := searchCTE("a", v, node)
	rows, e := s.Read.QueryContext(ctx, "EXPLAIN QUERY PLAN "+cte+"SELECT count(*) FROM keys", args...)
	if e != nil {
		t.Fatal(e)
	}
	plan := []string{}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if e = rows.Scan(&id, &parent, &unused, &detail); e != nil {
			t.Fatal(e)
		}
		plan = append(plan, detail)
	}
	rows.Close()
	var wg sync.WaitGroup
	writerErrors := make(chan error, 1)
	wg.Go(func() {
		for i := 0; i < 20; i++ {
			_, e := s.Write.ExecContext(ctx, "UPDATE files SET file_state=CASE WHEN file_state='available' THEN 'missing' ELSE 'available' END WHERE edition_id='scale-00001'")
			if e != nil {
				writerErrors <- e
				return
			}
		}
	})
	for i := 0; i < 10; i++ {
		r, e := svc.Search(ctx, "a", model.SearchInput{Query: `title:"匿名第1卷"`, FileState: "available", Collapse: "none"})
		if e != nil || r.Total > 1 || r.Total != len(r.Items) {
			t.Fatal(r.Total, e)
		}
		for _, hit := range r.Items {
			if hit.Representative.State != "available" {
				t.Fatal("mixed snapshots")
			}
		}
	}
	wg.Wait()
	select {
	case e := <-writerErrors:
		t.Fatal(e)
	default:
	}
	adminStart := time.Now()
	adminResult, e := svc.Admin(ctx, "a", model.SearchInput{PageSize: 50, Visibility: "active", IncludeDisabled: true})
	if e != nil || len(adminResult.Items) != 50 || adminResult.Total != 30004 {
		t.Fatal("admin 30k paging", len(adminResult.Items), adminResult.Total, e)
	}
	adminDuration := float64(time.Since(adminStart).Microseconds()) / 1000
	// A 1000-group series must remain a tiny summary, with members fetched separately.
	if _, e = s.Write.ExecContext(ctx, `CREATE TEMP TABLE scale_old_members AS SELECT * FROM series_members;
 DELETE FROM series_members;
 INSERT INTO series VALUES('admin-large','lib','Anonymous large series',1);
 INSERT INTO series_members SELECT 'admin-large',id,row_number() OVER(ORDER BY id) FROM works WHERE id LIKE 'scale-work-%' AND CAST(substr(id,12) AS INTEGER)<=1000;
 INSERT INTO series_members SELECT series_id,work_id,row_number() OVER(PARTITION BY series_id ORDER BY position) FROM scale_old_members WHERE CAST(substr(work_id,12) AS INTEGER)>1000;
 DROP TABLE scale_old_members;`); e != nil {
		t.Fatal(e)
	}
	summaryStart := time.Now()
	summary, e := s.SeriesDetail(ctx, "admin-large")
	if e != nil || len(summary.WorkIDs) != 0 || summary.MemberCount != 1000 {
		t.Fatal("summary eagerly expanded", summary, e)
	}
	summaryBytes, _ := json.Marshal(summary)
	summaryDuration := float64(time.Since(summaryStart).Microseconds()) / 1000
	if len(summaryBytes) > 4096 {
		t.Fatal("summary response unbounded", len(summaryBytes))
	}
	members, e := s.SeriesWorkPage(ctx, "admin-large", "", 1, 50)
	if e != nil || len(members.Items) != 50 || members.Total != 1000 {
		t.Fatal(members, e)
	}
	runtime.ReadMemStats(&after)
	report := map[string]any{"rows": 30000, "admin_page_size": 50, "admin_milliseconds": adminDuration, "large_series_groups": 1000, "summary_bytes": len(summaryBytes), "summary_milliseconds": summaryDuration, "samples": samples, "query_plan": plan, "heap_alloc_bytes": after.HeapAlloc, "total_alloc_delta_bytes": after.TotalAlloc - before.TotalAlloc, "concurrent_write_reads": 10, "platform": runtime.GOOS + "/" + runtime.GOARCH, "nas_performance_verified": false}
	raw, _ := json.MarshalIndent(report, "", "  ")
	if output := os.Getenv("NYAMANGA_SCALE_REPORT"); output != "" {
		if e = os.WriteFile(output, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	fmt.Println(string(raw))
}
