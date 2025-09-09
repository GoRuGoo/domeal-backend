package model

import "github.com/redis/go-redis/v9"

type ItemRedisInterface interface {
	test()
}

func (r *RedisItemRepository) test() {
}

type RedisItemRepository struct {
	rdb *redis.Client
}
