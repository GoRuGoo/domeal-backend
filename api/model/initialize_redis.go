package model

import (
	"crypto/tls"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
)

func InitRedis() (*redis.Client, error) {
	// REDIS_HOST には "host:port" の形で指定
	addr := os.Getenv("REDIS_HOST")

	// IS_PRODUCTION が true の場合のみTLSを有効化
	isProduction := strings.ToLower(os.Getenv("IS_PRODUCTION")) == "true"

	var tlsConfig *tls.Config
	if isProduction {
		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12, // AWS ElastiCacheはTLS1.2以上
		}
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:      addr,
		Password:  "", // パスワード不要なら空でOK
		DB:        0,
		TLSConfig: tlsConfig,
	})

	return rdb, nil
}
