package comic

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestImageSchedulingPrioritizesDisplayAndReservesPrefetchCapacity(t *testing.T) {
	s := &imageScheduler{}
	prefetch := s.enqueue(false)
	prefetchLater := s.enqueue(false)
	expectImageTicket(t, prefetch, true)
	expectImageTicket(t, prefetchLater, false)

	display := s.enqueue(true)
	displayLater := s.enqueue(true)
	expectImageTicket(t, display, true)
	expectImageTicket(t, displayLater, false)
	display.release()
	expectImageTicket(t, displayLater, true)
	expectImageTicket(t, prefetchLater, false)
	prefetch.release()
	expectImageTicket(t, prefetchLater, true)
	displayLater.release()
	prefetchLater.release()

	first := s.enqueue(false)
	coalesced := s.enqueue(false)
	expectImageTicket(t, coalesced, false)
	coalesced.setDisplay(true)
	expectImageTicket(t, coalesced, true)
	if s.active != ImageParallel || s.prefetch != ImagePrefetchParallel {
		t.Fatal("coalesced display did not reserve the remaining slot")
	}
	first.release()
	coalesced.release()
}

func expectImageTicket(t *testing.T, ticket *imageTicket, ready bool) {
	t.Helper()
	select {
	case <-ticket.ready:
		if !ready {
			t.Fatal("unexpected processing grant")
		}
	default:
		if ready {
			t.Fatal("expected processing grant")
		}
	}
}

func TestImageWaitExpiresAndReleasesQueueCapacity(t *testing.T) {
	s := &imageScheduler{}
	a, b := s.enqueue(true), s.enqueue(true)
	c := s.enqueue(true)
	past := WithImageDeadline(context.Background(), time.Now().Add(-time.Second))
	if _, e := c.wait(past); !errors.Is(e, ErrImageBusy) {
		t.Fatal(e)
	}
	d := s.enqueue(true)
	a.release()
	expectImageTicket(t, d, true)
	b.release()
	d.release()
	if s.active != 0 || len(s.queue) != 0 {
		t.Fatal("leaked processing ticket")
	}

	budget := NewBudget(32 << 20)
	release, e := budget.reserve(context.Background(), budget.limit)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	if _, e = budget.reserve(past, 1); !errors.Is(e, ErrImageBusy) {
		t.Fatal("memory wait did not report image_busy", e)
	}
}
