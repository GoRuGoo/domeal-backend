package model

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type RoleInterface interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

type RedisRepository struct {
	rdb *redis.Client
}

func NewRedisRepository(rdb *redis.Client) *RedisRepository {
	return &RedisRepository{
		rdb: rdb,
	}
}

type RoleRedisInterface interface {
	AssignRole(groupID int64, userID int64, role string) error
	ChangeRole(groupID int64, userID int64, newRole string) error
	GetAllCurrentRoles(groupID int64) (map[int64]string, error)
}

// ユーザーを役割に追加
func (repo *RedisRepository) AssignRole(ctx context.Context, groupID int64, userID int64, role string) error {
	key := fmt.Sprintf("group:%d:role:%s", groupID, role)
	return repo.rdb.SAdd(ctx, key, userID).Err()
}

// ユーザーの役割変更
func (repo *RedisRepository) ChangeRole(ctx context.Context, groupID int64, newRole string, userID int64) error {
	roles := []string{"shopping", "cooking", "cleaning"}
	pipe := repo.rdb.TxPipeline()
	for _, role := range roles {
		pipe.SRem(ctx, fmt.Sprintf("group:%d:role:%s", groupID, role), userID)
	}
	pipe.SAdd(ctx, fmt.Sprintf("group:%d:role:%s", groupID, newRole), userID)
	_, err := pipe.Exec(ctx)
	return err
}

// 現在の役割分担を取得
func (repo *RedisRepository) GetGroupRoles(ctx context.Context, groupID int64) (map[string][]string, error) {
	roles := []string{"shopping", "cooking", "cleaning"}
	result := make(map[string][]string)
	for _, role := range roles {
		members, err := repo.rdb.SMembers(ctx, fmt.Sprintf("group:%d:role:%s", groupID, role)).Result()
		if err != nil {
			return nil, err
		}
		result[role] = members
	}
	return result, nil
}
