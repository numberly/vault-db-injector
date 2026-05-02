package leadership

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLeaderElector allows testing liveness endpoint without real leader election.
type fakeLeaderElector struct {
	mu      sync.Mutex
	healthy bool
}

func (f *fakeLeaderElector) RunLeaderElection(_ context.Context, _ chan struct{}) {}

func (f *fakeLeaderElector) IsHealthy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.healthy
}

var _ LeaderElector = (*fakeLeaderElector)(nil)

// TestIsHealthy verifies the initial healthy state and toggling.
func TestIsHealthy_InitialState(t *testing.T) {
	le := &leaderElectorImpl{healthy: true}
	assert.True(t, le.IsHealthy())
}

func TestIsHealthy_False(t *testing.T) {
	le := &leaderElectorImpl{healthy: false}
	assert.False(t, le.IsHealthy())
}

// TestIsHealthy_ConcurrencySafe verifies that IsHealthy is safe to call from
// multiple goroutines simultaneously (data-race free under -race).
func TestIsHealthy_ConcurrencySafe(t *testing.T) {
	le := &leaderElectorImpl{healthy: true}
	const goroutines = 20

	var wg sync.WaitGroup
	results := make([]bool, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = le.IsHealthy()
		}(i)
	}
	wg.Wait()

	// All reads must have succeeded without panic or data race.
	for _, r := range results {
		assert.True(t, r)
	}
}

// TestIsHealthy_ConcurrentWrite verifies that concurrent reads and writes
// via the mutex are safe.
func TestIsHealthy_ConcurrentWrite(t *testing.T) {
	le := &leaderElectorImpl{healthy: true}
	done := make(chan struct{})

	// Writer goroutine toggles healthy rapidly.
	go func() {
		defer close(done)
		for i := range 100 {
			le.mu.Lock()
			le.healthy = i%2 == 0
			le.mu.Unlock()
		}
	}()

	// Reader goroutine calls IsHealthy concurrently.
	for range 100 {
		_ = le.IsHealthy()
	}
	<-done
}

// TestNewLeaderElector_NotNil verifies the constructor returns a non-nil interface.
func TestNewLeaderElector_NotNil(t *testing.T) {
	noopFunc := func(_ context.Context, _ chan struct{}) {}
	le := NewLeaderElector(nil, "test-id", noopFunc)
	require.NotNil(t, le)
}

// TestNewLeaderElector_IsHealthyTrue verifies a freshly constructed elector starts healthy.
func TestNewLeaderElector_IsHealthyTrue(t *testing.T) {
	noopFunc := func(_ context.Context, _ chan struct{}) {}
	le := NewLeaderElector(nil, "id", noopFunc)
	assert.True(t, le.IsHealthy())
}

