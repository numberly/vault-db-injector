//go:build linux

package controller

import (
	"context"

	"github.com/numberly/vault-db-injector/pkg/bpf"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
)

func runBPFAgent(ctx context.Context, cfg *config.Config, clientset k8s.KubernetesClient, log logger.Logger) error {
	return bpf.Run(ctx, cfg, clientset, log)
}
