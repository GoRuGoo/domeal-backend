package model

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type RoleInterface interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

type RoleRedisInterface interface {
	UpsertUserRole(ctx context.Context, groupID, userID int64, newRole, iconURL string) error
	GetAllCurrentRoles(ctx context.Context, groupID int64) (map[string][]RoleMember, error)
}

type RedisRepository struct {
	rdb *redis.Client
}

func NewRedisRepository(rdb *redis.Client) *RedisRepository {
	return &RedisRepository{
		rdb: rdb,
	}
}

type RoleMember struct {
	UserID  int64  `json:"user_id"`
	IconURL string `json:"icon_url"`
}

func (repo *RedisRepository) UpsertUserRole(ctx context.Context, groupID, userID int64, newRole, iconURL string) error {
	roles := []string{"shopping", "cooking", "cleaning"}

	// ユーザーが現在どの役割に属しているか確認
	var currentRole string
	for _, role := range roles {
		key := fmt.Sprintf("group:%d:role:%s", groupID, role)
		members, err := repo.rdb.SMembers(ctx, key).Result()
		if err != nil {
			return fmt.Errorf("failed to check membership: %w", err)
		}

		for _, m := range members {
			var rm RoleMember
			if json.Unmarshal([]byte(m), &rm) == nil && rm.UserID == userID {
				currentRole = role
				break
			}
		}
		if currentRole != "" {
			break
		}
	}

	// トランザクション開始
	pipe := repo.rdb.TxPipeline()

	// もし現在の役割があれば削除
	if currentRole != "" && currentRole != newRole {
		oldKey := fmt.Sprintf("group:%d:role:%s", groupID, currentRole)
		// 古いJSONを削除
		oldValue, _ := json.Marshal(RoleMember{UserID: userID})
		pipe.SRem(ctx, oldKey, oldValue)
	}

	// 新しい役割に追加
	newKey := fmt.Sprintf("group:%d:role:%s", groupID, newRole)
	newValue, _ := json.Marshal(RoleMember{UserID: userID, IconURL: iconURL})
	pipe.SAdd(ctx, newKey, newValue)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to update role: %w", err)
	}

	return nil
}

func (repo *RedisRepository) GetAllCurrentRoles(ctx context.Context, groupID int64) (map[string][]RoleMember, error) {
	roles := []string{"shopping", "cooking", "cleaning"}
	result := make(map[string][]RoleMember)

	for _, role := range roles {
		members, err := repo.rdb.SMembers(ctx, fmt.Sprintf("group:%d:role:%s", groupID, role)).Result()
		if err != nil {
			return nil, err
		}

		var parsedMembers []RoleMember
		for _, m := range members {
			var rm RoleMember
			if err := json.Unmarshal([]byte(m), &rm); err == nil {
				parsedMembers = append(parsedMembers, rm)
			}
		}
		result[role] = parsedMembers
	}

	return result, nil
}
