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
	UpsertUserRole(ctx context.Context, groupID, userID int64, newRole string) error
	GetAllCurrentRoles(ctx context.Context, groupID int64) (map[string][]string, error)
}

func (repo *RedisRepository) UpsertUserRole(ctx context.Context, groupID, userID int64, newRole string) error {
	roles := []string{"shopping", "cooking", "cleaning"}

	// ユーザーが現在どの役割に属しているか確認
	var currentRole string
	for _, role := range roles {
		key := fmt.Sprintf("group:%d:role:%s", groupID, role)
		isMember, err := repo.rdb.SIsMember(ctx, key, userID).Result()
		if err != nil {
			return fmt.Errorf("failed to check membership: %w", err)
		}
		if isMember {
			currentRole = role
			break
		}
	}

	// トランザクション開始
	pipe := repo.rdb.TxPipeline()

	// もし現在の役割があれば削除
	if currentRole != "" && currentRole != newRole {
		oldKey := fmt.Sprintf("group:%d:role:%s", groupID, currentRole)
		pipe.SRem(ctx, oldKey, userID)
	}

	// 新しい役割に追加
	newKey := fmt.Sprintf("group:%d:role:%s", groupID, newRole)
	pipe.SAdd(ctx, newKey, userID)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to update role: %w", err)
	}

	return nil
}

// 現在の役割分担を取得
func (repo *RedisRepository) GetAllCurrentRoles(ctx context.Context, groupID int64) (map[string][]string, error) {
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
