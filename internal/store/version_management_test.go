package store

import (
	"context"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	rec "nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/migrations"
	"reflect"
	"testing"
	"testing/fstest"
)

func TestVersionManagementLabelsOwnershipAndAtomicOrganization(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 3)
	for i, id := range []string{"e0000", "e0001"} {
		color := `"color"`
		if i == 1 {
			color = `"monochrome"`
		}
		p := model.BookProperties{ColorMode: model.Property{Value: json.RawMessage(color)}}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, e := s.Write.Exec(`INSERT OR REPLACE INTO effective_edition_properties VALUES(?,NULL,NULL,NULL,NULL,1,?)`, id, string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	finishDiscovery(t, s, "labels", []rec.PairEvidence{evidence(0, 1)})
	plans, e := s.WorkProposals(ctx, "", "lib", "", "pending", true, 1, 25)
	if e != nil || len(plans.Items) != 1 {
		t.Fatal(plans, e)
	}
	p := plans.Items[0]
	if _, e = s.DecideWorkProposal(ctx, "account", p.ID, "accept", rec.ProposalSelection{Revision: p.Revision, IDs: p.NewIDs}); e != nil {
		t.Fatal(e)
	}
	members, e := s.WorkEditionPage(ctx, "account", p.Target, "", 1, 100)
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range members.Items {
		if len(m.Labels) != 1 || m.LabelState.Mode != "auto" {
			raw, err := json.Marshal(m.Properties)
			t.Fatalf("automatic labels not saved: %+v properties=%s policy=%+v error=%v color=%+v", m, raw, m.LabelState, err, m.Properties.ColorMode)
		}
	}

	accepted, e := s.WorkProposal(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	manualContext, e := s.WorkContext(ctx, "account", nil, []string{p.Target}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeWork(ctx, "account", model.WorkCommand{Action: "edition_labels", EditionID: "e0000", Labels: []string{"Preserved manual"}, Expected: manualContext.Expected}, false, organization.PlanWork); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DecideWorkProposal(ctx, "account", p.ID, "undo", rec.ProposalSelection{Revision: accepted.Revision}); e != nil {
		t.Fatal(e)
	}
	var preserved string
	if e = s.Read.QueryRow("SELECT label FROM edition_version_labels WHERE edition_id='e0000'").Scan(&preserved); e != nil || preserved != "Preserved manual" {
		t.Fatal("undo replaced intervening manual labels", preserved, e)
	}
	var autoCount int
	if e = s.Read.QueryRow("SELECT count(*) FROM edition_automatic_labels").Scan(&autoCount); e != nil || autoCount != 0 {
		t.Fatal("undo retained group-only labels", autoCount, e)
	}
	all, e := s.WorkContext(ctx, "account", []string{"e0000", "e0001"}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	source := ""
	for _, w := range all.Works {
		if w.ID != p.Target {
			source = w.ID
		}
	}
	if _, e = s.ChangeWork(ctx, "account", model.WorkCommand{Action: "work_merge", ID: p.Target, WorkIDs: []string{source}, Expected: all.Expected}, false, organization.PlanWork); e != nil {
		t.Fatal(e)
	}
	c, e := s.WorkContext(ctx, "account", nil, []string{p.Target}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeWork(ctx, "account", model.WorkCommand{Action: "edition_labels", EditionID: "e0000", Labels: []string{}, Expected: c.Expected}, false, organization.PlanWork); e != nil {
		t.Fatal(e)
	}
	members, e = s.WorkEditionPage(ctx, "account", p.Target, "", 1, 100)
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range members.Items {
		if m.ID == "e0000" && (len(m.Labels) != 0 || m.LabelState.Mode != "manual") {
			t.Fatal("explicit empty lost", m)
		}
	}
	c, e = s.WorkContext(ctx, "account", nil, []string{p.Target}, nil)
	if e != nil {
		t.Fatal(e)
	}
	generated, e := s.GenerateVersionLabels(ctx, "account", p.Target, c.Expected, nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range generated.Items {
		if m.ID == "e0000" && len(m.Labels) != 0 {
			t.Fatal("generation replaced manual empty")
		}
	}
	generated, e = s.GenerateVersionLabels(ctx, "account", p.Target, c.Expected, []string{"e0000"})
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range generated.Items {
		if m.ID == "e0000" && (m.LabelState.Mode != "auto" || len(m.Labels) != 1) {
			t.Fatal("restore auto failed", m)
		}
	}
	if _, e = s.GenerateVersionLabels(ctx, "account", p.Target, map[string]int64{"work:" + p.Target: 999}, nil); !errors.Is(e, model.ErrConflict) {
		t.Fatal("stale generation accepted", e)
	}
	multi, n, e := s.WorkPage(ctx, "lib", "", 1, 25, 2)
	if e != nil || n != 1 || len(multi) != 1 {
		t.Fatal(multi, n, e)
	}
	filtered, e := s.WorkProposals(ctx, "", "lib", "", "undone", true, 1, 25, p.Target)
	if e != nil || filtered.Total != 1 {
		t.Fatal(filtered, e)
	}
}

func TestVersionManagementMigrationPreservesTenAndRollsBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	ten := schemaNine(t)
	raw, _ := migrations.Files.ReadFile("010_series_discovery.sql")
	ten["010_series_discovery.sql"] = &fstest.MapFile{Data: raw}
	s, e := Open(ctx, dir, ten)
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, s)
	if _, e = s.Write.Exec("INSERT INTO edition_version_labels VALUES('book',0,'译本')"); e != nil {
		t.Fatal(e)
	}
	before := dumpRows(t, s, "SELECT * FROM editions ORDER BY id")
	s.Close()
	broken := ten
	raw, _ = migrations.Files.ReadFile("011_version_management.sql")
	broken["011_version_management.sql"] = &fstest.MapFile{Data: append(append([]byte{}, raw...), []byte("\nINSERT INTO edition_label_policy VALUES('missing','auto',1,'',0);")...)}
	if db, e := Open(ctx, dir, broken); e == nil {
		db.Close()
		t.Fatal("broken migration accepted")
	}
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.SchemaVersion() != 30 || !reflect.DeepEqual(before, dumpRows(t, s, "SELECT * FROM editions ORDER BY id")) {
		t.Fatal("old Editions changed")
	}
	var mode, label string
	if e = s.Read.QueryRow("SELECT mode FROM edition_label_policy WHERE edition_id='book'").Scan(&mode); e != nil || mode != "manual" {
		t.Fatal(mode, e)
	}
	if e = s.Read.QueryRow("SELECT label FROM edition_version_labels WHERE edition_id='book'").Scan(&label); e != nil || label != "译本" {
		t.Fatal(label, e)
	}
	var n int
	s.Read.QueryRow("SELECT count(*) FROM edition_automatic_labels").Scan(&n)
	if n != 0 {
		t.Fatal("migration generated labels")
	}
}

func TestVersionManagementPropertiesSplitAndPurgeMaintainOnlyAutomaticLabels(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 3)
	setColor := func(id, color string) {
		t.Helper()
		var revision int64
		if e := s.Read.QueryRow("SELECT revision FROM editions WHERE id=?", id).Scan(&revision); e != nil {
			t.Fatal(e)
		}
		if e := s.ApplyOverrides(ctx, id, revision, map[string]model.Override{"color_mode": {Op: "set", Value: json.RawMessage(`"` + color + `"`)}}); e != nil {
			t.Fatal(e)
		}
	}
	setColor("e0000", "color")
	setColor("e0001", "monochrome")
	change := func(c model.WorkCommand) model.WorkResult {
		t.Helper()
		g, e := s.WorkContext(ctx, "account", nil, []string{"i:e0000", "i:e0001"}, nil)
		if e != nil {
			t.Fatal(e)
		}
		c.Expected = g.Expected
		r, e := s.ChangeWork(ctx, "account", c, false, organization.PlanWork)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	change(model.WorkCommand{Action: "work_merge", ID: "i:e0000", WorkIDs: []string{"i:e0001"}})
	read := func() model.WorkEditionPage {
		t.Helper()
		p, e := s.WorkEditionPage(ctx, "account", "i:e0000", "", 1, 100)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	before := read()
	if len(before.Items[0].Labels) != 1 {
		t.Fatal(before)
	}
	setColor("e0001", "color")
	p := read()
	for _, m := range p.Items {
		if !equalStrings(m.Labels, []string{"彩色"}) {
			t.Fatal("reliable shared property was suppressed", m)
		}
	}
	g, e := s.WorkContext(ctx, "account", nil, []string{"i:e0000"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeWork(ctx, "account", model.WorkCommand{Action: "edition_labels", EditionID: "e0000", Labels: []string{"Manual custom"}, Expected: g.Expected}, false, organization.PlanWork); e != nil {
		t.Fatal(e)
	}
	setColor("e0001", "monochrome")
	p = read()
	for _, m := range p.Items {
		if m.ID == "e0000" && (!reflect.DeepEqual(m.Labels, []string{"Manual custom"}) || m.LabelState.Mode != "manual") {
			t.Fatal("property refresh replaced manual", m)
		}
	}
	g, e = s.WorkContext(ctx, "account", nil, []string{"i:e0000"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	split, e := s.ChangeWork(ctx, "account", model.WorkCommand{Action: "work_split", ID: "i:e0000", NewWorkID: "split-anonymous", EditionIDs: []string{"e0001"}, Expected: g.Expected}, false, organization.PlanWork)
	if e != nil {
		t.Fatal(e)
	}
	lone, e := s.WorkEditionPage(ctx, "account", split.ID, "", 1, 100)
	if e != nil || len(lone.Items) != 1 || len(lone.Items[0].Labels) != 0 {
		t.Fatal("split retained unnecessary automatic labels", lone, e)
	}
	g, e = s.WorkContext(ctx, "account", nil, []string{"i:e0000", split.ID}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ChangeWork(ctx, "account", model.WorkCommand{Action: "work_merge", ID: "i:e0000", WorkIDs: []string{split.ID}, Expected: g.Expected}, false, organization.PlanWork); e != nil {
		t.Fatal(e)
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.Exec("UPDATE editions SET trashed_at=1 WHERE id='e0001'"); e != nil {
		t.Fatal(e)
	}
	if e = purgeBook(ctx, tx, "e0001"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	p = read()
	if p.Total != 1 || !reflect.DeepEqual(p.Items[0].Labels, []string{"Manual custom"}) {
		t.Fatal("purge altered manual or retained dead auto", p)
	}
	var n int
	if e = s.Read.QueryRow("SELECT count(*) FROM edition_automatic_labels").Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}

func TestVersionLabelsUnknownIsNoopAndReliableTagsArePublished(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 2)
	c, e := s.WorkContext(ctx, "account", []string{"e0000", "e0001"}, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.ChangeWork(ctx, "account", model.WorkCommand{Action: "work_merge", ID: "i:e0000", WorkIDs: []string{"i:e0001"}, Expected: c.Expected}, false, organization.PlanWork)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec(`INSERT INTO edition_automatic_labels VALUES('e0000',0,'other','其他')`); e != nil {
		t.Fatal(e)
	}
	c, _ = s.WorkContext(ctx, "account", nil, []string{"i:e0000"}, nil)
	if _, e = s.GenerateVersionLabels(ctx, "account", "i:e0000", c.Expected, nil); e != nil {
		t.Fatal(e)
	}
	var label string
	if e = s.Read.QueryRow("SELECT label FROM edition_automatic_labels WHERE edition_id='e0000'").Scan(&label); e != nil || label != "其他" {
		t.Fatal(label, e)
	}
	// Full saved titles and tags are input; not the shortened UI display title.
	if _, e = s.Write.Exec(`INSERT OR REPLACE INTO effective_editions(edition_id,title,fields_json) VALUES('e0000','Anonymous','{"title_original":"[DL] Anonymous","tags":[{"namespace":"language","name":"translated"}]}')`); e != nil {
		t.Fatal(e)
	}
	c, _ = s.WorkContext(ctx, "account", nil, []string{"i:e0000"}, nil)
	page, e := s.GenerateVersionLabels(ctx, "account", "i:e0000", c.Expected, nil)
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range page.Items {
		if m.ID == "e0000" && (!equalStrings(m.Labels, []string{"译本", "DL"}) || m.LabelState.Mode != "auto") {
			t.Fatal(m)
		}
	}
}

func TestVersionLabelPreviewAndStoredLabelsShareRules(t *testing.T) {
	ctx := context.Background()
	s := discoveryStore(t, 2)
	if _, e := s.Write.Exec(`INSERT OR REPLACE INTO effective_editions VALUES('e0000','Anonymous','{"title":"Anonymous","tags":[{"namespace":"language","name":"translated"},{"namespace":"other","name":"digital"}]}')`); e != nil {
		t.Fatal(e)
	}
	finishDiscovery(t, s, "preview-labels", []rec.PairEvidence{evidence(0, 1)})
	page, e := s.WorkProposals(ctx, "preview-labels", "lib", "", "pending", true, 1, 25)
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	p := page.Items[0]
	var preview []string
	for _, m := range p.Members {
		if m.ID == "e0000" {
			for _, v := range m.Suggested.Presets {
				preview = append(preview, v.Label)
			}
		}
	}
	if !equalStrings(preview, []string{"译本", "DL"}) {
		t.Fatal(preview)
	}
	detail, e := s.WorkProposal(ctx, p.ID)
	if e != nil || detail.Revision != p.Revision {
		t.Fatal(detail, e)
	}
	if _, e = s.DecideWorkProposal(ctx, "account", p.ID, "accept", rec.ProposalSelection{Revision: p.Revision, IDs: p.NewIDs}); e != nil {
		t.Fatal(e)
	}
	members, e := s.WorkEditionPage(ctx, "account", p.Target, "", 1, 100)
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range members.Items {
		if m.ID == "e0000" && !equalStrings(m.Labels, preview) {
			t.Fatal("preview/write mismatch", m.Labels, preview)
		}
	}
}
