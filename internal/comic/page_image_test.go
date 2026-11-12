package comic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
			page, e := p.Page(ctx, key, true, func(string) bool { return false })
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
			blocked := WithImageDeadline(ctx, time.Now().Add(-time.Second))
			if _, err := b.reserve(blocked, b.limit); !errors.Is(err, ErrImageBusy) {
				t.Fatal("active response reservation evicted", err)
			}
			page.Release()
			free, e := b.reserve(ctx, b.limit)
			if e != nil {
				t.Fatal(e)
			}
			free()
			second, e := p.Page(ctx, key, true, func(tag string) bool { return tag == page.ETag })
			if e != nil || !second.NotModified || len(second.Bytes) != 0 || decodes.Load() != 1 {
				t.Fatal("verification reused decode or lost validator", second, decodes.Load(), e)
			}
			p.Sweep(time.Now().Add(PageValidationIdle))
			third, e := p.Page(ctx, key, true, func(tag string) bool { return tag == page.ETag })
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
					v, e := p.Page(ctx, key, true, func(string) bool { return false })
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

func TestPageVerificationRevalidatesCacheEpochAndRejectsFaults(t *testing.T) {
	ctx := context.Background()
	b := NewBudget(32 << 20)
	p := NewPoolWithBudget(b)
	defer p.Close()
	var decodes atomic.Int64
	p.beforePageDecode = func(context.Context, PageKey) { decodes.Add(1) }
	raw := picture(t, "png")
	root, _, _ := archive(t, []entry{{"01.png", raw, false}})
	key := testPageKey(t, root)
	noCondition := func(string) bool { return false }
	first, e := p.Page(ctx, key, true, noCondition)
	if e != nil {
		t.Fatal(e)
	}
	first.Release()
	key.Epoch = "changed-epoch"
	revalidated, e := p.Page(ctx, key, true, func(tag string) bool { return tag == first.ETag })
	if e != nil || revalidated.NotModified || revalidated.ETag != first.ETag || decodes.Load() != 2 {
		t.Fatal("new cache epoch reused a validator", decodes.Load(), e)
	}
	revalidated.Release()
	for _, fault := range []string{"bad_image", "bad_crc", "over_limit"} {
		t.Run(fault, func(t *testing.T) {
			data := raw
			limit := int64(32 << 20)
			if fault == "bad_image" {
				data = []byte("anonymous invalid png")
			} else if fault == "over_limit" {
				limit = 1
			}
			root, _, _ := archive(t, []entry{{"01.png", data, false}})
			if fault == "bad_crc" {
				file := filepath.Join(root, "sample.cbz")
				zipped, e := os.ReadFile(file)
				if e != nil {
					t.Fatal(e)
				}
				at := bytes.Index(zipped, raw)
				if at < 0 {
					t.Fatal("ZIP fixture payload missing")
				}
				zipped[at+len(raw)-1] ^= 1
				if e = os.WriteFile(file, zipped, 0600); e != nil {
					t.Fatal(e)
				}
			}
			b := NewBudget(limit)
			p := NewPoolWithBudget(b)
			defer p.Close()
			key := testPageKey(t, root)
			for range 2 {
				_, e := p.Page(ctx, key, true, func(string) bool { return true })
				want := error(ErrImage)
				if fault == "over_limit" {
					want = ErrLimit
				}
				if !errors.Is(e, want) {
					t.Fatal(fault, e)
				}
			}
			b.mu.Lock()
			defer b.mu.Unlock()
			if b.verified.Len() != 0 || b.hotBytes != 0 || b.used != 0 {
				t.Fatal("failed image was published or charged")
			}
		})
	}
}

func TestPageVerificationBoundsRecordsAndServesAboveHotLimit(t *testing.T) {
	ctx := context.Background()
	noCondition := func(string) bool { return false }
	b := NewBudget(3 << 20)
	p := NewPoolWithBudget(b)
	defer p.Close()
	raw := append(picture(t, "png"), make([]byte, 512<<10)...)
	root, _, _ := archive(t, []entry{{"01.png", raw, false}})
	key := testPageKey(t, root)
	out, e := p.Page(ctx, key, true, noCondition)
	if e != nil || !bytes.Equal(out.Bytes, raw) {
		t.Fatal("page above hot-cache cap failed", e)
	}
	out.Release()
	b.mu.Lock()
	hot, used := b.hotBytes, b.used
	b.mu.Unlock()
	if hot != 0 || used != 0 {
		t.Fatal("page above hot cap retained bytes", hot, used)
	}
	if hit, e := p.Page(ctx, key, true, func(tag string) bool { return tag == out.ETag }); e != nil || !hit.NotModified {
		t.Fatal("large page lost validation record", e)
	}

	root, _, _ = archive(t, []entry{{"01.png", picture(t, "png"), false}})
	key = testPageKey(t, root)
	first := key
	for i := range PageValidationLimit + 1 {
		key.EditionID = fmt.Sprintf("sample-%d", i)
		if i == 0 {
			first = key
		}
		out, e = p.Page(ctx, key, true, noCondition)
		if e != nil {
			t.Fatal(e)
		}
		out.Release()
	}
	first.representation = pageRepresentation
	key.representation = pageRepresentation
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pages) != PageValidationLimit || b.pages[first] != nil || b.pages[key] == nil || b.hotBytes > b.limit/8 {
		t.Fatal("page cache bounds or least-recently-used eviction", len(b.pages), b.hotBytes)
	}
}
