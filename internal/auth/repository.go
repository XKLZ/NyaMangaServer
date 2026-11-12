package auth

import (
	"context"
	"nyamangaserver.local/server/internal/model"
)

// Repository describes the persistence operations required by this service.
type Repository interface {
	CreateDeviceSession(context.Context, model.Account, model.DeviceSession, []byte) error
	DeviceSession(context.Context, []byte, int64) (*model.DeviceSession, error)
	DeviceSessions(context.Context) ([]model.DeviceSession, error)
	RenewDeviceSession(context.Context, int64) (model.DeviceSession, error)
	RevokeDeviceSession(context.Context, string, int64) error
	TouchDeviceSession(context.Context, int64) error
	HasEditingLibrary(context.Context) (bool, error)
	Accounts(context.Context) ([]model.AccountView, error)
	CreateAccount(context.Context, string, string, string, int64) (model.AccountView, error)
	UpdateAccount(context.Context, string, model.AccountUpdate, string, int64) error
	Account(ctx context.Context) (*model.Account, error)
	AccountByID(context.Context, string) (*model.Account, error)
	AccountByUsername(context.Context, string) (*model.Account, error)
	ChangePassword(ctx context.Context, a model.Account, newHash string, now int64) error
	CreateSession(ctx context.Context, a model.Account, v model.Session) error
	Initialize(ctx context.Context, a model.Account, v model.Session) error
	Session(ctx context.Context, hash []byte, now int64) (*model.Session, error)
	Logout(context.Context, []byte) error
	Ping(context.Context) error
	SchemaVersion() int
	Baseline() string
}
