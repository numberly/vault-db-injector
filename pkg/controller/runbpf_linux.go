//go:build linux

package controller

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
)

// runBPFAgent is the Linux entry point for the BPF DaemonSet runtime.
// Stubbed pending the pkg/bpf package introduced by subsequent tasks
// (cgroup resolver, persister, BPF loader, runner). Once those land,
// this body will be replaced with a single line: bpf.Run(ctx, cfg, clientset, log).
func runBPFAgent(_ context.Context, _ *config.Config, _ k8s.KubernetesClient, _ logger.Logger) error {
	return errors.New("BPF agent not yet linked; complete the pkg/bpf runner task")
}
