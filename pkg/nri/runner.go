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
