package nri

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/logger"
)

// fakeStub is a controllable nriStub for tests. Each Run() blocks until
// either Stop() is called or a value is sent on the runReturns channel.
type fakeStub struct {
	id          int
	runReturns  chan error
	runStarted  chan struct{}
	stopped     atomic.Bool
	runStartedOnce sync.Once
}

func newFakeStub(id int) *fakeStub {
	return &fakeStub{
		id:         id,
		runReturns: make(chan error, 1),
		runStarted: make(chan struct{}),
	}
}

func (f *fakeStub) Run(ctx context.Context) error {
	f.runStartedOnce.Do(func() { close(f.runStarted) })
	select {
	case err := <-f.runReturns:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeStub) Stop() {
	if f.stopped.CompareAndSwap(false, true) {
		// Unblock Run with nil (graceful close).
		select {
		case f.runReturns <- nil:
		default:
		}
	}
}

func (f *fakeStub) Wait() {}

// shortBackoff makes tests fast — 5ms per retry, 5 slots = 25ms total window.
var shortBackoff = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond, 5 * time.Millisecond, 5 * time.Millisecond, 5 * time.Millisecond}

func TestStubLifecycle_GracefulShutdown(t *testing.T) {
	stub := newFakeStub(1)
	factory := func() (nriStub, error) { return stub, nil }
	lc := newStubLifecycleWithFactory(factory, logger.GetLogger(), shortBackoff)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- lc.run(ctx) }()

	<-stub.runStarted
	cancel()
	// Sidecar would call shutdown; emulate it.
	lc.shutdown()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected nil on graceful shutdown, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run() did not return after shutdown")
	}
}

func TestStubLifecycle_ReconnectOnDisconnect(t *testing.T) {
	stubs := []*fakeStub{newFakeStub(1), newFakeStub(2)}
	var idx atomic.Int32
	factory := func() (nriStub, error) {
		i := idx.Add(1) - 1
		if int(i) >= len(stubs) {
			return nil, errors.New("test factory exhausted")
		}
		return stubs[i], nil
	}
	lc := newStubLifecycleWithFactory(factory, logger.GetLogger(), shortBackoff)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- lc.run(ctx) }()

	// Stub #1 starts, then simulate ttrpc close (Run returns nil).
	<-stubs[0].runStarted
	stubs[0].runReturns <- nil

	// Stub #2 should be constructed and started after backoff.
	select {
	case <-stubs[1].runStarted:
		// Good — reconnect happened.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected stub #2 to start after disconnect, timed out")
	}

	// Clean shutdown.
	cancel()
	lc.shutdown()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected nil on graceful shutdown after reconnect, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run() did not return after shutdown")
	}
}

func TestStubLifecycle_ExhaustionReturnsError(t *testing.T) {
	// Factory produces a fresh fakeStub on each call; each one returns nil
	// from Run immediately (simulating ttrpc close).
	disconnectCount := atomic.Int32{}
	factory := func() (nriStub, error) {
		s := newFakeStub(int(disconnectCount.Add(1)))
		// Pre-queue the nil so Run returns immediately.
		s.runReturns <- nil
		return s, nil
	}
	// 3-slot backoff with negligible waits for the test.
	tinyBackoff := []time.Duration{1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond}
	lc := newStubLifecycleWithFactory(factory, logger.GetLogger(), tinyBackoff)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- lc.run(ctx) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error on exhaustion, got nil")
		}
		// 1 initial + 3 reconnects = 4 stubs constructed, then exhaustion.
		if disconnectCount.Load() < 4 {
			t.Errorf("expected at least 4 attempts before exhaustion, got %d", disconnectCount.Load())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run() did not return after exhaustion")
	}
}

func TestStubLifecycle_ShutdownDuringBackoff(t *testing.T) {
	stub := newFakeStub(1)
	factory := func() (nriStub, error) { return stub, nil }
	// Long backoff so we're definitely sleeping when shutdown fires.
	longBackoff := []time.Duration{5 * time.Second}
	lc := newStubLifecycleWithFactory(factory, logger.GetLogger(), longBackoff)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- lc.run(ctx) }()

	<-stub.runStarted
	stub.runReturns <- nil // simulate disconnect → enters backoff

	// Give the loop a moment to enter time.After.
	time.Sleep(50 * time.Millisecond)
	cancel()
	lc.shutdown()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected nil on shutdown during backoff, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run() did not return after shutdown during backoff")
	}
}

func TestStubLifecycle_FactoryFailureExhausts(t *testing.T) {
	factory := func() (nriStub, error) {
		return nil, errors.New("simulated factory error")
	}
	lc := newStubLifecycleWithFactory(factory, logger.GetLogger(), shortBackoff)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- lc.run(ctx) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error when initial factory fails, got nil")
		}
	case <-time.After(time.Second):
		t.Fatal("run() did not return after initial factory failure")
	}
}
