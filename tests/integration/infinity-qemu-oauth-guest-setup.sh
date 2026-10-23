#!/usr/bin/env bash
# Root-only in a disposable QEMU guest; no production host may run this.
set -euo pipefail
test "$(id -u)" = 0
test "$(hostname -s)" = infinity-oauth-reboot-vm
LAB=/srv/infinity-oauth-ci
TLS=/etc/portico-qemu-oauth-tls
PROJECT=portico-oauth-kernel-ci
dc() { docker compose -p "$PROJECT" --project-directory "$LAB" -f "$LAB/compose.yaml" -f "$LAB/compose.integrated-auth.yaml" -f "$LAB/ci.yml" "$@"; }
api() { curl -fsS --connect-timeout 6 --max-time 35 -H 'Host: mcp.example.invalid' -H 'X-Forwarded-Proto: https' "$@"; }
field() { python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$1" "$2"; }
healthy() {
  test "$(systemctl is-active docker)" = active
  for svc in zitadel-postgres zitadel-api; do
    cid="$(dc ps -q "$svc")"
    test -n "$cid"
    test "$(docker inspect -f '{{.State.Health.Status}}' "$cid")" = healthy
  done
  api http://127.0.0.1:18082/.well-known/openid-configuration |
    python3 -c 'import json,sys;assert json.load(sys.stdin)["issuer"]=="https://mcp.example.invalid"'
}
if [ "$1" = --verify ]; then
  test -s "$LAB/token.json"
  test -s /home/infinity-ci/portico-oauth-token
  test -r "$TLS/ca.pem"
  test "$(systemctl is-active nginx)" = active
  api --cacert "$TLS/ca.pem" https://127.0.0.1:18443/.well-known/openid-configuration >/dev/null
  healthy
  echo 'INFINITY_QEMU_OAUTH_ZITADEL_POSTGRES_RECOVERED_AFTER_REAL_REBOOT: PASS'
  exit 0
fi
test "$1" = --setup
test ! -e "$LAB"
umask 077
install -d -m 0700 "$LAB" "$LAB/config" "$LAB/state"
install -d -o root -g root -m 0755 "$TLS"
install -m 0600 /tmp/compose.yaml "$LAB/compose.yaml"
install -m 0600 /tmp/compose.integrated-auth.yaml "$LAB/compose.integrated-auth.yaml"
printf 'version: 1\nmode: scoped\n' > "$LAB/config/policy.yaml"
cat > "$LAB/ci.yml" <<'YAML'
services:
  zitadel-api:
    ports:
      - "127.0.0.1:18082:8080"
YAML
cat > "$LAB/.env" <<EOF
VPS_AGENT_ADMIN_TOKEN=$(openssl rand -hex 32)
VPS_AGENT_STATIC_TOKEN=$(openssl rand -hex 32)
VPS_AGENT_SUBJECT=ci-guest
VPS_AGENT_SCOPE_ROOT=/opt
VPS_AGENT_WHOLE_HOST=0
VPS_AGENT_AUTH_MODE=integrated
VPS_AGENT_DOMAIN=mcp.example.invalid
VPS_AGENT_PUBLIC_URL=https://mcp.example.invalid/mcp
VPS_AGENT_EDGE_NETWORK=ci-oauth-private
ZITADEL_VERSION=v4.19.4
ZITADEL_SESSION_COOKIE_SECRET=$(openssl rand -hex 32)
ZITADEL_MASTERKEY=$(openssl rand -hex 16)
ZITADEL_POSTGRES_DB=zitadel
ZITADEL_POSTGRES_USER=zitadel
ZITADEL_POSTGRES_PASSWORD=$(openssl rand -hex 24)
ZITADEL_BOOTSTRAP_ADMIN_PASSWORD=T1!$(openssl rand -hex 18)
ZITADEL_BOOTSTRAP_PAT_EXPIRATION=2099-01-01T00:00:00Z
ZITADEL_LOGIN_PAT_EXPIRATION=2099-01-01T00:00:00Z
EOF
chmod 0600 "$LAB/.env" "$LAB/ci.yml"
# HTTPS introspection is mandatory for the production Gateway. Use nginx
# with a throwaway self-signed loopback certificate trusted ONLY inside guest.
# HTTP never leaves this VM; TLS verification is not disabled.
openssl req -x509 -newkey rsa:2048 -nodes -days 2 -sha256 \
  -keyout "$TLS/loopback.key" -out "$TLS/ca.pem" \
  -subj /CN=portico-qemu-oauth-loopback \
  -addext "subjectAltName=IP:127.0.0.1" >/dev/null 2>&1
chmod 0600 "$TLS/loopback.key"
chmod 0644 "$TLS/ca.pem"
cat > /etc/nginx/conf.d/portico-qemu-oauth.conf <<EOF
server {
    listen 127.0.0.1:18443 ssl;
    server_name localhost;
    ssl_certificate $TLS/ca.pem;
    ssl_certificate_key $TLS/loopback.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    location / {
        proxy_pass http://127.0.0.1:18082;
        proxy_set_header Host mcp.example.invalid;
        proxy_set_header X-Forwarded-Proto https;
        proxy_connect_timeout 5s;
        proxy_read_timeout 25s;
    }
}
EOF
nginx -t >/dev/null 2>&1
systemctl enable --now nginx >/dev/null
docker network create --internal ci-oauth-private >/dev/null
dc up -d zitadel-postgres zitadel-api >/dev/null
ready=0
for i in $(seq 1 240); do if healthy >/dev/null 2>&1; then ready=1; break; fi; sleep 2; done
test "$ready" = 1 || { echo "OAuth guest stack unhealthy" >&2; dc ps >&2; exit 1; }
# Fail closed if private certificate trust, SNI/IP validation or TLS
# forwarding does not work before trying to authenticate MCP.
api --cacert "$TLS/ca.pem" \
  https://127.0.0.1:18443/.well-known/openid-configuration \
  | python3 -c 'import json,sys;assert json.load(sys.stdin)["issuer"]=="https://mcp.example.invalid"'
PAT="$(docker run --rm -v "$PROJECT"_zitadel-bootstrap:/bootstrap:ro alpine:3.22 cat /bootstrap/bootstrap-admin.pat)"
test -n "$PAT"
api -X POST http://127.0.0.1:18082/management/v1/projects -H "Authorization: Bearer $PAT" \
  -H 'Content-Type: application/json' --data '{"name":"QEMU real kernel OAuth resource"}' > "$LAB/project.json"
P="$(field "$LAB/project.json" id)"
api -X POST "http://127.0.0.1:18082/management/v1/projects/$P/apps/api" -H "Authorization: Bearer $PAT" \
  -H 'Content-Type: application/json' --data '{"name":"QEMU introspection client","authMethodType":"API_AUTH_METHOD_TYPE_BASIC"}' > "$LAB/client.json"
C="$(field "$LAB/client.json" clientId)"
S="$(field "$LAB/client.json" clientSecret)"
api -X POST http://127.0.0.1:18082/management/v1/users/machine -H "Authorization: Bearer $PAT" \
  -H 'Content-Type: application/json' --data '{"userName":"ci-kernel-operator","name":"CI Kernel Operator","accessTokenType":"ACCESS_TOKEN_TYPE_BEARER"}' > "$LAB/machine.json"
U="$(field "$LAB/machine.json" userId)"
api -X PUT "http://127.0.0.1:18082/management/v1/users/$U/secret" -H "Authorization: Bearer $PAT" \
  -H 'Content-Type: application/json' --data '{}' > "$LAB/machine-secret.json"
MC="$(field "$LAB/machine-secret.json" clientId)"
MS="$(field "$LAB/machine-secret.json" clientSecret)"
A="urn:zitadel:iam:org:project:id:$P:aud"
api -X POST http://127.0.0.1:18082/oauth/v2/token -u "$MC:$MS" \
  -H 'Content-Type: application/x-www-form-urlencoded' --data 'grant_type=client_credentials' \
  --data-urlencode "scope=openid $A" > "$LAB/token.json"
TOKEN="$(field "$LAB/token.json" access_token)"
api -X POST http://127.0.0.1:18082/oauth/v2/introspect -u "$C:$S" \
  -H 'Content-Type: application/x-www-form-urlencoded' --data-urlencode "token=$TOKEN" > "$LAB/introspection.json"
python3 - "$LAB/introspection.json" "$P" "$A" <<'PY'
import json,sys,time
v=json.load(open(sys.argv[1])); p,a=sys.argv[2:]
aud=v.get("aud",[])
if isinstance(aud,str): aud=[aud]
assert v.get("active") is True and p in aud and a in v.get("scope","").split()
assert v.get("iss")=="https://mcp.example.invalid" and v.get("sub")
assert int(v.get("exp",0))>time.time()+300
PY
field "$LAB/introspection.json" sub > "$LAB/subject"
cat > /etc/portico-kernel-gateway.env <<EOF
VPS_AGENT_LISTEN=0.0.0.0:8080
VPS_AGENT_BROKER_SOCKET=/run/portico-infinity/broker.sock
VPS_AGENT_AUTH_MODE=integrated
VPS_AGENT_OIDC_ISSUER=https://mcp.example.invalid
VPS_AGENT_PUBLIC_URL=https://mcp.example.invalid/mcp
VPS_AGENT_INTEGRATED_INTROSPECTION_URL=https://127.0.0.1:18443/oauth/v2/introspect
VPS_AGENT_INTEGRATED_INTROSPECTION_HOST=mcp.example.invalid
VPS_AGENT_INTEGRATED_INTROSPECTION_CLIENT_ID=$C
VPS_AGENT_INTEGRATED_INTROSPECTION_CLIENT_SECRET=$S
VPS_AGENT_INTEGRATED_AUDIENCE_PROJECT_ID=$P
VPS_AGENT_REQUIRED_SCOPES=openid $A
SSL_CERT_FILE=$TLS/ca.pem
VPS_AGENT_INSTANCE_ID=ci-guest-oauth-reboot
VPS_AGENT_INSTANCE_NAME=ci-guest-oauth-reboot
EOF
chmod 0600 /etc/portico-kernel-gateway.env
install -o infinity-ci -g infinity-ci -m 0600 /dev/null /home/infinity-ci/portico-oauth-token
printf '%s\n' "$TOKEN" > /home/infinity-ci/portico-oauth-token
chown infinity-ci:infinity-ci /home/infinity-ci/portico-oauth-token
echo 'INFINITY_QEMU_REAL_ZITADEL_TOKEN_AND_AUDIENCE_CREATED: PASS'
