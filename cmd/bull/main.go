// cmd/bull/main.go
//
// Bull — directed-work task queue + L402-gated billing (Module B).
//
// Task queue: POST /v1/tasks, GET /v1/tasks/next, PATCH status,
// exception, skip — three task types: receiving, replenishment, cycle_count.
//
// Replenishment trigger: background goroutine subscribes to
// inventory:replenish stream and creates replenishment tasks via Min/Max.
//
// Billing: L402 charge cycle + satoshi cost rollup under
// /v1/billing/* (API-key gated).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/ruptiv/canary/internal/billing"
	"github.com/ruptiv/canary/internal/cmdutil"
	"github.com/ruptiv/canary/internal/config"
	"github.com/ruptiv/canary/internal/db"
	"github.com/ruptiv/canary/internal/identity"
	"github.com/ruptiv/canary/internal/replenishment"
	"github.com/ruptiv/canary/internal/task"
	"github.com/ruptiv/canary/internal/workflow"
)

const serviceName = "canary-bull"

func main() {
	cfg := config.Load(serviceName)

	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("db connect", zap.Error(err))
	}
	defer pool.Close()

	opt, err := redis.ParseURL(cfg.ValkeyURL)
	if err != nil {
		logger.Fatal("valkey url parse", zap.Error(err))
	}
	valkeyClient := redis.NewClient(opt)
	defer func() { _ = valkeyClient.Close() }()

	// Register L402 charge cycle workflow at boot. Idempotent.
	wfStore := workflow.NewStore(pool)
	def, err := workflow.RegisterL402ChargeCycle(ctx, wfStore)
	if err != nil {
		logger.Fatal("register l402 charge cycle", zap.Error(err))
	}
	logger.Info("workflow registered",
		zap.String("workflow_code", def.WorkflowCode),
		zap.Int("version", def.Version),
		zap.String("workflow_id", def.ID.String()),
	)

	store := billing.NewStore(pool)
	h := billing.New(store, logger)

	taskStore := task.NewStore(pool)
	taskHandler := task.NewHandler(taskStore, logger)

	// Background replenishment trigger: inventory:replenish → Min/Max → task.
	//
	// Per-message panics are caught inside Trigger.handleSafely (poison-pill
	// stays unacked for redelivery). The wrapper below is a defensive belt-
	// and-suspenders layer: if Run itself panics or returns a non-cancellation
	// error, restart it after a short backoff. A sliding window caps restarts
	// at 5 in 60s so a tight panic-loop can't burn CPU.
	trigger := replenishment.NewTrigger(pool, taskStore, valkeyClient, logger)
	go func() {
		const (
			backoff       = 1 * time.Second
			maxRestarts   = 5
			restartWindow = 60 * time.Second
		)
		var (
			restarts    int
			windowStart = time.Now()
		)
		for {
			if ctx.Err() != nil {
				return
			}
			if time.Since(windowStart) > restartWindow {
				restarts = 0
				windowStart = time.Now()
			}
			err := runTriggerWithRecover(ctx, trigger, logger)
			if err == nil || errors.Is(err, context.Canceled) {
				return
			}
			restarts++
			if restarts > maxRestarts {
				logger.Error("replenishment trigger: too many restarts in window — giving up",
					zap.Int("restarts", restarts),
					zap.Duration("window", restartWindow),
					zap.Error(err),
				)
				return
			}
			logger.Warn("replenishment trigger crashed, restarting",
				zap.Int("restarts", restarts),
				zap.Duration("backoff", backoff),
				zap.Error(err),
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
		}
	}()

	limiter := cmdutil.MustValkeyRateLimiterFromClient(valkeyClient)

	closeRecorder := cmdutil.MustLastUsedRecorder(ctx, pool)
	defer closeRecorder()

	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer)
	r.Get("/health", health(cfg))

	// GRO-928: taskHandler carried tenant from query/header pre-fix
	// and was mounted outside the auth group. Move it inside so a
	// caller hitting cmd/bull's port directly (i.e., not via the
	// gateway mesh) gets 401'd before any tenant inference runs.
	r.Group(func(r chi.Router) {
		r.Use(identity.APIKeyMiddleware(identity.APIKeyMiddlewareOpts{
			Pool:     pool,
			Required: true,
			Limiter:  limiter,
		}))
		taskHandler.Mount(r)
		h.Mount(r)
	})

	addr := ":" + cfg.Port
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Fatal("listen", zap.Error(err))
	}
	logger.Info("starting",
		zap.String("service", serviceName),
		zap.String("addr", ln.Addr().String()),
	)
	srv := &http.Server{Handler: r}
	if err := cmdutil.RunServer(ctx, srv, ln, logger, 30*time.Second); err != nil &&
		!errors.Is(err, http.ErrServerClosed) {
		logger.Fatal("server", zap.Error(err))
	}
}

// runTriggerWithRecover invokes trigger.Run, converting any escaping panic
// into an error so the caller can apply backoff/restart logic. Per-message
// panics are already caught one level down in Trigger.handleSafely; this
// guards against a panic in Run itself (e.g. a future change to the
// XREADGROUP/EnsureGroup path).
func runTriggerWithRecover(ctx context.Context, t *replenishment.Trigger, log *zap.Logger) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("replenishment trigger panic",
				zap.Any("panic", r),
				zap.ByteString("stack", debug.Stack()),
			)
			err = fmt.Errorf("replenishment: panic: %v", r)
		}
	}()
	return t.Run(ctx)
}

func health(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"service": cfg.ServiceName,
			"version": "1.0.0",
		})
	}
}
