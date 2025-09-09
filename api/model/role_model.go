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
	CreateRole(ctx context.Context, groupID, userID int64, role string) error
}

// CreateRole データベースに役割を永続化
func (repo *Repository) CreateRole(ctx context.Context, groupID, userID int64, role string) error {
	// まず、既存の役割があれば削除
	deleteQuery := `
		DELETE FROM
			roles
		WHERE
			group_id = $1
		AND
			user_id = $2`
	_, err := repo.db.ExecContext(ctx, deleteQuery, groupID, userID)
	if err != nil {
		return fmt.Errorf("failed to delete existing role: %w", err)
	}

	// 新しい役割を挿入
	insertQuery := `
		INSERT INTO
			roles (group_id, user_id, role, created_at)
		VALUES
			($1, $2, $3, CURRENT_TIMESTAMP)
	`
	_, err = repo.db.ExecContext(ctx, insertQuery, groupID, userID, role)
	if err != nil {
		return fmt.Errorf("failed to insert new role: %w", err)
	}

	return nil
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

	var currentRole string
	var currentMemberJSON string

	// 1. 現在の役割を特定（完全一致するJSONも取得）
	for _, role := range roles {
		key := fmt.Sprintf("group:%d:role:%s", groupID, role)
		members, err := repo.rdb.SMembers(ctx, key).Result()
		if err != nil {
			return fmt.Errorf("failed to check membership: %w", err)
		}

		for _, m := range members {
			var rm RoleMember
			if err := json.Unmarshal([]byte(m), &rm); err != nil {
				continue // JSON不正ならスキップ
			}
			if rm.UserID == userID {
				currentRole = role
				currentMemberJSON = m // 完全一致するJSONを保持
				break
			}
		}
		if currentRole != "" {
			break
		}
	}

	// 2. トランザクション開始
	pipe := repo.rdb.TxPipeline()

	// 3. 古い役割があり、かつ新しい役割と異なる場合は削除
	if currentRole != "" && currentRole != newRole {
		oldKey := fmt.Sprintf("group:%d:role:%s", groupID, currentRole)
		pipe.SRem(ctx, oldKey, currentMemberJSON)
	}

	// 4. 新しい役割を追加
	newKey := fmt.Sprintf("group:%d:role:%s", groupID, newRole)
	newValue, err := json.Marshal(RoleMember{UserID: userID, IconURL: iconURL})
	if err != nil {
		return fmt.Errorf("failed to marshal new role: %w", err)
	}
	pipe.SAdd(ctx, newKey, newValue)

	// 5. コマンド実行
	if _, err := pipe.Exec(ctx); err != nil {
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
