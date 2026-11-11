package recommendation

import (
	"context"
	"encoding/json"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"strings"
)

const DiscoveryExecutionVersion = "work-discovery-incremental-v1"

type DiscoveryDiagnosticsRepository interface {
	SaveDiscoveryDiagnostic(context.Context, model.Job, string, string, int) error
}
type DiscoveryCacheRepository interface {
	CachedDiscoveryFeatures(context.Context, string, string) (FeatureResult, bool, error)
	SaveDiscoveryFeatures(context.Context, model.Job, InputEdition, string, FeatureResult, bool) error
	DiscoveryReuse(context.Context, string) (*PairReuse, error)
}
type PairReuse struct {
	Graph     PairGraph
	Matching  map[string]string
	Current   map[string]string
	positions map[string]int
	edges     map[[2]int]PairEvidence
}

func (r *PairReuse) Prepare() {
	r.positions = map[string]int{}
	r.edges = map[[2]int]PairEvidence{}
	for i, id := range r.Graph.IDs {
		r.positions[id] = i
	}
	for _, e := range r.Graph.Edges {
		r.edges[[2]int{e.A, e.B}] = e
	}
}

// Covered rows encode all outcomes, including below-threshold negatives. Missing
// rows are never reusable. Indices are mapped by stable Edition identity.
func (r *PairReuse) Row(ids []string, row int) (PairBlock, []int) {
	b := PairBlock{Row: row, NextRow: row + 1, Compared: len(ids) - row - 1, HardBits: make([]byte, (len(ids)-row-1+7)/8), Edges: []PairEvidence{}}
	missing := []int{}
	for j := row + 1; j < len(ids); j++ {
		a, aok := r.positions[ids[row]]
		c, cok := r.positions[ids[j]]
		if a > c {
			a, c = c, a
		}
		bits, covered := r.Graph.Hard[a]
		if !aok || !cok || !covered || len(bits) != (len(r.Graph.IDs)-a-1+7)/8 || r.Current[ids[row]] == "" || r.Current[ids[j]] == "" || r.Current[ids[row]] != r.Matching[ids[row]] || r.Current[ids[j]] != r.Matching[ids[j]] {
			missing = append(missing, j)
			continue
		}
		b.Reused++
		if bits[(c-a-1)/8]&(1<<((c-a-1)%8)) != 0 {
			b.HardBits[(j-row-1)/8] |= 1 << ((j - row - 1) % 8)
		}
		if e, ok := r.edges[[2]int{a, c}]; ok {
			e.A = row
			e.B = j
			b.Edges = append(b.Edges, e)
		}
	}
	return b, missing
}
func PairInputFingerprint(input InputEdition, result FeatureResult) string {
	// Runtime-only labels are excluded from matching; organization changes must not
	// trigger feature inference or pair recomputation.
	input.Book.CustomLabels = nil
	raw, _ := json.Marshal(result)
	return Fingerprint([]string{Algorithm, EvidenceVersion, EvidenceFingerprint(input), input.PreprocessingKey, string(raw)})
}

// Only immutable registered materials with verified identity can skip image IO.
// Ordinary source files without a stable material identity take the safe path.
func (s *Service) discoveryImageKey(input InputEdition) string {
	if input.Material == nil {
		return ""
	}
	a := input.Material
	path := filepath.Join(s.data, filepath.FromSlash(a.Key))
	if a.Key == "" || !strings.HasPrefix(a.Key, "assets/") || strings.Contains(a.Key, "..") || filepath.IsAbs(a.Key) || appdata.NoSymlinks(path) != nil {
		return ""
	}
	info, e := os.Stat(path)
	if e != nil || !info.Mode().IsRegular() || info.Size() != a.Size {
		return ""
	}
	title := input.Book.TitleOriginal
	if title == "" {
		title = input.Book.Title
	}
	return Fingerprint([]any{Algorithm, title, a.Digest, a.Size, info.ModTime().UnixNano(), input.ContentRevision})
}

type DiscoveryPreprocessingRepository interface {
	SaveDiscoveryPreprocessing(context.Context, model.Job, map[string]string, string) error
}
