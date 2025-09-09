package model

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/redis/go-redis/v9"
)

type ItemRedisInterface interface {
	InsertItemByGroupID(ctx context.Context, groupID int64, item PurchaseItem) error
	ChoiseItemByReceiptIDAndUserData(ctx context.Context, groupID, receiptID, userID int64, iconURL string) error
	RemoveItemChoiceByReceiptIDAndUserData(ctx context.Context, groupID, receiptID, userID int64, iconURL string) error
}

type RedisItemRepository struct {
	rdb *redis.Client
}

func NewRedisItemRepository(rdb *redis.Client) *RedisItemRepository {
	return &RedisItemRepository{
		rdb: rdb,
	}
}

func (r *RedisItemRepository) InsertItemByGroupID(ctx context.Context, groupID int64, item PurchaseItem) error {
	key := fmt.Sprintf("items:%d:itemInfo", groupID)
	data, err := json.Marshal(item)
	if err != nil {
		slog.Error("failed to marshal item", "error", err, "item", item)
		return fmt.Errorf("failed to marshal item: %w", err)
	}
	err = r.rdb.HSet(ctx, key, strconv.FormatInt(item.ID, 10), data).Err()
	if err != nil {
		slog.Error("failed to insert item into redis", "error", err, "key", key, "item", item)
		return fmt.Errorf("failed to insert item into redis: %w", err)
	}

	return nil
}

func (r *RedisItemRepository) ChoiseItemByReceiptIDAndUserData(ctx context.Context, groupID, receiptID, userID int64, iconURL string) error {
	key := fmt.Sprintf("items:%d:%d", groupID, receiptID)

	// ユーザー情報をJSON化
	userInfo := struct {
		UserID  int64  `json:"user_id"`
		IconURL string `json:"icon_url"`
	}{
		UserID:  userID,
		IconURL: iconURL,
	}

	data, err := json.Marshal(userInfo)
	if err != nil {
		slog.Error("failed to marshal user info", "error", err, "userInfo", userInfo)
		return fmt.Errorf("failed to marshal user info: %w", err)
	}

	// Setに追加（重複は無視される）
	if err := r.rdb.SAdd(ctx, key, data).Err(); err != nil {
		slog.Error("failed to add user info to redis set", "error", err, "key", key, "userInfo", userInfo)
		return fmt.Errorf("failed to add user info to redis set: %w", err)
	}

	return nil
}

func (r *RedisItemRepository) RemoveItemChoiceByReceiptIDAndUserData(ctx context.Context, groupID, receiptID, userID int64, iconURL string) error {
	key := fmt.Sprintf("items:%d:%d", groupID, receiptID)

	// 削除対象のユーザー情報をJSON化
	userInfo := struct {
		UserID  int64  `json:"user_id"`
		IconURL string `json:"icon_url"`
	}{
		UserID:  userID,
		IconURL: iconURL,
	}

	data, err := json.Marshal(userInfo)
	if err != nil {
		slog.Error("failed to marshal user info for removal", "error", err, "userInfo", userInfo)
		return fmt.Errorf("failed to marshal user info for removal: %w", err)
	}

	// Setから削除
	if err := r.rdb.SRem(ctx, key, data).Err(); err != nil {
		slog.Error("failed to remove user info from redis set", "error", err, "key", key, "userInfo", userInfo)
		return fmt.Errorf("failed to remove user info from redis set: %w", err)
	}

	return nil
}

type ItemInterface interface {
	GetAllItemsDataByReceiptID(receiptID int64) ([]PurchaseItem, error)
}

func (r *Repository) GetAllItemsDataByReceiptID(receiptID int64) ([]PurchaseItem, error) {
	query := `
		SELECT
			id,
			receipt_id,
			group_id,
			item_name,
			price,
			quantity,
			created_at
		FROM
			purchase_items
		WHERE
			receipt_id = $1
		ORDER BY id ASC
	`

	rows, err := r.db.Query(query, receiptID)
	if err != nil {
		return nil, fmt.Errorf("failed to query purchase_items: %w", err)
	}
	defer rows.Close()

	var items []PurchaseItem
	for rows.Next() {
		var item PurchaseItem
		if err := rows.Scan(
			&item.ID,
			&item.ReceiptID,
			&item.GroupID,
			&item.ItemName,
			&item.Price,
			&item.Quantity,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan purchase_item row: %w", err)
		}
		items = append(items, item)
	}

	// rows.Err() でループ中のエラーをチェック
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return items, nil
}
