package recommendation

import (
	"cmp"
	"context"
	"encoding/json"
	"math"
	"nyamangaserver.local/server/internal/model"
	"slices"
	"strings"
)

type graphLookup struct {
	positions map[string]int
	edges     map[[2]int]PairEvidence
	hard      map[int][]byte
}

func lookup(g PairGraph) graphLookup {
	v := graphLookup{map[string]int{}, map[[2]int]PairEvidence{}, g.Hard}
	for i, id := range g.IDs {
		v.positions[id] = i
	}
	for _, e := range g.Edges {
		v.edges[[2]int{e.A, e.B}] = e
	}
	return v
}
func (g graphLookup) edge(a, b string) (PairEvidence, bool) {
	x, ok := g.positions[a]
	y, ok2 := g.positions[b]
	if !ok || !ok2 {
		return PairEvidence{}, false
	}
	if x > y {
		x, y = y, x
	}
	e, ok := g.edges[[2]int{x, y}]
	return e, ok
}
func (g graphLookup) conflict(a, b string) bool {
	x, ok := g.positions[a]
	y, ok2 := g.positions[b]
	if !ok || !ok2 {
		return false
	}
	if x > y {
		x, y = y, x
	}
	n := y - x - 1
	bits := g.hard[x]
	return n >= 0 && n/8 < len(bits) && bits[n/8]&(1<<uint(n%8)) != 0
}

// maximalCliques enumerates pairwise-qualified sets, never connected components.
// Bounded search fails explicitly rather than silently losing alternatives.
func maximalCliques(ctx context.Context, nodes []string, g graphLookup) ([][]string, error) {
	out := [][]string{}
	steps := 0
	var visit func([]string, []string, []string) error
	visit = func(r, p, x []string) error {
		steps++
		if e := ctx.Err(); e != nil {
			return e
		}
		if steps > 2_000_000 || len(out) > MaxDiscoveryProposals {
			return model.ErrCapacity
		}
		if len(r) > 100 {
			return model.ErrCapacity
		}
		if len(p) == 0 && len(x) == 0 {
			if len(r) > 0 {
				out = append(out, slices.Clone(r))
			}
			return nil
		}
		// Pivot limits complete-neighborhood branching while preserving maximal sets.
		pivot := ""
		degree := -1
		for _, u := range append(slices.Clone(p), x...) {
			n := 0
			for _, v := range p {
				if _, ok := g.edge(u, v); ok {
					n++
				}
			}
			if n > degree {
				degree = n
				pivot = u
			}
		}
		candidates := []string{}
		for _, v := range p {
			if _, ok := g.edge(pivot, v); !ok {
				candidates = append(candidates, v)
			}
		}
		for _, v := range candidates {
			np, nx := []string{}, []string{}
			for _, u := range p {
				if _, ok := g.edge(v, u); ok {
					np = append(np, u)
				}
			}
			for _, u := range x {
				if _, ok := g.edge(v, u); ok {
					nx = append(nx, u)
				}
			}
			if e := visit(append(slices.Clone(r), v), np, nx); e != nil {
				return e
			}
			p = slices.DeleteFunc(p, func(s string) bool { return s == v })
			x = append(x, v)
		}
		return nil
	}
	if e := visit(nil, slices.Clone(nodes), nil); e != nil {
		return nil, e
	}
	return out, nil
}
func ProposalKey(kind, target string, ids []string, evidence map[string]string) string {
	v := slices.Clone(ids)
	slices.Sort(v)
	inputs := []string{EvidenceVersion, kind, target}
	for _, id := range v {
		inputs = append(inputs, id, evidence[id])
	}
	return Fingerprint(inputs)
}

