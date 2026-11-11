package comic

import (
	"context"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

// WebInfo reads only the selected bounded hint entry; it never extracts an archive.
func (p *Pool) WebInfo(ctx context.Context, root, relative, encoding string, size, mtime int64) (string, error) {
	s, release, e := p.acquire(ctx, root, relative, size, mtime)
	if e != nil {
		return "", e
	}
	defer release()
	type candidate struct {
		index int
		name  string
	}
	var matches []candidate
	for i, f := range s.reader.File {
		if e = ctx.Err(); e != nil {
			return "", e
		}
		name, e := decodeName(f, encoding)
		if e != nil {
			return "", e
		}
		if !validName(name) || f.Mode()&os.ModeSymlink != 0 {
			return "", ErrName
		}
		parts := strings.Split(name, "/")
		if !f.FileInfo().IsDir() && strings.EqualFold(parts[len(parts)-1], "webinfo.txt") {
			matches = append(matches, candidate{i, name})
		}
	}
	if len(matches) == 0 {
		return "", nil
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i].name, matches[j].name
		if strings.EqualFold(a, "webinfo.txt") != strings.EqualFold(b, "webinfo.txt") {
			return strings.EqualFold(a, "webinfo.txt")
		}
		return NaturalLess(a, b)
	})
	f := s.reader.File[matches[0].index]
	if f.UncompressedSize64 > 4096 {
		return "", ErrLimit
	}
	reader, e := f.Open()
	if e != nil {
		return "", ErrArchive
	}
	defer reader.Close()
	data, e := io.ReadAll(io.LimitReader(contextReader{ctx, reader}, 4097))
	if e != nil {
		return "", ErrArchive
	}
	if len(data) > 4096 {
		return "", ErrLimit
	}
	if !utf8.Valid(data) {
		return "", ErrEncoding
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	if strings.ContainsAny(text, "\r\n\x00") {
		return "", ErrName
	}
	return text, ctx.Err()
}
