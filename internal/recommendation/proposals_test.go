package recommendation

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"slices"
	"testing"
)

func proposalFixture(ids ...string) (Pool, model.WorkContext, PairGraph) {
	p := Pool{}
	c := model.WorkContext{}
	g := PairGraph{IDs: ids, Hard: map[int][]byte{}}
	for i, id := range ids {
		p.Editions = append(p.Editions, InputEdition{Book: BookInput{ID: id, Title: id, LibraryID: "lib", Languages: []string{}}, WorkID: "w" + id, IndexedAt: int64(i + 1)})
		c.Editions = append(c.Editions, model.WorkEdition{ID: id, WorkID: "w" + id, LibraryID: "lib", Title: id, Revision: 1})
		c.Works = append(c.Works, model.Work{ID: "w" + id, LibraryID: "lib", EditionIDs: []string{id}, PreferredEditionID: id, Revision: 1, Name: id})
	}
	return p, c, g
}
func pair(a, b int, tier string) PairEvidence {
	score := .9
	return PairEvidence{A: a, B: b, Tier: tier, Score: &score, Signal: []byte(`{"risks":[]}`)}
}
func TestProposalsAreCliquesNotConnectedComponents(t *testing.T) {
	p, c, g := proposalFixture("a", "b", "c")
	g.Edges = []PairEvidence{pair(0, 1, "strong"), pair(1, 2, "strong")}
	out, e := BuildProposals(p, c, g)
	if e != nil || len(out) != 2 {
		t.Fatal(out, e)
	}
	primary := 0
	for _, v := range out {
		if len(v.NewIDs) != 2 {
			t.Fatal("chained", v)
		}
		if v.Primary {
			primary++
		}
	}
	if primary != 1 {
		t.Fatal(primary)
	}
	g.Edges = append(g.Edges, pair(0, 2, "strong"))
	out, e = BuildProposals(p, c, g)
	if e != nil || len(out) != 1 || len(out[0].NewIDs) != 3 {
		t.Fatal(out, e)
	}
}
func TestAddUsesBestVersionAndAllTargetHardConflicts(t *testing.T) {
	p, c, g := proposalFixture("a", "b", "c")
	c.Works = c.Works[:2]
	c.Works[1].EditionIDs = []string{"b", "c"}
	c.Works[1].PreferredEditionID = "b"
	c.Editions[2].WorkID = "wb"
	p.Editions[2].WorkID = "wb"
	g.Edges = []PairEvidence{pair(0, 2, "strong")}
	out, e := BuildProposals(p, c, g)
	if e != nil || len(out) != 1 || out[0].Kind != "add_to_work" || out[0].Tier != "strong" {
		t.Fatal(out, e)
	}
	key := out[0].Key
	g.Hard[0] = []byte{1}
	out, e = BuildProposals(p, c, g)
	if e != nil || out[0].Tier != "supplementary" {
		t.Fatal(out, e)
	}
	p.Editions[1].Book.Title = "changed"
	out, e = BuildProposals(p, c, g)
	if e != nil || out[0].Key == key {
		t.Fatal("target input omitted", out, e)
	}
}
func TestActualMultiWithRecycledMemberIsNeverSingleton(t *testing.T) {
	p, c, g := proposalFixture("a", "b", "c")
	c.Works = c.Works[:2]
	c.Works[1].EditionIDs = []string{"b", "c"}
	c.Editions[2].WorkID = "wb"
	p.Editions = p.Editions[:2]
	g.IDs = []string{"a", "b"}
	g.Edges = []PairEvidence{pair(0, 1, "strong")}
	out, e := BuildProposals(p, c, g)
	if e != nil || len(out) != 1 || out[0].Kind != "add_to_work" {
		t.Fatal(out, e)
	}
	if !slices.ContainsFunc(out[0].Members, func(m ProposalMember) bool { return m.ID == "c" && m.Role == "existing" }) {
		t.Fatal(out)
	}
}
func TestEvidenceIdentityIgnoresOrganizationAndPersonalLabels(t *testing.T) {
	a := InputEdition{Book: BookInput{ID: "a", Title: "same"}, WorkID: "old", Revision: 1, OrganizationRevision: 1}
	b := a
	b.WorkID = "new"
	b.Revision = 20
	b.OrganizationRevision = 30
	b.Book.CustomLabels = []string{"manual"}
	if EvidenceFingerprint(a) != EvidenceFingerprint(b) {
		t.Fatal("organization invalidated pair")
	}
	b.Book.Title = "different"
	if EvidenceFingerprint(a) == EvidenceFingerprint(b) {
		t.Fatal("title change lost")
	}
}

func TestProposalComputationHonorsCancellation(t *testing.T) {
	p, c, g := proposalFixture("a", "b")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := BuildProposalsContext(ctx, p, c, g); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
