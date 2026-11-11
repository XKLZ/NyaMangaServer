package comic

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io"
)

// LookupSHA1 verifies the representative member's full original stream and ZIP
// checksum without decoding pixels. Success is not a full image-health check.
func (p *Pool) LookupSHA1(ctx context.Context, root, relative, encoding string, size, mtime int64) (string, int, error) {
	entries, e := p.Inspect(ctx, root, relative, encoding, size, mtime)
	if e != nil {
		return "", 0, e
	}
	index := 0
	if len(entries) >= 8 {
		index = 7
	}
	s, release, e := p.acquire(ctx, root, relative, size, mtime)
	if e != nil {
		return "", len(entries), e
	}
	defer release()
	f := s.reader.File[entries[index].Entry]
	if f.UncompressedSize64 > MaxPage {
		return "", len(entries), limitError("lookup", "page_bytes", int64(min(f.UncompressedSize64, uint64(1<<63-1))), MaxPage)
	}
	r, e := f.Open()
	if e != nil {
		return "", len(entries), ErrImage
	}
	defer r.Close()
	h := sha1.New()
	n, e := io.CopyBuffer(h, contextReader{ctx, io.LimitReader(r, int64(f.UncompressedSize64)+1)}, make([]byte, 32<<10))
	if ctx.Err() != nil {
		return "", len(entries), ctx.Err()
	}
	if e != nil || n != int64(f.UncompressedSize64) {
		return "", len(entries), ErrImage
	}
	// Reacquiring checks the source inode/stat identity before publishing the hash.
	_, check, e := p.acquire(ctx, root, relative, size, mtime)
	if e != nil {
		return "", len(entries), e
	}
	check()
	return fmt.Sprintf("%x", h.Sum(nil)), len(entries), nil
}
