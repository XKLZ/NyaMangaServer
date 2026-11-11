package recommendation

import (
	"errors"
	"testing"
)

func TestSeriesPublicationFailureCategories(t *testing.T) {
	for _, v := range []struct{ phase, reason, code string }{
		{"publish", "database_constraint", "series_publication_failed"},
		{"publish", "storage_capacity", "series_publication_failed"},
		{"publish", "proposal_invalid", "series_publication_failed"},
		{"finalize_evidence", "storage_unavailable", "series_evidence_finalize_failed"},
	} {
		private := errors.New("private path and title must never be diagnostics")
		e := &SeriesPublicationError{Reason: v.reason, Err: private}
		code, reason := seriesFailureCategory(v.phase, e)
		if code != v.code || reason != v.reason || e.Error() == private.Error() {
			t.Fatal(code, reason, e)
		}
	}
	if code, reason := seriesFailureCategory("compare", ErrWorker); code != "series_discovery_failed" || reason != "runtime_unavailable" {
		t.Fatal(code, reason)
	}
}
