package store

import (
	"context"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func TestWorkRepresentativeTitlesAreReadOnlyAndFollowPreferred(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	if _, e := s.Write.Exec(`INSERT INTO effective_editions(edition_id,title,fields_json) VALUES('book','Translated','{"title":"Translated","title_original":"[Group] 原文"}')`); e != nil {
		t.Fatal(e)
	}
	before, e := s.Work(ctx, "i:book")
	if e != nil {
		t.Fatal(e)
	}
	changeWork(t, o, model.WorkCommand{Action: "work_name", ID: "i:book", NameOp: "set", Name: "Manual name"})
	w, e := s.Work(ctx, "i:book")
	if e != nil || w.Name != "Manual name" || w.Title != "Translated" || w.TitleOriginal != "[Group] 原文" {
		t.Fatal(w, e)
	}
	page, _, e := s.WorkPage(ctx, "lib", "", 1, 100)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, v := range page {
		if v.ID == w.ID {
			found = true
			if v.TitleOriginal != w.TitleOriginal || v.Title != w.Title {
				t.Fatal(v)
			}
		}
	}
	if !found {
		t.Fatal("missing Work")
	}
	g, e := s.WorkContext(ctx, "a", nil, []string{w.ID}, nil)
	if e != nil || g.Works[0].TitleOriginal != w.TitleOriginal {
		t.Fatal(g, e)
	}
	if w.Revision != before.Revision+1 {
		t.Fatal("read changed revision", w)
	}
	changeWork(t, o, model.WorkCommand{Action: "work_merge", ID: w.ID, WorkIDs: []string{"i:b2"}})
	changeWork(t, o, model.WorkCommand{Action: "work_preferred", ID: w.ID, PreferredEditionID: "b2"})
	w, e = s.Work(ctx, w.ID)
	if e != nil || w.Name != "Manual name" || w.Title != "第2卷" || w.TitleOriginal != "" {
		t.Fatal(w, e)
	}
}

func TestSeriesProposalMembersIncludeRepresentativeTitles(t *testing.T) {
	s := discoveryStore(t, 4)
	ctx := context.Background()
	_, plans := finishSeries(t, s, "titles", [][]int{{0, 1, 2}})
	p := plans[0]
	first, e := s.Work(ctx, p.Order[0])
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec(`INSERT INTO effective_editions(edition_id,title,fields_json) VALUES(?,'Translation','{"title":"Translation","title_original":"[Circle] 原作"}') ON CONFLICT(edition_id) DO UPDATE SET title=excluded.title,fields_json=excluded.fields_json`, first.PreferredEditionID); e != nil {
		t.Fatal(e)
	}
	members, e := s.SeriesMembers(ctx, p.ID, 1, 1)
	if e != nil || len(members.Items) != 1 || members.Items[0].TitleOriginal != "[Circle] 原作" || members.Items[0].Title != "Translation" || members.Revision != p.Revision {
		t.Fatal(members, e)
	}
	again, e := s.SeriesMembers(ctx, p.ID, 2, 1)
	if e != nil || len(again.Items) != 1 || again.Items[0].Title == "" || again.Items[0].TitleOriginal != "" {
		t.Fatal(again, e)
	}
}
