package library

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"

	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/model"
)

const downloadBlock = 1 << 20

var downloadKey = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

func (s *Service) PrepareDownload(ctx context.Context, edition string, req model.DownloadRequest) (model.Download, error) {
	if req.ContentRevision < 1 || !downloadKey.MatchString(req.Key) {
		return model.Download{}, model.ErrInput
	}
	key, err := s.downloadSource(ctx, edition, req.ContentRevision)
	if err != nil {
		return model.Download{}, err
	}
	id, err := model.NewID()
	if err != nil {
		return model.Download{}, err
	}
	d, err := s.store.PrepareDownload(ctx, model.Download{ID: id, EditionID: edition, ContentRevision: req.ContentRevision,
		Length: key.Source.Size, Epoch: key.Epoch, Key: req.Key, Source: downloadSourceID(key)})
	if err == nil && d.State == "preparing" {
		select {
		case s.downloadWake <- struct{}{}:
		default:
		}
	}
	return d, err
}

func (s *Service) Download(ctx context.Context, id string) (model.Download, error) {
	d, err := s.store.Download(ctx, id)
	if err != nil {
		return d, err
	}
	b, err := s.store.Edition(ctx, d.EditionID)
	if err == nil && b.ContentRevision != d.ContentRevision {
		err = comic.ErrChanged
	}
	return d, err
}

func (s *Service) AbandonDownload(ctx context.Context, id string) error {
	if err := s.store.AbandonDownload(ctx, id); err != nil {
		return err
	}
	s.downloadMu.Lock()
	defer s.downloadMu.Unlock()
	if s.downloadID == id {
		s.downloadCancel()
	}
	return nil
}

func (s *Service) downloadSource(ctx context.Context, edition string, revision int64) (comic.PageKey, error) {
	b, err := s.store.Edition(ctx, edition)
	if err != nil {
		return comic.PageKey{}, err
	}
	if b.ContentRevision != revision {
		return comic.PageKey{}, comic.ErrChanged
	}
	key, _, err := s.pageSource(ctx, *b)
	return key, err
}

func downloadSourceID(key comic.PageKey) string {
	return fmt.Sprintf("%x", sha256.Sum256(fmt.Appendf(nil, "%q:%q:%#v", key.Root, key.Path, key.Source)))
}

func openDownloadFile(key comic.PageKey) (*os.File, error) {
	root, err := os.OpenRoot(key.Root)
	if err != nil {
		return nil, ErrRoot
	}
	defer root.Close()
	f, err := root.Open(key.Path)
	if err != nil {
		return nil, ErrUnavailable
	}
	if err = checkDownloadFile(f, key); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func checkDownloadFile(f *os.File, key comic.PageKey) error {
	info, err := f.Stat()
	if err != nil || !key.Source.Matches(info) {
		return comic.ErrChanged
	}
	return nil
}

type DownloadFile struct {
	file                      *os.File
	Download                  model.Download
	Filename                  string
	check                     func() error
	pos, end, verifiedThrough int64
	ReadError                 error
}

func (s *Service) OpenDownload(ctx context.Context, id string, authenticate func() error) (*DownloadFile, error) {
	d, err := s.Download(ctx, id)
	if err != nil {
		return nil, err
	}
	if d.State != "ready" {
		return nil, model.ErrDownloadPending
	}
	key, err := s.downloadSource(ctx, d.EditionID, d.ContentRevision)
	if err != nil {
		return nil, err
	}
	if key.Epoch != d.Epoch || downloadSourceID(key) != d.Source {
		return nil, comic.ErrChanged
	}
	f, err := openDownloadFile(key)
	if err != nil {
		return nil, err
	}
	file := &DownloadFile{file: f, Download: d, Filename: downloadFilename(key.Path), end: d.Length}
	file.check = func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := authenticate(); err != nil {
			return err
		}
		current, err := s.store.Download(ctx, id)
		if err != nil {
			return err
		}
		if current.State != "ready" {
			return model.ErrDownloadPending
		}
		currentKey, err := s.downloadSource(ctx, d.EditionID, d.ContentRevision)
		if err != nil {
			return err
		}
		if downloadSourceID(currentKey) != d.Source {
			return comic.ErrChanged
		}
		return checkDownloadFile(f, key)
	}
	if err = file.check(); err != nil {
		f.Close()
		return nil, err
	}
	return file, nil
}

