#!/usr/bin/env bash
# scripts/mirror-public.sh
#
# Syncs curated paths from CanaryGo into the growdirect-llc/canary-go
# public mirror. Runs as a transient clone — no persistent local copy.
#
# Usage:
#   bash scripts/mirror-public.sh
#
# What gets mirrored:
#   services/canary-protocol/openapi/openapi.yaml  — OpenAPI 3.0 spec
#   services/canary-protocol/openapi/README.md     — spec docs
#   README.md, LICENSE, CONTRIBUTING.md            — managed in the mirror
#
# The mirror README/LICENSE/CONTRIBUTING are maintained in the mirror
# directly and are NOT overwritten by this script. Only the OpenAPI
# spec is synced from source.
#
# Requires: git, gh (authenticated to growdirect-llc org)

set -euo pipefail

REPO="growdirect-llc/canary-go"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CANARY_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MIRROR_DIR="$(mktemp -d)/canary-go-mirror"

echo "==> Cloning $REPO to $MIRROR_DIR"
gh repo clone "$REPO" "$MIRROR_DIR"

echo "==> Syncing OpenAPI spec"
mkdir -p "$MIRROR_DIR/services/canary-protocol/openapi"
cp "$CANARY_ROOT/../services/canary-protocol/openapi/openapi.yaml" \
   "$MIRROR_DIR/services/canary-protocol/openapi/openapi.yaml"
cp "$CANARY_ROOT/../services/canary-protocol/openapi/README.md" \
   "$MIRROR_DIR/services/canary-protocol/openapi/README.md"

echo "==> Syncing SDD library"
mkdir -p "$MIRROR_DIR/docs/sdds"
cp "$CANARY_ROOT/../docs/sdds/go-handoff/"*.md "$MIRROR_DIR/docs/sdds/"

cd "$MIRROR_DIR"

if git diff --quiet && git diff --cached --quiet; then
  echo "==> No changes to sync"
else
  git add services/canary-protocol/openapi/ docs/sdds/
  git -c user.email="bonsallprotea@gmail.com" -c user.name="GrowDirect" \
    commit -m "chore(mirror): sync OpenAPI spec and SDDs from source"
  git push
  echo "==> Pushed to $REPO"
fi

cd /
rm -rf "$MIRROR_DIR"
echo "==> Done"
