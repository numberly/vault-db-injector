package leadership

import (
	"context"
	"sync"
	"time"

	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/metrics"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

var _ LeaderElector = (*leaderElectorImpl)(nil)

type LeaderElector interface {
	RunLeaderElection(ctx context.Context, stopChan chan struct{})
	IsHealthy() bool
}

type leaderElectorImpl struct {
	lock       *resourcelock.LeaseLock
	id         string
	leaderFunc LeaderFunc
	log        logger.Logger
	mu         sync.Mutex
	healthy    bool
}

func (le *leaderElectorImpl) IsHealthy() bool {
	le.mu.Lock()
	defer le.mu.Unlock()
	return le.healthy
}

func NewLeaderElector(lock *resourcelock.LeaseLock, id string, leaderFunc LeaderFunc) LeaderElector {
	return &leaderElectorImpl{
		lock:       lock,
		id:         id,
		leaderFunc: leaderFunc,
		log:        logger.GetLogger(),
		healthy:    true,
	}
}

// LeaderFunc is the callback invoked when this instance becomes leader.
// cfg and clientset are NOT passed as parameters; callers must capture them in a closure.
type LeaderFunc func(ctx context.Context, stopChan chan struct{})

func NewLock(client coordinationv1.CoordinationV1Interface, lockName, podname, namespace string) *resourcelock.LeaseLock {
	return &resourcelock.LeaseLock{
		LeaseMeta: v1.ObjectMeta{
			Name:      lockName,
			Namespace: namespace,
		},
		Client: client,
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: podname,
		},
	}
}

// stopLeading is invoked once leadership is lost. RunOrDie returns and this
// process never re-enters the election, but the metrics and healthcheck
// servers keep it alive: a zombie serving a frozen Prometheus registry
// (stale vdbi_last_synchronization_success and expired *_expiration series).
// Marking it unhealthy lets the liveness probe restart the pod, which both
// re-enters the election and starts from an empty registry.
func (le *leaderElectorImpl) stopLeading(stopChan chan struct{}) {
	metrics.IsLeader.WithLabelValues(le.lock.LeaseMeta.GetName()).Set(0)
	le.log.Info("No longer leader, stopping process and failing liveness so kubelet restarts the pod")
	le.mu.Lock()
	le.healthy = false
	le.mu.Unlock()
	close(stopChan)
}

// runLeaderElection runs leadership election. If an instance of the controller is the leader and stops leading it will shutdown.

func (le *leaderElectorImpl) RunLeaderElection(ctx context.Context, stopChan chan struct{}) {
	metrics.LeaseElectionAttempts.WithLabelValues(le.lock.LeaseMeta.GetName()).Inc()
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            le.lock,
		ReleaseOnCancel: true,
		LeaseDuration:   15 * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				metrics.IsLeader.WithLabelValues(le.lock.LeaseMeta.GetName()).Set(1)
				le.log.Info("Became leader, starting process")
				go le.leaderFunc(ctx, stopChan)
			},
			OnStoppedLeading: func() {
				le.stopLeading(stopChan)
			},
			OnNewLeader: func(currentID string) {
				if currentID == le.id {
					le.log.Info("still the leader!")
					return
				}
				le.log.Infof("new leader is %s", currentID)
			},
		},
	})
}