func downloadFilename(relative string) string {
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) || r == '/' || r == '\\' {
			return -1
		}
		return r
	}, path.Base(relative))
	if name == "" || name == "." || name == ".." || len(name) > 240 {
		return "comic" + strings.ToLower(path.Ext(relative))
	}
	return name
}

func (f *DownloadFile) Close() error { return f.file.Close() }

func (f *DownloadFile) SetEnd(end int64) { f.end = end }

func (f *DownloadFile) Seek(n int64, whence int) (int64, error) {
	pos, err := f.file.Seek(n, whence)
	if err == nil {
		f.pos = pos
		f.verifiedThrough = pos
	}
	return pos, err
}

func (f *DownloadFile) Read(p []byte) (int, error) {
	if f.pos >= f.end {
		return 0, io.EOF
	}
	if f.pos >= f.verifiedThrough {
		if err := f.check(); err != nil {
			f.ReadError = err
			return 0, err
		}
		f.verifiedThrough = f.pos + downloadBlock
	}
	p = p[:min(int64(len(p)), f.end-f.pos, f.verifiedThrough-f.pos)]
	n, err := f.file.Read(p)
	f.pos += int64(n)
	if f.pos == f.end {
		if checkErr := f.check(); checkErr != nil {
			f.ReadError = checkErr
			return 0, checkErr
		}
	} else if err == io.EOF || n == 0 && err == nil {
		err = io.ErrUnexpectedEOF
	}
	if err != nil && err != io.EOF {
		f.ReadError = err
	}
	return n, err
}

func (s *Service) runDownloads(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		d, err := s.store.NextDownload(ctx)
		if err == nil {
			s.hashDownload(ctx, d)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.downloadWake:
		}
	}
}

func (s *Service) hashDownload(ctx context.Context, d model.Download) {
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	s.downloadMu.Lock()
	s.downloadID, s.downloadCancel = d.ID, cancel
	s.downloadMu.Unlock()
	defer func() { s.downloadMu.Lock(); s.downloadID, s.downloadCancel = "", nil; s.downloadMu.Unlock() }()
	digest, err := s.digestDownload(work, d)
	if work.Err() != nil || errors.Is(err, model.ErrHealthMaintenance) {
		return
	}
	reason := ""
	if err != nil {
		reason = "source_unavailable"
		if errors.Is(err, comic.ErrChanged) {
			reason = "content_changed"
		}
	}
	_ = s.store.FinishDownload(ctx, d, digest, reason)
}

func (s *Service) digestDownload(ctx context.Context, d model.Download) (string, error) {
	account, err := s.store.AccountByID(ctx, d.AccountID)
	if err != nil {
		return "", err
	}
	if account == nil || !account.Enabled || account.MustChangePassword {
		return "", model.ErrForbidden
	}
	ctx = model.WithActor(ctx, *account)
	verify := func() (comic.PageKey, error) {
		if err := ctx.Err(); err != nil {
			return comic.PageKey{}, err
		}
		if err := s.store.HealthWriteAllowed(ctx); err != nil {
			return comic.PageKey{}, err
		}
		row, err := s.store.Download(ctx, d.ID)
		if err != nil {
			return comic.PageKey{}, err
		}
		if row.State != "preparing" {
			return comic.PageKey{}, model.ErrDownloadPending
		}
		key, err := s.downloadSource(ctx, d.EditionID, d.ContentRevision)
		if err == nil && downloadSourceID(key) != d.Source {
			err = comic.ErrChanged
		}
		return key, err
	}
	key, err := verify()
	if err != nil {
		return "", err
	}
	f, err := openDownloadFile(key)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	buf := make([]byte, downloadBlock)
	for remaining := d.Length; remaining > 0; {
		if _, err = verify(); err != nil {
			return "", err
		}
		n, err := io.ReadFull(f, buf[:min(int64(len(buf)), remaining)])
		if err != nil {
			return "", err
		}
		_, _ = hash.Write(buf[:n])
		remaining -= int64(n)
	}
	if _, err = verify(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), checkDownloadFile(f, key)
}
