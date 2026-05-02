//go:build !linux

package controller

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
)

// runBPFAgent on non-Linux platforms returns an explicit error. The BPF LSM
// hook used by the substitution program is a Linux kernel feature; the
// binary still compiles on other OSes for development convenience.
func runBPFAgent(_ context.Context, _ *config.Config, _ k8s.KubernetesClient, _ logger.Logger) error {
	return errors.New("BPF mode not supported on this platform; requires Linux with BPF LSM")
}
