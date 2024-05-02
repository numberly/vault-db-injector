package leadership

import (
	"context"
	"sync"
	"time"

	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/config"
	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/k8s"
	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/logger"
	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/prometheus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

var (
	// Used for liveness probe
	m       sync.Mutex
	healthy bool = true
)
var _ LeaderElector = (*leaderElectorImpl)(nil)

type LeaderElector interface {
	RunLeaderElection(ctx context.Context, stopChan chan struct{})
}

type leaderElectorImpl struct {
	lock       *resourcelock.LeaseLock
	cfg        *config.Config
	id         string
	clientset  k8s.KubernetesClient
	leaderFunc LeaderFunc
	log        logger.Logger
}

func NewLeaderElector(lock *resourcelock.LeaseLock, cfg *config.Config, id string, clientset k8s.KubernetesClient, leaderFunc LeaderFunc) LeaderElector {
	return &leaderElectorImpl{
		lock:       lock,
		cfg:        cfg,
		id:         id,
		clientset:  clientset,
		leaderFunc: leaderFunc,
		log:        logger.GetLogger(),
	}
}

type LeaderFunc func(ctx context.Context, stopChan chan struct{}, cfg *config.Config, clientset k8s.KubernetesClient)

func GetNewLock(client coordinationv1.CoordinationV1Interface, lockName, podname, namespace string) *resourcelock.LeaseLock {
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

// runLeaderElection runs leadership election. If an instance of the controller is the leader and stops leading it will shutdown.

func (le *leaderElectorImpl) RunLeaderElection(ctx context.Context, stopChan chan struct{}) {

	//log.SetOutput(new(logger.LogrusWriter))

	prometheus.LeaseElectionAttempts.WithLabelValues(le.lock.LeaseMeta.GetName()).Inc()
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            le.lock,
		ReleaseOnCancel: true,
		LeaseDuration:   15 * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				prometheus.IsLeader.WithLabelValues(le.lock.LeaseMeta.GetName()).Set(1)
				le.log.Info("Became leader, starting process")
				go le.leaderFunc(ctx, stopChan, le.cfg, le.clientset)
			},
			OnStoppedLeading: func() {
				prometheus.IsLeader.WithLabelValues(le.lock.LeaseMeta.GetName()).Set(0)
				le.log.Info("No longer leader, stopping process")
				close(stopChan) // Signal TokenSync1Hours to stop
				// It's a good practice to recreate the stopChan if the instance can become a leader again later
				//stopChan = make(chan struct{})
			},
			OnNewLeader: func(current_id string) {
				if current_id == le.id {
					le.log.Info("still the leader!")
					return
				}
				le.log.Infof("new leader is %s", current_id)
			},
		},
	})
}
