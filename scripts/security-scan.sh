#!/usr/bin/env bash
# scripts/security-scan.sh
#
# Runs the pinned local scanner suite for CanaryGo. Missing scanners are
# installed at the exact versions supplied by Makefile variables.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GOVULNCHECK_VERSION="${GOVULNCHECK_VERSION:-v1.3.0}"
STATICCHECK_VERSION="${STATICCHECK_VERSION:-v0.7.0}"
GITLEAKS_VERSION="${GITLEAKS_VERSION:-8.30.1}"
TRUFFLEHOG_VERSION="${TRUFFLEHOG_VERSION:-3.95.2}"
TRIVY_VERSION="${TRIVY_VERSION:-0.70.0}"
export GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/canarygo-go-build-cache}"
export STATICCHECK_CACHE="${STATICCHECK_CACHE:-${TMPDIR:-/tmp}/canarygo-staticcheck-cache}"
export TRIVY_CACHE_DIR="${TRIVY_CACHE_DIR:-${TMPDIR:-/tmp}/canarygo-trivy-cache}"
mkdir -p "$GOCACHE"
mkdir -p "$STATICCHECK_CACHE"
mkdir -p "$TRIVY_CACHE_DIR"

go_bin_dir() {
  if [[ -n "${GOBIN:-}" ]]; then
    printf '%s\n' "$GOBIN"
    return
  fi

  local gopath
  gopath="$(go env GOPATH)"
  printf '%s/bin\n' "$gopath"
}

find_tool() {
  local name="$1"
  local gobin
  gobin="$(go_bin_dir)"

  if command -v "$name" >/dev/null 2>&1; then
    command -v "$name"
    return
  fi

  if [[ -x "$gobin/$name" ]]; then
    printf '%s/%s\n' "$gobin" "$name"
    return
  fi

  return 1
}

tool_matches_version() {
  local path="$1"
  local version="$2"

  "$path" -version 2>&1 | grep -F "$version" >/dev/null
}

find_pinned_tool() {
  local name="$1"
  local version="$2"
  local path
  local gobin
  gobin="$(go_bin_dir)"

  if path="$(command -v "$name" 2>/dev/null)" && tool_matches_version "$path" "$version"; then
    printf '%s\n' "$path"
    return
  fi

  if [[ -x "$gobin/$name" ]] && tool_matches_version "$gobin/$name" "$version"; then
    printf '%s/%s\n' "$gobin" "$name"
    return
  fi

  return 1
}

install_tool() {
  local name="$1"
  local module="$2"
  local version="$3"

  echo "==> installing $name@$version" >&2
  if ! go install "$module@$version"; then
    cat >&2 <<EOF
FAIL: could not install $name@$version.
Blocker: install requires network access and a working Go module proxy, or a
preinstalled local binary discoverable on PATH or in $(go_bin_dir).
EOF
    exit 1
  fi
}

ensure_tool() {
  local name="$1"
  local module="$2"
  local version="$3"

  if ! find_pinned_tool "$name" "$version" >/dev/null; then
    install_tool "$name" "$module" "$version"
  fi

  if ! find_pinned_tool "$name" "$version"; then
    local found="missing"
    if found="$(find_tool "$name" 2>/dev/null)"; then
      found="$found ($("$found" -version 2>&1 | head -1))"
    fi

    cat >&2 <<EOF
FAIL: $name is not pinned to $version after install.
Found: $found
Blocker: check GOBIN/PATH permissions or install $module@$version manually.
EOF
    exit 1
  fi
}

check_go_packages() {
  local output

  if output="$(go list ./... 2>&1 >/dev/null)"; then
    return
  fi

  cat >&2 <<EOF
FAIL: Go package loading failed before scanners ran.
Blocker: scanners require 'go list ./...' to complete with writable Go caches
and available module dependencies. Details:
$output
EOF
  exit 1
}

run_vulncheck() {
  local govulncheck
  govulncheck="$(ensure_tool govulncheck golang.org/x/vuln/cmd/govulncheck "$GOVULNCHECK_VERSION")"

  echo "==> govulncheck ($GOVULNCHECK_VERSION)"
  "$govulncheck" ./...
}

run_staticcheck() {
  local staticcheck
  staticcheck="$(ensure_tool staticcheck honnef.co/go/tools/cmd/staticcheck "$STATICCHECK_VERSION")"

  echo "==> staticcheck ($STATICCHECK_VERSION)"
  "$staticcheck" ./...
}

ensure_local_version() {
  local name="$1"
  local version="$2"
  local version_cmd="$3"
  local path

  if ! path="$(command -v "$name" 2>/dev/null)"; then
    cat >&2 <<EOF
FAIL: $name is not installed.
Blocker: install $name $version locally or provide it on PATH.
EOF
    exit 1
  fi

  if ! eval "\"$path\" $version_cmd" 2>&1 | grep -F "$version" >/dev/null; then
    cat >&2 <<EOF
FAIL: $name is not pinned to expected version $version.
Found: $(eval "\"$path\" $version_cmd" 2>&1 | head -1)
Blocker: install the pinned scanner version or update the Makefile pin with evidence.
EOF
    exit 1
  fi

  printf '%s\n' "$path"
}

run_gitleaks() {
  local gitleaks
  gitleaks="$(ensure_local_version gitleaks "$GITLEAKS_VERSION" "version")"

  echo "==> gitleaks ($GITLEAKS_VERSION)"
  "$gitleaks" detect --source . --redact --no-banner
}

run_trufflehog() {
  local trufflehog
  trufflehog="$(ensure_local_version trufflehog "$TRUFFLEHOG_VERSION" "--version")"

  echo "==> trufflehog ($TRUFFLEHOG_VERSION)"
  "$trufflehog" filesystem . --no-update --json --fail --exclude-paths .trufflehog/exclude.txt
}

run_trivy() {
  local trivy
  trivy="$(ensure_local_version trivy "$TRIVY_VERSION" "--version")"

  echo "==> trivy ($TRIVY_VERSION)"
  "$trivy" fs --scanners vuln,secret,misconfig --severity HIGH,CRITICAL \
    --exit-code 1 --skip-dirs .git --skip-dirs .gocache .
}

main() {
  cd "$ROOT"
  check_go_packages

  case "${1:-all}" in
    all)
      run_staticcheck
      run_vulncheck
      run_gitleaks
      run_trufflehog
      run_trivy
      ;;
    vulncheck)
      run_vulncheck
      ;;
    *)
      echo "usage: $0 [all|vulncheck]" >&2
      exit 2
      ;;
  esac
}

main "$@"
