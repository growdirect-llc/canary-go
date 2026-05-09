package cmdutil

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// RunServer serves HTTP on the supplied listener until ctx is cancelled
// (typically by a SIGINT/SIGTERM trap from signal.NotifyContext), then
// drains in-flight requests within gracePeriod via srv.Shutdown.
//
// Replaces the bare `http.ListenAndServe` pattern in 19+ cmd binaries —
// that pattern killed in-flight requests immediately on SIGTERM, dropping
// webhooks and triggering idempotent retries on every deploy.
//
// Accepting an explicit net.Listener (rather than calling Serve from
// srv.Addr) lets tests bind to ephemeral ports and lets cmd binaries
// keep listener creation explicit if they want to log the bound address
// before serving.
//
// Returns nil on a clean shutdown. Returns the underlying error from
// srv.Serve if listening fails for a non-clean reason. http.ErrServerClosed
// is returned via Serve when Shutdown closes the server — callers
// typically treat it as a successful shutdown:
//
//	if err := cmdutil.RunServer(ctx, srv, ln, logger, 30*time.Second); err != nil &&
//	    !errors.Is(err, http.ErrServerClosed) {
//	    logger.Fatal("server crashed", zap.Error(err))
//	}
//
// Usage with the standard cmd pattern:
//
//	ctx, stop := signal.NotifyContext(context.Background(),
//	    os.Interrupt, syscall.SIGTERM)
//	defer stop()
//
//	ln, err := net.Listen("tcp", ":8086")
//	if err != nil { logger.Fatal("listen", zap.Error(err)) }
//
//	srv := &http.Server{Handler: r}
//	logger.Info("listening", zap.String("addr", ln.Addr().String()))
//	if err := cmdutil.RunServer(ctx, srv, ln, logger, 30*time.Second); err != nil &&
//	    !errors.Is(err, http.ErrServerClosed) {
//	    logger.Fatal("server crashed", zap.Error(err))
//	}
func RunServer(ctx context.Context, srv *http.Server, ln net.Listener, logger *zap.Logger, gracePeriod time.Duration) error {
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(ln)
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received; draining",
			zap.Duration("grace_period", gracePeriod))
		shutdownCtx, cancel := context.WithTimeout(context.Background(), gracePeriod)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed; falling back to Close",
				zap.Error(err))
			_ = srv.Close()
			return err
		}
		// Wait for Serve to return ErrServerClosed after Shutdown completes,
		// but don't block past the grace period.
		select {
		case err := <-serveErr:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-time.After(gracePeriod):
			return nil
		}
	}
}
