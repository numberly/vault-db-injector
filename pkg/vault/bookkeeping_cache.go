package vault

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/numberly/vault-db-injector/pkg/config"
)

// cacheEntry holds a cached Vault token and its expiry.
type cacheEntry struct {
	token     string
	expiresAt time.Time
}

// BookkeepingTokenCache caches the injector-SA Vault login token used for KV
// bookkeeping writes. The same token is reused across admissions since it is
// the injector's own SA identity, not a per-pod identity. The cache is keyed
// by kubeRole so that webhook (cfg.KubeRole) and NRI plugin (cfg.KubeRoleNri)
// can share one instance without colliding.
//
// Tokens are considered valid for 30 minutes — half of a typical 1h token_ttl.
// When fewer than 5 minutes remain, the cache refreshes proactively.
//
// Concurrency: safe for concurrent use. A singleflight.Group prevents
// stampedes when the entry is missing or stale.
type BookkeepingTokenCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry // key: kubeRole
	sf      singleflight.Group
}

// NewBookkeepingTokenCache returns a ready-to-use cache.
func NewBookkeepingTokenCache() *BookkeepingTokenCache {
	return &BookkeepingTokenCache{
		entries: make(map[string]*cacheEntry),
	}
}

// Get returns a cached injector-SA Vault token for the given kubeRole,
// refreshing it when missing or near-expiry (< 5 minutes left).
// k8sSaToken is the injector binary's mounted ServiceAccount JWT.
func (c *BookkeepingTokenCache) Get(ctx context.Context, cfg *config.Config, k8sSaToken, kubeRole string) (string, error) {
	if kubeRole == "" {
		kubeRole = cfg.KubeRole
	}

	// Fast path: valid cached token.
	c.mu.Lock()
	if e, ok := c.entries[kubeRole]; ok && time.Until(e.expiresAt) > 5*time.Minute {
		tok := e.token
		c.mu.Unlock()
		return tok, nil
	}
	c.mu.Unlock()

	// Slow path: refresh under singleflight to avoid stampedes.
	sfKey := kubeRole
	v, err, _ := c.sf.Do(sfKey, func() (interface{}, error) {
		// Re-check under lock: a concurrent caller that won the singleflight
		// may have already populated the entry.
		c.mu.Lock()
		if e, ok := c.entries[kubeRole]; ok && time.Until(e.expiresAt) > 5*time.Minute {
			tok := e.token
			c.mu.Unlock()
			return tok, nil
		}
		c.mu.Unlock()

		tok, err := LoginAsInjectorSA(ctx, cfg, k8sSaToken, kubeRole)
		if err != nil {
			return "", err
		}
		// Cache for 30 minutes — half of a typical 1h token_ttl.
		c.mu.Lock()
		c.entries[kubeRole] = &cacheEntry{
			token:     tok,
			expiresAt: time.Now().Add(30 * time.Minute),
		}
		c.mu.Unlock()
		return tok, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}
