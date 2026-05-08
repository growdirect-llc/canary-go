package keystore

import (
	"context"
	"errors"
	"os"

	"go.uber.org/zap"
)

// BootstrapDevKeyIfEmpty seeds a fresh RS256 key into the keystore
// if it has no active key. Dev-mode convenience: a fresh `db-reset`
// leaves app.signing_keys empty, which would make every JWT mint
// fail. In production (ENV=production) we fatal instead — keys must
// be published via the rotation runbook, never auto-generated, so a
// key compromise can't be silently masked by an auto-recovery path.
//
// T-2 / GRO-862. Extracted for reuse by the gateway and identity
// binaries (T-1.a / GRO-848).
func BootstrapDevKeyIfEmpty(ctx context.Context, ks *Store, logger *zap.Logger) {
	if _, err := ks.Active(ctx); err == nil {
		return
	} else if !errors.Is(err, ErrNoActiveKey) {
		logger.Fatal("keystore active probe failed", zap.Error(err))
	}

	if os.Getenv("ENV") == "production" {
		logger.Fatal("keystore: no active signing key in production; publish a key via the rotation runbook before boot")
	}

	logger.Warn("keystore: no active signing key; bootstrapping a dev RS256 key (auto-generated — production fatals here)")
	sk, err := GenerateRSA()
	if err != nil {
		logger.Fatal("keystore bootstrap GenerateRSA", zap.Error(err))
	}
	if err := ks.Insert(ctx, sk); err != nil {
		logger.Fatal("keystore bootstrap Insert", zap.Error(err))
	}
	logger.Info("keystore: dev key inserted", zap.String("kid", sk.Kid))
}
