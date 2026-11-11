// Package auth implements credential policy without HTTP or SQL details.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"nyamangaserver.local/server/internal/model"
)

var ErrCredentials = errors.New("invalid credentials")
var ErrPassword = errors.New("密码需包含12–128个字符，且不超过512字节")
var ErrSession = errors.New("invalid session")

type Hasher interface {
	Hash(context.Context, string) (string, error)
	Verify(context.Context, string, string) (bool, error)
}
type Argon struct{ slots chan struct{} }

func NewArgon() *Argon { return &Argon{make(chan struct{}, 2)} }
func (a *Argon) enter(ctx context.Context) error {
	select {
	case a.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (a *Argon) Hash(ctx context.Context, password string) (string, error) {
	if e := a.enter(ctx); e != nil {
		return "", e
	}
	defer func() { <-a.slots }()
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 1, 32)
	return "$argon2id$v=19$m=65536,t=3,p=1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func (a *Argon) Verify(ctx context.Context, encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=3,p=1" {
		return false, errors.New("unsupported password hash")
	}
	salt, e := base64.RawStdEncoding.DecodeString(parts[4])
	if e != nil || len(salt) != 16 {
		return false, errors.New("invalid salt")
	}
	expected, e := base64.RawStdEncoding.DecodeString(parts[5])
	if e != nil || len(expected) != 32 {
		return false, errors.New("invalid password hash")
	}
	if e = a.enter(ctx); e != nil {
		return false, e
	}
	defer func() { <-a.slots }()
	actual := argon2.IDKey([]byte(password), salt, 3, 64*1024, 1, 32)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}
func ValidatePassword(p string) error {
	n := utf8.RuneCountInString(p)
	if !utf8.ValidString(p) || n < 12 || n > 128 || len(p) > 512 {
		return ErrPassword
	}
	return nil
}
func RandomToken(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func HashToken(token string) []byte { h := sha256.Sum256([]byte(token)); return h[:] }
func CSRF(token string) string {
	h := sha256.Sum256([]byte("nyamangaserver-csrf-v1:" + token))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

type Service struct {
	store     Repository
	Hasher    Hasher
	SetupHash []byte
	Now       func() time.Time
}

func New(s Repository, setupToken string) *Service {
	return &Service{s, NewArgon(), HashToken(setupToken), time.Now}
}
func (s *Service) newSession(id string) (string, model.Session, error) {
	token, e := RandomToken(32)
	now := s.Now().Unix()
	return token, model.Session{Hash: HashToken(token), AccountID: id, Created: now, Expires: now + 7*24*3600}, e
}
func (s *Service) Initialized(ctx context.Context) (bool, error) {
	a, e := s.store.Account(ctx)
	return a != nil && a.PasswordHash != "", e
}
func (s *Service) Setup(ctx context.Context, token, password string) (string, model.Session, error) {
	a, e := s.store.Account(ctx)
	if e != nil {
		return "", model.Session{}, e
	}
	if a != nil && a.PasswordHash != "" {
		return "", model.Session{}, model.ErrInitialized
	}
	if subtle.ConstantTimeCompare(HashToken(token), s.SetupHash) != 1 {
		return "", model.Session{}, ErrCredentials
	}
	if e = ValidatePassword(password); e != nil {
		return "", model.Session{}, e
	}
	hash, e := s.Hasher.Hash(ctx, password)
	if e != nil {
		return "", model.Session{}, e
	}
	if a == nil {
		id, e := RandomToken(16)
		if e != nil {
			return "", model.Session{}, e
		}
		a = &model.Account{ID: id, Revision: 1}
	} else {
		a.Revision++
	}
	a.PasswordHash = hash
	raw, v, e := s.newSession(a.ID)
	if e != nil {
		return "", v, e
	}
	if e = s.store.Initialize(ctx, *a, v); e != nil {
		return "", v, e
	}
	v.Account, e = s.store.AccountByID(ctx, a.ID)
	if e != nil {
		return "", v, e
	}
	return raw, v, nil
}
func (s *Service) Login(ctx context.Context, password string) (string, model.Session, error) {
	return s.LoginUsername(ctx, "", password)
}
func (s *Service) LoginUsername(ctx context.Context, username, password string) (string, model.Session, error) {
	if len(password) > 512 || !utf8.ValidString(password) {
		return "", model.Session{}, ErrCredentials
	}
	var a *model.Account
	var e error
	if username == "" {
		a, e = s.store.Account(ctx)
	} else {
		a, e = s.store.AccountByUsername(ctx, username)
	}
	if e != nil {
		return "", model.Session{}, e
	}
	if a == nil || !a.Enabled || a.PasswordHash == "" {
		return "", model.Session{}, ErrCredentials
	}
	ok, e := s.Hasher.Verify(ctx, a.PasswordHash, password)
	if e != nil {
		return "", model.Session{}, fmt.Errorf("verify credential: %w", e)
	}
	if !ok {
		return "", model.Session{}, ErrCredentials
	}
	raw, v, e := s.newSession(a.ID)
	if e != nil {
		return "", v, e
	}
	if e = s.store.CreateSession(ctx, *a, v); errors.Is(e, model.ErrConflict) {
		e = ErrCredentials
	}
	if e != nil {
		return "", v, e
	}
	v.Account, e = s.store.AccountByID(ctx, a.ID)
	if e != nil {
		return "", v, e
	}
	return raw, v, nil
}
func (s *Service) Session(ctx context.Context, raw string) (*model.Session, error) {
	if len(raw) != 43 {
		return nil, ErrSession
	}
	v, e := s.store.Session(ctx, HashToken(raw), s.Now().Unix())
	if e != nil {
		return nil, e
	}
	if v == nil {
		return nil, ErrSession
	}
	return v, nil
}
func (s *Service) ChangePassword(ctx context.Context, id, current, next string) error {
	if e := ValidatePassword(next); e != nil {
		return e
	}
	if len(current) > 512 {
		return ErrCredentials
	}
	a, e := s.store.AccountByID(ctx, id)
	if e != nil {
		return e
	}
	if a == nil || a.ID != id {
		return ErrSession
	}
	ok, e := s.Hasher.Verify(ctx, a.PasswordHash, current)
	if e != nil {
		return e
	}
	if !ok {
		return ErrCredentials
	}
	hash, e := s.Hasher.Hash(ctx, next)
	if e != nil {
		return e
	}
	return s.store.ChangePassword(model.WithPasswordChange(ctx), *a, hash, s.Now().Unix())
}

func (s *Service) Logout(ctx context.Context, hash []byte) error { return s.store.Logout(ctx, hash) }
func (s *Service) Ready(ctx context.Context) error               { return s.store.Ping(ctx) }
func (s *Service) SchemaVersion() int                            { return s.store.SchemaVersion() }
func (s *Service) Baseline() string                              { return s.store.Baseline() }
