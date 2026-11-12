package comic

import (
	"container/list"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

const PageValidationLimit = 1024
const PageValidationIdle = 5 * time.Minute
const PageHotBytes = 64 << 20
const pageRepresentation = "archive-page-v1"

type SourceIdentity struct {
	device, inode uint64
	Size, Mtime   int64
}

// IdentifySource checks the configured root boundary and the live regular file.
func IdentifySource(root, relative string) (SourceIdentity, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return SourceIdentity{}, err
	}
	defer r.Close()
	info, err := r.Stat(relative)
	if err != nil {
		return SourceIdentity{}, err
	}
	source, ok := sourceIdentity(info)
	if !ok {
		return SourceIdentity{}, ErrArchive
	}
	return source, nil
}

func sourceIdentity(info os.FileInfo) (SourceIdentity, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return SourceIdentity{}, false
	}
	return SourceIdentity{uint64(st.Dev), uint64(st.Ino), info.Size(), info.ModTime().UnixNano()}, true
}

func (s SourceIdentity) Matches(info os.FileInfo) bool {
	current, ok := sourceIdentity(info)
	return ok && s == current
}

type PageKey struct {
	Epoch, EditionID   string
	Root, Path         string
	ContentRevision    int64
	ParsingFingerprint string
	Entry              int
	Source             SourceIdentity
	representation     string
}

type PageImage struct {
	Bytes       []byte
	Kind, ETag  string
	NotModified bool
	Release     func()
}

type pageBytes struct {
	data []byte
	kind string
	refs int // active flights and response leases
}

type pageRecord struct {
	key        PageKey
	etag, kind string
	touched    time.Time
	bytes      *pageBytes
	hot        *list.Element
}

type pageWaiter struct {
	ctx     context.Context
	display bool
}

type pageFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	ticket  *imageTicket
	waiters map[*pageWaiter]struct{}
	output  *pageBytes
	etag    string
	err     error
}

func (b *Budget) removeHotLocked(r *pageRecord) {
	b.hot.Remove(r.hot)
	b.hotBytes -= int64(cap(r.bytes.data))
	if r.bytes.refs == 0 {
		b.freeLocked(int64(cap(r.bytes.data)))
	}
	r.bytes, r.hot = nil, nil
}

func (b *Budget) evictHotLocked() bool {
	for e := b.hot.Back(); e != nil; e = e.Prev() {
		r := e.Value.(*pageRecord)
		if r.bytes.refs == 0 {
			b.removeHotLocked(r)
			return true
		}
	}
	return false
}

func (b *Budget) removeRecordLocked(e *list.Element) {
	r := e.Value.(*pageRecord)
	if r.hot != nil {
		b.removeHotLocked(r)
	}
	delete(b.pages, r.key)
	b.verified.Remove(e)
}

func (b *Budget) sweepPagesLocked(now time.Time) {
	for e := b.verified.Back(); e != nil; e = b.verified.Back() {
		if now.Sub(e.Value.(*pageRecord).touched) < PageValidationIdle {
			return
		}
		b.removeRecordLocked(e)
	}
}

func (b *Budget) dropPageRefLocked(output *pageBytes, key PageKey) {
	output.refs--
	if output.refs != 0 {
		return
	}
	if e := b.pages[key]; e == nil || e.Value.(*pageRecord).bytes != output {
		b.freeLocked(int64(cap(output.data)))
	} else {
		// A parked decode may now evict this hot buffer.
		b.signalLocked()
	}
}

func (b *Budget) leasePageLocked(output *pageBytes, key PageKey, tag string) PageImage {
	output.refs++
	var once sync.Once
	return PageImage{Bytes: output.data, Kind: output.kind, ETag: tag, Release: func() {
		once.Do(func() {
			b.mu.Lock()
			b.dropPageRefLocked(output, key)
			b.mu.Unlock()
		})
	}}
}

