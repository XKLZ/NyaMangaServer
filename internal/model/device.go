package model

import (
	"context"
	"errors"
	"time"
)

var ErrDeviceSession = errors.New("invalid device session")
var ErrDeviceLimit = errors.New("device session limit")

const (
	DeviceLimit              = 20
	DeviceLifetimeSeconds    = 30 * 24 * 3600
	DeviceRenewWindowSeconds = 7 * 24 * 3600
	DeviceActivitySeconds    = 5 * 60
)

type DeviceSession struct {
	ID               string   `json:"id"`
	AccountID        string   `json:"-"`
	ClientInstanceID string   `json:"client_instance_id"`
	DeviceName       string   `json:"device_name"`
	CreatedAt        int64    `json:"created_at"`
	ExpiresAt        int64    `json:"expires_at"`
	LastActiveAt     int64    `json:"last_active_at"`
	RevokedAt        *int64   `json:"revoked_at"`
	Account          *Account `json:"-"`
}

type deviceActorKey struct{}

type DevicePrincipal struct {
	ID  string
	Now func() time.Time
}

func WithDeviceActor(ctx context.Context, account Account, id string, now func() time.Time) context.Context {
	return context.WithValue(WithActor(ctx, account), deviceActorKey{}, DevicePrincipal{ID: id, Now: now})
}

func DeviceActor(ctx context.Context) (DevicePrincipal, bool) {
	principal, ok := ctx.Value(deviceActorKey{}).(DevicePrincipal)
	return principal, ok
}
