package comic

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSidecarsAreFilteredAfterSafetyValidation(t *testing.T) {
	p := NewPool()
	defer p.Close()
	raw := picture(t, "png")
	sidecar := make([]byte, 217)
	binary.BigEndian.PutUint32(sidecar, 0x00051607)
	binary.BigEndian.PutUint32(sidecar[4:], 0x00020000)
	root, size, mtime := archive(t, []entry{{"__MACOSX/._01.png", make([]byte, 217), false}, {"pages/._02.png", sidecar, false}, {"._real.png", raw, false}, {".hidden/01.png", raw, false}, {".cover.png", raw, false}})
	pages, e := p.Inspect(context.Background(), root, "sample.cbz", "utf8", size, mtime)
	if e != nil || len(pages) != 3 {
		t.Fatal(pages, e)
	}
	root, size, mtime = archive(t, []entry{{"__MACOSX/../escape.png", raw, false}, {"01.png", raw, false}})
	if _, e = p.Inspect(context.Background(), root, "sample.cbz", "utf8", size, mtime); !errors.Is(e, ErrName) {
		t.Fatal("sidecar bypassed unsafe path", e)
	}
}
func TestLookupStreamsWithoutDecodeAndRejectsZIPCRC(t *testing.T) {
	raw := append(picture(t, "png"), bytes.Repeat([]byte("anonymous raw payload"), 10000)...)
	root, size, mtime := archive(t, []entry{{"01.png", raw, false}})
	b := NewBudget(1)
	p := NewPoolWithBudget(b)
	defer p.Close()
	hash, _, e := p.LookupSHA1(context.Background(), root, "sample.cbz", "utf8", size, mtime)
	if e != nil || hash != fmt.Sprintf("%x", sha1.Sum(raw)) {
		t.Fatal(hash, e)
	}
	if _, _, _, e = p.Image(context.Background(), root, "sample.cbz", size, mtime, 0, false); !errors.Is(e, ErrLimit) {
		t.Fatal("decode budget was bypassed", e)
	}
	path := filepath.Join(root, "sample.cbz")
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	at := bytes.Index(data, raw)
	if at < 0 {
		t.Fatal("payload missing")
	}
	data[at+len(raw)-1] ^= 1
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	info, _ := os.Stat(path)
	if hash, _, e = p.LookupSHA1(context.Background(), root, "sample.cbz", "utf8", info.Size(), info.ModTime().UnixNano()); e == nil || hash != "" {
		t.Fatal("ZIP CRC not checked", hash, e)
	}
}
func TestSharedImageBudgetAcrossPoolsCancellationAndCapacity(t *testing.T) {
	b := NewBudget(DefaultImageMemory)
	a, c := NewPoolWithBudget(b), NewPoolWithBudget(b)
	defer a.Close()
	defer c.Close()
	release, e := a.reserve(context.Background(), 351<<20)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, e = c.reserve(ctx, 257<<20); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("pools did not share budget", e)
	}
	release()
	free, e := c.reserve(context.Background(), 351<<20)
	if e != nil {
		t.Fatal(e)
	}
	free()
	_, e = a.reserve(context.Background(), DefaultImageMemory+1)
	var detail *LimitError
	if !errors.As(e, &detail) || detail.Actual != DefaultImageMemory+1 || detail.Limit != DefaultImageMemory || detail.Resource != "estimated_memory_bytes" {
		t.Fatal(e)
	}
	if a.imageSlots != c.imageSlots {
		t.Fatal("decode concurrency not shared")
	}
}

func TestHighResolutionThumbnailUsesConfiguredBudget(t *testing.T) {
	var data bytes.Buffer
	if e := png.Encode(&data, image.NewGray(image.Rect(0, 0, 5000, 5000))); e != nil {
		t.Fatal(e)
	}
	root, size, mtime := archive(t, []entry{{"01.png", data.Bytes(), false}})
	small := NewPoolWithBudget(NewBudget(256 << 20))
	defer small.Close()
	if _, _, _, e := small.Image(context.Background(), root, "sample.cbz", size, mtime, 0, true); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	normal := NewPoolWithBudget(NewBudget(DefaultImageMemory))
	defer normal.Close()
	raw, _, _, e := normal.Image(context.Background(), root, "sample.cbz", size, mtime, 0, true)
	if e != nil {
		t.Fatal(e)
	}
	cfg, _, e := image.DecodeConfig(bytes.NewReader(raw))
	if e != nil || cfg.Width > 320 || cfg.Height > 480 {
		t.Fatal(cfg, e)
	}
}
