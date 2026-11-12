package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"nyamangaserver.local/server/internal/model"
)

const devicePrefix = "nya_device_v1_"

var clientInstancePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func (s *Service) LoginDevice(ctx context.Context, username, password, name, instance string) (string, model.DeviceSession, error) {
	var v model.DeviceSession
	name = strings.TrimSpace(name)
	if !usernamePattern.MatchString(username) || !clientInstancePattern.MatchString(instance) || name == "" ||
		!utf8.ValidString(name) || utf8.RuneCountInString(name) > 100 || strings.ContainsFunc(name, unicode.IsControl) {
		return "", v, model.ErrInput
	}
	a, e := s.verifyLogin(ctx, username, password)
	if e != nil {
		return "", v, e
	}
	token, e := RandomToken(32)
	if e != nil {
		return "", v, e
	}
	id, e := RandomToken(16)
	if e != nil {
		return "", v, e
	}
	raw := devicePrefix + token
	now := s.Now().Unix()
	v = model.DeviceSession{ID: id, AccountID: a.ID, ClientInstanceID: instance, DeviceName: name, CreatedAt: now,
		ExpiresAt: now + model.DeviceLifetimeSeconds, LastActiveAt: now}
	if e = s.store.CreateDeviceSession(ctx, *a, v, HashToken("nyamangaserver-device-session-v1:"+raw)); e != nil {
		if errors.Is(e, model.ErrConflict) {
			e = ErrCredentials
		}
		return "", model.DeviceSession{}, e
	}
	v.Account = a
	return raw, v, nil
}

func (s *Service) DeviceSession(ctx context.Context, raw string) (*model.DeviceSession, error) {
	payload, ok := strings.CutPrefix(raw, devicePrefix)
	if !ok || len(payload) != 43 {
		return nil, model.ErrDeviceSession
	}
	if _, e := base64.RawURLEncoding.Strict().DecodeString(payload); e != nil {
		return nil, model.ErrDeviceSession
	}
	v, e := s.store.DeviceSession(ctx, HashToken("nyamangaserver-device-session-v1:"+raw), s.Now().Unix())
	if e == nil && v == nil {
		e = model.ErrDeviceSession
	}
	return v, e
}

func (s *Service) DeviceSessions(ctx context.Context) ([]model.DeviceSession, error) {
	return s.store.DeviceSessions(ctx)
}

func (s *Service) RenewDeviceSession(ctx context.Context) (model.DeviceSession, error) {
	return s.store.RenewDeviceSession(ctx, s.Now().Unix())
}

func (s *Service) RevokeDeviceSession(ctx context.Context, id string) error {
	return s.store.RevokeDeviceSession(ctx, id, s.Now().Unix())
}

func (s *Service) TouchDeviceSession(ctx context.Context, v *model.DeviceSession) error {
	now := s.Now().Unix()
	if now-v.LastActiveAt < model.DeviceActivitySeconds {
		return nil
	}
	return s.store.TouchDeviceSession(ctx, now)
}
