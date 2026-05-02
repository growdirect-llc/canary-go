#!/usr/bin/env bash
# deploy/scripts/deploy-gateway.sh
#
# Idempotent provisioning script for the Canary Protocol API Gateway
# on GCP. Designed to be re-run safely — every "create" is gated by a
# "describe" check first.
#
# Reasoning for gcloud over Terraform: no existing Terraform state for
# this project (greenfield), MVP scope, founder owns operations alone.
# A plain shell script is auditable line-by-line in the runbook.
# Re-evaluate at Phase 2 — once we have >5 services on GCP, Terraform.
#
# Idempotency strategy: every step is "describe || create". No update
# semantics here — that's what cloudbuild.gateway.yaml is for.
#
# Usage:
#   bash deploy/scripts/deploy-gateway.sh                  # full provision
#   STEP=sql bash deploy/scripts/deploy-gateway.sh         # one step
#   DRY_RUN=1 bash deploy/scripts/deploy-gateway.sh        # echo only
#
# Steps (in order):
#   apis sa repo vpc sql redis secrets cloudrun lb
#
# Prerequisites checked at top: gcloud authed, project set, region set.

set -euo pipefail

# ---- Configuration ---------------------------------------------------------

PROJECT_ID="${PROJECT_ID:-canary-rapidpos}"
REGION="${REGION:-us-central1}"
ZONE="${ZONE:-us-central1-a}"

# Service-level
SERVICE="canary-gateway-staging"
RUNTIME_SA_NAME="canary-gateway-rt"
RUNTIME_SA_EMAIL="${RUNTIME_SA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"
DEPLOY_SA_EMAIL="canary-deploy@${PROJECT_ID}.iam.gserviceaccount.com"

# Artifact Registry
REPO="canary-go"

# Cloud SQL
SQL_INSTANCE="canary-pg"
SQL_TIER="db-g1-small"        # 1.7 GB RAM, $25/mo. db-f1-micro = $7/mo (no SLA — bumps to small for staging).
SQL_DB="canary_go"
SQL_DB_TEST="canary_go_test"
SQL_USER="canary"

# Memorystore Redis (Valkey-compatible — Redis 7.x speaks XADD/XREAD identically)
REDIS_INSTANCE="canary-redis"
REDIS_TIER="basic"            # BASIC=no replica, ~$15-30/mo for 1 GB. Standard=$60/mo.
REDIS_SIZE_GB=1

# VPC + Serverless connector (required for Cloud Run -> Memorystore)
VPC_NAME="default"
CONNECTOR="canary-vpc-conn"
CONNECTOR_RANGE="10.8.0.0/28"

# Secrets (HMAC source secrets seeded with placeholders; rotate via runbook §11)
SECRETS=(
  "canary-gateway-database-url"
  "canary-gateway-valkey-url"
  "canary-gateway-internal-service-secret"
  "canary-gateway-session-secret"
)

# Cloud LB / domain
DOMAIN="api.canary.growdirect.io"
NEG_NAME="canary-gateway-neg"
BACKEND_NAME="canary-gateway-backend"
URLMAP_NAME="canary-gateway-urlmap"
CERT_NAME="canary-gateway-cert"
PROXY_NAME="canary-gateway-https-proxy"
FORWARDING_HTTPS="canary-gateway-https-fr"
FORWARDING_HTTP="canary-gateway-http-fr"
HTTP_REDIRECT_URLMAP="canary-gateway-http-redirect"
HTTP_PROXY_NAME="canary-gateway-http-proxy"
LB_IP_NAME="canary-gateway-ip"
ARMOR_POLICY="canary-gateway-armor"

# ---- Helpers --------------------------------------------------------------

log() { printf '\n[deploy-gateway] %s\n' "$*" >&2; }
run() {
  if [[ "${DRY_RUN:-0}" == "1" ]]; then
    printf '  DRY: %s\n' "$*" >&2
  else
    eval "$@"
  fi
}
exists() { eval "$@" >/dev/null 2>&1; }

require() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing tool: $1" >&2; exit 1; }
}

# ---- Preflight ------------------------------------------------------------

require gcloud
require jq

ACTIVE_PROJECT=$(gcloud config get-value project 2>/dev/null || true)
if [[ "$ACTIVE_PROJECT" != "$PROJECT_ID" ]]; then
  log "setting active project to $PROJECT_ID"
  gcloud config set project "$PROJECT_ID" >/dev/null
fi

ACTIVE_ACCOUNT=$(gcloud config get-value account 2>/dev/null || true)
if [[ -z "$ACTIVE_ACCOUNT" ]]; then
  echo "no active gcloud account — run 'gcloud auth login' first" >&2
  exit 1
