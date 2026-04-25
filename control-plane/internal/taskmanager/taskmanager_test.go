package taskmanager

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m := New(Config{
		RetainTerminal:   50 * time.Millisecond,
		SubscriberBuffer: 8,
		GCInterval:       20 * time.Millisecond,
	})
	t.Cleanup(m.Close)
	return m
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", msg)
}

func TestStartAndSucceed(t *testing.T) {
	m := newTestManager(t)
	sub, unsub := m.Subscribe()
	defer unsub()

	var ran atomic.Bool
	id := m.Start(StartOpts{
		Type: TaskInstanceCreate,
		Run: func(ctx context.Context, h *Handle) error {
			ran.Store(true)
			h.UpdateMessage("step 1")
			return nil
		},
	})

	waitFor(t, func() bool { return ran.Load() }, time.Second, "run executed")

	// Drain events
	seen := map[EventType]int{}
	timeout := time.After(500 * time.Millisecond)
drain:
	for {
		select {
		case ev := <-sub:
			seen[ev.Type]++
			if ev.Type == EventEnded {
				if ev.Task.State != StateSucceeded {
					t.Fatalf("expected succeeded, got %s", ev.Task.State)
				}
				break drain
			}
		case <-timeout:
			t.Fatalf("did not observe ended event; saw: %+v", seen)
		}
	}

	got, ok := m.Get(id)
	if !ok || got.State != StateSucceeded {
		t.Fatalf("get: ok=%v state=%s", ok, got.State)
	}
	if got.EndedAt == nil {
		t.Fatalf("EndedAt not set")
	}
}

func TestStartAndFail(t *testing.T) {
	m := newTestManager(t)
	boom := errors.New("boom")
	id := m.Start(StartOpts{
		Type: TaskBackupCreate,
		Run: func(ctx context.Context, h *Handle) error {
			return boom
		},
	})
	waitFor(t, func() bool {
		t, _ := m.Get(id)
		return t.State == StateFailed
	}, time.Second, "failed state")
	got, _ := m.Get(id)
	if got.Message != "boom" {
		t.Fatalf("want message 'boom', got %q", got.Message)
	}
}

func TestCancelRunsOnCancelAndMarksCanceled(t *testing.T) {
	m := newTestManager(t)

	var onCancelCount atomic.Int32
	started := make(chan struct{})
	id := m.Start(StartOpts{
		Type: TaskBackupCreate,
		OnCancel: func(ctx context.Context) {
			onCancelCount.Add(1)
		},
		Run: func(ctx context.Context, h *Handle) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	})
	<-started

	if err := m.Cancel(id); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	// Second cancel is a no-op (idempotent).
	if err := m.Cancel(id); err != nil && !errors.Is(err, ErrAlreadyTerminal) {
		t.Fatalf("second Cancel returned %v", err)
	}

	waitFor(t, func() bool {
		t, _ := m.Get(id)
		return t.State == StateCanceled
	}, time.Second, "canceled state")

	if onCancelCount.Load() != 1 {
		t.Fatalf("OnCancel called %d times, want 1", onCancelCount.Load())
	}
}

func TestCancelNonCancellable(t *testing.T) {
	m := newTestManager(t)
	started := make(chan struct{})
	done := make(chan struct{})
	id := m.Start(StartOpts{
		Type: TaskInstanceCreate, // OnCancel nil
		Run: func(ctx context.Context, h *Handle) error {
			close(started)
			<-done
			return nil
		},
	})
	<-started
	err := m.Cancel(id)
	if !errors.Is(err, ErrNotCancellable) {
		t.Fatalf("want ErrNotCancellable, got %v", err)
	}
	close(done)
}

