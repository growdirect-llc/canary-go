---
title: Runbook — Deploy Canary Protocol Gateway to GCP
runbook-id: gateway-gcp-deploy
version: 1
status: ready
domain: canary
last-compiled: 2026-05-02
linear: GRO-756
---

# Runbook — Deploy Canary Protocol Gateway to GCP

This runbook brings up `api.canary.growdirect.io` end-to-end on GCP — Cloud SQL Postgres 17, Memorystore Redis, Cloud Run, Cloud Load Balancer with Google-managed cert, Cloud Armor WAF, Secret Manager, and a first end-to-end smoke test of a signed webhook from outside the laptop.

**Audience.** Coordinator (next-session ALX or founder). Executable cold by someone outside the originating conversation. Every command is copy-paste-ready; every "expected output" line tells you what success looks like before you proceed.

**Patent reference.** This deployment is the operating embodiment of Node 2 in Patent Application 63/991,596 (Universal Event Notarization, Six-Node Architecture).

**Total time.** ~90–120 minutes wall clock, with ~30 minutes of that being managed-cert provisioning (DNS-gated, async).

## §0 — Cost confirmation gate (mandatory)

Before any provisioning runs, confirm the projected monthly cost.

| Component | Tier | Monthly cost |
|---|---|---|
| Cloud Run | min-instance=1 | ~$8 |
| Cloud SQL | db-g1-small | ~$25 |
| Memorystore Redis | BASIC 1 GB | ~$30 |
| Cloud LB + Armor | 1 rule | ~$23 |
| Secret Manager | ~10 secrets | ~$1 |
| VPC connector | 2 e2-micro | ~$10 |
| Other | AR + logging + monitoring | ~$1 |
| **Total** | | **~$98 / mo** |

**Hard ceiling per session escalation criteria: $200/mo.** If during execution any service estimate jumps materially above its line, stop and check with founder before continuing.

**Confirm with founder:**
> "Projected ~$98/mo MVP run rate confirmed. Proceeding with provisioning."

If founder does not confirm, halt here.

## §1 — Preconditions

Run these checks before anything else. All must pass.

```bash
# 1. gcloud authenticated to the right account
gcloud config get-value account
# Expected: gclyle@growdirect.io

# 2. Active project is canary-rapidpos
gcloud config get-value project
# Expected: canary-rapidpos
# If not: gcloud config set project canary-rapidpos

# 3. Application Default Credentials available (for Terraform/sdk fall-throughs)
gcloud auth application-default print-access-token | head -c 20
# Expected: a token prefix (ya29....)
# If missing: gcloud auth application-default login

# 4. Required tools
which gcloud jq openssl curl docker
# All four paths must print

# 5. Local Docker stack up (for source secret HMAC computation in smoke test)
docker ps --filter "name=growdirect_postgres" --format "{{.Status}}"
# Expected: Up (healthy)

# 6. On the gro-756 worktree, branch is checked out
cd /Users/gclyle/GrowDirect/.worktrees/gro-756
git branch --show-current
# Expected: gclyle/gro-756-phase-1j-deploy-canary-protocol-gateway-to-gcp-cloud-run

# 7. Wave 1-3 merges are in place — main contains migrations 016, 017, 018
git -C /Users/gclyle/GrowDirect log --oneline main -10 | grep -E "GRO-(687|693|694|748|746)"
# Expected: at least one line per dispatch GRO number
```

**Halt conditions.** If gcloud is on the wrong account, project, or has no ADC token — fix before continuing. If migrations 016/017/018 aren't on main — Wave 3 isn't done; return to coordinator review.

## §2 — Environment variables for this session

Set these once at the top of your shell — every later command references them.

```bash
export PROJECT_ID=canary-rapidpos
export REGION=us-central1
export DOMAIN=api.canary.growdirect.io
export CONNECTION_NAME=$PROJECT_ID:$REGION:canary-pg
```

## §3 — Provision the substrate (idempotent, ~10 min)

The provisioning script is idempotent — every step is `describe || create`. Safe to re-run if it fails midway.

```bash
cd /Users/gclyle/GrowDirect/.worktrees/gro-756/CanaryGo
bash deploy/scripts/deploy-gateway.sh
# Expected output (last line):
# [deploy-gateway] deploy-gateway.sh complete. Next: trigger Cloud Build (runbook §9).
```

**What the script provisions** (in this order):

