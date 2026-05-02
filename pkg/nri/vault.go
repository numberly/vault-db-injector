package nri

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/vault"
)

// unwrapAndBuildMapping unwraps the wrap token in the NRIMapping annotation
// and returns a placeholder→real-value map ready for Substitute.
func unwrapAndBuildMapping(ctx context.Context, cfg *config.Config, m k8s.NRIMapping) (map[string]string, error) {
	k8sClient := k8s.NewClient()
	tok, err := k8sClient.GetServiceAccountToken()
	if err != nil {
		return nil, errors.Wrap(err, "get serviceaccount token")
	}
	conn := vault.NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, cfg.KubeRole, "", "", tok, cfg.VaultRateLimit)
	if err := conn.Login(ctx); err != nil {
		return nil, errors.Wrap(err, "vault login")
	}
	payload, err := conn.UnwrapValues(ctx, m.WrapToken)
	if err != nil {
		return nil, errors.Wrap(err, "vault unwrap")
	}
	out := make(map[string]string, len(m.Placeholders))
	for ph, key := range m.Placeholders {
		v, ok := payload[key]
		if !ok {
			return nil, errors.Newf("unwrap payload missing key %q", key)
		}
		out[ph] = v
	}
	return out, nil
}
