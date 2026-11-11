package auth

import (
	"context"
	"nyamangaserver.local/server/internal/model"
	"regexp"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func (s *Service) Accounts(ctx context.Context) ([]model.AccountView, error) {
	return s.store.Accounts(ctx)
}
func (s *Service) CreateAccount(ctx context.Context, username, password string) (model.AccountView, error) {
	if !usernamePattern.MatchString(username) {
		return model.AccountView{}, model.ErrInput
	}
	if e := ValidatePassword(password); e != nil {
		return model.AccountView{}, e
	}
	hash, e := s.Hasher.Hash(ctx, password)
	if e != nil {
		return model.AccountView{}, e
	}
	id, e := RandomToken(16)
	if e != nil {
		return model.AccountView{}, e
	}
	return s.store.CreateAccount(ctx, id, username, hash, s.Now().Unix())
}
func (s *Service) UpdateAccount(ctx context.Context, id string, in model.AccountUpdate, password *string) error {
	hash := ""
	if password != nil {
		if e := ValidatePassword(*password); e != nil {
			return e
		}
		var e error
		hash, e = s.Hasher.Hash(ctx, *password)
		if e != nil {
			return e
		}
	}
	return s.store.UpdateAccount(ctx, id, in, hash, s.Now().Unix())
}

func (s *Service) HasEditingLibrary(ctx context.Context) (bool, error) {
	return s.store.HasEditingLibrary(ctx)
}
