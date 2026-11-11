// Package comic reads bounded ZIP/CBZ sessions without extracting source files.
package comic

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
)

const DirectoryVersion = "natural-bmp-macos-v3"
const RepresentativeAlgorithm = "content-page8-v1"
const MaxManifestBytes = 32 << 20
const MaxEntries = 10000
const MaxPage = 64 << 20
const MaxPixels = 100_000_000
const MaxBook = 8 << 30
const MaxDirectory = 16 << 20

type Error string

func (e Error) Error() string { return string(e) }

const ErrArchive Error = "invalid_archive"
const ErrLimit Error = "resource_limit"
const ErrName Error = "invalid_archive_name"
const ErrEncoding Error = "invalid_archive_encoding"
const ErrImage Error = "page_corrupt"
const ErrChanged Error = "content_changed"

type Entry struct {
	Index, Entry    int
	Name, MediaType string
}

type session struct {
	manifestGate  chan struct{}
	manifests     map[string][]Entry
	manifestBytes int64
	file          *os.File
	reader        *zip.Reader
	refs          int
	touched       time.Time
}
type Pool struct {
	manifestBytes int64
	mu            sync.Mutex
	entries       map[string]*session
	changed       chan struct{}
	closed        bool
	imageSlots    chan struct{}
	budget        *Budget
}

