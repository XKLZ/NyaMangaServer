package comic

import (
	"container/list"
	"context"
	"sync"
)

const DefaultImageMemory = int64(512 << 20)

// LimitError contains only bounded diagnostic values, never archive paths.
type LimitError struct {
	Stage    string `json:"stage"`
	Resource string `json:"resource"`
	Actual   int64  `json:"actual"`
	Limit    int64  `json:"limit"`
}

func (e *LimitError) Error() string { return string(ErrLimit) }
func (e *LimitError) Unwrap() error { return ErrLimit }
func limitError(stage, resource string, actual, limit int64) error {
	return &LimitError{Stage: stage, Resource: resource, Actual: actual, Limit: limit}
}

// Budget belongs to the application, and can be shared by separate archive pools.
// Reservations cover source bytes, decoded pixels and derived output together.
type Budget struct {
	mu          sync.Mutex
	used, limit int64
	changed     chan struct{}
	slots       chan struct{}
	pages       map[PageKey]*list.Element
	verified    list.List
	hot         list.List
	hotBytes    int64
	flights     map[PageKey]*pageFlight
}

func NewBudget(bytes int64) *Budget {
	if bytes <= 0 {
		bytes = DefaultImageMemory
	}
	return &Budget{limit: bytes, changed: make(chan struct{}), slots: make(chan struct{}, 2), pages: make(map[PageKey]*list.Element), flights: make(map[PageKey]*pageFlight)}
}

var defaultBudget = NewBudget(DefaultImageMemory)

func (b *Budget) reserve(ctx context.Context, n int64) (func(), error) {
	r, e := b.reserveMemory(ctx, n)
	if e != nil {
		return nil, e
	}
	var once sync.Once
	return func() { once.Do(r.release) }, nil
}

type imageMemory struct {
	budget *Budget
	bytes  int64
}

func (r *imageMemory) release() {
	r.budget.mu.Lock()
	r.budget.freeLocked(r.bytes)
	r.budget.mu.Unlock()
}

// Transfer the output reservation before releasing the temporary decode space.
func (r *imageMemory) retain(data []byte, kind string) (*pageBytes, error) {
	n := int64(cap(data))
	if n > r.bytes {
		return nil, limitError("encode", "response_memory_bytes", n, r.bytes)
	}
	r.bytes -= n
	return &pageBytes{data: data, kind: kind, refs: 1}, nil
}

func (b *Budget) freeLocked(n int64) {
	b.used -= n
	b.signalLocked()
}

func (b *Budget) signalLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

func (b *Budget) reserveMemory(ctx context.Context, n int64) (*imageMemory, error) {
	if n < 0 || n > b.limit {
		return nil, limitError("decode", "estimated_memory_bytes", n, b.limit)
	}
	for {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		b.mu.Lock()
		for b.used+n > b.limit && b.evictHotLocked() {
		}
		if b.used+n <= b.limit {
			b.used += n
			b.mu.Unlock()
			return &imageMemory{budget: b, bytes: n}, nil
		}
		wait := b.changed
		b.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
