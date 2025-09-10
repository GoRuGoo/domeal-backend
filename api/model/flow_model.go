package model

import (
	"context"

	"github.com/redis/go-redis/v9"
)

type FlowRedisInterface interface {
	Subscribe(ctx context.Context, channel string) *redis.PubSub
	Publish(ctx context.Context, channel, message string) error
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
