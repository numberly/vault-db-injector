package nri

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/logger"
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

	s, err := stubFor(p)
	if err != nil {
		return err
	}
	log.Infof("NRI plugin connecting on %s", cfg.NRI.SocketPath)
	if err := s.Run(ctx); err != nil {
		return errors.Wrap(err, "NRI plugin run loop")
	}
	return nil
}
