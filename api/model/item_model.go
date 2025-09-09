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
