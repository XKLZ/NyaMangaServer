package model

import (
	"errors"
	"time"
)

const EnhancementWork = "work_recommendations"
const EnhancementSeries = "series_recommendations"
const EnhancementDictionary = "chinese_dictionary"

var ErrEnhancementDisabled = errors.New("enhancement_disabled")
var ErrEnhancementImpact = errors.New("enhancement_impact_changed")
var ErrEnhancementTransition = errors.New("enhancement_transition_conflict")

type EnhancementTransition struct {
	TargetEnabled bool   `json:"target_enabled"`
	ID            string `json:"id"`
	Feature       string `json:"feature"`
	Attempt       int64  `json:"attempt"`
	State         string `json:"state"`
	Error         string `json:"error_code"`
	AffectedTasks int    `json:"affected_tasks"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}
type EnhancementFeature struct {
	EffectiveEnabled bool                   `json:"effective_enabled"`
	Enabled          bool                   `json:"enabled"`
	State            string                 `json:"state"`
	AffectedTasks    int                    `json:"affected_tasks"`
	ImpactToken      string                 `json:"impact_token"`
	ResourceState    string                 `json:"resource_state"`
	Transition       *EnhancementTransition `json:"transition"`
}
type Enhancements struct {
	Revision int64                         `json:"revision"`
	Features map[string]EnhancementFeature `json:"features"`
}
type EnhancementPatch struct {
	Expected          int64  `json:"expected_revision"`
	Feature           string `json:"feature"`
	Enabled           *bool  `json:"enabled,omitempty"`
	ConfirmPause      bool   `json:"confirm_pause,omitempty"`
	ImpactToken       string `json:"impact_token,omitempty"`
	RetryTransitionID string `json:"retry_transition_id,omitempty"`
}

func EnhancementJobType(feature string) string {
	switch feature {
	case EnhancementWork:
		return "work_recommendation"
	case EnhancementSeries:
		return "series_recommendation"
	case EnhancementDictionary:
		return "tag_translation"
	}
	return ""
}
func EnhancementForJob(kind string) string {
	switch kind {
	case "work_recommendation":
		return EnhancementWork
	case "series_recommendation":
		return EnhancementSeries
	case "tag_translation":
		return EnhancementDictionary
	}
	return ""
}

type ServiceInfo struct {
	Version       string    `json:"version"`
	Commit        string    `json:"commit"`
	StartedAt     time.Time `json:"started_at"`
	UptimeSeconds int64     `json:"uptime_seconds"`
	OS            string    `json:"os"`
	Architecture  string    `json:"architecture"`
	Schema        int       `json:"database_schema"`
	Baseline      string    `json:"database_baseline"`
	State         string    `json:"state"`
}
