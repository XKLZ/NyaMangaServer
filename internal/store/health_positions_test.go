package store

import (
	"context"
	"encoding/json"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func TestHealthSeriesPositionOriginAndGaps(t *testing.T) {
	for _, tc := range []struct {
		name      string
		positions []int
		code      string
	}{{"empty", nil, "series_valid"}, {"single_zero", []int{0}, "series_positions_start"}, {"zero_origin", []int{0, 1, 2}, "series_positions_start"}, {"one_origin", []int{1, 2, 3}, "series_valid"}, {"gap", []int{0, 2, 3}, "series_positions_gap"}, {"offset", []int{2, 3, 4}, "series_positions_start"}} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := openTest(t)
			seedBook(t, s)
			if _, e := s.Write.Exec("INSERT INTO series VALUES('series-origin','lib','Anonymous sequence',1)"); e != nil {
				t.Fatal(e)
			}
			for i, pos := range tc.positions {
				id := []string{"one", "two", "three"}[i]
				seedEdition(t, s, id, "lib", id+".cbz", id)
				if _, e := s.Write.Exec("INSERT INTO series_members SELECT 'series-origin',work_id,? FROM editions WHERE id=?", pos, id); e != nil {
					t.Fatal(e)
				}
			}
			out, e := s.HealthRules(context.Background(), model.HealthObject{Kind: "series", ID: "series-origin"})
			if e != nil || len(out) != 1 || out[0].Code != tc.code {
				t.Fatal(out, e)
			}
			var details map[string]any
			if e = json.Unmarshal(out[0].Details, &details); e != nil || details["name"] != "Anonymous sequence" || details["member_count"] != float64(len(tc.positions)) {
				t.Fatal(details, e)
			}
		})
	}
}
