package comic

import (
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"golang.org/x/image/bmp"
	"image"
	"image/color"
	"os"
	"sync"
	"testing"
	"time"
)

func TestBMPManifestCachingAndLifecycle(t *testing.T) {
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 3, 4))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	if e := bmp.Encode(&b, img); e != nil {
		t.Fatal(e)
	}
	root, size, mtime := archive(t, []entry{{"2.bmp", b.Bytes(), false}, {"1.png", picture(t, "png"), false}})
	p := NewPool()
	defer p.Close()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := p.Inspect(context.Background(), root, "sample.cbz", "utf8", size, mtime)
			if e != nil || len(v) != 2 {
				t.Errorf("%v %v", v, e)
			}
		}()
	}
	wg.Wait()
	entries, e := p.Inspect(context.Background(), root, "sample.cbz", "utf8", size, mtime)
	if e != nil {
		t.Fatal(e)
	}
	raw, kind, hash, e := p.Image(context.Background(), root, "sample.cbz", size, mtime, entries[1].Entry, false)
	if e != nil || kind != "image/png" || hash != fmt.Sprintf("%x", sha1.Sum(b.Bytes())) {
		t.Fatal(kind, hash, e)
	}
	if _, _, e = image.Decode(bytes.NewReader(raw)); e != nil {
		t.Fatal(e)
	}
	if _, e = p.NormalizeUpload(context.Background(), b.Bytes()); e != nil {
		t.Fatal(e)
	}
	p.mu.Lock()
	charge := p.manifestBytes
	n := len(p.entries)
	p.mu.Unlock()
	if charge <= 0 || n != 1 {
		t.Fatal(charge, n)
	}
	entries[0].Name = "changed by caller"
	again, _ := p.Inspect(context.Background(), root, "sample.cbz", "utf8", size, mtime)
	if again[0].Name != "1.png" {
		t.Fatal("mutable cached directory")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = p.Inspect(ctx, root, "sample.cbz", "utf8", size, mtime); e == nil {
		t.Fatal("cancelled lookup accepted")
	}
	p.Sweep(time.Now().Add(time.Minute * 2))
	if p.manifestBytes != 0 || len(p.entries) != 0 {
		t.Fatal("manifest leaked")
	}
	if _, e = p.Inspect(context.Background(), root, "sample.cbz", "utf8", size, mtime); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(root+"/sample.cbz", []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Inspect(context.Background(), root, "sample.cbz", "utf8", size, mtime); e != ErrChanged {
		t.Fatal("changed archive accepted", e)
	}
}
