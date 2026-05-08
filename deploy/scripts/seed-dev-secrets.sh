#!/usr/bin/env bash
# seed-dev-secrets.sh — substitute placeholder secrets in deploy/schema/99_seed.sql
# with random dev values, apply the seed, and write the values to .env.local
# so the gateway smoke-test caller can sign requests with the same secret.
#
# Why: 99_seed.sql ships placeholders (e.g. __SEED_HMAC_PLACEHOLDER__) so the
# repo never carries committable plaintext secrets. Local dev needs real
# values to actually exercise HMAC-validated flows.
#
# Usage (from CanaryGo/):
#   ./deploy/scripts/seed-dev-secrets.sh                  # apply with new values
#   ./deploy/scripts/seed-dev-secrets.sh --reuse          # reuse the values currently in .env.local
#
# Output:
#   - .env.local                      written / merged with the generated values
#   - canary_gcp database              seeded (placeholders replaced inline)
#
# GRO-859 / Sprint 2 T-W.

set -euo pipefail

cd "$(dirname "$0")/../.."  # CanaryGo/

PG_CONTAINER="${PG_CONTAINER:-growdirect_postgres}"
PG_USER="${PG_USER:-growdirect}"
PG_DB="${PG_DB:-canary_gcp}"
SEED_FILE="deploy/schema/99_seed.sql"
ENV_LOCAL=".env.local"

REUSE=0
if [ "${1:-}" = "--reuse" ]; then
  REUSE=1
fi

# Read or generate the dev HMAC.
if [ "$REUSE" = "1" ] && [ -f "$ENV_LOCAL" ] && grep -q "^DEV_SEED_HMAC=" "$ENV_LOCAL"; then
  DEV_SEED_HMAC=$(grep "^DEV_SEED_HMAC=" "$ENV_LOCAL" | cut -d= -f2-)
  echo "==> reusing DEV_SEED_HMAC from $ENV_LOCAL"
else
  DEV_SEED_HMAC=$(openssl rand -hex 32)
  echo "==> generated DEV_SEED_HMAC ($(echo -n "$DEV_SEED_HMAC" | wc -c | tr -d ' ') chars)"
fi

# Render the seed file with substituted values into a temp file. The
# committed 99_seed.sql is left unchanged on disk.
TMP_SEED=$(mktemp)
trap 'rm -f "$TMP_SEED"' EXIT
sed "s|__SEED_HMAC_PLACEHOLDER__|$DEV_SEED_HMAC|g" "$SEED_FILE" > "$TMP_SEED"

# Apply.
echo "==> applying seed to $PG_DB"
docker exec -i "$PG_CONTAINER" psql -U "$PG_USER" -d "$PG_DB" -v ON_ERROR_STOP=1 < "$TMP_SEED" >/dev/null

# Write / merge .env.local
touch "$ENV_LOCAL"
if grep -q "^DEV_SEED_HMAC=" "$ENV_LOCAL"; then
  # macOS sed requires the empty -i ''; Linux ignores it.
  if [[ "$OSTYPE" == "darwin"* ]]; then
    sed -i '' "s|^DEV_SEED_HMAC=.*|DEV_SEED_HMAC=$DEV_SEED_HMAC|" "$ENV_LOCAL"
  else
    sed -i "s|^DEV_SEED_HMAC=.*|DEV_SEED_HMAC=$DEV_SEED_HMAC|" "$ENV_LOCAL"
  fi
else
  echo "DEV_SEED_HMAC=$DEV_SEED_HMAC" >> "$ENV_LOCAL"
fi

echo "==> wrote DEV_SEED_HMAC to $ENV_LOCAL"
echo "==> done. Use \$DEV_SEED_HMAC when signing webhook requests in local smoke tests."