// Page loads and validates one archive member. Only source-verified keys are
// accepted from the library. The caller rechecks access before writing headers.
func (p *Pool) Page(ctx context.Context, key PageKey, display bool, matches func(string) bool) (PageImage, error) {
	key.representation = pageRepresentation
	b := p.budget
	b.mu.Lock()
	if err := ctx.Err(); err != nil {
		b.mu.Unlock()
		return PageImage{}, err
	}
	now := time.Now()
	b.sweepPagesLocked(now)
	if e := b.pages[key]; e != nil {
		r := e.Value.(*pageRecord)
		r.touched = now
		b.verified.MoveToFront(e)
		if matches(r.etag) {
			b.mu.Unlock()
			return PageImage{Kind: r.kind, ETag: r.etag, NotModified: true}, nil
		}
		if r.bytes != nil {
			b.hot.MoveToFront(r.hot)
			out := b.leasePageLocked(r.bytes, key, r.etag)
			b.mu.Unlock()
			return out, nil
		}
	}
	f := b.flights[key]
	if f == nil {
		deadline, ok := ctx.Value(imageDeadlineKey{}).(time.Time)
		if !ok {
			deadline = now.Add(ImageWait)
		}
		work, cancel := context.WithCancel(WithImageDeadline(context.Background(), deadline))
		f = &pageFlight{done: make(chan struct{}), cancel: cancel, ticket: b.scheduler.enqueue(display), waiters: make(map[*pageWaiter]struct{})}
		b.flights[key] = f
		go p.loadPage(work, key, f)
	}
	w := &pageWaiter{ctx: ctx, display: display}
	if display {
		f.ticket.setDisplay(true)
	}
	f.waiters[w] = struct{}{}
	b.mu.Unlock()
	select {
	case <-ctx.Done():
	case <-f.done:
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(f.waiters, w)
	defer func() {
		if len(f.waiters) != 0 {
			display := false
			for waiter := range f.waiters {
				display = display || waiter.display && waiter.ctx.Err() == nil
			}
			f.ticket.setDisplay(display)
		} else {
			f.cancel()
			if b.flights[key] == f {
				delete(b.flights, key)
			}
			if f.output != nil {
				b.dropPageRefLocked(f.output, key)
				f.output = nil
			}
		}
	}()
	if err := ctx.Err(); err != nil {
		return PageImage{}, err
	}
	if f.err != nil {
		return PageImage{}, f.err
	}
	return b.leasePageLocked(f.output, key, f.etag), nil
}

func (p *Pool) loadPage(ctx context.Context, key PageKey, f *pageFlight) {
	var output *pageBytes
	load := func() error {
		release, err := f.ticket.wait(ctx)
		if err != nil {
			return err
		}
		defer release()
		if p.beforePageDecode != nil {
			p.beforePageDecode(ctx, key)
		}
		_, _, _, err = p.decodeArchiveImage(ctx, key.Root, key.Path, key.Source.Size, key.Source.Mtime, key.Entry, false, &key.Source,
			func(memory *imageMemory, data []byte, kind string) error {
				var err error
				output, err = memory.retain(data, kind)
				return err
			})
		return err
	}
	err := load()
	if err == nil {
		if current, e := IdentifySource(key.Root, key.Path); e != nil || current != key.Source {
			err = ErrChanged
		}
	}
	var etag string
	if err == nil {
		etag = fmt.Sprintf("\"%x\"", sha256.Sum256(output.data))
	}

	b := p.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	defer close(f.done)
	if b.flights[key] == f {
		delete(b.flights, key)
	}
	live := false
	for w := range f.waiters {
		live = live || w.ctx.Err() == nil
	}
	if !live {
		if output != nil {
			b.dropPageRefLocked(output, key)
		}
		f.err = context.Canceled
		return
	}
	f.err = err
	if err != nil {
		if output != nil {
			b.dropPageRefLocked(output, key)
		}
		return
	}
	f.output, f.etag = output, etag
	now := time.Now()
	b.sweepPagesLocked(now)
	if e := b.pages[key]; e != nil {
		b.removeRecordLocked(e)
	}
	for len(b.pages) >= PageValidationLimit {
		b.removeRecordLocked(b.verified.Back())
	}
	r := &pageRecord{key: key, etag: etag, kind: output.kind, touched: now}
	b.pages[key] = b.verified.PushFront(r)
	n, limit := int64(cap(output.data)), min(PageHotBytes, b.limit/8)
	if n <= limit {
		for b.hotBytes+n > limit && b.evictHotLocked() {
		}
		if b.hotBytes+n <= limit {
			r.bytes, r.hot = output, b.hot.PushFront(r)
			b.hotBytes += n
		}
	}
}