func NewPool() *Pool {
	return NewPoolWithBudget(defaultBudget)
}
func NewPoolWithBudget(b *Budget) *Pool {
	if b == nil {
		b = defaultBudget
	}
	return &Pool{entries: map[string]*session{}, changed: make(chan struct{}), imageSlots: b.slots, budget: b}
}
func (p *Pool) signal() { close(p.changed); p.changed = make(chan struct{}) }
func (p *Pool) Sweep(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, s := range p.entries {
		if s.refs == 0 && now.Sub(s.touched) >= 60*time.Second {
			s.file.Close()
			delete(p.entries, key)
			p.manifestBytes -= s.manifestBytes
			p.signal()
		}
	}
}
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for key, s := range p.entries {
		if s.refs == 0 {
			s.file.Close()
			delete(p.entries, key)
			p.manifestBytes -= s.manifestBytes
		}
	}
	p.signal()
}
func (p *Pool) acquire(ctx context.Context, root, relative string, size, mtime int64) (*session, func(), error) {
	key := fmt.Sprintf("%s\x00%s\x00%d:%d", root, relative, size, mtime)
	for {
		if e := ctx.Err(); e != nil {
			return nil, nil, e
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, nil, context.Canceled
		}
		if s := p.entries[key]; s != nil {
			r, e := os.OpenRoot(root)
			if e != nil {
				p.mu.Unlock()
				return nil, nil, ErrChanged
			}
			info, e := r.Stat(relative)
			r.Close()
			old, oldErr := s.file.Stat()
			if e != nil || oldErr != nil || !os.SameFile(old, info) || info.Size() != size || info.ModTime().UnixNano() != mtime {
				p.mu.Unlock()
				return nil, nil, ErrChanged
			}
			s.refs++
			p.mu.Unlock()
			return s, p.release(key, s), nil
		}
		if len(p.entries) >= 8 {
			var oldest string
			var when time.Time
			for k, s := range p.entries {
				if s.refs == 0 && (oldest == "" || s.touched.Before(when)) {
					oldest = k
					when = s.touched
				}
			}
			if oldest != "" {
				p.entries[oldest].file.Close()
				p.manifestBytes -= p.entries[oldest].manifestBytes
				delete(p.entries, oldest)
			} else {
				wait := p.changed
				p.mu.Unlock()
				select {
				case <-wait:
					continue
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				}
			}
		}
		// Serialize opening so concurrent requests cannot exceed the descriptor cap.
		r, e := os.OpenRoot(root)
		if e != nil {
			p.mu.Unlock()
			return nil, nil, ErrArchive
		}
		f, e := r.Open(relative)
		r.Close()
		if e != nil {
			p.mu.Unlock()
			return nil, nil, ErrArchive
		}
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() != size || info.ModTime().UnixNano() != mtime {
			f.Close()
			p.mu.Unlock()
			return nil, nil, ErrChanged
		}
		if e = preflight(f, size); e != nil {
			f.Close()
			p.mu.Unlock()
			return nil, nil, e
		}
		z, e := zip.NewReader(f, size)
		if e != nil {
			f.Close()
			p.mu.Unlock()
			return nil, nil, ErrArchive
		}
		s := &session{file: f, reader: z, refs: 1, touched: time.Now(), manifestGate: make(chan struct{}, 1)}
		p.entries[key] = s
		p.mu.Unlock()
		return s, p.release(key, s), nil
	}
}
func (p *Pool) release(key string, s *session) func() {
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		s.refs--
		s.touched = time.Now()
		if p.closed && s.refs == 0 {
			s.file.Close()
			delete(p.entries, key)
			p.manifestBytes -= s.manifestBytes
		}
		p.signal()
	}
}
func preflight(f *os.File, size int64) error {
	if size < 22 {
		return ErrArchive
	}
	n := min(size, int64(65557))
	tail := make([]byte, n)
	if _, e := f.ReadAt(tail, size-n); e != nil {
		return ErrArchive
	}
	offset := -1
	for i := len(tail) - 22; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:]) == 0x06054b50 && i+22+int(binary.LittleEndian.Uint16(tail[i+20:])) == len(tail) {
			offset = i
			break
		}
	}
	if offset < 0 {
		return ErrArchive
	}
	b := tail[offset:]
	if binary.LittleEndian.Uint16(b[4:]) != 0 || binary.LittleEndian.Uint16(b[6:]) != 0 {
		return ErrArchive
	}
	count := uint64(binary.LittleEndian.Uint16(b[10:]))
	directory := uint64(binary.LittleEndian.Uint32(b[12:]))
	if count == 65535 || directory == 0xffffffff {
		pos := size - n + int64(offset)
		locator := make([]byte, 20)
		if pos < 20 {
			return ErrArchive
		}
		if _, e := f.ReadAt(locator, pos-20); e != nil || binary.LittleEndian.Uint32(locator) != 0x07064b50 || binary.LittleEndian.Uint32(locator[4:]) != 0 || binary.LittleEndian.Uint32(locator[16:]) != 1 {
			return ErrArchive
		}
		at := binary.LittleEndian.Uint64(locator[8:])
		if at > uint64(size-56) {
			return ErrArchive
		}
		record := make([]byte, 56)
		if _, e := f.ReadAt(record, int64(at)); e != nil || binary.LittleEndian.Uint32(record) != 0x06064b50 {
			return ErrArchive
		}
		count = binary.LittleEndian.Uint64(record[32:])
		directory = binary.LittleEndian.Uint64(record[40:])
	}
	if count > MaxEntries {
		return limitError("directory", "entries", int64(count), MaxEntries)
	}
	if directory > MaxDirectory {
		return limitError("directory", "directory_bytes", int64(directory), MaxDirectory)
	}
	return nil
}
func decodeName(f *zip.File, encoding string) (string, error) {
	raw := []byte(f.Name)
	if f.Flags&0x800 != 0 {
		if !utf8.Valid(raw) {
			return "", ErrEncoding
		}
		return string(raw), nil
	}
	if utf8.Valid(raw) {
		return string(raw), nil
	}
	var b []byte
	var e error
	switch encoding {
	case "gb18030":
		b, e = simplifiedchinese.GB18030.NewDecoder().Bytes(raw)
	case "shift_jis":
		b, e = japanese.ShiftJIS.NewDecoder().Bytes(raw)
	default:
		return "", ErrEncoding
	}
	if e != nil || !utf8.Valid(b) || bytes.Contains(b, []byte("\xef\xbf\xbd")) {
		return "", ErrEncoding
	}
	return string(b), nil
}
func validName(n string) bool {
	if n == "" || strings.HasPrefix(n, "/") || strings.ContainsAny(n, "\\\x00") {
		return false
	}
	for _, c := range strings.Split(n, "/") {
		if len(c) >= 2 && ((c[0] >= 'A' && c[0] <= 'Z') || (c[0] >= 'a' && c[0] <= 'z')) && c[1] == ':' {
			return false
		}
		if c == ".." {
			return false
		}
	}
	return true
}
func media(n string) string {
	switch strings.ToLower(path.Ext(n)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".gif":
		return "image/png"
	}
	return ""
}

