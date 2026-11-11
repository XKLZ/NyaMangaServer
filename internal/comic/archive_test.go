package comic

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
)

type entry struct {
	name    string
	data    []byte
	nonUTF8 bool
}

func picture(t *testing.T, kind string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 6))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	var e error
	switch kind {
	case "png":
		e = png.Encode(&b, img)
	case "jpeg":
		e = jpeg.Encode(&b, img, nil)
	case "gif":
		e = gif.Encode(&b, img, nil)
	}
	if e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func archive(t *testing.T, entries []entry) (string, int64, int64) {
	t.Helper()
	dir := t.TempDir()
	filename := filepath.Join(dir, "sample.cbz")
	file, e := os.Create(filename)
	if e != nil {
		t.Fatal(e)
	}
	w := zip.NewWriter(file)
	for _, v := range entries {
		f, e := w.CreateHeader(&zip.FileHeader{Name: v.name, Method: zip.Store, NonUTF8: v.nonUTF8})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write(v.data); e != nil {
			t.Fatal(e)
		}
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	file.Close()
	info, _ := os.Stat(filename)
	return dir, info.Size(), info.ModTime().UnixNano()
}
func TestPageOrderAndImages(t *testing.T) {
	ctx := context.Background()
	webp, e := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
	if e != nil {
		t.Fatal(e)
	}
	dir, size, mtime := archive(t, []entry{{"目录/10.PNG", picture(t, "png"), false}, {"目录/2.jpg", picture(t, "jpeg"), false}, {"目录/01.gif", picture(t, "gif"), false}, {"目录/1.webp", webp, false}, {"目录/2.jpg", picture(t, "jpeg"), false}, {"notes.txt", []byte("anonymous"), false}})
	p := NewPool()
	defer p.Close()
	pages, e := p.Inspect(ctx, dir, "sample.cbz", "utf8", size, mtime)
	if e != nil {
		t.Fatal(e)
	}
	want := []string{"目录/1.webp", "目录/01.gif", "目录/2.jpg", "目录/2.jpg", "目录/10.PNG"}
	for i, page := range pages {
		if page.Name != want[i] || page.Index != i+1 {
			t.Fatal(pages)
		}
		raw, kind, hash, e := p.Image(ctx, dir, "sample.cbz", size, mtime, page.Entry, false)
		if e != nil {
			t.Fatal(page.Name, e)
		}
		if hash == "" || kind == "" {
			t.Fatal("missing image metadata")
		}
		if _, _, e = image.Decode(bytes.NewReader(raw)); e != nil {
			t.Fatal(e)
		}
	}
	if pages[2].Entry == pages[3].Entry {
		t.Fatal("duplicate names collapsed")
	}
	raw, kind, _, e := p.Image(ctx, dir, "sample.cbz", size, mtime, pages[2].Entry, true)
	if e != nil || kind != "image/png" {
		t.Fatal(e)
	}
	cfg, _, e := image.DecodeConfig(bytes.NewReader(raw))
	if e != nil || cfg.Width > 320 || cfg.Height > 480 {
		t.Fatal(cfg, e)
	}
}
func TestEncodingAndArchiveBoundaries(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"gb18030", "shift_jis"} {
		var raw []byte
		var e error
		if kind == "gb18030" {
			raw, e = simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("漫画/第1页.png"))
		} else {
			raw, e = japanese.ShiftJIS.NewEncoder().Bytes([]byte("漫画/第1頁.png"))
		}
		if e != nil {
			t.Fatal(e)
		}
		dir, size, mtime := archive(t, []entry{{string(raw), picture(t, "png"), true}})
		p := NewPool()
		if _, e = p.Inspect(ctx, dir, "sample.cbz", "utf8", size, mtime); !errors.Is(e, ErrEncoding) {
			t.Fatal("legacy encoding guessed", e)
		}
		if pages, e := p.Inspect(ctx, dir, "sample.cbz", kind, size, mtime); e != nil || len(pages) != 1 {
			t.Fatal(e)
		}
		p.Close()
	}
	for _, name := range []string{"../bad.png", "/bad.png", "C:/bad.png", "dir\\bad.png", "bad\x00.png"} {
		dir, size, mtime := archive(t, []entry{{name, picture(t, "png"), false}})
		p := NewPool()
		if _, e := p.Inspect(ctx, dir, "sample.cbz", "utf8", size, mtime); e == nil {
			t.Fatal("unsafe name accepted")
		}
		p.Close()
	}
	dir, size, mtime := archive(t, []entry{{"1.png", []byte("bad page"), false}, {"2.png", picture(t, "png"), false}})
	p := NewPool()
	defer p.Close()
	pages, e := p.Inspect(ctx, dir, "sample.cbz", "utf8", size, mtime)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = p.Image(ctx, dir, "sample.cbz", size, mtime, pages[0].Entry, false); e == nil {
		t.Fatal("bad image accepted")
	}
	if _, _, _, e = p.Image(ctx, dir, "sample.cbz", size, mtime, pages[1].Entry, false); e != nil {
		t.Fatal("bad page blocked valid neighbor", e)
	}
	if _, e = p.Inspect(ctx, dir, "../sample.cbz", "utf8", size, mtime); e == nil {
		t.Fatal("root escape")
	}
	entries := make([]entry, MaxEntries+1)
	for i := range entries {
		entries[i] = entry{"x.txt", nil, false}
	}
	dir, size, mtime = archive(t, entries)
	if _, e = p.Inspect(ctx, dir, "sample.cbz", "utf8", size, mtime); !errors.Is(e, ErrLimit) {
		t.Fatal("central directory cap", e)
	}
}
func TestPoolAndMemoryBounds(t *testing.T) {
	p := NewPool()
	defer p.Close()
	ctx := context.Background()
	var release []func()
	for i := 0; i < 8; i++ {
		dir, size, mtime := archive(t, []entry{{"1.png", picture(t, "png"), false}})
		_, free, e := p.acquire(ctx, dir, "sample.cbz", size, mtime)
		if e != nil {
			t.Fatal(e)
		}
		release = append(release, free)
	}
	dir, size, mtime := archive(t, []entry{{"1.png", picture(t, "png"), false}})
	deadline, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if _, _, e := p.acquire(deadline, dir, "sample.cbz", size, mtime); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("session cap", e)
	}
	for _, free := range release {
		free()
	}
	p.Sweep(time.Now().Add(time.Minute))
	if len(p.entries) != 0 {
		t.Fatal("idle sessions retained")
	}
	if _, e := p.reserve(ctx, DefaultImageMemory+1); !errors.Is(e, ErrLimit) {
		t.Fatal("unbounded memory")
	}
	free, e := p.reserve(ctx, DefaultImageMemory)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.reserve(deadline, 1); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("memory wait not cancellable", e)
	}
	free()
}

