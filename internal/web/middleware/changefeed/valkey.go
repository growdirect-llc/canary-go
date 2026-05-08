// Valkey-backed Watermark implementation. Production constructor.
package changefeed

import (
	"context"
	"errors"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// ValkeyWatermark reads watermarks from a Valkey STRING per stream key.
// Producers (TSP sealers, alert generators, owl indexers) write the
// watermark via SET — that wiring isn't this middleware's responsibility.
//
// Key shape: "watermark:" + stream key. Stays in lockstep with whatever
// producer convention the platform settles on.
type ValkeyWatermark struct {
	rdb    *redis.Client
	prefix string
}

// NewValkeyWatermark wraps an existing go-redis client. The client's
// lifecycle stays with the gateway.
func NewValkeyWatermark(rdb *redis.Client, prefix string) *ValkeyWatermark {
	if prefix == "" {
		prefix = "watermark:"
	}
	return &ValkeyWatermark{rdb: rdb, prefix: prefix}
}

func (v *ValkeyWatermark) Get(ctx context.Context, key string) (int64, bool, error) {
	res, err := v.rdb.Get(ctx, v.prefix+key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, false, nil
		}
		return 0, false, err
	}
	parsed, perr := strconv.ParseInt(res, 10, 64)
	if perr != nil {
		return 0, false, ErrInvalidWatermark
	}
	return parsed, true, nil
}
