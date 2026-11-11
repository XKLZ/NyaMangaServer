package model

import (
	"context"
	"errors"
)

var ErrTagFollowConflict = errors.New("tag_follow_revision_conflict")

type TagFollow struct {
	TagID    string `json:"tag_id"`
	Followed bool   `json:"followed"`
	Revision int64  `json:"revision"`
}

// Account identity is server-assigned context, never a JSON input or source field.
type tagAccountKey struct{}

func WithTagAccount(ctx context.Context, account string) context.Context {
	return context.WithValue(ctx, tagAccountKey{}, account)
}
func TagAccount(ctx context.Context) string { v, _ := ctx.Value(tagAccountKey{}).(string); return v }