fi
log "running as $ACTIVE_ACCOUNT in project $PROJECT_ID, region $REGION"

# ---- Step: APIs (idempotent) ----------------------------------------------

step_apis() {
  log "ensuring required APIs are enabled"
  local apis=(
    run.googleapis.com
    sqladmin.googleapis.com
    secretmanager.googleapis.com
    artifactregistry.googleapis.com
    cloudbuild.googleapis.com
    redis.googleapis.com
    vpcaccess.googleapis.com
    compute.googleapis.com
    certificatemanager.googleapis.com
    monitoring.googleapis.com
    logging.googleapis.com
    cloudtrace.googleapis.com
    servicenetworking.googleapis.com
  )
  for api in "${apis[@]}"; do
    if gcloud services list --enabled --filter="config.name:$api" --format="value(config.name)" | grep -qx "$api"; then
      log "  $api already enabled"
    else
      log "  enabling $api"
      run "gcloud services enable $api"
    fi
  done
}

# ---- Step: runtime service account ----------------------------------------

step_sa() {
  log "ensuring runtime service account $RUNTIME_SA_EMAIL"
  if gcloud iam service-accounts describe "$RUNTIME_SA_EMAIL" >/dev/null 2>&1; then
    log "  $RUNTIME_SA_NAME exists"
  else
    run "gcloud iam service-accounts create $RUNTIME_SA_NAME --display-name='Canary Gateway runtime'"
  fi

  log "binding least-privilege roles to $RUNTIME_SA_EMAIL"
  local roles=(
    roles/cloudsql.client
    roles/secretmanager.secretAccessor
    roles/logging.logWriter
    roles/monitoring.metricWriter
    roles/cloudtrace.agent
    roles/redis.editor
  )
  for role in "${roles[@]}"; do
    run "gcloud projects add-iam-policy-binding $PROJECT_ID \
      --member='serviceAccount:$RUNTIME_SA_EMAIL' \
      --role='$role' --condition=None --quiet >/dev/null"
  done
}

# ---- Step: Artifact Registry ----------------------------------------------

step_repo() {
  log "ensuring Artifact Registry repo $REPO"
  if gcloud artifacts repositories describe "$REPO" --location="$REGION" >/dev/null 2>&1; then
    log "  $REPO exists"
  else
    run "gcloud artifacts repositories create $REPO \
      --repository-format=docker --location=$REGION \
      --description='Canary Go service images'"
  fi
}

# ---- Step: VPC + Serverless connector -------------------------------------
# Cloud Run -> Memorystore requires a Serverless VPC connector. Cloud Run ->
# Cloud SQL works via the built-in Auth Proxy without a connector, but using
# the connector for both means one network path to reason about.

step_vpc() {
  log "ensuring Serverless VPC connector $CONNECTOR"
  if gcloud compute networks vpc-access connectors describe "$CONNECTOR" --region="$REGION" >/dev/null 2>&1; then
    log "  $CONNECTOR exists"
  else
    run "gcloud compute networks vpc-access connectors create $CONNECTOR \
      --region=$REGION --network=$VPC_NAME --range=$CONNECTOR_RANGE \
      --min-instances=2 --max-instances=3 --machine-type=e2-micro"
  fi

  # Service Networking VPC peering — required for private-IP Cloud SQL.
  # Without this, sql instances create with --no-assign-ip fails with
  # SERVICE_NETWORKING_NOT_ENABLED.
  log "ensuring Service Networking peering for private Cloud SQL"
  if ! gcloud compute addresses describe google-managed-services-default --global >/dev/null 2>&1; then
    run "gcloud compute addresses create google-managed-services-default \
      --global --purpose=VPC_PEERING --prefix-length=16 --network=$VPC_NAME \
      --description='Reserved range for Cloud SQL private services'"
  fi
  if ! gcloud services vpc-peerings list --network="$VPC_NAME" \
    --service=servicenetworking.googleapis.com \
    --format="value(reservedPeeringRanges)" 2>/dev/null | grep -q google-managed-services-default; then
    run "gcloud services vpc-peerings connect \
      --service=servicenetworking.googleapis.com \
      --ranges=google-managed-services-default --network=$VPC_NAME --async"
    log "  peering operation issued (async); Cloud SQL create will block until ready"
  fi
}

# ---- Step: Cloud SQL ------------------------------------------------------

