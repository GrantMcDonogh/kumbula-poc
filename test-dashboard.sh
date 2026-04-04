#!/usr/bin/env bash
set -euo pipefail

echo "=== KumbulaCloud Dashboard Smoke Test ==="

ENGINE="http://localhost:9000"

echo "1. Health check..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$ENGINE/health")
if [ "$STATUS" != "200" ]; then
    echo "FAIL: Engine not healthy (got $STATUS)"
    exit 1
fi
echo "   PASS: Engine healthy"

echo "2. Login page loads..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$ENGINE/login")
if [ "$STATUS" != "200" ]; then
    echo "FAIL: Login page returned $STATUS"
    exit 1
fi
echo "   PASS: Login page loads"

echo "3. Signup page loads..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$ENGINE/signup")
if [ "$STATUS" != "200" ]; then
    echo "FAIL: Signup page returned $STATUS"
    exit 1
fi
echo "   PASS: Signup page loads"

echo "4. Dashboard redirects to login when not authenticated..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" -L --max-redirs 0 "$ENGINE/")
if [ "$STATUS" != "303" ]; then
    echo "FAIL: Dashboard should redirect (got $STATUS)"
    exit 1
fi
echo "   PASS: Dashboard redirects to login"

echo ""
echo "=== All smoke tests passed ==="
echo ""
echo "Manual test steps:"
echo "  1. Open http://dashboard.kumbula.local"
echo "  2. Sign up with a new account"
echo "  3. Create a project"
echo "  4. Push code and watch the build log"