// NaturalLess compares arbitrary-length ASCII digit runs without integer overflow.
func NaturalLess(a, b string) bool {
	x, y := strings.ToLower(a), strings.ToLower(b)
	for i, j := 0, 0; i < len(x) && j < len(y); {
		if x[i] >= '0' && x[i] <= '9' && y[j] >= '0' && y[j] <= '9' {
			ii, jj := i, j
			for ii < len(x) && x[ii] >= '0' && x[ii] <= '9' {
				ii++
			}
			for jj < len(y) && y[jj] >= '0' && y[jj] <= '9' {
				jj++
			}
			u, v := strings.TrimLeft(x[i:ii], "0"), strings.TrimLeft(y[j:jj], "0")
			if len(u) != len(v) {
				return len(u) < len(v)
			}
			if u != v {
				return u < v
			}
			if ii-i != jj-j {
				return ii-i < jj-j
			}
			i, j = ii, jj
			continue
		}
		if x[i] != y[j] {
			return x[i] < y[j]
		}
		i++
		j++
		if i == len(x) || j == len(y) {
			if len(x) != len(y) {
				return i == len(x)
			}
		}
	}
	if x != y {
		return len(x) < len(y)
	}
	return a < b
}
func (p *Pool) Inspect(ctx context.Context, root, relative, encoding string, size, mtime int64) ([]Entry, error) {
	return p.inspect(ctx, root, relative, encoding, size, mtime, true)
}

