package comic

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

const ImageParallel = 2
const ImagePrefetchParallel = 1
const ImageWait = 5 * time.Second
const ErrImageBusy Error = "image_busy"

type imageDeadlineKey struct{}

func WithImageDeadline(ctx context.Context, deadline time.Time) context.Context {
	return context.WithValue(ctx, imageDeadlineKey{}, deadline)
}

func imageWaitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Value(imageDeadlineKey{}).(time.Time); ok {
		return context.WithDeadline(ctx, deadline)
	}
	return ctx, func() {}
}

func imageWaitError(ctx context.Context, err error) error {
	if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return ErrImageBusy
	}
	return err
}

type imageScheduler struct {
	mu               sync.Mutex
	active, prefetch int
	queue            []*imageTicket
}

type imageTicket struct {
	s       *imageScheduler
	ready   chan struct{}
	display bool
	started bool
}

func (s *imageScheduler) enqueue(display bool) *imageTicket {
	ticket := &imageTicket{s: s, ready: make(chan struct{}), display: display}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, ticket)
	s.dispatch()
	return ticket
}

// The caller holds the scheduler mutex. Displays keep FIFO order within
// their class and always take the next slot ahead of queued prefetches.
func (s *imageScheduler) dispatch() {
	for s.active < ImageParallel {
		index := slices.IndexFunc(s.queue, func(t *imageTicket) bool { return t.display })
		if index < 0 && len(s.queue) > 0 && s.prefetch < ImagePrefetchParallel {
			index = 0
		}
		if index < 0 {
			return
		}
		t := s.queue[index]
		s.queue = slices.Delete(s.queue, index, index+1)
		t.started = true
		s.active++
		if !t.display {
			s.prefetch++
		}
		close(t.ready)
	}
}

func (t *imageTicket) setDisplay(display bool) {
	t.s.mu.Lock()
	defer t.s.mu.Unlock()
	if !t.started {
		t.display = display
		t.s.dispatch()
	}
}

func (t *imageTicket) release() {
	s := t.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.started {
		s.active--
		if !t.display {
			s.prefetch--
		}
	} else if i := slices.Index(s.queue, t); i >= 0 {
		s.queue = slices.Delete(s.queue, i, i+1)
	}
	s.dispatch()
}

func (t *imageTicket) wait(ctx context.Context) (func(), error) {
	wait, cancel := imageWaitContext(ctx)
	defer cancel()
	select {
	case <-t.ready:
	case <-wait.Done():
	}
	if err := wait.Err(); err != nil {
		t.release()
		return nil, imageWaitError(ctx, err)
	}
	return t.release, nil
}

func (p *Pool) enterImage(ctx context.Context) (func(), error) {
	return p.budget.scheduler.enqueue(true).wait(ctx)
}

// ReserveResponse keeps legacy generated cover bytes charged while the HTTP
// writer owns them. Page responses already carry a lease from the decode.
func (p *Pool) ReserveResponse(ctx context.Context, data []byte) (func(), error) {
	return p.reserve(ctx, int64(cap(data)))
}