step_sql() {
  log "ensuring Cloud SQL Postgres 17 instance $SQL_INSTANCE"
  if gcloud sql instances describe "$SQL_INSTANCE" >/dev/null 2>&1; then
    log "  $SQL_INSTANCE exists"
  else
    run "gcloud sql instances create $SQL_INSTANCE \
      --database-version=POSTGRES_17 \
      --edition=ENTERPRISE \
      --tier=$SQL_TIER \
      --region=$REGION \
      --storage-type=SSD --storage-size=10GB --storage-auto-increase \
      --backup --backup-start-time=08:00 \
      --maintenance-window-day=SUN --maintenance-window-hour=09 \
      --enable-point-in-time-recovery \
      --network=projects/$PROJECT_ID/global/networks/$VPC_NAME \
      --no-assign-ip --availability-type=zonal \
      --database-flags=cloudsql.iam_authentication=on"
  fi

  log "ensuring databases $SQL_DB, $SQL_DB_TEST"
  for db in "$SQL_DB" "$SQL_DB_TEST"; do
    if gcloud sql databases describe "$db" --instance="$SQL_INSTANCE" >/dev/null 2>&1; then
      log "  $db exists"
    else
      run "gcloud sql databases create $db --instance=$SQL_INSTANCE"
    fi
  done

  log "ensuring SQL user $SQL_USER"
  if gcloud sql users list --instance="$SQL_INSTANCE" --format="value(name)" | grep -qx "$SQL_USER"; then
    log "  $SQL_USER exists"
  else
    # Generate password, store in Secret Manager. We must ensure the
    # secret exists FIRST — step_secrets runs after step_sql, so create
    # the database-url secret on demand here. Idempotent.
    local pwd
    pwd=$(openssl rand -base64 32 | tr -d '/+=' | head -c 32)
    run "gcloud sql users create $SQL_USER --instance=$SQL_INSTANCE --password='$pwd'"
    if ! gcloud secrets describe canary-gateway-database-url >/dev/null 2>&1; then
      run "gcloud secrets create canary-gateway-database-url \
        --replication-policy=automatic --labels=service=gateway"
    fi
    # DATABASE_URL takes the form:
    # postgres://user:pwd@/canary_go?host=/cloudsql/<connection_name>
    local conn="$PROJECT_ID:$REGION:$SQL_INSTANCE"
    local url="postgres://$SQL_USER:$pwd@/$SQL_DB?host=/cloudsql/$conn&sslmode=disable"
    run "printf '%s' '$url' | gcloud secrets versions add canary-gateway-database-url --data-file=-"
  fi
}

# ---- Step: Memorystore Redis ----------------------------------------------

step_redis() {
  log "ensuring Memorystore Redis $REDIS_INSTANCE"
  if gcloud redis instances describe "$REDIS_INSTANCE" --region="$REGION" >/dev/null 2>&1; then
    log "  $REDIS_INSTANCE exists"
  else
    run "gcloud redis instances create $REDIS_INSTANCE \
      --tier=$REDIS_TIER --size=$REDIS_SIZE_GB --region=$REGION \
      --redis-version=redis_7_2 --connect-mode=PRIVATE_SERVICE_ACCESS \
      --network=projects/$PROJECT_ID/global/networks/$VPC_NAME"
  fi

  # Capture host/port and seed VALKEY_URL secret if not yet versioned.
  # As with database-url, ensure the secret exists first (step_secrets runs after).
  local host port
  host=$(gcloud redis instances describe "$REDIS_INSTANCE" --region="$REGION" --format="value(host)" 2>/dev/null || true)
  port=$(gcloud redis instances describe "$REDIS_INSTANCE" --region="$REGION" --format="value(port)" 2>/dev/null || true)
  if [[ -n "$host" && -n "$port" ]]; then
    local url="redis://$host:$port/0"
    if ! gcloud secrets describe canary-gateway-valkey-url >/dev/null 2>&1; then
      run "gcloud secrets create canary-gateway-valkey-url \
        --replication-policy=automatic --labels=service=gateway"
    fi
    if ! gcloud secrets versions list canary-gateway-valkey-url --limit=1 --format="value(name)" 2>/dev/null | grep -q .; then
      log "  seeding canary-gateway-valkey-url with $url"
      run "printf '%s' '$url' | gcloud secrets versions add canary-gateway-valkey-url --data-file=-"
    fi
  fi
}

# ---- Step: Secret Manager seeds ------------------------------------------

