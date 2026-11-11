package model

import (
	"context"
	"errors"
)

// Older repositories and anonymous fakes have no restore hold. A configured
// repository failure must never turn into permission to run background work.
func AutomaticWorkAllowed(ctx context.Context, repo any) bool {
	if r, ok := repo.(interface {
		AutomaticWorkAllowed(context.Context) (bool, error)
	}); ok {
		allowed, e := r.AutomaticWorkAllowed(ctx)
		return e == nil && allowed
	}
	return true
}

var (
	ErrInstancePackage  = errors.New("invalid_instance_package")
	ErrInstanceBuild    = errors.New("instance_build_mismatch")
	ErrInstanceRecovery = errors.New("instance_recovery_required")
	ErrInstanceMapping  = errors.New("instance_mapping_invalid")
)

type InstanceOperation struct {
	MaterialsCleaned bool   `json:"materials_cleaned"`
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	State            string `json:"state"`
	Phase            string `json:"phase"`
	Revision         int64  `json:"revision"`
	Bytes            int64  `json:"bytes"`
	Digest           string `json:"sha256"`
	Error            string `json:"error_code"`
	Created          int64  `json:"created_at"`
	Updated          int64  `json:"updated_at"`
}

type InstanceRequest struct {
	RequestKey string `json:"request_key"`
	Expected   int64  `json:"expected_revision"`
}

type InstanceState struct {
	Owner          *string `json:"operation_id"`
	BackgroundHold bool    `json:"background_hold"`
	WriteEpoch     int64   `json:"write_epoch"`
	Revision       int64   `json:"revision"`
}
