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
// cancelled or the plugin connection drops fatally. In parallel it labels
// the node so the webhook's nodeAffinity gates pod scheduling on plugin
// readiness.
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

	// Build a k8s client for the readiness reconciler. Failure to obtain a
	// client is non-fatal — we degrade to "no node label" mode (operator
	// must monitor metrics instead). The plugin still works for scheduling
	// scenarios that don't rely on the affinity gate.
	var readinessG *errgroup.Group
	var readinessCtx context.Context
	c := k8s.NewClient()
	clientset, kerr := c.GetKubernetesClient()
	if kerr != nil {
		log.Warnf("kubernetes client init failed: %v (readiness label disabled)", kerr)
	} else if name := nodeNameFromEnv(); name == "" {
		log.Warn("NODE_NAME env var unset; readiness label disabled")
	} else {
		rec := newReadinessReconciler(clientset, name, log)
		readinessG, readinessCtx = errgroup.WithContext(ctx)
		readinessG.Go(func() error { return rec.Run(readinessCtx) })
	}

	s, err := stubFor(p)
	if err != nil {
		return err
	}
	log.Infof("NRI plugin connecting on %s", cfg.NRI.SocketPath)
	runErr := s.Run(ctx)

	// Wait for readiness goroutine to clean up (label removal) before returning.
	if readinessG != nil {
		_ = readinessG.Wait()
	}
	if runErr != nil {
		return errors.Wrap(runErr, "NRI plugin run loop")
	}
	return nil
}
