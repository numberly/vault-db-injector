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
			if name := nodeNameFromEnv(); name != "" {
				sw := newSweeper(clientset, p, name, log)
				g.Go(func() error { return sw.Run(gctx) })
			} else {
				log.Warn("NODE_NAME env unset; cache sweeper disabled")
			}
		} else {
			log.Warnf("kubernetes client init failed: %v (cache sweeper disabled)", kerr)
		}
	}

	s, err := stubFor(p)
	if err != nil {
		return err
	}
	log.Infof("NRI plugin connecting on %s", cfg.NRI.SocketPath)

	// Run the stub in a goroutine so we can react to ctx cancellation
	// (SIGTERM) by calling Stop() explicitly. The NRI stub's Run() blocks
	// until Stop() or a fatal error — without an explicit Stop, containerd
	// may keep the plugin connection registered past pod termination,
	// causing "plugins X and X both tried to set env" errors when the
	// next DS pod reconnects with the same idx+name.
	runDone := make(chan error, 1)
	go func() { runDone <- s.Run(ctx) }()

	var runErr error
	select {
	case <-ctx.Done():
		log.Infof("NRI plugin shutting down (ctx cancelled)")
		s.Stop()
		s.Wait()
		// Drain Run's return so the goroutine doesn't leak.
		<-runDone
	case runErr = <-runDone:
		// Plugin exited on its own (fatal connection error). Make sure the
		// stub's resources are released before returning.
		s.Stop()
	}

	_ = g.Wait()

	if runErr != nil {
		return errors.Wrap(runErr, "NRI plugin run loop")
	}
	return nil
}
