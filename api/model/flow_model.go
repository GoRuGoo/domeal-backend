package model

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type FlowRedisInterface interface {
	Subscribe(ctx context.Context, channel string) *redis.PubSub
	Publish(ctx context.Context, channel, message string) error
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	Get(ctx context.Context, key string) (string, error)
}

type FlowRedisRepository struct {
	rdb *redis.Client
}

func NewFlowRedisRepository(rdb *redis.Client) *FlowRedisRepository {
	return &FlowRedisRepository{
		rdb: rdb,
	}
}

func (r *FlowRedisRepository) Subscribe(ctx context.Context, channel string) *redis.PubSub {
	return r.rdb.Subscribe(ctx, channel)
}

func (r *FlowRedisRepository) Publish(ctx context.Context, channel, message string) error {
	return r.rdb.Publish(ctx, channel, message).Err()
}

func (r *FlowRedisRepository) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return r.rdb.Set(ctx, key, value, expiration).Err()
}

func (r *FlowRedisRepository) Get(ctx context.Context, key string) (string, error) {
	return r.rdb.Get(ctx, key).Result()
}