// InspectLegacyOrder is only for proving position preservation across v2→v3.
func (p *Pool) InspectLegacyOrder(ctx context.Context, root, relative, encoding string, size, mtime int64) ([]Entry, error) {
	return p.inspect(ctx, root, relative, encoding, size, mtime, false)
}
func (p *Pool) inspect(ctx context.Context, root, relative, encoding string, size, mtime int64, filterSidecars bool) ([]Entry, error) {
	s, release, e := p.acquire(ctx, root, relative, size, mtime)
	if e != nil {
		return nil, e
	}
	defer release()
	key := encoding + ":" + DirectoryVersion
	if !filterSidecars {
		key += ":legacy-order"
	}
	select {
	case s.manifestGate <- struct{}{}:
		defer func() { <-s.manifestGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if entries, ok := s.manifests[key]; ok {
		return append([]Entry(nil), entries...), nil
	}
	// Charge a conservative bound before decoding names or allocating entries.
	charge := int64(len(s.reader.File)) * 128
	for _, f := range s.reader.File {
		charge += int64(len(f.Name)) * 4
	}
	p.mu.Lock()
	for k, v := range p.entries {
		if v != s && v.refs == 0 && p.manifestBytes+charge > MaxManifestBytes {
			v.file.Close()
			p.manifestBytes -= v.manifestBytes
			delete(p.entries, k)
		}
	}
	if p.closed || p.manifestBytes+charge > MaxManifestBytes {
		actual := p.manifestBytes + charge
		p.mu.Unlock()
		return nil, limitError("directory", "manifest_bytes", actual, MaxManifestBytes)
	}
	p.manifestBytes += charge
	s.manifestBytes += charge
	p.mu.Unlock()
	published := false
	defer func() {
		if !published {
			p.mu.Lock()
			p.manifestBytes -= charge
			s.manifestBytes -= charge
			p.signal()
			p.mu.Unlock()
		}
	}()
	if len(s.reader.File) > MaxEntries {
		return nil, limitError("directory", "entries", int64(len(s.reader.File)), MaxEntries)
	}
	out := []Entry{}
	var total uint64
	for index, f := range s.reader.File {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		name, e := decodeName(f, encoding)
		if e != nil {
			return nil, e
		}
		if !validName(name) || f.Mode()&os.ModeSymlink != 0 {
			return nil, ErrName
		}
		if f.FileInfo().IsDir() || filterSidecars && (IsSystemSidecar(name) || (strings.HasPrefix(path.Base(name), "._") && appleDouble(ctx, f))) {
			continue
		}
		kind := media(name)
		if kind == "" {
			continue
		}
		if f.Flags&1 != 0 || (f.Method != zip.Store && f.Method != zip.Deflate) {
			return nil, ErrArchive
		}
		if f.UncompressedSize64 > MaxPage {
			return nil, limitError("directory", "page_bytes", int64(min(f.UncompressedSize64, uint64(1<<63-1))), MaxPage)
		}
		if total+f.UncompressedSize64 > MaxBook {
			return nil, limitError("directory", "book_bytes", int64(total+f.UncompressedSize64), MaxBook)
		}
		total += f.UncompressedSize64
		out = append(out, Entry{Entry: index, Name: name, MediaType: kind})
	}
	if len(out) == 0 {
		return nil, ErrArchive
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Entry < out[j].Entry
		}
		return NaturalLess(out[i].Name, out[j].Name)
	})
	for i := range out {
		out[i].Index = i + 1
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if s.manifests == nil {
		s.manifests = map[string][]Entry{}
	}
	s.manifests[key] = out
	published = true
	return append([]Entry(nil), out...), nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(b)
}
func (p *Pool) reserve(ctx context.Context, n int64) (func(), error) { return p.budget.reserve(ctx, n) }

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > MaxPage {
		return 0, ErrLimit
	}
	return b.Buffer.Write(p)
}
func (p *Pool) Image(ctx context.Context, root, relative string, size, mtime int64, entry int, cover bool) ([]byte, string, string, error) {
	select {
	case p.imageSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, "", "", ctx.Err()
	}
	defer func() { <-p.imageSlots }()
	s, release, e := p.acquire(ctx, root, relative, size, mtime)
	if e != nil {
		return nil, "", "", e
	}
	defer release()
	if entry < 0 || entry >= len(s.reader.File) {
		return nil, "", "", ErrImage
	}
	f := s.reader.File[entry]
	if f.UncompressedSize64 > MaxPage {
		return nil, "", "", limitError("read", "page_bytes", int64(min(f.UncompressedSize64, uint64(1<<63-1))), MaxPage)
	}
	reader, e := f.Open()
	if e != nil {
		return nil, "", "", ErrImage
	}
	cfg, format, e := image.DecodeConfig(contextReader{ctx, io.LimitReader(reader, int64(f.UncompressedSize64)+1)})
	reader.Close()
	if e != nil {
		return nil, "", "", ErrImage
	}
	pixels := int64(cfg.Width) * int64(cfg.Height)
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > MaxPixels || cfg.Height > MaxPixels || pixels < 0 || pixels > MaxPixels {
		return nil, "", "", limitError("decode", "pixels", pixels, MaxPixels)
	}
	if format != "jpeg" && format != "png" && format != "webp" && format != "gif" && format != "bmp" {
		return nil, "", "", ErrImage
	}
	free, e := p.reserve(ctx, int64(f.UncompressedSize64)*2+pixels*12+1<<20)
	if e != nil {
		return nil, "", "", e
	}
	defer free()
	reader, e = f.Open()
	if e != nil {
		return nil, "", "", ErrImage
	}
	raw, e := io.ReadAll(contextReader{ctx, io.LimitReader(reader, int64(f.UncompressedSize64)+1)})
	reader.Close()
	if e != nil || uint64(len(raw)) != f.UncompressedSize64 {
		return nil, "", "", ErrImage
	}
	if e = ctx.Err(); e != nil {
		return nil, "", "", e
	}
	decoded, actual, e := image.Decode(contextReader{ctx, bytes.NewReader(raw)})
	if e != nil || actual != format {
		return nil, "", "", ErrImage
	}
	hash := fmt.Sprintf("%x", sha1.Sum(raw))
	kind := "image/" + format
	if format == "jpeg" {
		kind = "image/jpeg"
	}
	if cover {
		bounds := decoded.Bounds()
		w, h := bounds.Dx(), bounds.Dy()
		scale := min(1.0, min(320.0/float64(w), 480.0/float64(h)))
		nw, nh := max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
		thumb := image.NewRGBA(image.Rect(0, 0, nw, nh))
		for y := 0; y < nh; y++ {
			if e = ctx.Err(); e != nil {
				return nil, "", "", e
			}
			for x := 0; x < nw; x++ {
				thumb.Set(x, y, decoded.At(bounds.Min.X+x*w/nw, bounds.Min.Y+y*h/nh))
			}
		}
		decoded = thumb
	}
	if cover || format == "gif" || format == "bmp" {
		var out limitedBuffer
		if e = png.Encode(&out, decoded); e != nil {
			return nil, "", "", ErrLimit
		}
		return out.Bytes(), "image/png", hash, nil
	}
	return raw, kind, hash, nil
}