func TestCancelNotFound(t *testing.T) {
	m := newTestManager(t)
	if err := m.Cancel("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestListFilters(t *testing.T) {
	m := newTestManager(t)
	idA := m.Start(StartOpts{Type: TaskBackupCreate, InstanceID: 1, ResourceID: "b-1",
		Run:      func(ctx context.Context, h *Handle) error { <-ctx.Done(); return nil },
		OnCancel: func(ctx context.Context) {}})
	idB := m.Start(StartOpts{Type: TaskInstanceCreate, InstanceID: 2,
		Run: func(ctx context.Context, h *Handle) error { return nil }})

	waitFor(t, func() bool {
		t, _ := m.Get(idB)
		return t.State == StateSucceeded
	}, time.Second, "B finished")

	all := m.List(Filter{})
	if len(all) != 2 {
		t.Fatalf("want 2 tasks, got %d", len(all))
	}
	byKind := m.List(Filter{Type: TaskBackupCreate})
	if len(byKind) != 1 || byKind[0].ID != idA {
		t.Fatalf("wrong filter by type: %+v", byKind)
	}
	byInst := m.List(Filter{InstanceID: 2})
	if len(byInst) != 1 || byInst[0].ID != idB {
		t.Fatalf("wrong filter by instance: %+v", byInst)
	}
	active := m.List(Filter{OnlyActive: true})
	if len(active) != 1 || active[0].ID != idA {
		t.Fatalf("OnlyActive did not filter: %+v", active)
	}
	// cleanup
	_ = m.Cancel(idA)
}

func TestSubscriberBackpressureDoesNotBlock(t *testing.T) {
	m := newTestManager(t)
	// Subscriber that never reads — its channel will fill and events drop.
	_, unsub := m.Subscribe()
	defer unsub()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Fire many tasks; if broadcast blocked on the saturated subscriber,
		// these would stall.
		for i := 0; i < 200; i++ {
			m.Start(StartOpts{Type: TaskInstanceRestart,
				Run: func(ctx context.Context, h *Handle) error { return nil }})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("broadcast blocked on slow subscriber")
	}
}

func TestTerminalTaskGC(t *testing.T) {
	m := newTestManager(t) // RetainTerminal=50ms, GCInterval=20ms
	id := m.Start(StartOpts{Type: TaskSkillDeploy,
		Run: func(ctx context.Context, h *Handle) error { return nil }})
	waitFor(t, func() bool {
		t, _ := m.Get(id)
		return t.State == StateSucceeded
	}, time.Second, "finished")
	// Wait long enough for at least one GC tick past the retention window.
	waitFor(t, func() bool {
		_, ok := m.Get(id)
		return !ok
	}, time.Second, "task GC'd")
}

func TestOnCancelDoesNotFireForSuccess(t *testing.T) {
	m := newTestManager(t)
	var onCancel atomic.Int32
	id := m.Start(StartOpts{
		Type:     TaskBackupCreate,
		OnCancel: func(ctx context.Context) { onCancel.Add(1) },
		Run:      func(ctx context.Context, h *Handle) error { return nil },
	})
	waitFor(t, func() bool {
		t, _ := m.Get(id)
		return t.State == StateSucceeded
	}, time.Second, "succeeded")
	time.Sleep(20 * time.Millisecond)
	if onCancel.Load() != 0 {
		t.Fatalf("OnCancel fired on success")
	}
}

func TestRunPanicBecomesFailed(t *testing.T) {
	m := newTestManager(t)
	id := m.Start(StartOpts{
		Type: TaskInstanceCreate,
		Run: func(ctx context.Context, h *Handle) error {
			panic("kaboom")
		},
	})
	waitFor(t, func() bool {
		t, _ := m.Get(id)
		return t.State == StateFailed
	}, time.Second, "panic → failed")
}

func TestConcurrentStartCancel(t *testing.T) {
	m := newTestManager(t)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := m.Start(StartOpts{
				Type:     TaskBackupCreate,
				OnCancel: func(ctx context.Context) {},
				Run: func(ctx context.Context, h *Handle) error {
					<-ctx.Done()
					return ctx.Err()
				},
			})
			_ = m.Cancel(id)
		}()
	}
	wg.Wait()
	// All tasks should settle into Canceled.
	waitFor(t, func() bool {
		for _, t := range m.List(Filter{}) {
			if t.State == StateRunning {
				return false
			}
		}
		return true
	}, 2*time.Second, "all tasks terminal")
}
