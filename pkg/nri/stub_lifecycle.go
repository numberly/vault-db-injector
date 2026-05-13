package nri

import (
	"context"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/metrics"
)

// reconnectBackoff is the schedule of waits between reconnect attempts after
// the NRI ttrpc connection drops. Total recovery window is ~48s. Designed to
// absorb a containerd reload (logrotate, config update) without restarting
// the DS pod, while bounding the time spent in a degraded "disconnected"
// state — beyond that, the lifecycle gives up and returns an error so kubelet
// restarts the pod via the DaemonSet's restart policy.
var reconnectBackoff = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
}

// nriStub is the subset of containerd/nri/pkg/stub.Stub that stubLifecycle
// consumes. Defined as an interface so tests can inject a fake.
type nriStub interface {
	Run(ctx context.Context) error
	Stop()
	Wait()
}

// stubFactory builds a fresh nriStub for each (re)connect attempt.
type stubFactory func() (nriStub, error)

// stubLifecycle runs the NRI stub with bounded automatic reconnection on
// unexpected ttrpc disconnects (the prime motivator: containerd's daily
// reload at midnight UTC). The plugin's in-memory state (cache, cacheSource,
// prewarmer informer, sweeper) is shared across reconnects — only the
// stub.Stub is rebuilt — so reconnection is cheap and stateless from
// containerd's POV (NRI's Synchronize hook fires on each connect and
// re-establishes visibility of running containers on the node).
type stubLifecycle struct {
	factory stubFactory
	log     logger.Logger
	backoff []time.Duration

	mu      sync.Mutex
	current nriStub
}

// newStubLifecycle constructs a lifecycle with the production backoff.
func newStubLifecycle(p *plugin, log logger.Logger) *stubLifecycle {
	return newStubLifecycleWithFactory(
		func() (nriStub, error) { return stubFor(p) },
		log,
		reconnectBackoff,
	)
}

// newStubLifecycleWithFactory is the test-friendly constructor.
func newStubLifecycleWithFactory(factory stubFactory, log logger.Logger, backoff []time.Duration) *stubLifecycle {
	return &stubLifecycle{
		factory: factory,
		log:     log,
		backoff: backoff,
	}
}

// run blocks until ctx is cancelled (graceful shutdown — returns nil) or
// the reconnect attempts are exhausted (returns a wrapped error so the
// caller's errgroup propagates fatal exit up to main).
func (l *stubLifecycle) run(ctx context.Context) error {
	s, err := l.factory()
	if err != nil {
		return errors.Wrap(err, "initial NRI stub construction")
	}
	l.setCurrent(s)

	for attempt := 0; ; attempt++ {
		runErr := s.Run(ctx)

		// Was shutdown requested while we were running? Treat that as a
		// clean exit regardless of what Run returned (containerd may close
		// the connection with no error on shutdown).
		if ctx.Err() != nil {
			return nil
		}

		// Unexpected disconnect.
		metrics.NRIReconnectTotal.WithLabelValues("attempted").Inc()

		if attempt >= len(l.backoff) {
			metrics.NRIReconnectTotal.WithLabelValues("exhausted").Inc()
			if runErr == nil {
				runErr = errors.New("ttrpc connection closed by containerd")
			}
			return errors.Wrapf(runErr,
				"NRI plugin disconnected %d times in a row; restarting pod", attempt)
		}

		wait := l.backoff[attempt]
		l.log.Warnf("NRI plugin disconnected (attempt %d/%d, runErr=%v); reconnecting in %s",
			attempt+1, len(l.backoff)+1, runErr, wait)

		// Release the old stub before sleeping. Stop is idempotent with
		// the lifecycle's shutdown goroutine if SIGTERM races us here.
		s.Stop()

		// Wait for backoff, but short-circuit on shutdown.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}

		// Build a fresh stub. plugin.cache, cacheSource, prewarmer, and
		// sweeper all survive across reconnects.
		s, err = l.factory()
		if err != nil {
			metrics.NRIReconnectTotal.WithLabelValues("exhausted").Inc()
			return errors.Wrap(err, "NRI stub reconnect failed")
		}
		l.setCurrent(s)
		metrics.NRIReconnectTotal.WithLabelValues("succeeded").Inc()
		l.log.Infof("NRI plugin reconnected (attempt %d)", attempt+1)
	}
}

// shutdown stops the currently-running stub. Safe to call concurrently with
// run — used by the runner's sidecar goroutine on gctx cancellation to make
// the inner s.Run return promptly (the stub's Run does not honour ctx alone,
// only Stop).
func (l *stubLifecycle) shutdown() {
	l.mu.Lock()
	s := l.current
	l.mu.Unlock()
	if s == nil {
		return
	}
	s.Stop()
	s.Wait()
}

func (l *stubLifecycle) setCurrent(s nriStub) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.current = s
}
