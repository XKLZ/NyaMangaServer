package comic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func testPageKey(t *testing.T, root string) PageKey {
	t.Helper()
	source, e := IdentifySource(root, "sample.cbz")
	if e != nil {
		t.Fatal(e)
	}
	return PageKey{Epoch: "anonymous-epoch", EditionID: "sample", Root: root, Path: "sample.cbz",
		ContentRevision: 1, ParsingFingerprint: "parsing-1", Source: source}
}

func TestPageVerificationSurvivesByteEvictionAndChargesResponse(t *testing.T) {
	for _, format := range []string{"png", "gif", "bmp"} {
		t.Run(format, func(t *testing.T) {
			b := NewBudget(32 << 20)
			p := NewPoolWithBudget(b)
			defer p.Close()
			raw := picture(t, format)
			root, _, _ := archive(t, []entry{{"01." + format, raw, false}})
			key := testPageKey(t, root)
			var decodes atomic.Int64
			p.beforePageDecode = func(context.Context, PageKey) { decodes.Add(1) }
			ctx := context.Background()
			page, e := p.Page(ctx, key, func(string) bool { return false })
			if e != nil {
				t.Fatal(e)
			}
			defer page.Release()
			if page.Kind != "image/png" || page.ETag != fmt.Sprintf("\"%x\"", sha256.Sum256(page.Bytes)) {
				t.Fatal("invalid response digest or format", page.Kind, page.ETag)
			}
			if (format == "png") != bytes.Equal(raw, page.Bytes) {
				t.Fatal("unexpected source-byte conversion", format)
			}
			b.mu.Lock()
			if b.used != int64(cap(page.Bytes)) || b.hotBytes != b.used || b.verified.Len() != 1 {
				t.Errorf("decode reservation or response accounting incorrect: %d %d %d", b.used, b.hotBytes, b.verified.Len())
			}
			b.mu.Unlock()
			page.Release()
			free, e := b.reserve(ctx, b.limit)
			if e != nil {
				t.Fatal(e)
			}
			free()
			second, e := p.Page(ctx, key, func(tag string) bool { return tag == page.ETag })
			if e != nil || !second.NotModified || len(second.Bytes) != 0 || decodes.Load() != 1 {
				t.Fatal("verification reused decode or lost validator", second, decodes.Load(), e)
			}
			p.Sweep(time.Now().Add(PageValidationIdle))
			third, e := p.Page(ctx, key, func(tag string) bool { return tag == page.ETag })
			if e != nil || third.NotModified || decodes.Load() != 2 {
				t.Fatal("idle record did not expire", third, decodes.Load(), e)
			}
			third.Release()
		})
	}
}

func waitPageWaiters(t *testing.T, b *Budget, key PageKey, count int) *pageFlight {
	t.Helper()
	key.representation = pageRepresentation
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		f := b.flights[key]
		if f != nil && len(f.waiters) == count {
			b.mu.Unlock()
			return f
		}
		b.mu.Unlock()
		runtime.Gosched()
	}
	t.Fatalf("expected %d page waiters", count)
	return nil
}

func TestPageCoalescingCancelsOnlyAbandonedWork(t *testing.T) {
	for _, keepOne := range []bool{true, false} {
		t.Run(fmt.Sprint(keepOne), func(t *testing.T) {
			b := NewBudget(32 << 20)
			p := NewPoolWithBudget(b)
			defer p.Close()
			root, _, _ := archive(t, []entry{{"01.png", picture(t, "png"), false}})
			key := testPageKey(t, root)
			entered := make(chan struct{})
			resume := make(chan struct{})
			var decodes atomic.Int64
			p.beforePageDecode = func(ctx context.Context, _ PageKey) {
				decodes.Add(1)
				close(entered)
				select {
				case <-ctx.Done():
				case <-resume:
				}
			}
			ctxA, cancelA := context.WithCancel(context.Background())
			defer cancelA()
			ctxB, cancelB := context.WithCancel(context.Background())
			defer cancelB()
			type result struct {
				image PageImage
				err   error
			}
			call := func(ctx context.Context) <-chan result {
				c := make(chan result, 1)
				go func() {
					v, e := p.Page(ctx, key, func(string) bool { return false })
					c <- result{v, e}
				}()
				return c
			}
			a := call(ctxA)
			<-entered
			c := call(ctxB)
			f := waitPageWaiters(t, b, key, 2)
			cancelA()
			if v := <-a; !errors.Is(v.err, context.Canceled) {
				t.Fatal(v)
			}
			if !keepOne {
				cancelB()
				if v := <-c; !errors.Is(v.err, context.Canceled) {
					t.Fatal(v)
				}
				<-f.done
				b.mu.Lock()
				defer b.mu.Unlock()
				if b.verified.Len() != 0 || b.used != 0 || len(b.flights) != 0 {
					t.Fatal("canceled page published or leaked")
				}
				return
			}
			close(resume)
			v := <-c
			if v.err != nil || len(v.image.Bytes) == 0 || decodes.Load() != 1 {
				t.Fatal(v.err, decodes.Load())
			}
			v.image.Release()
		})
	}
}
