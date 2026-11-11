package comic

import (
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
}

func NewBudget(bytes int64) *Budget {
	if bytes <= 0 {
		bytes = DefaultImageMemory
	}
	return &Budget{limit: bytes, changed: make(chan struct{}), slots: make(chan struct{}, 2)}
}

var defaultBudget = NewBudget(DefaultImageMemory)

func (b *Budget) reserve(ctx context.Context, n int64) (func(), error) {
	if n < 0 || n > b.limit {
		return nil, limitError("decode", "estimated_memory_bytes", n, b.limit)
	}
	for {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		b.mu.Lock()
		if b.used+n <= b.limit {
			b.used += n
			b.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					b.mu.Lock()
					defer b.mu.Unlock()
					b.used -= n
					close(b.changed)
					b.changed = make(chan struct{})
				})
			}, nil
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
