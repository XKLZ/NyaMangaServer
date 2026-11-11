package auth

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"strings"
	"sync"
	"testing"
	"time"
)

type fastHasher struct{ verify func() }

func (h *fastHasher) Hash(ctx context.Context, p string) (string, error) { return "test:" + p, nil }
func (h *fastHasher) Verify(ctx context.Context, encoded, p string) (bool, error) {
	if h.verify != nil {
		h.verify()
	}
	return encoded == "test:"+p, nil
}
func service(t *testing.T) *Service {
	t.Helper()
	db, e := store.Open(context.Background(), t.TempDir(), migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	s := New(db, "anonymous-setup")
	s.Hasher = &fastHasher{}
	return s
}
func TestArgonAndPasswordPolicy(t *testing.T) {
	ctx := context.Background()
	a := NewArgon()
	p := "  anonymous password  "
	encoded, e := a.Hash(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	if ok, e := a.Verify(ctx, encoded, p); e != nil || !ok {
		t.Fatal(e)
	}
	if ok, e := a.Verify(ctx, encoded, strings.TrimSpace(p)); e != nil || ok {
		t.Fatal("password silently trimmed", e)
	}
	if _, e = a.Verify(ctx, strings.Replace(encoded, "m=65536", "m=999999999", 1), p); e == nil {
		t.Fatal("unbounded hash parameters accepted")
	}
	for _, p := range []string{"short", strings.Repeat("a", 129), string([]byte{255})} {
		if ValidatePassword(p) == nil {
			t.Fatal("bad password accepted")
		}
	}
	if e = ValidatePassword(strings.Repeat("😀", 128)); e != nil {
		t.Fatal(e)
	}
	a.slots <- struct{}{}
	a.slots <- struct{}{}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = a.Hash(cancelled, "password long"); !errors.Is(e, context.Canceled) {
		t.Fatal("queue cancellation", e)
	}
	<-a.slots
	<-a.slots
}
func TestConcurrentSetup(t *testing.T) {
	s := service(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _, e := s.Setup(ctx, "anonymous-setup", "anonymous password"); errs <- e }()
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for e := range errs {
		if e == nil {
			success++
		} else if errors.Is(e, model.ErrInitialized) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
}
func TestLoginCannotRacePasswordChange(t *testing.T) {
	s := service(t)
	ctx := context.Background()
	if _, _, e := s.Setup(ctx, "anonymous-setup", "old password long"); e != nil {
		t.Fatal(e)
	}
	read := make(chan struct{})
	resume := make(chan struct{})
	s.Hasher = &fastHasher{verify: func() { close(read); <-resume }}
	result := make(chan error, 1)
	go func() { _, _, e := s.Login(ctx, "old password long"); result <- e }()
	<-read
	a, e := s.store.Account(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.store.ChangePassword(ctx, *a, "test:new password long", time.Now().Unix()); e != nil {
		t.Fatal(e)
	}
	close(resume)
	if e = <-result; !errors.Is(e, ErrCredentials) {
		t.Fatal("stale login succeeded", e)
	}
	var count int
	if e = s.store.(*store.Store).Read.QueryRow("SELECT count(*) FROM sessions").Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
}
func TestSessionExpiryAndLogout(t *testing.T) {
	s := service(t)
	ctx := context.Background()
	now := time.Unix(2000, 0)
	s.Now = func() time.Time { return now }
	token, v, e := s.Setup(ctx, "anonymous-setup", "a valid password")
	if e != nil {
		t.Fatal(e)
	}
	if v.Expires-v.Created != 7*24*3600 {
		t.Fatal(v)
	}
	if _, e = s.Session(ctx, token); e != nil {
		t.Fatal(e)
	}
	now = now.Add(7 * 24 * time.Hour)
	if _, e = s.Session(ctx, token); !errors.Is(e, ErrSession) {
		t.Fatal("expiry", e)
	}
	now = now.Add(-time.Hour)
	if e = s.store.Logout(ctx, v.Hash); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Session(ctx, token); !errors.Is(e, ErrSession) {
		t.Fatal("logout", e)
	}
}
