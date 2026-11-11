package galleryhighlight

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

type fakeGallery struct {
	Repository
	block bool
}

func (f *fakeGallery) GalleryGrant(_ context.Context, h []byte, _ int64) (model.GalleryGrant, error) {
	return model.GalleryGrant{ID: base64.RawURLEncoding.EncodeToString(h), Hash: h}, nil
}
func (f *fakeGallery) GalleryLookup(ctx context.Context, _ model.GalleryGrant, ids []string, _ int64) ([]model.GalleryMatch, error) {
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	out := []model.GalleryMatch{}
	for _, id := range ids {
		out = append(out, model.GalleryMatch{GalleryID: id, State: "none"})
	}
	return out, nil
}
func (f *fakeGallery) GalleryPublish(_ context.Context, _ model.GalleryGrant, _ func() int64, fn func() error) error {
	return fn()
}
func fakeToken(n byte) string {
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{n}, 32))
}
func TestGalleryNormalizeAndBoundedRate(t *testing.T) {
	for _, in := range []model.GalleryLookupInput{{Provider: "other", GalleryIDs: []string{"1"}}, {Provider: "ehentai"}, {Provider: "ehentai", GalleryIDs: []string{"0"}}, {Provider: "ehentai", GalleryIDs: []string{"-1"}}, {Provider: "ehentai", GalleryIDs: []string{"1e2"}}, {Provider: "ehentai", GalleryIDs: []string{"9223372036854775808"}}, {Provider: "ehentai", GalleryIDs: []string{"https://exhentai.org/g/1/0123456789/"}}, {Provider: "ehentai", GalleryIDs: make([]string, 201)}} {
		if _, e := Normalize(in); !errors.Is(e, model.ErrInput) {
			t.Fatal(e)
		}
	}
	ids, e := Normalize(model.GalleryLookupInput{Provider: "ehentai", GalleryIDs: []string{"001", "1", "2"}})
	if e != nil || len(ids) != 2 || ids[0] != "1" {
		t.Fatal(ids, e)
	}
	svc := New(&fakeGallery{})
	var clock atomic.Int64
	clock.Store(1000)
	svc.Now = func() time.Time { return time.Unix(clock.Load(), 0) }
	input := model.GalleryLookupInput{Provider: "ehentai"}
	for i := 1; i <= 200; i++ {
		input.GalleryIDs = append(input.GalleryIDs, strconv.Itoa(i))
	}
	for i := 0; i < 5; i++ {
		if e := svc.Lookup(context.Background(), fakeToken(1), input, func(model.GalleryLookup) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	e = svc.Lookup(context.Background(), fakeToken(1), model.GalleryLookupInput{Provider: "ehentai", GalleryIDs: []string{"1"}}, func(model.GalleryLookup) error { return nil })
	var rate *RateError
	if !errors.As(e, &rate) || rate.RetryAfter < 1 {
		t.Fatal(e)
	}
	clock.Add(60)
	if e = svc.Status(context.Background(), fakeToken(1), func(model.GalleryStatus) error { return nil }); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 59; i++ {
		if e = svc.Status(context.Background(), fakeToken(2), func(model.GalleryStatus) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	if e = svc.Status(context.Background(), fakeToken(2), func(model.GalleryStatus) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if e = svc.Status(context.Background(), fakeToken(2), func(model.GalleryStatus) error { return nil }); !errors.As(e, &rate) {
		t.Fatal(e)
	}
}
func TestGalleryConcurrencyAndTimeoutRelease(t *testing.T) {
	svc := New(&fakeGallery{})
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	done := make(chan error, 4)
	for i := 0; i < 4; i++ {
		token := fakeToken(byte(i/2 + 1))
		go func() {
			done <- svc.Status(context.Background(), token, func(model.GalleryStatus) error { entered <- struct{}{}; <-release; return nil })
		}()
	}
	for i := 0; i < 4; i++ {
		<-entered
	}
	var rate *RateError
	if e := svc.Status(context.Background(), fakeToken(3), func(model.GalleryStatus) error { return nil }); !errors.As(e, &rate) {
		t.Fatal(e)
	}
	close(release)
	for i := 0; i < 4; i++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	svc = New(&fakeGallery{block: true})
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	e := svc.Lookup(ctx, fakeToken(1), model.GalleryLookupInput{Provider: "ehentai", GalleryIDs: []string{"1"}}, func(model.GalleryLookup) error { return nil })
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if e = svc.Status(context.Background(), fakeToken(1), func(model.GalleryStatus) error { return nil }); e != nil {
		t.Fatal("slot leaked", e)
	}
}