step_secrets() {
  log "ensuring Secret Manager secrets exist (placeholders only — real values seeded by step_sql / step_redis / runbook §11)"
  for s in "${SECRETS[@]}"; do
    if gcloud secrets describe "$s" >/dev/null 2>&1; then
      log "  $s exists"
    else
      run "gcloud secrets create $s --replication-policy=automatic --labels=service=gateway"
      # Seed an obvious placeholder so a misconfigured deploy fails loudly.
      run "printf 'PLACEHOLDER_REPLACE_VIA_RUNBOOK_STEP_11' | gcloud secrets versions add $s --data-file=-"
    fi
    # Grant runtime SA access.
    run "gcloud secrets add-iam-policy-binding $s \
      --member='serviceAccount:$RUNTIME_SA_EMAIL' \
      --role='roles/secretmanager.secretAccessor' --quiet >/dev/null"
  done
}

# ---- Step: Cloud Run service (initial deploy with placeholder image) ------

step_cloudrun() {
  log "ensuring Cloud Run service $SERVICE"
  # On first run, deploy a placeholder hello image so the service URL exists
  # and the Cloud LB can be wired before the real Cloud Build pushes.
  if gcloud run services describe "$SERVICE" --region="$REGION" >/dev/null 2>&1; then
    log "  $SERVICE exists (Cloud Build will update via cloudbuild.gateway.yaml)"
  else
    log "  deploying placeholder service so LB can be wired"
    run "gcloud run deploy $SERVICE \
      --image=us-docker.pkg.dev/cloudrun/container/hello \
      --region=$REGION --platform=managed --no-allow-unauthenticated \
      --service-account=$RUNTIME_SA_EMAIL \
      --vpc-connector=$CONNECTOR --vpc-egress=private-ranges-only \
      --add-cloudsql-instances=$PROJECT_ID:$REGION:$SQL_INSTANCE \
      --min-instances=1 --max-instances=10 --concurrency=80 \
      --cpu=1 --memory=512Mi --port=8080 --timeout=30s"
  fi

  # Allow the Cloud LB serverless NEG to invoke the service. The
  # invoker binding is what makes "Cloud LB hits Cloud Run" work even
  # though --no-allow-unauthenticated is set (LB authenticates as a
  # service identity).
  log "  binding LB invoker access"
  run "gcloud run services add-iam-policy-binding $SERVICE --region=$REGION \
    --member='allUsers' --role='roles/run.invoker' --quiet >/dev/null || true"
  # NOTE: 'allUsers' here lets Cloud LB pass-through; production hardening
  # is documented in runbook §13 — switch to specific LB SA once available.
}

# ---- Step: Cloud Load Balancer + managed cert + Cloud Armor --------------