func TestAdvertisedLimitsAndMalformedUTF8(t *testing.T) {
	ctx := context.Background()
	p := NewPool()
	defer p.Close()
	for _, boundary := range []string{"page", "directory", "utf8"} {
		dir, size, mtime := archive(t, []entry{{"1.png", picture(t, "png"), false}})
		filename := filepath.Join(dir, "sample.cbz")
		raw, e := os.ReadFile(filename)
		if e != nil {
			t.Fatal(e)
		}
		central := bytes.Index(raw, []byte{'P', 'K', 1, 2})
		if central < 0 {
			t.Fatal("fixture missing directory")
		}
		want := error(ErrLimit)
		switch boundary {
		case "page":
			binary.LittleEndian.PutUint32(raw[central+24:], MaxPage+1)
		case "directory":
			binary.LittleEndian.PutUint32(raw[len(raw)-22+12:], MaxDirectory+1)
		case "utf8":
			raw[central+46] = 255
			flags := binary.LittleEndian.Uint16(raw[central+8:]) | 0x800
			binary.LittleEndian.PutUint16(raw[central+8:], flags)
			want = ErrEncoding
		}
		if e = os.WriteFile(filename, raw, 0600); e != nil {
			t.Fatal(e)
		}
		info, _ := os.Stat(filename)
		mtime = info.ModTime().UnixNano()
		if _, e = p.Inspect(ctx, dir, "sample.cbz", "utf8", size, mtime); !errors.Is(e, want) {
			t.Fatal(boundary, e)
		}
	}
	// Valid PNG metadata advertises >100MP. Reject before any pixel allocation.
	var b bytes.Buffer
	b.Write([]byte("\x89PNG\r\n\x1a\n"))
	chunk := func(kind string, data []byte) {
		binary.Write(&b, binary.BigEndian, uint32(len(data)))
		b.WriteString(kind)
		b.Write(data)
		binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(kind), data...)))
	}
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header, 10001)
	binary.BigEndian.PutUint32(header[4:], 10001)
	header[8] = 8
	header[9] = 6
	chunk("IHDR", header)
	chunk("IDAT", nil)
	chunk("IEND", nil)
	dir, size, mtime := archive(t, []entry{{"1.png", b.Bytes(), false}})
	pages, e := p.Inspect(ctx, dir, "sample.cbz", "utf8", size, mtime)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = p.Image(ctx, dir, "sample.cbz", size, mtime, pages[0].Entry, false); !errors.Is(e, ErrLimit) {
		t.Fatal("pixel limit", e)
	}
}