| Step | What | Verification |
|---|---|---|
| `apis` | Enables 12 GCP APIs | `gcloud services list --enabled` shows all |
| `sa` | Runtime SA `canary-gateway-rt` + 6 IAM roles | `gcloud iam service-accounts describe canary-gateway-rt@$PROJECT_ID.iam.gserviceaccount.com` |
| `repo` | Artifact Registry `canary-go` in `$REGION` | `gcloud artifacts repositories describe canary-go --location=$REGION` |
| `vpc` | Serverless VPC connector `canary-vpc-conn` | `gcloud compute networks vpc-access connectors describe canary-vpc-conn --region=$REGION` |
| `sql` | Cloud SQL Postgres 17 `canary-pg` (db-g1-small, private IP, PITR) + `canary_go` + `canary_go_test` DBs + `canary` user (password seeded into SM) | `gcloud sql instances describe canary-pg` shows `state: RUNNABLE` |
| `redis` | Memorystore Redis 7.2 `canary-redis` (BASIC 1 GB, private service access); seeds `canary-gateway-valkey-url` secret | `gcloud redis instances describe canary-redis --region=$REGION` shows `state: READY` |
| `secrets` | 4 Secret Manager secrets (placeholders for what isn't already populated by `sql` and `redis` steps) | `gcloud secrets list --filter="name:canary-gateway"` shows 4 entries |
| `cloudrun` | Cloud Run service `canary-gateway-staging` with placeholder hello image | `gcloud run services describe canary-gateway-staging --region=$REGION` shows a URL |
| `lb` | Static IP, NEG, Armor policy, backend, URL map, cert, HTTPS proxy + forwarding rule, HTTP→HTTPS redirect | `gcloud compute addresses describe canary-gateway-ip --global` shows an IP |

**If a step fails:** the script prints the failing `gcloud` command. Run it manually with `--verbosity=debug` to diagnose. Common causes: API not yet propagated (wait 60s and retry), org policy blocking a resource (escalate to founder), quota limit (escalate).

**Cost-watch checkpoint after this step:** `gcloud billing accounts list` and check the linked project's projected spend. Should not have changed materially yet (pay-per-use kicks in at first traffic).

## §4 — Capture the LB static IP for DNS

```bash
LB_IP=$(gcloud compute addresses describe canary-gateway-ip --global --format="value(address)")
echo "Cloudflare A-record target: $LB_IP"
# Expected: an IPv4 like 34.149.x.x
```

## §5 — Create Cloudflare DNS A-record (FOUNDER STEP)

**ESCALATE TO FOUNDER.** Cloudflare API access is on the session escalation list.

> "Need a Cloudflare DNS A-record:
> - Type: A
> - Name: api.canary
> - Content: `$LB_IP` (printed above)
> - Proxy status: **DNS only** (grey cloud / unproxied) — required so Google's managed cert sees the apex
> - TTL: Auto"

After founder confirms record is created, verify resolution from the laptop:

```bash
# May take 30-60s after the record lands
dig +short $DOMAIN
# Expected: $LB_IP (the same IP from §4)

# If using DoH / DoT clients, also try:
nslookup $DOMAIN 1.1.1.1
```

If DNS isn't resolving after 5 minutes, check the Cloudflare record was saved as proxy-disabled (orange cloud breaks managed-cert challenge).

## §6 — Wait for Google-managed cert provisioning

The cert was created in §3 but is in `PROVISIONING` until DNS resolves and Google completes its ACME-equivalent challenge.

```bash
# Poll every 60s; usually finishes in 15-30 min after DNS resolves.
gcloud compute ssl-certificates describe canary-gateway-cert --global \
  --format="value(managed.status,managed.domainStatus)"
# Expected progression:
#   PROVISIONING  api.canary.growdirect.io: PROVISIONING
#   ACTIVE        api.canary.growdirect.io: ACTIVE
```

Loop until status is `ACTIVE`:

```bash
until [ "$(gcloud compute ssl-certificates describe canary-gateway-cert --global --format='value(managed.status)')" = "ACTIVE" ]; do
  echo "[$(date +%H:%M:%S)] cert still PROVISIONING"
  sleep 60
done
echo "cert ACTIVE"
```

**If cert stays PROVISIONING > 60 min**: something is wrong with DNS. Re-verify §5. Common cause: Cloudflare proxy is on (orange cloud) — Google can't see the actual IP for ACME challenge.

## §7 — Apply database migrations

The first time, run migrations from a workstation that can reach Cloud SQL via the public IP (temporary) OR via the Cloud SQL Auth Proxy.

**Easiest path — Cloud SQL Auth Proxy on the laptop:**

```bash
# In a separate terminal:
gcloud auth application-default login   # if not already done
cloud-sql-proxy --port 5433 $CONNECTION_NAME &
PROXY_PID=$!

# Get the password from Secret Manager (the password set during step_sql)
DATABASE_URL=$(gcloud secrets versions access latest --secret=canary-gateway-database-url)
# DATABASE_URL is in unix-socket form for Cloud Run; for the proxy on localhost,
# extract password and rebuild as TCP:
PWD=$(echo "$DATABASE_URL" | sed -n 's|.*://canary:\([^@]*\)@.*|\1|p')
LOCAL_URL="postgres://canary:$PWD@127.0.0.1:5433/canary_go?sslmode=disable"

# Run migrations from the gateway worktree (or main — they have the same migrations after Wave 3)
cd /Users/gclyle/GrowDirect/.worktrees/gro-756/CanaryGo
migrate -path=deploy/migrations -database="$LOCAL_URL" up

# Expected output:
# 15/u protocol_source_secrets (NN ms)
# 16/u protocol_audit_log (NN ms)
# 17/u protocol_evidence (NN ms)
# 18/u protocol_source_secrets_sm_ref (NN ms)
# (plus any migrations 001-014 not yet applied)

# Tear down the proxy
kill $PROXY_PID
```

**Verify the schema landed:**

```bash
# Reconnect briefly to check
cloud-sql-proxy --port 5433 $CONNECTION_NAME &
PROXY_PID=$!

PGPASSWORD=$PWD psql -h 127.0.0.1 -p 5433 -U canary -d canary_go -c \
  "SELECT table_schema, table_name FROM information_schema.tables WHERE table_schema='protocol' ORDER BY table_name;"
# Expected at minimum:
#  protocol | audit_log         (or app.audit_log per GRO-694 reuse pattern)
#  protocol | evidence
#  protocol | source_secrets

kill $PROXY_PID
```

## §8 — Seed a test merchant secret

The gateway needs at least one HMAC source secret to verify webhooks against. Seed one for smoke-test purposes.

```bash
# Generate a strong secret
TEST_SECRET=$(openssl rand -hex 32)
TEST_MERCHANT_UUID=$(uuidgen | tr '[:upper:]' '[:lower:]')
echo "TEST_MERCHANT_UUID=$TEST_MERCHANT_UUID"
echo "TEST_SECRET=$TEST_SECRET"   # capture both — needed for §11 smoke test

# Create the SM secret (per the SmResolver naming convention from GRO-687)
SM_NAME="canary-source-$TEST_MERCHANT_UUID-square"
gcloud secrets create "$SM_NAME" \
  --replication-policy=automatic \
  --labels=service=gateway,kind=source-hmac

printf '%s' "$TEST_SECRET" | gcloud secrets versions add "$SM_NAME" --data-file=-

# Grant runtime SA access (covered by the project-wide binding for now;
# Phase 2 hardening narrows this with a condition)

# Insert metadata row in protocol.source_secrets pointing at this SM ref
cloud-sql-proxy --port 5433 $CONNECTION_NAME &
PROXY_PID=$!
PGPASSWORD=$PWD psql -h 127.0.0.1 -p 5433 -U canary -d canary_go <<SQL
INSERT INTO protocol.source_secrets
  (merchant_id, source_code, signature_algo, replay_window_seconds, status, secret_sm_ref)
VALUES
  ('$TEST_MERCHANT_UUID', 'square', 'hmac-sha256', 300, 'active',
   'projects/$PROJECT_ID/secrets/$SM_NAME/versions/latest')
ON CONFLICT (merchant_id, source_code) DO UPDATE
  SET secret_sm_ref = EXCLUDED.secret_sm_ref, status = 'active';
SQL
kill $PROXY_PID
```

## §9 — First Cloud Build deploy of the real gateway image

```bash
cd /Users/gclyle/GrowDirect/.worktrees/gro-756/CanaryGo
gcloud builds submit \
  --config=deploy/cloudbuild.gateway.yaml \
  --region=$REGION \
  .
# Expected: "DURATION ... STATUS SUCCESS" at the end.
# Expected: Cloud Run service updated to image gateway:$SHORT_SHA.
```

If the build fails on the deploy step, the most likely cause is the runtime SA missing `iam.serviceAccountUser` on itself or the build SA missing `actAs` on the runtime SA. The provisioning script grants both; re-run it if needed.

**Verify the new revision is live:**

```bash
gcloud run revisions list --service=canary-gateway-staging --region=$REGION --limit=3
# Expected: most recent revision is the one just deployed; ACTIVE.

gcloud run services describe canary-gateway-staging --region=$REGION \
  --format="value(status.latestReadyRevisionName,status.url)"
# Expected: revision name and *.run.app URL (which is locked to LB anyway)
```

## §10 — Set the SECRET_BACKEND env var for production

Once the SmResolver is wired (GRO-687 already merged into main), tell the gateway to use it.

```bash
gcloud run services update canary-gateway-staging --region=$REGION \
  --update-env-vars=SECRET_BACKEND=sm,GCP_PROJECT=$PROJECT_ID,LOG_LEVEL=info
# Expected: a new revision is created and serves traffic immediately.
```

## §11 — Smoke test the live URL

This is the moment of truth. From the laptop (outside the GCP project), POST a signed webhook to `https://api.canary.growdirect.io` and verify it lands.

```bash
# Same TEST_MERCHANT_UUID and TEST_SECRET captured in §8
PAYLOAD='{"event_type":"order.created","occurred_at":"2026-05-02T19:00:00Z","data":{"order_id":"smoke-test-001","total":42.00}}'

# Compute HMAC-SHA256 of the raw payload (matches the gateway's hmac.go logic)
SIG=$(printf '%s' "$PAYLOAD" | openssl dgst -sha256 -hmac "$TEST_SECRET" -hex | awk '{print $2}')
TIMESTAMP=$(date +%s)

curl -i -X POST "https://$DOMAIN/v1/protocol/webhook/square" \
  -H "Content-Type: application/json" \
  -H "X-Merchant-ID: $TEST_MERCHANT_UUID" \
  -H "X-Signature: sha256=$SIG" \
  -H "X-Timestamp: $TIMESTAMP" \
  -d "$PAYLOAD"

# Expected:
#   HTTP/2 200
#   ...
#   {"event_id":"<uuid>","accepted":true}
# Capture the event_id — needed for the bilateral verify check.
```

**If you get HTTP 401 / 403:** the SmResolver is not finding the secret. Check:
- `gcloud run services describe canary-gateway-staging --region=$REGION --format="value(spec.template.spec.containers[0].env)"` — `SECRET_BACKEND=sm` set?
- `gcloud secrets versions list canary-source-$TEST_MERCHANT_UUID-square` — version ACTIVE?
- `protocol.source_secrets` row exists for `(merchant_id, source_code) = ($TEST_MERCHANT_UUID, 'square')` with `status='active'`?

**If you get HTTP 404 on the path:** the gateway isn't routing `/v1/protocol/webhook/{source}` correctly. Inspect logs (§12).

**If you get HTTP 503 / 504:** Cloud Run is cold-starting. min-instances=1 should prevent this; if it persists, escalate.

## §12 — Verify the event landed in Cloud SQL

```bash
EVENT_ID="<uuid from §11 response>"

cloud-sql-proxy --port 5433 $CONNECTION_NAME &
PROXY_PID=$!
PGPASSWORD=$PWD psql -h 127.0.0.1 -p 5433 -U canary -d canary_go <<SQL
SELECT event_id, source_code, merchant_id, ingested_at
FROM protocol.evidence
WHERE event_id = '$EVENT_ID';
SQL
# Expected: 1 row matching the event_id from §11

# Also verify the audit log captured the request
PGPASSWORD=$PWD psql -h 127.0.0.1 -p 5433 -U canary -d canary_go <<SQL
SELECT event_id, action, status_code, latency_ms, source_ip
FROM app.audit_log
WHERE event_id = '$EVENT_ID';
SQL
# Expected: 1 row with status_code=200 and a sub-100ms latency_ms
kill $PROXY_PID
```

## §13 — Verify the bilateral retrieval API works from outside

```bash
curl -i "https://$DOMAIN/v1/protocol/evidence/$EVENT_HASH"
# (event_hash is in the §11 response payload — re-issue if you didn't capture)
# Expected: HTTP 200 with the canonical evidence record (event_id, event_hash, chain_hash, prev_chain_hash, ingested_at, raw_payload).
```

## §14 — Capture screenshots / outputs for the ship comment

Capture these for the GRO-756 + GRO-739 ship comments:

- `gcloud run services describe canary-gateway-staging --region=$REGION --format=yaml > /tmp/cloudrun.yaml`
- `gcloud sql instances describe canary-pg --format=yaml > /tmp/sql.yaml`
- `gcloud redis instances describe canary-redis --region=$REGION --format=yaml > /tmp/redis.yaml`
- `dig +short $DOMAIN`
- The full `curl -i` response from §11 (200 OK + accepted body)
- The Cloud SQL row from §12

## §15 — Update Linear + memory bus

```bash
# From /Users/gclyle/GrowDirect (main repo, not worktree)

# Linear: GRO-756 transition to Done with ship comment
# (use Linear MCP save_comment with body referencing the captured outputs)

# Linear: GRO-739 ship-comment update
# (per the parent dispatch's tracking comment pattern)

# Memory bus reseed if any Brain/wiki/ changed
python3 services/memory-bus/scripts/seed_standalone.py
```

## Rollback

If the deploy goes wrong and you need to back out:

| What you did | Roll back |
|---|---|
| Cloud Run deploy | `gcloud run services update-traffic canary-gateway-staging --region=$REGION --to-revisions=<previous-revision>=100` |
| Cloud SQL migration | `cd CanaryGo && migrate -path=deploy/migrations -database=$LOCAL_URL down 1` (one step at a time) |
| Cloudflare DNS | Delete the A-record. The LB IP remains; cert eventually fails health check after 30 days |
| Whole substrate | `gcloud run services delete`, `gcloud sql instances delete`, `gcloud redis instances delete`, `gcloud compute *` deletions in reverse order. The script's `describe || create` makes this safe to redo, but Cloud SQL has a 7-day quarantine before name reuse |

**Do not** delete Secret Manager secrets — they're cheap, and accidental deletion is hard to recover. Disable versions instead.

## Failure modes (canonical)

**Symptom: `cert PROVISIONING > 60 min`.**
Cause: DNS not resolving to the LB IP, or Cloudflare proxy is on.
Fix: Verify §5 with `dig +short $DOMAIN` returning the LB IP exactly. Toggle Cloudflare proxy off (grey cloud).

**Symptom: `curl returns 401 / 403`.**
Cause: HMAC mismatch (most common). Less commonly: SmResolver not finding the secret.
Fix: Re-derive the signature exactly (`printf '%s' "$PAYLOAD"` matters; trailing newlines break HMAC). Check Secret Manager version is ACTIVE.

**Symptom: `Cloud Run revision in error state`.**
Cause: Migration not applied, env var misconfig, runtime SA missing a role.
Fix: `gcloud run revisions describe <rev> --region=$REGION --format=yaml` — read the Conditions array. Most often Cloud SQL connection error or Secret Manager 403.

**Symptom: `Build deploys but service not reachable through LB`.**
Cause: Forwarding rule not yet pointing at the active backend, or Armor blocking traffic.
Fix: `gcloud compute forwarding-rules describe canary-gateway-https-fr --global --format=yaml` — confirm `targetHttpsProxy` is `canary-gateway-https-proxy`. Check Cloud Armor logs for blocked requests.

**Symptom: `Memorystore unreachable from Cloud Run`.**
Cause: VPC connector misconfigured or in a different region.
Fix: `gcloud compute networks vpc-access connectors describe canary-vpc-conn --region=$REGION` — must be in same region as Cloud Run.

## Related

- Design doc: `docs/sdds/canary-go/gcp-deployment-gateway.md`
- Provisioning script: `CanaryGo/deploy/scripts/deploy-gateway.sh`
- Cloud Build config: `CanaryGo/deploy/cloudbuild.gateway.yaml`
- Container image: `CanaryGo/deploy/Dockerfile.gateway`
- Secret Manager integration: `docs/sdds/canary-go/secrets-manager-integration.md` (GRO-687)
- Foundation runbook: `Brain/wiki/cards/gcp-foundation-runbook.md`
- Linear: [GRO-756](https://linear.app/growdirect/issue/GRO-756) · [GRO-739](https://linear.app/growdirect/issue/GRO-739)
- Patent: Application 63/991,596
