package nri

import (
	"context"
	"os"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/logger"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// sweeper periodically lists pods on this node via the k8s API and evicts
// cache entries for pods that no longer exist. This handles force-deleted
// pods (kubectl delete --grace-period=0 --force), where NRI's RemovePodSandbox
// event is not delivered. Without this sweep, unwrapped credentials of
// force-deleted pods sit on tmpfs until the next plugin restart.
type sweeper struct {
	client   kubernetes.Interface
	plugin   *plugin
	nodeName string
	interval time.Duration
	log      logger.Logger
}

func newSweeper(client kubernetes.Interface, p *plugin, nodeName string, log logger.Logger) *sweeper {
	return &sweeper{
		client:   client,
		plugin:   p,
		nodeName: nodeName,
		interval: 5 * time.Minute,
		log:      log,
	}
}

// Run starts the periodic sweep loop, returns when ctx cancels.
func (s *sweeper) Run(ctx context.Context) error {
	if s.client == nil || s.nodeName == "" {
		s.log.Warn("cache sweeper disabled (no k8s client or NODE_NAME)")
		<-ctx.Done()
		return nil
	}
	s.log.Infof("cache sweeper running every %s for node %s", s.interval, s.nodeName)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := s.sweepOnce(ctx); err != nil {
				s.log.Warnf("cache sweep: %v", err)
			}
		}
	}
}

func (s *sweeper) sweepOnce(ctx context.Context) error {
	pods, err := s.client.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + s.nodeName,
	})
	if err != nil {
		return errors.Wrap(err, "list pods on node")
	}
	live := make(map[string]struct{}, len(pods.Items))
	for i := range pods.Items {
		live[string(pods.Items[i].UID)] = struct{}{}
	}

	s.plugin.mu.Lock()
	evicted := 0
	for uid := range s.plugin.cache {
		if _, ok := live[uid]; !ok {
			delete(s.plugin.cache, uid)
			evicted++
		}
	}
	s.plugin.mu.Unlock()

	if evicted > 0 {
		s.log.Infof("cache sweep evicted %d stale entries", evicted)
		if err := saveCache(s.plugin.cfg.NRI.CachePath, s.plugin.snapshot()); err != nil {
			return errors.Wrap(err, "save cache after sweep")
		}
	}
	return nil
}

// nodeNameFromEnv returns NODE_NAME from the environment (set via
// downwardAPI in the DS spec). Returns empty string if unset.
func nodeNameFromEnv() string { return os.Getenv("NODE_NAME") }
