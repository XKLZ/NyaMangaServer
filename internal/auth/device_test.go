package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"nyamangaserver.local/server/internal/model"
)

func TestDeviceLifecycle(t *testing.T) {
	s := service(t)
	ctx := context.Background()
	now := time.Unix(1800000000, 0)
	s.Now = func() time.Time { return now }
	const password = "anonymous password"
	if _, _, e := s.Setup(ctx, "anonymous-setup", password); e != nil {
		t.Fatal(e)
	}
	login := func() (string, model.DeviceSession) {
		t.Helper()
		raw, v, e := s.LoginDevice(ctx, "admin", password, "匿名客户端", "sample")
		if e != nil {
			t.Fatal(e)
		}
		return raw, v
	}
	raw, first := login()
	if _, e := s.Session(ctx, raw); !errors.Is(e, ErrSession) {
		t.Fatalf("device credential treated as a web session: %v", e)
	}
	device := model.WithDeviceActor(ctx, *first.Account, first.ID, s.Now)
	v, e := s.RenewDeviceSession(device)
	if e != nil || v.ExpiresAt != first.ExpiresAt {
		t.Fatal(v, e)
	}
	now = time.Unix(first.ExpiresAt-model.DeviceRenewWindowSeconds+1, 0)
	v, e = s.RenewDeviceSession(device)
	if e != nil || v.ExpiresAt != now.Unix()+model.DeviceLifetimeSeconds {
		t.Fatal(v, e)
	}
	if retry, e := s.RenewDeviceSession(device); e != nil || retry.ExpiresAt != v.ExpiresAt {
		t.Fatal(retry, e)
	}
	web := model.WithActor(ctx, *first.Account)
	var wg sync.WaitGroup
	wg.Go(func() {
		if _, err := s.RenewDeviceSession(device); err != nil && !errors.Is(err, model.ErrDeviceSession) {
			t.Error(err)
		}
	})
	wg.Go(func() {
		if err := s.RevokeDeviceSession(web, first.ID); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
	if _, e = s.DeviceSession(ctx, raw); !errors.Is(e, model.ErrDeviceSession) {
		t.Fatal(e)
	}
	if e = s.RevokeDeviceSession(web, first.ID); e != nil {
		t.Fatal(e)
	}
	for range model.DeviceLimit {
		raw, v = login()
	}
	if _, _, e = s.LoginDevice(ctx, "admin", password, "limit", "sample"); !errors.Is(e, model.ErrDeviceLimit) {
		t.Fatal(e)
	}
	now = time.Unix(v.ExpiresAt, 0)
	if _, e = s.DeviceSession(ctx, raw); !errors.Is(e, model.ErrDeviceSession) {
		t.Fatal(e)
	}
	raw, v = login()
	if e = s.ChangePassword(model.WithDeviceActor(ctx, *v.Account, v.ID, s.Now), v.AccountID, password, "new anonymous password"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DeviceSession(ctx, raw); !errors.Is(e, model.ErrDeviceSession) {
		t.Fatal(e)
	}
}
