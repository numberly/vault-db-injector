package controller

import (
	"context"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/nri"
)

func runNRIAgent(ctx context.Context, cfg *config.Config, log logger.Logger) error {
	return nri.Run(ctx, cfg, log)
}
