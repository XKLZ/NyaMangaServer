package model

import "context"

// Actor is assigned by authenticated server code; clients cannot choose it.
type actorKey struct{}

func WithActor(ctx context.Context, a Account) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}
func Actor(ctx context.Context) (Account, bool) {
	a, ok := ctx.Value(actorKey{}).(Account)
	return a, ok
}

type passwordChangeKey struct{}

func WithPasswordChange(ctx context.Context) context.Context {
	return context.WithValue(ctx, passwordChangeKey{}, true)
}
func PasswordChange(ctx context.Context) bool {
	v, _ := ctx.Value(passwordChangeKey{}).(bool)
	return v
}

// Global services ask their persistence boundary for current administrator
// authority. Missing adapters fail closed for authenticated callers.
func AuthorizeAdministrator(ctx context.Context, repository any) error {
	if _, ok := Actor(ctx); !ok {
		return nil
	}
	if r, ok := repository.(interface{ RequireAdministrator(context.Context) error }); ok {
		return r.RequireAdministrator(ctx)
	}
	return ErrForbidden
}

// WithoutActor is reserved for infrastructure snapshots/canonical schema
// construction, never for a domain operation or a persisted worker.
func WithoutActor(ctx context.Context) context.Context {
	return context.WithValue(ctx, actorKey{}, nil)
}
