#!/usr/bin/env bash
# KumbulaCloud PoC — Validation Script
# Run after completing all phases to verify the setup.
set -uo pipefail

RESULTS_FILE=$(mktemp)
echo "0 0 0" > "$RESULTS_FILE"
trap "rm -f $RESULTS_FILE" EXIT

pass() {
  echo "  PASS: $1"
  read P F W < "$RESULTS_FILE"; echo "$((P+1)) $F $W" > "$RESULTS_FILE"
}
fail() {
  echo "  FAIL: $1"
  read P F W < "$RESULTS_FILE"; echo "$P $((F+1)) $W" > "$RESULTS_FILE"
}
warn() {
  echo "  WARN: $1"
  read P F W < "$RESULTS_FILE"; echo "$P $F $((W+1))" > "$RESULTS_FILE"
}

echo "============================================"
echo " KumbulaCloud PoC — Validation"
echo "============================================"
echo ""

# --- 1. Prerequisites ---
echo "[1/7] Prerequisites"
command -v docker &>/dev/null && pass "Docker installed ($(docker --version | cut -d' ' -f3 | tr -d ','))" || fail "Docker not installed"
command -v go &>/dev/null && pass "Go installed ($(go version | cut -d' ' -f3))" || fail "Go not installed"
command -v jq &>/dev/null && pass "jq installed" || fail "jq not installed"
command -v psql &>/dev/null && pass "psql installed" || fail "psql not installed"
echo ""

# --- 2. DNS ---
echo "[2/7] DNS Resolution"
if getent hosts test.kumbula.local &>/dev/null; then
  RESOLVED_IP=$(getent hosts test.kumbula.local | awk '{print $1}' | head -1)
  # Check resolution speed (should be < 1 second)
  START=$(date +%s%N)
  getent hosts speed-test.kumbula.local &>/dev/null
  END=$(date +%s%N)
  ELAPSED_MS=$(( (END - START) / 1000000 ))
  if [ "$ELAPSED_MS" -lt 1000 ]; then
    pass "*.kumbula.local resolves to $RESOLVED_IP (${ELAPSED_MS}ms)"
  else
    warn "*.kumbula.local resolves but slowly (${ELAPSED_MS}ms) — check AAAA record in dnsmasq"
  fi
else
  fail "*.kumbula.local does not resolve — check dnsmasq and /etc/resolv.conf"
fi
echo ""

# --- 3. Docker Compose Stack ---
echo "[3/7] Docker Compose Stack"
for svc in traefik gitea kumbula-postgres; do
  STATUS=$(docker inspect -f '{{.State.Status}}' "$svc" 2>/dev/null || echo "missing")
  if [ "$STATUS" = "running" ]; then
    pass "$svc is running"
  else
    fail "$svc is $STATUS"
  fi
done

# Check Traefik can see Docker containers
ROUTER_COUNT=$(curl -s http://localhost:8080/api/http/routers 2>/dev/null | python3 -c "
import sys,json
try:
  routers = json.load(sys.stdin)
  print(sum(1 for r in routers if r.get('provider') != 'internal'))
except: print(0)
" 2>/dev/null)
if [ "$ROUTER_COUNT" -gt 0 ]; then
  pass "Traefik sees $ROUTER_COUNT Docker route(s)"
else
  fail "Traefik has no Docker routes — check Docker API version compatibility"
fi
echo ""

# --- 4. Gitea ---
echo "[4/7] Gitea"
GITEA_STATUS=$(docker exec gitea curl -s -o /dev/null -w "%{http_code}" http://localhost:3000/api/v1/settings/api 2>/dev/null)
if [ "$GITEA_STATUS" = "200" ]; then
  pass "Gitea API responding"
else
  fail "Gitea API returned $GITEA_STATUS (still in install mode?)"
fi

GITEA_USER=$(docker exec gitea curl -s -u "kumbula:kumbula123" http://localhost:3000/api/v1/user 2>/dev/null | python3 -c "import sys,json; print(json.load(sys.stdin).get('login',''))" 2>/dev/null)
if [ "$GITEA_USER" = "kumbula" ]; then
  pass "Gitea admin user 'kumbula' exists"
else
  fail "Gitea admin user not found — run: docker exec -u git gitea gitea admin user create ..."
fi

GITEA_VIA_TRAEFIK=$(curl -s -o /dev/null -w "%{http_code}" -H "Host: gitea.kumbula.local" http://localhost:80/ 2>/dev/null)
if [ "$GITEA_VIA_TRAEFIK" = "200" ]; then
  pass "Gitea accessible via Traefik"
else
  fail "Gitea not routed via Traefik (got $GITEA_VIA_TRAEFIK)"
fi
echo ""

# --- 5. Engine ---
echo "[5/7] Engine"
ENGINE_HEALTH=$(curl -s http://localhost:9000/health 2>/dev/null | python3 -c "import sys,json; print(json.load(sys.stdin).get('status',''))" 2>/dev/null)
if [ "$ENGINE_HEALTH" = "healthy" ]; then
  pass "Engine is healthy on :9000"
else
  fail "Engine not responding — start it with: cd ~/kumbula-poc/engine && ./kumbula-engine"
fi
echo ""

# --- 6. PostgreSQL ---
echo "[6/7] PostgreSQL"
PG_OK=$(PGPASSWORD=kumbula_secret_2024 psql -h localhost -U kumbula_admin -d kumbula_system -c "SELECT 1" -t 2>/dev/null | tr -d ' \n')
if [ "$PG_OK" = "1" ]; then
  pass "PostgreSQL connection works"
else
  fail "Cannot connect to PostgreSQL"
fi
echo ""

# --- 7. Deployed Apps ---
echo "[7/7] Deployed Apps"
APP_COUNT=$(curl -s http://localhost:9000/apps 2>/dev/null | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null)
if [ "${APP_COUNT:-0}" -gt 0 ]; then
  pass "$APP_COUNT app(s) registered in engine"

  # Test each app via Traefik
  curl -s http://localhost:9000/apps 2>/dev/null | python3 -c "
import sys, json
apps = json.load(sys.stdin)
for name, info in apps.items():
    print(f'{name}|{info[\"status\"]}|{info[\"url\"]}')
" 2>/dev/null | while IFS='|' read -r APP_NAME APP_STATUS APP_URL; do
    CONTAINER_STATUS=$(docker inspect -f '{{.State.Status}}' "kumbula-app-$APP_NAME" 2>/dev/null || echo "missing")
    if [ "$CONTAINER_STATUS" = "running" ]; then
      # Test via Traefik
      HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 3 \
        -H "Host: ${APP_NAME}.kumbula.local" http://localhost:80/ 2>/dev/null)
      if [ "$HTTP_CODE" = "200" ]; then
        echo "  PASS: $APP_NAME -> container running, HTTP 200 via Traefik"
      else
        echo "  FAIL: $APP_NAME -> container running but Traefik returned $HTTP_CODE"
      fi
    else
      echo "  FAIL: $APP_NAME -> container is $CONTAINER_STATUS"
    fi
  done
else
  warn "No apps deployed yet — push an app to test the full pipeline"
fi
echo ""

# --- Summary ---
read PASS FAIL WARN < "$RESULTS_FILE"
echo "============================================"
echo " Results: $PASS passed, $FAIL failed, $WARN warnings"
echo "============================================"
if [ "$FAIL" -gt 0 ]; then
  echo " Fix the failures above before running the demo."
  exit 1
else
  echo " KumbulaCloud is ready for the demo!"
  exit 0
fi
