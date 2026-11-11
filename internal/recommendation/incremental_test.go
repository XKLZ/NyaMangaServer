package recommendation

import (
	"reflect"
	"testing"
)

func TestIncrementalPairReuseCoverageStableIdentityAndChangedInputs(t *testing.T) {
	r := &PairReuse{Graph: PairGraph{IDs: []string{"a", "b", "c"}, Hard: map[int][]byte{0: {2}, 1: {0}, 2: {}}, Edges: []PairEvidence{{A: 0, B: 1, Tier: "strong"}}}, Matching: map[string]string{"a": "a1", "b": "b1", "c": "c1"}, Current: map[string]string{"a": "a1", "b": "b1", "c": "c1", "d": "d1"}}
	r.Prepare()
	row, missing := r.Row([]string{"b", "c", "d"}, 0)
	if row.Reused != 1 || !reflect.DeepEqual(missing, []int{2}) || len(row.Edges) != 0 {
		t.Fatal(row, missing)
	} // known below-threshold pair b-c is reused
	row, missing = r.Row([]string{"a", "c"}, 0)
	if row.Reused != 1 || len(missing) != 0 || row.HardBits[0] != 1 {
		t.Fatal(row, missing)
	}
	r.Current["c"] = "changed"
	row, missing = r.Row([]string{"a", "c"}, 0)
	if row.Reused != 0 || len(missing) != 1 {
		t.Fatal(row, missing)
	}
	delete(r.Graph.Hard, 0)
	row, missing = r.Row([]string{"a", "b"}, 0)
	if row.Reused != 0 || len(missing) != 1 {
		t.Fatal("missing coverage reused", row, missing)
	}
}