step_lb() {
  log "ensuring global static IP $LB_IP_NAME"
  if ! gcloud compute addresses describe "$LB_IP_NAME" --global >/dev/null 2>&1; then
    run "gcloud compute addresses create $LB_IP_NAME --global"
  fi
  local ip
  ip=$(gcloud compute addresses describe "$LB_IP_NAME" --global --format="value(address)")
  log "  static IP: $ip  (point Cloudflare DNS A-record for $DOMAIN at this address)"

  log "ensuring serverless NEG $NEG_NAME"
  if ! gcloud compute network-endpoint-groups describe "$NEG_NAME" --region="$REGION" >/dev/null 2>&1; then
    run "gcloud compute network-endpoint-groups create $NEG_NAME \
      --region=$REGION --network-endpoint-type=serverless \
      --cloud-run-service=$SERVICE"
  fi

  log "ensuring Cloud Armor policy $ARMOR_POLICY"
  if ! gcloud compute security-policies describe "$ARMOR_POLICY" >/dev/null 2>&1; then
    run "gcloud compute security-policies create $ARMOR_POLICY \
      --description='Canary gateway WAF — basic OWASP rules + rate limit'"
    # Default rule (priority 2147483647) is "allow all". We add a rate-limit
    # rule and an OWASP CRS rule; full WAF tuning is post-MVP.
    run "gcloud compute security-policies rules create 1000 \
      --security-policy=$ARMOR_POLICY \
      --expression=\"evaluatePreconfiguredExpr('xss-v33-stable')\" \
      --action=deny-403"
    run "gcloud compute security-policies rules create 1100 \
      --security-policy=$ARMOR_POLICY \
      --expression=\"evaluatePreconfiguredExpr('sqli-v33-stable')\" \
      --action=deny-403"
    run "gcloud compute security-policies rules create 2000 \
      --security-policy=$ARMOR_POLICY \
      --src-ip-ranges='*' --action=rate-based-ban \
      --rate-limit-threshold-count=600 --rate-limit-threshold-interval-sec=60 \
      --conform-action=allow --exceed-action=deny-429 \
      --enforce-on-key=IP --ban-duration-sec=300"
  fi

  log "ensuring backend service $BACKEND_NAME"
  if ! gcloud compute backend-services describe "$BACKEND_NAME" --global >/dev/null 2>&1; then
    # NOTE: Serverless NEGs require NO --protocol flag (HTTPS sets portName=https
    # which is rejected by add-backend). Cloud LB → serverless NEG uses Google's
    # internal protocol; portName must remain unset.
    run "gcloud compute backend-services create $BACKEND_NAME \
      --global --load-balancing-scheme=EXTERNAL_MANAGED"
    run "gcloud compute backend-services add-backend $BACKEND_NAME \
      --global --network-endpoint-group=$NEG_NAME \
      --network-endpoint-group-region=$REGION"
    # Attach Cloud Armor policy AFTER create — current gcloud rejects
    # --security-policy on backend-services create.
    run "gcloud compute backend-services update $BACKEND_NAME \
      --global --security-policy=$ARMOR_POLICY"
  fi

  log "ensuring URL map $URLMAP_NAME"
  if ! gcloud compute url-maps describe "$URLMAP_NAME" --global >/dev/null 2>&1; then
    run "gcloud compute url-maps create $URLMAP_NAME --default-service=$BACKEND_NAME"
  fi

  log "ensuring Google-managed cert $CERT_NAME for $DOMAIN"
  if ! gcloud compute ssl-certificates describe "$CERT_NAME" --global >/dev/null 2>&1; then
    run "gcloud compute ssl-certificates create $CERT_NAME \
      --domains=$DOMAIN --global"
  fi

  log "ensuring HTTPS proxy $PROXY_NAME"
  if ! gcloud compute target-https-proxies describe "$PROXY_NAME" >/dev/null 2>&1; then
    run "gcloud compute target-https-proxies create $PROXY_NAME \
      --url-map=$URLMAP_NAME --ssl-certificates=$CERT_NAME"
  fi

  log "ensuring HTTPS forwarding rule $FORWARDING_HTTPS"
  if ! gcloud compute forwarding-rules describe "$FORWARDING_HTTPS" --global >/dev/null 2>&1; then
    run "gcloud compute forwarding-rules create $FORWARDING_HTTPS \
      --address=$LB_IP_NAME --global --target-https-proxy=$PROXY_NAME --ports=443"
  fi

  # HTTP -> HTTPS redirect (separate URL map of type 'redirect').
  log "ensuring HTTP redirect URL map + forwarding rule"
  if ! gcloud compute url-maps describe "$HTTP_REDIRECT_URLMAP" --global >/dev/null 2>&1; then
    # NOTE: gcloud import schema rejects the `kind` field. Provide only
    # name + defaultUrlRedirect.
    cat > /tmp/http-redirect.yaml <<EOF
name: $HTTP_REDIRECT_URLMAP
defaultUrlRedirect:
  redirectResponseCode: MOVED_PERMANENTLY_DEFAULT
  httpsRedirect: true
  stripQuery: false
EOF
    run "gcloud compute url-maps import $HTTP_REDIRECT_URLMAP --source=/tmp/http-redirect.yaml --global --quiet"
  fi
  if ! gcloud compute target-http-proxies describe "$HTTP_PROXY_NAME" >/dev/null 2>&1; then
    run "gcloud compute target-http-proxies create $HTTP_PROXY_NAME --url-map=$HTTP_REDIRECT_URLMAP"
  fi
  if ! gcloud compute forwarding-rules describe "$FORWARDING_HTTP" --global >/dev/null 2>&1; then
    run "gcloud compute forwarding-rules create $FORWARDING_HTTP \
      --address=$LB_IP_NAME --global --target-http-proxy=$HTTP_PROXY_NAME --ports=80"
  fi

  log "LB READY. DNS step required:"
  log "  In Cloudflare: create A-record  $DOMAIN -> $ip  (proxy disabled / grey-cloud)"
  log "  Managed cert provisioning waits on DNS — usually 15–60 min after DNS resolves."
}

# ---- Dispatch -------------------------------------------------------------

run_step() {
  case "$1" in
    apis) step_apis ;;
    sa) step_sa ;;
    repo) step_repo ;;
    vpc) step_vpc ;;
    sql) step_sql ;;
    redis) step_redis ;;
    secrets) step_secrets ;;
    cloudrun) step_cloudrun ;;
    lb) step_lb ;;
    *) echo "unknown step: $1" >&2; exit 2 ;;
  esac
}

if [[ -n "${STEP:-}" ]]; then
  run_step "$STEP"
else
  for s in apis sa repo vpc sql redis secrets cloudrun lb; do
    run_step "$s"
  done
fi

log "deploy-gateway.sh complete. Next: trigger Cloud Build (runbook §9)."
