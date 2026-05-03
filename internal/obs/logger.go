// internal/obs/logger.go
//
// Structured zap logger with trace-id correlation. The codebase already
// imports go.uber.org/zap across modules (item, inventory, fox, sub2);
// this file gives the consistent constructor + the With() helper that
// adds trace_id/span_id from an OTel-instrumented request context.

package obs

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger constructs a zap.Logger configured per env:
//
//	ENV=dev  → console encoder, debug level, colored
//	ENV=*    → json encoder, info level, structured
//
// LOG_LEVEL overrides the default level when set ("debug", "info",
// "warn", "error"). Service name is attached as a constant field.
func NewLogger(serviceName string) *zap.Logger {
	level := parseLevel(os.Getenv("LOG_LEVEL"))
	dev := isDev()

	var cfg zap.Config
	if dev {
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	} else {
		cfg = zap.NewProductionConfig()
		cfg.EncoderConfig.TimeKey = "ts"
		cfg.EncoderConfig.MessageKey = "msg"
		cfg.EncoderConfig.LevelKey = "level"
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	}
	cfg.Level = zap.NewAtomicLevelAt(level)
	cfg.InitialFields = map[string]any{"service": serviceName}

	logger, err := cfg.Build(zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	if err != nil {
		// zap construction can only fail on bad encoder config; fall
		// back to a no-op logger rather than panic at process boot.
		return zap.NewNop()
	}
	return logger
}

// WithTrace returns a child logger that carries trace_id and span_id
// from the OTel span attached to ctx. If no span is present the original
// logger is returned unchanged.
//
// Use this in handlers that have already received an OTel span via the
// chi middleware in obs.Middleware:
//
//	log := obs.WithTrace(ctx, h.Logger)
//	log.Info("handled request", zap.Int("status", 200))
func WithTrace(ctx context.Context, l *zap.Logger) *zap.Logger {
	span := trace.SpanFromContext(ctx)
	sc := span.SpanContext()
	if !sc.IsValid() {
		return l
	}
	return l.With(
		zap.String("trace_id", sc.TraceID().String()),
		zap.String("span_id", sc.SpanID().String()),
	)
}

func parseLevel(s string) zapcore.Level {
	switch s {
	case "debug", "DEBUG":
		return zapcore.DebugLevel
	case "warn", "WARN", "warning":
		return zapcore.WarnLevel
	case "error", "ERROR":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

func isDev() bool {
	switch os.Getenv("ENV") {
	case "dev", "development", "":
		return true
	default:
		return false
	}
}