// BuildProposals is independent of SQL/HTTP. Work identity comes from the actual
// full organization context, so a recycled member cannot turn a multi into singleton.
func BuildProposals(pool Pool, c model.WorkContext, graph PairGraph) ([]Proposal, error) {
	return BuildProposalsContext(context.Background(), pool, c, graph)
}
func BuildProposalsContext(ctx context.Context, pool Pool, context model.WorkContext, graph PairGraph) ([]Proposal, error) {
	g := lookup(graph)
	books := map[string]InputEdition{}
	for _, v := range pool.Editions {
		books[v.Book.ID] = v
	}
	works := map[string]model.Work{}
	editions := map[string]model.WorkEdition{}
	singles := []string{}
	multi := []model.Work{}
	for _, ed := range context.Editions {
		editions[ed.ID] = ed
	}
	for _, w := range context.Works {
		works[w.ID] = w
		if len(w.EditionIDs) == 1 {
			if _, ok := books[w.EditionIDs[0]]; ok {
				singles = append(singles, w.EditionIDs[0])
			}
		} else if len(w.EditionIDs) > 1 {
			multi = append(multi, w)
		}
	}
	slices.Sort(singles)
	plans := []Proposal{}
	makePlan := func(kind string, ids []string, target model.Work) Proposal {
		slices.Sort(ids)
		p := Proposal{Kind: kind, Target: target.ID, TargetName: target.Name, Tier: "strong", Members: []ProposalMember{}, NewIDs: slices.Clone(ids), Risks: []string{}, Evidence: map[string]string{}, Expected: map[string]int64{}, State: "pending", Revision: 1}
		all := slices.Clone(ids)
		if kind == "add_to_work" {
			all = append(all, target.EditionIDs...)
		}
		score := math.Inf(1)
		hasScore := false
		series := map[string]bool{}
		for _, id := range all {
			ed := editions[id]
			w := works[ed.WorkID]
			role := "new"
			if kind == "add_to_work" && w.ID == target.ID {
				role = "existing"
			}
			b, ok := books[id]
			langs := []string{}
			if ok {
				p.Evidence[id] = EvidenceFingerprint(b)
				langs = b.Book.Languages
			}
			tags := []model.TagName{}
			for _, t := range b.Book.Tags {
				tags = append(tags, model.TagName{Namespace: t[0], Name: t[1]})
			}
			p.Members = append(p.Members, ProposalMember{Tags: tags, ID: id, WorkID: w.ID, Title: ed.Title, TitleOriginal: b.Book.TitleOriginal, CoverRevision: b.CoverRevision, Properties: CompactProperties(b), Languages: langs, Labels: ed.Labels, ContentRevision: ed.ContentRevision, Role: role})
			p.Expected["edition:"+id] = ed.Revision
			p.Expected["work:"+w.ID] = w.Revision
			if w.SeriesID != "" {
				series[w.SeriesID] = true
				for _, s := range context.Series {
					if s.ID == w.SeriesID {
						p.Expected["series:"+s.ID] = s.Revision
					}
				}
			}
		}
		for _, a := range ids {
			bestTier := ""
			bestScore := -2.
			if kind == "add_to_work" {
				for _, b := range target.EditionIDs {
					if edge, ok := g.edge(a, b); ok {
						if edge.Tier == "strong" {
							bestTier = "strong"
						} else if bestTier == "" {
							bestTier = "supplementary"
						}
						if edge.Score != nil {
							bestScore = max(bestScore, *edge.Score)
						}
					}
				}
				if bestTier != "strong" {
					p.Tier = "supplementary"
				}
				if bestScore > -2 {
					score = min(score, bestScore)
					hasScore = true
				}
			}
		}
		for i, a := range all {
			for _, b := range all[i+1:] {
				aTarget := kind == "add_to_work" && editions[a].WorkID == target.ID
				bTarget := kind == "add_to_work" && editions[b].WorkID == target.ID
				if aTarget && bTarget {
					continue
				}
				edge, ok := g.edge(a, b)
				if ok {
					if !aTarget && !bTarget {
						if edge.Tier != "strong" {
							p.Tier = "supplementary"
						}
						if edge.Score != nil {
							score = min(score, *edge.Score)
							hasScore = true
						}
					}
					var signal struct {
						Risks []string `json:"risks"`
					}
					_ = json.Unmarshal(edge.Signal, &signal)
					p.Risks = append(p.Risks, signal.Risks...)
				}
				if g.conflict(a, b) {
					p.Risks = append(p.Risks, "explicit_member_conflict")
					p.Tier = "supplementary"
				}
			}
		}
		if len(all) > 100 {
			p.Blocked = true
			p.Risks = append(p.Risks, "target_work_capacity")
		}
		if hasScore {
			p.Score = &score
		}
		if len(series) > 1 {
			p.Blocked = true
			p.Risks = append(p.Risks, "cross_series_requires_manual_organization")
		}
		slices.Sort(p.Risks)
		p.Risks = slices.Compact(p.Risks)
		SuggestPresets(p.Members)
		p.Key = ProposalKey(kind, p.Target, all, p.Evidence)
		return p
	}
	cliques, e := maximalCliques(ctx, singles, g)
	if e != nil {
		return nil, e
	}
	for _, ids := range cliques {
		if len(ids) < 2 {
			continue
		}
		retained := ids[0]
		for _, id := range ids[1:] {
			a, b := books[id], books[retained]
			if a.Book.ID != "" && (a.IndexedAt < b.IndexedAt || a.IndexedAt == b.IndexedAt && a.WorkID < b.WorkID) {
				retained = id
			}
		}
		plans = append(plans, makePlan("create_work", ids, works[editions[retained].WorkID]))
	}
	for _, target := range multi {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		changed := false
		for _, member := range target.EditionIDs {
			if _, was := g.positions[member]; was {
				if _, available := books[member]; !available {
					changed = true
				}
			}
		}
		if changed {
			continue
		}

		eligible := []string{}
		for _, id := range singles {
			for _, member := range target.EditionIDs {
				if _, ok := g.edge(id, member); ok {
					eligible = append(eligible, id)
					break
				}
			}
		}
		sets, e := maximalCliques(ctx, eligible, g)
		if e != nil {
			return nil, e
		}
		for _, ids := range sets {

			plans = append(plans, makePlan("add_to_work", ids, target))
		}
	}
	if len(plans) > MaxDiscoveryProposals {
		return nil, model.ErrCapacity
	}
	SortProposals(plans)
	taken := map[string]bool{}
	for i := range plans {
		p := &plans[i]
		p.Primary = !p.Blocked
		for _, id := range p.NewIDs {
			if taken[id] {
				p.Primary = false
			}
		}
		if p.Primary {
			for _, id := range p.NewIDs {
				taken[id] = true
			}
		}
	}
	return plans, nil
}
func SortProposals(plans []Proposal) {
	slices.SortFunc(plans, func(a, b Proposal) int {
		if a.Tier != b.Tier {
			if a.Tier == "strong" {
				return -1
			}
			return 1
		}
		av, bv := -2., -2.
		if a.Score != nil {
			av = *a.Score
		}
		if b.Score != nil {
			bv = *b.Score
		}
		if c := cmp.Compare(bv, av); c != 0 {
			return c
		}
		if a.Kind != b.Kind {
			if a.Kind == "add_to_work" {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Key, b.Key)
	})
}
