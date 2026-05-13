package nri

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"golang.org/x/sync/errgroup"
)

// Run registers the NRI plugin with containerd and blocks until ctx is
// cancelled or the plugin connection drops fatally.
func Run(ctx context.Context, cfg *config.Config, log logger.Logger) error {
	p := newPlugin(cfg, log)

	// Load cache from tmpfs so we keep credentials for pods whose wrap
	// tokens were already consumed by a previous plugin instance. Survives
	// DS pod restart but not node reboot (tmpfs).
	cached, err := loadCache(cfg.NRI.CachePath)
	if err != nil {
		log.Warnf("load cache from %s: %v (continuing with empty cache)", cfg.NRI.CachePath, err)
	} else if len(cached) > 0 {
		p.mu.Lock()
		p.cache = cached
		p.mu.Unlock()
		log.Infof("loaded %d pod entries from cache at %s", len(cached), cfg.NRI.CachePath)
	}

	// Periodic cache sweep: list pods on this node via k8s API and evict
	// cache entries for pods that no longer exist. Required because NRI's
	// RemovePodSandbox event is not delivered for force-deleted pods.
	g, gctx := errgroup.WithContext(ctx)
	if c := k8s.NewClient(); c != nil {
		if clientset, kerr := c.GetKubernetesClient(); kerr == nil {
			name := nodeNameFromEnv()
			if name != "" {
				sw := newSweeper(clientset, p, name, log)
				g.Go(func() error { return sw.Run(gctx) })
			} else {
				log.Warn("NODE_NAME env unset; cache sweeper disabled")
			}
			if cfg.NRI.Prewarmer.Enabled {
				if name != "" {
					pw := newPrewarmer(p, clientset, name, cfg.NRI.Prewarmer.MaxConcurrent, log)
					g.Go(func() error { return pw.Run(gctx) })
				} else {
					log.Warn("NODE_NAME env unset; NRI prewarmer disabled")
				}
			} else {
				log.Info("NRI prewarmer disabled by config (nri.prewarmer.enabled=false)")
			}
		} else {
			log.Warnf("kubernetes client init failed: %v (cache sweeper and prewarmer disabled)", kerr)
		}
	}

	log.Infof("NRI plugin connecting on %s", cfg.NRI.SocketPath)

	// stubLifecycle handles bounded reconnect on unexpected ttrpc disconnects
	// (containerd reload, log rotation, etc.). On exhaustion it returns a
	// non-nil error, which the errgroup propagates to main → exit non-zero →
	// kubelet restarts the DS pod. The plugin's in-memory state survives
	// across reconnects.
	lifecycle := newStubLifecycle(p, log)

	g.Go(func() error {
		return lifecycle.run(ctx)
	})

	// Sidecar: when gctx is cancelled (parent SIGTERM, or another goroutine
	// returned an error), stop the current stub so its Run unblocks. The NRI
	// stub's Run does not honour ctx alone — only Stop().
	g.Go(func() error {
		<-gctx.Done()
		log.Infof("NRI plugin shutting down (ctx cancelled)")
		lifecycle.shutdown()
		return nil
	})

	err = g.Wait()
	if err != nil && !errors.Is(err, context.Canceled) {
		return errors.Wrap(err, "NRI plugin run loop")
	}
	return nil
}
