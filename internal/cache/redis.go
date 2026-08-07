// redis.go
package cache

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisDoer is the minimal command surface RedisCache needs. Isolating it from
// *redis.Client keeps the cache logic unit-testable against an in-memory fake,
// so CI needs no Redis server; the real client is exercised in integration.
type redisDoer interface {
	Get(ctx context.Context, key string) (value string, found bool, err error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Del(ctx context.Context, key string) error
	Close() error
}

// RedisCache is a Redis-backed Cache. It is shared across replicas, so unlike
// Memory it gives every instance the same view. Construct via NewRedis (or the
// factory with CACHE_DRIVER=redis).
type RedisCache struct {
	doer redisDoer
}

// NewRedis dials a Redis-compatible server (Redis, Valkey, KeyDB, DragonflyDB,
// Elasticache, MemoryDB) and verifies connectivity with a PING so a bad address
// fails fast at startup. Password is a secret sourced from the environment.
//
// Set cfg.TLS for any server reached over a real network: without it the AUTH
// password and every cached value travel in plaintext.
func NewRedis(ctx context.Context, cfg RedisConfig) (*RedisCache, error) {
	opts := &redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	}
	if cfg.TLS {
		// ServerName is derived from Addr for hostname verification; certificate
		// validation stays on (no InsecureSkipVerify) so a MITM fails the dial.
		host, _, err := net.SplitHostPort(cfg.Addr)
		if err != nil {
			return nil, fmt.Errorf("parsing redis address %s: %w", cfg.Addr, err)
		}
		opts.TLSConfig = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	client := redis.NewClient(opts)
	if pingErr := client.Ping(ctx).Err(); pingErr != nil {
		// The client is abandoned, but its close error is still reported:
		// errors.Join drops nils, so a clean close leaves the message unchanged
		// while a failing one surfaces instead of vanishing.
		failure := pingErr
		if closeErr := client.Close(); closeErr != nil {
			failure = errors.Join(pingErr, fmt.Errorf("closing redis client: %w", closeErr))
		}
		return nil, fmt.Errorf("pinging redis at %s: %w", cfg.Addr, failure)
	}
	return &RedisCache{doer: goRedis{client: client}}, nil
}

// Get returns (nil, false, nil) on a miss and surfaces backend errors.
func (r *RedisCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	v, found, err := r.doer.Get(ctx, key)
	if err != nil {
		return nil, false, fmt.Errorf("cache get %s: %w", key, err)
	}
	if !found {
		return nil, false, nil
	}
	return []byte(v), true, nil
}

// Set stores value under key with the given TTL (non-positive means no expiry).
func (r *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := r.doer.Set(ctx, key, string(value), ttl); err != nil {
		return fmt.Errorf("cache set %s: %w", key, err)
	}
	return nil
}

// Delete removes key.
func (r *RedisCache) Delete(ctx context.Context, key string) error {
	if err := r.doer.Del(ctx, key); err != nil {
		return fmt.Errorf("cache delete %s: %w", key, err)
	}
	return nil
}

// Close releases the underlying connection pool.
func (r *RedisCache) Close() error { return r.doer.Close() }

// goRedis adapts *redis.Client to redisDoer, mapping redis.Nil (key absent) to
// found=false so a miss is never an error.
type goRedis struct{ client *redis.Client }

func (g goRedis) Get(ctx context.Context, key string) (string, bool, error) {
	v, err := g.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (g goRedis) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return g.client.Set(ctx, key, value, ttl).Err()
}

func (g goRedis) Del(ctx context.Context, key string) error {
	return g.client.Del(ctx, key).Err()
}

func (g goRedis) Close() error { return g.client.Close() }
