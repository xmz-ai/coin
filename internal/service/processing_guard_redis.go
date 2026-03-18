package service

import (
	"context"
	"errors"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type RedisProcessingGuard struct {
	client *goredis.Client
	ttl    time.Duration
}

func NewRedisProcessingGuard(client *goredis.Client, ttl time.Duration) *RedisProcessingGuard {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &RedisProcessingGuard{client: client, ttl: ttl}
}

func (g *RedisProcessingGuard) TryBegin(txnNo, stage string) bool {
	if g == nil || g.client == nil {
		return false
	}
	key := ProcessingKey(txnNo, stage)
	if strings.TrimSpace(key) == "+" {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	ok, err := g.client.SetNX(ctx, key, "1", g.ttl).Result()
	if err != nil {
		return false
	}
	return ok
}

func (g *RedisProcessingGuard) TryBeginWithError(txnNo, stage string) (bool, error) {
	if g == nil || g.client == nil {
		return false, ErrProcessingGuardUnavailable
	}
	key := ProcessingKey(txnNo, stage)
	if strings.TrimSpace(key) == "+" {
		return false, ErrProcessingGuardUnavailable
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	ok, err := g.client.SetNX(ctx, key, "1", g.ttl).Result()
	if err != nil {
		return false, errors.Join(ErrProcessingGuardUnavailable, err)
	}
	return ok, nil
}

func (g *RedisProcessingGuard) End(txnNo, stage string) {
	if g == nil || g.client == nil {
		return
	}
	key := ProcessingKey(txnNo, stage)
	if strings.TrimSpace(key) == "+" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = g.client.Del(ctx, key).Err()
}
