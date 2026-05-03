package nri

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/logger"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// NodeReadyLabel is the node label set by the plugin once it has registered
// with containerd and loaded its cache. The webhook adds nodeAffinity on
// pods with nri-mapping annotations to require this label, ensuring no
// pod is scheduled to a node where substitution would silently fail.
const NodeReadyLabel = "vault-db-injector.numberly.io/nri-ready"

// readinessReconciler keeps NodeReadyLabel applied to a single node by
// patching periodically (defends against accidental kubectl label removal)
// and removes the label on shutdown.
type readinessReconciler struct {
	client   kubernetes.Interface
	nodeName string
	interval time.Duration
	log      logger.Logger
}

func newReadinessReconciler(client kubernetes.Interface, nodeName string, log logger.Logger) *readinessReconciler {
	return &readinessReconciler{
		client:   client,
		nodeName: nodeName,
		interval: 30 * time.Second,
		log:      log,
	}
}

// Run applies the label, then re-applies on a ticker until ctx cancels.
// On cancel, removes the label (best-effort, 5s deadline).
// Returns nil when ctx is cancelled and cleanup completes.
func (r *readinessReconciler) Run(ctx context.Context) error {
	if r.client == nil || r.nodeName == "" {
		r.log.Warnf("readiness reconciler disabled: client=%v nodeName=%q", r.client != nil, r.nodeName)
		<-ctx.Done()
		return nil
	}
	if err := r.applyLabel(ctx); err != nil {
		r.log.Warnf("initial label apply failed for node %s: %v", r.nodeName, err)
	} else {
		r.log.Infof("node %s labeled %s=true", r.nodeName, NodeReadyLabel)
	}

	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := r.removeLabel(cleanupCtx); err != nil {
				r.log.Warnf("remove label on shutdown failed: %v", err)
			} else {
				r.log.Infof("node %s label %s removed", r.nodeName, NodeReadyLabel)
			}
			return nil
		case <-t.C:
			if err := r.applyLabel(ctx); err != nil {
				r.log.Warnf("re-apply label for node %s: %v", r.nodeName, err)
			}
		}
	}
}

// applyLabel issues a strategic-merge patch setting NodeReadyLabel=true.
// Idempotent: re-applying the same label is a no-op on the API server.
func (r *readinessReconciler) applyLabel(ctx context.Context) error {
	return patchNodeLabel(ctx, r.client, r.nodeName, map[string]any{NodeReadyLabel: "true"})
}

// removeLabel issues a strategic-merge patch with the label set to null,
// which the API server interprets as "delete this label key".
func (r *readinessReconciler) removeLabel(ctx context.Context) error {
	return patchNodeLabel(ctx, r.client, r.nodeName, map[string]any{NodeReadyLabel: nil})
}

func patchNodeLabel(ctx context.Context, client kubernetes.Interface, nodeName string, labels map[string]any) error {
	patch := map[string]any{
		"metadata": map[string]any{
			"labels": labels,
		},
	}
	b, err := json.Marshal(patch)
	if err != nil {
		return errors.Wrap(err, "marshal node patch")
	}
	_, err = client.CoreV1().Nodes().Patch(ctx, nodeName, types.StrategicMergePatchType, b, metav1.PatchOptions{})
	if err != nil {
		return errors.Wrapf(err, "patch node %s", nodeName)
	}
	return nil
}

// nodeNameFromEnv returns NODE_NAME from the environment (set via
// downwardAPI in the DS spec). Returns empty string if unset.
func nodeNameFromEnv() string { return os.Getenv("NODE_NAME") }

// Compile-time check that we use corev1 (some lints flag unused imports).
var _ = corev1.ResourceCPU
