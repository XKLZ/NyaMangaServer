package recommendation

import "errors"

// SeriesPublicationError carries a bounded category, never database text in the
// worker/API diagnostic. The original error remains available for conflict guards.
type SeriesPublicationError struct {
	Reason string
	Err    error
}

func (e *SeriesPublicationError) Error() string { return "series publication failed" }
func (e *SeriesPublicationError) Unwrap() error { return e.Err }

func seriesFailureCategory(phase string, e error) (code, reason string) {
	code, reason = "series_discovery_failed", "runtime_unavailable"
	var publication *SeriesPublicationError
	if errors.As(e, &publication) {
		reason = publication.Reason
	}
	if phase == "publish" {
		code = "series_publication_failed"
		if publication == nil {
			reason = "proposal_invalid"
		}
	}
	if phase == "finalize_evidence" {
		code = "series_evidence_finalize_failed"
		if publication == nil {
			reason = "storage_unavailable"
		}
	}
	return
}
