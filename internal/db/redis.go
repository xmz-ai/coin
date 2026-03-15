package db

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

func NewClient(ctx context.Context, addr, password string, db int) (*goredis.Client, error) {
	client := goredis.NewClient(&goredis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}
