// Package replenishment implements the Min/Max replenishment trigger.
//
// It subscribes to the inventory:replenish Valkey stream (emitted by the
// inventory sale consumer when SOH <= 0). For each signal:
//
//  1. Load the display_min/display_max threshold for (tenant, item, location)
//     from app.inventory_thresholds. Falls back to min=1 when no row exists.
//  2. Parse SOH from the stream message.
//  3. If SOH < display_min → check for an open replenishment task.
//  4. If none exists → create a replenishment task in app.directed_tasks.
//
// Consumer group: "replenishment-trigger". One consumer per process.
//
// GRO-799: Loop 4 Min/Max engine wired to SOH events.
package replenishment

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/growdirect-llc/rapidpos/internal/task"
)

const (
	replenishStream   = "inventory:replenish"
	consumerGroup     = "replenishment-trigger"
	batchSize         = 20
	blockTimeout      = 2 * time.Second
	defaultDisplayMin = "1"
)

// Trigger subscribes to inventory:replenish and generates replenishment tasks.
type Trigger struct {
	pool      *pgxpool.Pool
	taskStore *task.Store
	rdb       *redis.Client
	consumer  string
	logger    *zap.Logger
}

// NewTrigger constructs a Trigger. consumer is a unique name for this instance
// (defaults to hostname).
func NewTrigger(pool *pgxpool.Pool, ts *task.Store, rdb *redis.Client, logger *zap.Logger) *Trigger {
	if logger == nil {
		logger = zap.NewNop()
	}
	consumer, _ := os.Hostname()
	if consumer == "" {
		consumer = "replenishment-trigger-default"
	}
	return &Trigger{
		pool:      pool,
		taskStore: ts,
		rdb:       rdb,
		consumer:  consumer,
		logger:    logger.With(zap.String("svc", "replenishment-trigger")),
	}
}

// EnsureGroup creates the consumer group if it doesn't exist yet.
func (t *Trigger) EnsureGroup(ctx context.Context) error {
	err := t.rdb.XGroupCreateMkStream(ctx, replenishStream, consumerGroup, "$").Err()
	if err != nil && !isBusyGroup(err) {
		return fmt.Errorf("replenishment: xgroup create: %w", err)
	}
	return nil
}

// Run blocks processing messages until ctx is cancelled.
func (t *Trigger) Run(ctx context.Context) error {
	if err := t.EnsureGroup(ctx); err != nil {
		return err
	}
	t.logger.Info("replenishment trigger starting",
		zap.String("stream", replenishStream),
		zap.String("group", consumerGroup),
	)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := t.processBatch(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			t.logger.Warn("batch error", zap.Error(err))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
}

func (t *Trigger) processBatch(ctx context.Context) error {
	res, err := t.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    consumerGroup,
		Consumer: t.consumer,
		Streams:  []string{replenishStream, ">"},
		Count:    batchSize,
		Block:    blockTimeout,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return fmt.Errorf("xreadgroup: %w", err)
	}
	for _, stream := range res {
		for _, msg := range stream.Messages {
			t.handle(ctx, msg)
		}
	}
	return nil
}

func (t *Trigger) handle(ctx context.Context, msg redis.XMessage) {
	tenantID, itemID, locationID, soh, err := parseReplenishMsg(msg)
	if err != nil {
		t.logger.Warn("malformed replenish message", zap.String("id", msg.ID), zap.Error(err))
		t.ack(ctx, msg.ID)
		return
	}

	threshold, err := t.loadThreshold(ctx, tenantID, itemID, locationID)
	if err != nil {
		t.logger.Error("load threshold", zap.Error(err))
		// Leave un-acked — transient DB error, retry is safe.
		return
	}

	if !belowMin(soh, threshold.displayMin) {
		t.ack(ctx, msg.ID) // SOH above threshold — no action needed.
		return
	}

	// Deduplication: don't create a second open replenishment task.
	exists, err := t.taskStore.OpenReplenishmentExists(ctx, tenantID, itemID, locationID)
	if err != nil {
		t.logger.Error("open replenishment check", zap.Error(err))
		return
	}
	if exists {
		t.ack(ctx, msg.ID)
		return
	}

	qty := quantityToPull(soh, threshold.displayMax, threshold.displayMin)
	req := task.CreateTaskRequest{
		TenantID:   tenantID,
		TaskType:   task.TypeReplenishment,
		Priority:   2, // replenishment is high-priority directed work
		ItemID:     &itemID,
		LocationID: &locationID,
		Quantity:   &qty,
	}
	if _, err := t.taskStore.Create(ctx, req); err != nil {
		t.logger.Error("create replenishment task",
			zap.String("item_id", itemID.String()),
			zap.String("location_id", locationID.String()),
			zap.Error(err),
		)
		return
	}

	t.logger.Info("replenishment task created",
		zap.String("item_id", itemID.String()),
		zap.String("location_id", locationID.String()),
		zap.String("qty", qty),
	)
	t.ack(ctx, msg.ID)
}

type threshold struct {
	displayMin float64
	displayMax *float64
}

func (t *Trigger) loadThreshold(ctx context.Context, tenantID, itemID, locationID uuid.UUID) (threshold, error) {
	const q = `
		SELECT display_min::text, display_max::text
		  FROM app.inventory_thresholds
		 WHERE tenant_id = $1 AND item_id = $2 AND location_id = $3
		 LIMIT 1`
	var minStr string
	var maxStr *string
	err := t.pool.QueryRow(ctx, q, tenantID, itemID, locationID).Scan(&minStr, &maxStr)
	if err != nil {
		// No row — use default min of 1, no max.
		return threshold{displayMin: 1}, nil
	}
	min, _ := strconv.ParseFloat(minStr, 64)
	th := threshold{displayMin: min}
	if maxStr != nil {
		v, _ := strconv.ParseFloat(*maxStr, 64)
		th.displayMax = &v
	}
	return th, nil
}

func parseReplenishMsg(msg redis.XMessage) (tenantID, itemID, locationID uuid.UUID, soh float64, err error) {
	get := func(key string) string {
		if v, ok := msg.Values[key].(string); ok {
			return v
		}
		return ""
	}
	tenantID, err = uuid.Parse(get("tenant_id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, 0, fmt.Errorf("tenant_id: %w", err)
	}
	itemID, err = uuid.Parse(get("item_id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, 0, fmt.Errorf("item_id: %w", err)
	}
	locationID, err = uuid.Parse(get("location_id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, 0, fmt.Errorf("location_id: %w", err)
	}
	sohStr := get("soh")
	soh, err = strconv.ParseFloat(sohStr, 64)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, 0, fmt.Errorf("soh: %w", err)
	}
	return tenantID, itemID, locationID, soh, nil
}

// belowMin returns true when soh < displayMin.
func belowMin(soh float64, min float64) bool {
	return soh < min
}

// quantityToPull computes display_max - SOH if max is set; else display_min.
func quantityToPull(soh float64, max *float64, min float64) string {
	var qty float64
	if max != nil && *max > soh {
		qty = *max - soh
	} else {
		qty = min
	}
	if qty <= 0 {
		qty = min
	}
	return strconv.FormatFloat(qty, 'f', 4, 64)
}

func (t *Trigger) ack(ctx context.Context, id string) {
	if _, err := t.rdb.XAck(ctx, replenishStream, consumerGroup, id).Result(); err != nil {
		t.logger.Warn("ack failed", zap.String("id", id), zap.Error(err))
	}
}

func isBusyGroup(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "BUSYGROUP") || strings.Contains(s, "already exists")
}