// VerifyArchive checks every supported image's full ZIP stream (including CRC)
// and decoding through the same pixel/memory limits as normal reading.
func (p *Pool) VerifyArchive(ctx context.Context, root, relative, encoding string, size, mtime, budget int64) (int, int64, error) {
	entries, e := p.Inspect(ctx, root, relative, encoding, size, mtime)
	if e != nil {
		return 0, 0, e
	}
	session, release, e := p.acquire(ctx, root, relative, size, mtime)
	if e != nil {
		return 0, 0, e
	}
	defer release()
	var read int64
	pages := 0
	for _, entry := range entries {
		n := session.reader.File[entry.Entry].UncompressedSize64
		if n > uint64(MaxPage) || n > uint64(max(0, budget-read)) {
			return pages, read, ErrLimit
		}
		// Charge the bounded declared payload even when decoding fails, so a
		// run of damaged pages cannot repeatedly bypass the logical IO budget.
		read += int64(n)
		if _, _, _, e = p.Image(ctx, root, relative, size, mtime, entry.Entry, false); e != nil {
			return pages, read, e
		}
		pages++
	}
	info, e := session.file.Stat()
	if e != nil {
		return pages, read, e
	}
	if info.Size() != size || info.ModTime().UnixNano() != mtime {
		return pages, read, ErrChanged
	}
	rootHandle, e := os.OpenRoot(root)
	if e != nil {
		return pages, read, e
	}
	defer rootHandle.Close()
	current, e := rootHandle.Stat(relative)
	if e != nil {
		return pages, read, e
	}
	if !os.SameFile(info, current) || current.Size() != size || current.ModTime().UnixNano() != mtime {
		return pages, read, ErrChanged
	}
	return pages, read, nil
}

// IsSystemSidecar is deliberately narrower than hidden-file filtering. Call only
// after validating the entire member path and rejecting symbolic links.
func IsSystemSidecar(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if part == "__MACOSX" {
			return true
		}
	}
	base := path.Base(name)
	return base == ".DS_Store"
}

// Name alone is insufficient: real user images can legitimately start with ._.
func appleDouble(ctx context.Context, f *zip.File) bool {
	if f.UncompressedSize64 < 26 {
		return false
	}
	r, e := f.Open()
	if e != nil {
		return false
	}
	defer r.Close()
	var header [26]byte
	if _, e = io.ReadFull(contextReader{ctx, r}, header[:]); e != nil {
		return false
	}
	return AppleDoubleHeader(header[:])
}
func AppleDoubleHeader(b []byte) bool {
	return len(b) >= 26 && binary.BigEndian.Uint32(b) == 0x00051607 && (binary.BigEndian.Uint32(b[4:]) == 0x00020000 || binary.BigEndian.Uint32(b[4:]) == 0x00010000)
}
